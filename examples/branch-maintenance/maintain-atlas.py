"""Keep one Revyl branch current with an exact build-to-Explore handoff."""

from __future__ import annotations

import json
import os
import signal
import subprocess
import sys
import time
from collections.abc import Callable
from pathlib import Path
from typing import TypeAlias

JsonObject: TypeAlias = dict[str, object]
CHECKPOINT: Path = Path("revyl-maintenance-result.json")
TERMINAL_BUILDS: frozenset[str] = frozenset(
    {"success", "failed", "cancelled", "timeout"}
)


def cli(*arguments: str, timeout_seconds: float = 180) -> JsonObject:
    result = subprocess.run(
        ["revyl", *arguments, "--json"],
        check=False,
        stdout=subprocess.PIPE,
        text=True,
        timeout=timeout_seconds,
    )
    try:
        payload: object = json.loads(result.stdout)
    except json.JSONDecodeError as error:
        raise RuntimeError(
            "No valid CLI receipt; inspect the remote state before retrying"
        ) from error
    if not isinstance(payload, dict):
        raise TypeError("Expected a CLI JSON object")
    if result.returncode and not (
        arguments[:2] == ("explore", "status")
        and payload.get("execution_status") in {"completed", "failed", "cancelled"}
    ):
        raise RuntimeError("CLI request failed; inspect its diagnostic before retrying")
    return payload


def required_string(payload: JsonObject, key: str) -> str:
    value = payload.get(key)
    if not isinstance(value, str) or not value:
        raise RuntimeError(f"Missing {key} in CLI receipt")
    return value


def exploration_finished(payload: JsonObject) -> bool:
    execution, atlas = payload.get("execution_status"), payload.get("atlas_status")
    return execution == "cancelled" or (
        execution in {"completed", "failed"}
        and (
            atlas in {"completed", "partial", "failed"}
            or (execution == "failed" and atlas == "not_started")
        )
    )


def wait_for(
    command: tuple[str, ...], finished: Callable[[JsonObject], bool], seconds: int
) -> JsonObject:
    deadline = time.monotonic() + seconds
    while (remaining := deadline - time.monotonic()) > 0:
        payload = cli(*command, timeout_seconds=min(180, remaining))
        if finished(payload):
            return payload
        remaining = deadline - time.monotonic()
        if remaining > 0:
            time.sleep(min(10, remaining))
    raise TimeoutError("Remote operation did not settle before the wait deadline")


def checkpoint(values: JsonObject) -> None:
    temporary = CHECKPOINT.with_suffix(".tmp")
    temporary.write_text(json.dumps(values, indent=2) + "\n")
    temporary.replace(CHECKPOINT)


def main() -> None:
    os.umask(0o077)
    app_id = os.environ["REVYL_APP_ID"]
    destination = os.environ["REVYL_DESTINATION"]
    summary: JsonObject = {
        "app_id": app_id,
        "destination": destination,
        "source_sha": os.environ["SOURCE_SHA"],
    }
    build_job_id: str | None = None
    exploration_id: str | None = None
    checkpoint(summary)
    try:
        artifact = os.environ.get("REVYL_ARTIFACT")
        if artifact:
            receipt = cli(
                "build",
                "upload",
                "--app",
                app_id,
                "--file",
                artifact,
                "--branch",
                destination,
                "--yes",
            )
            build = receipt.get("build")
            if (
                receipt.get("success") is not True
                or receipt.get("count") != 1
                or not isinstance(build, dict)
                or build.get("app_id") != app_id
            ):
                raise RuntimeError("Upload did not return exactly one successful build")
            build_id = required_string(build, "build_id")
        else:
            receipt = cli(
                "build",
                "--remote",
                "--detach",
                "--profile",
                os.environ["REVYL_PROFILE"],
                "--platform",
                os.environ["REVYL_PLATFORM"],
                "--branch",
                destination,
                "--timeout",
                "1800",
            )
            build_job_id = required_string(receipt, "build_job_id")
            summary["build_job_id"] = build_job_id
            checkpoint(summary)
            result = wait_for(
                ("build", "status", build_job_id),
                lambda value: value.get("status") in TERMINAL_BUILDS,
                2400,
            )
            build_job_id = None
            if result.get("status") != "success" or result.get("app_id") != app_id:
                raise RuntimeError(
                    "Remote build failed or the recipe targets a different app"
                )
            build_id = required_string(result, "version_id")
        summary["build_id"] = build_id
        checkpoint(summary)
        login: tuple[str, ...] = ()
        if launch_var := os.environ.get("REVYL_LOGIN_VARIABLE"):
            login = ("--launch-var", launch_var)
        launch = cli(
            "explore",
            "run",
            app_id,
            "--build-id",
            build_id,
            "--explorers",
            "1",
            "--max-duration",
            "20m",
            "--no-wait",
            *login,
        )
        exploration_id = required_string(launch, "run_id")
        summary["run_id"] = exploration_id
        checkpoint(summary)
        if launch.get("build_id") != build_id:
            raise RuntimeError("Explore receipt did not select the uploaded build")
        result = wait_for(
            ("explore", "status", exploration_id), exploration_finished, 3600
        )
        exploration_id = None
        summary.update(
            {
                key: result.get(key)
                for key in (
                    "outcome",
                    "success",
                    "report_url",
                    "branch_id",
                    "branch_name",
                )
            }
        )
        checkpoint(summary)
        if result.get("success") is not True or result.get("build_id") != build_id:
            raise RuntimeError(
                "Explore did not produce a usable map for the uploaded build"
            )
    finally:
        for kind, identifier in (("explore", exploration_id), ("build", build_job_id)):
            if identifier is None:
                continue
            try:
                cli(kind, "cancel", identifier)
                predicate = (
                    exploration_finished
                    if kind == "explore"
                    else lambda value: value.get("status") in TERMINAL_BUILDS
                )
                wait_for((kind, "status", identifier), predicate, 120)
            except (
                RuntimeError,
                TypeError,
                TimeoutError,
                subprocess.TimeoutExpired,
            ) as error:
                summary["unsettled_operation"] = {"kind": kind, "id": identifier}
                checkpoint(summary)
                print(
                    f"Cleanup not confirmed for {kind} {identifier}; settle it before another writer starts: {error}",
                    file=sys.stderr,
                )
                raise


if __name__ == "__main__":
    signal.signal(signal.SIGTERM, lambda _signal, _frame: sys.exit(143))
    main()
