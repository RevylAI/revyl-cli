from __future__ import annotations

import asyncio
import os
import pwd
from pathlib import Path
from uuid import UUID, uuid4

import pytest

from test_computer_cli import ComputerCLI, cli as cli


def test_real_cli_drives_private_canary_against_loopback_fixture(
    request: pytest.FixtureRequest, monkeypatch: pytest.MonkeyPatch, tmp_path: Path
) -> None:
    from computer_runtime.macos.canary import run_canary
    from computer_runtime.macos.host import HostConfig
    from computer_runtime.macos.protocol import HostIdentity, SealedImage
    from cognisim_schemas.schemas.computer_schema import (
        ComputerExecResult,
        ComputerSucceeded,
    )

    client: ComputerCLI = request.getfixturevalue("cli")

    client.executor.scripts.append(
        ComputerSucceeded(
            result=ComputerExecResult(exit_code=0, stdout=b"private-mac-canary")
        )
    )
    config = HostConfig(
        identity=HostIdentity(host_id=uuid4(), environment="offline"),
        org_id=UUID("711eaa7f-076d-44ed-a17c-d7c7e51536a2"),
        computer_session_id=uuid4(),
        image=SealedImage(local_name="fixture", sha256="a" * 64, sealed=True),
        image_manifest="/unused/image.json",
        artifact_sha256="b" * 64,
        state_directory="/unused/state",
        tart_home="/unused/tart",
        tart_binary="/unused/tart-bin",
        tart_sha256="c" * 64,
        socket_path="/unused/socket",
        socket_gid=0,
        vm_user=pwd.getpwuid(os.getuid()).pw_name,
    )
    monkeypatch.setenv("REVYL_API_KEY", "synthetic-owner")
    asyncio.run(
        run_canary(
            config, client.binary, client.env["REVYL_BACKEND_URL"], tmp_path / "output"
        )
    )
    assert (tmp_path / "output/result.json").is_file()
    assert client.executor.releases == 1
    assert len(client.executor.dispatches) == 7
