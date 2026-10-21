from __future__ import annotations

import json
import os
import socket
import subprocess
import time
from collections.abc import Iterator
from dataclasses import dataclass
from pathlib import Path
from threading import Thread
from typing import TYPE_CHECKING, Any
from unittest.mock import AsyncMock, MagicMock, patch
from uuid import UUID, uuid4

import pytest
import uvicorn
from fastapi import FastAPI, Request

if TYPE_CHECKING:
    from computer_runtime.tests.support import FaultEvidence, ScriptedExecutor


@dataclass
class ComputerCLI:
    binary: Path
    env: dict[str, str]
    executor: ScriptedExecutor
    evidence: FaultEvidence
    app: FastAPI

    def run(
        self, *args: str, data: bytes | None = None, token: str = "synthetic-owner"
    ) -> subprocess.CompletedProcess[bytes]:
        return subprocess.run(
            [str(self.binary), "computer", *args],
            input=data,
            capture_output=True,
            env=self.env | {"REVYL_API_KEY": token},
            timeout=15,
            check=False,
        )


@pytest.fixture
def cli(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> Iterator[ComputerCLI]:
    from fastapi import HTTPException

    auth_mock = MagicMock()
    auth_mock.optional_user = AsyncMock(return_value=None)
    with patch("propelauth_fastapi.init_auth", return_value=auth_mock):
        from app.routes.computer_runs_routes.computer_runs_xpt import router
        from app.services import posthog_capture
        from app.services.computer_runs import (
            ComputerRunService,
            require_computer_run_service,
        )
        from app.utils import auth_helpers
        from cognisim_schemas.schemas.device_schema import AuthInfo
        from computer_runtime.owner import ComputerRuntime
        from computer_runtime.tests.support import FaultEvidence, ScriptedExecutor

    org_id = UUID("711eaa7f-076d-44ed-a17c-d7c7e51536a2")

    async def identity(request: Request) -> AuthInfo | None:
        token = request.headers.get("authorization")
        if token == "Bearer synthetic-owner":
            return AuthInfo(user_id="synthetic-user", org_id=org_id, user_role="Member")
        if token == "Bearer synthetic-foreign":
            return AuthInfo(
                user_id="synthetic-other",
                org_id=UUID("3a2f25bd-ded7-4579-a311-abfc1b95d264"),
            )
        if token == "Bearer synthetic-service":
            return AuthInfo(user_id="synthetic-service", org_id=org_id, service=True)
        return None

    app = FastAPI()
    app.include_router(router, prefix="/api/v1/computer-runs")
    evidence = FaultEvidence()
    executor = ScriptedExecutor()
    runtime = ComputerRuntime(evidence, executor)
    service = ComputerRunService(runtime)
    app.dependency_overrides[require_computer_run_service] = lambda: service
    app.dependency_overrides[auth_helpers.auth.optional_user] = identity
    monkeypatch.setattr(
        auth_helpers,
        "cached_validate_api_key",
        AsyncMock(side_effect=HTTPException(401)),
    )
    monkeypatch.setattr(posthog_capture, "_capture_product_event", MagicMock())
    listener = socket.socket()
    listener.bind(("127.0.0.1", 0))
    port = listener.getsockname()[1]
    config = uvicorn.Config(app, log_level="error", access_log=False, lifespan="off")
    server = uvicorn.Server(config)
    thread = Thread(target=server.run, kwargs={"sockets": [listener]}, daemon=True)
    thread.start()
    deadline = time.monotonic() + 5
    while not server.started and thread.is_alive() and time.monotonic() < deadline:
        time.sleep(0.01)
    assert server.started
    root = Path(__file__).resolve().parents[2]
    binary = root / "revyl-cli/build/revyl"
    assert binary.is_file(), "Run make -C revyl-cli build before this integration test"
    env = {
        "PATH": os.environ["PATH"],
        "HOME": str(tmp_path),
        "REVYL_BACKEND_URL": f"http://127.0.0.1:{port}",
        "REVYL_TELEMETRY_DISABLED": "1",
        "REVYL_NO_UPDATE_NOTIFIER": "1",
    }
    try:
        yield ComputerCLI(binary, env, executor, evidence, app)
    finally:
        server.should_exit = True
        thread.join(timeout=5)
        listener.close()
        assert not thread.is_alive()


def read_json(
    result: subprocess.CompletedProcess[bytes], code: int = 0
) -> dict[str, Any]:
    """Decode the dynamic CLI envelope for assertions at the subprocess boundary."""
    assert result.returncode == code, result.stderr.decode(errors="replace")
    value = json.loads(result.stdout)
    assert isinstance(value, dict)
    return value


@pytest.mark.parametrize("os_name", ["macos", "linux"])
def test_computer_cli_complete_binary_loop(cli: ComputerCLI, os_name: str) -> None:
    c1 = str(uuid4())
    report = read_json(cli.run("create", "--id", c1, "--os", os_name, "--json"))
    assert report["computer_session_id"] == c1
    report_id = report["report_id"]
    request_id = str(uuid4())
    execution = read_json(
        cli.run(
            "exec", c1, "--request-id", request_id, "--json", "--", "fixture-command"
        )
    )
    operation_id = execution["operation_id"]
    assert execution["execution"]["outcome"] == "succeeded"
    duplicate = read_json(
        cli.run(
            "exec", c1, "--request-id", request_id, "--json", "--", "fixture-command"
        )
    )
    assert duplicate["operation_id"] == operation_id
    assert len(cli.executor.dispatches) == 1
    conflict = read_json(
        cli.run(
            "exec", c1, "--request-id", request_id, "--json", "--", "changed-command"
        ),
        1,
    )
    assert "request_conflict" in conflict["error"]
    assert len(cli.executor.dispatches) == 1
    data = bytes([251, 255, 0]) * 21_845 + b"\xff"
    assert len(data) == 65_536
    read_json(
        cli.run(
            "files", "write", c1, "workspace/file", "--from", "-", "--json", data=data
        )
    )
    raw = cli.run("files", "read", c1, "workspace/file")
    assert raw.returncode == 0 and raw.stdout == data
    read_json(cli.run("files", "stat", c1, "workspace/file", "--json"))
    listing = read_json(cli.run("process", "list", c1, "--json"))
    handle = listing["execution"]["result"]["processes"][0]["handle"]["handle_id"]
    read_json(cli.run("process", "inspect", c1, handle, "--json"))
    read_json(cli.run("process", "terminate", c1, handle, "--json"))
    read_json(cli.run("operation", "get", c1, operation_id, "--json"))
    before = read_json(cli.run("report", c1, "--format", "json"))
    atif_result = cli.run("report", c1, "--format", "atif")
    atif = read_json(atif_result)
    from cognisim_schemas.schemas.computer_report_atif import ComputerATIFTrajectory

    ComputerATIFTrajectory.model_validate_json(atif_result.stdout)
    assert atif["trajectory_id"] == report_id and atif["session_id"] == c1
    assert len(atif["steps"]) > 1 and before["report_id"] == report_id
    for token, code in [
        ("synthetic-foreign", "inaccessible"),
        ("synthetic-service", "forbidden"),
        ("invalid-token", "unauthenticated"),
    ]:
        denied = read_json(cli.run("get", c1, "--json", token=token), 1)
        assert code in denied["error"]
    stopped = read_json(cli.run("stop", c1, "--json"))
    assert stopped["release"] == "confirmed" and stopped["state"] == "stopped"
    assert read_json(cli.run("report", c1))["report_id"] == report_id
    assert (
        read_json(cli.run("report", c1, "--format", "atif"))["trajectory_id"]
        == report_id
    )


def test_computer_cli_truthful_outcomes_and_bounds(cli: ComputerCLI) -> None:
    from cognisim_schemas.schemas.computer_schema import (
        ComputerExecResult,
        ComputerFailed,
        ComputerTimedOut,
        ComputerUnknown,
    )

    c1 = str(uuid4())
    read_json(cli.run("create", "--id", c1, "--os", "macos", "--json"))
    from cognisim_schemas.schemas.computer_schema import ComputerSucceeded

    maximum = b"\xff" * 65_536
    cli.executor.scripts.append(
        ComputerSucceeded(result=ComputerExecResult(exit_code=0, stdout=maximum))
    )
    raw = cli.run("exec", c1, "--", "fixture-command")
    assert raw.returncode == 0 and raw.stdout == maximum
    for outcome, code in [
        (
            ComputerFailed(
                error_code="exec_nonzero",
                result=ComputerExecResult(exit_code=7, stdout=b"output-canary"),
            ),
            1,
        ),
        (ComputerTimedOut(termination="confirmed"), 124),
        (ComputerTimedOut(termination="unknown"), 124),
        (ComputerUnknown(error_code="lost_ack"), 125),
    ]:
        c1 = str(uuid4())
        read_json(cli.run("create", "--id", c1, "--os", "macos", "--json"))
        cli.executor.scripts.append(outcome)
        result = read_json(cli.run("exec", c1, "--json", "--", "fixture-command"), code)
        assert result["execution"]["outcome"] == outcome.outcome
    c1 = str(uuid4())
    read_json(cli.run("create", "--id", c1, "--os", "macos", "--json"))
    cli.evidence.fail_completion = True
    result = read_json(cli.run("exec", c1, "--json", "--", "fixture-command"), 74)
    assert (
        result["execution"]["outcome"] == "succeeded" and result["evidence"] == "failed"
    )
    cli.evidence.fail_completion = False
    count = len(cli.executor.dispatches)
    for args, data in [
        (
            ("files", "write", c1, "workspace/oversize", "--from", "-", "--json"),
            b"x" * 65_537,
        ),
        (("files", "read", c1, "../private-canary", "--json"), None),
        (("exec", c1, "--stdin", "-", "--json", "--", "x"), b"x" * 16_384),
        (("exec", c1, "--timeout", "6m", "--json", "--", "x"), None),
    ]:
        invalid = cli.run(*args, data=data)
        read_json(invalid, 1)
        assert b"private-canary" not in invalid.stdout + invalid.stderr
    assert len(cli.executor.dispatches) == count
    cli.executor.release_confirmation = "unknown"
    stopped = read_json(cli.run("stop", c1, "--json"), 125)
    assert stopped["release"] == "unknown"


def test_computer_cli_unconfigured_runtime_fails_closed(cli: ComputerCLI) -> None:
    from app.services.computer_runs import require_computer_run_service

    del cli.app.dependency_overrides[require_computer_run_service]
    result = read_json(cli.run("create", "--os", "macos", "--json"), 1)
    assert "runtime_unavailable" in result["error"]
    assert not cli.executor.dispatches


def test_computer_cli_non_allowlisted_organization_fails_closed(
    cli: ComputerCLI, monkeypatch: pytest.MonkeyPatch
) -> None:
    from app.services import hosted_computer_runtime
    from app.services.computer_runs import (
        ComputerRunService,
        require_computer_run_service,
    )
    from app.services.hosted_computer_runtime import HostedComputerRuntime
    from app.db.repositories.computer_runs_repo import ComputerRunOrigin
    from cognisim_schemas.schemas.session_schema import ExecutionSource

    monkeypatch.setattr(
        hosted_computer_runtime, "is_computer_enabled_for_org", lambda _: False
    )
    runtime = HostedComputerRuntime(
        origin=ComputerRunOrigin(source=ExecutionSource.CLI, user_id="synthetic-user")
    )
    cli.app.dependency_overrides[require_computer_run_service] = (
        lambda: ComputerRunService(runtime, provider="macos_vm")
    )
    result = read_json(cli.run("create", "--os", "macos", "--json"), 1)
    assert result["error"] == "computer request refused: computer_not_enabled"
    assert not cli.executor.dispatches
