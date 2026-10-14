#!/usr/bin/env python3
"""Exercise GitLab commands against a real local preview and fixture repository."""

import argparse
import json
import os
from pathlib import Path
import pty
import select
import stat
import subprocess
import termios
import time
from urllib.parse import urlparse


def require(condition: bool, message: str) -> None:
    if not condition:
        raise RuntimeError(message)


def decode_object(output: bytes) -> dict[str, object]:
    decoded: object = json.loads(output)
    if not isinstance(decoded, dict) or any(
        not isinstance(key, str) for key in decoded
    ):
        raise RuntimeError("CLI must emit one JSON object")
    return {key: value for key, value in decoded.items()}


def run_terminal(
    command: list[str],
    environment: dict[str, str],
    prompt: bytes,
    answer: bytes,
    *,
    secret: bytes,
) -> bytes:
    master, slave = pty.openpty()
    original_terminal_state = termios.tcgetattr(slave)
    original_terminal_state[3] &= ~termios.PENDIN
    output = bytearray()
    answered = False
    deadline = time.monotonic() + 90
    process = subprocess.Popen(
        command,
        env=environment,
        stdin=slave,
        stdout=slave,
        stderr=slave,
    )
    try:
        while time.monotonic() < deadline:
            if select.select([master], [], [], 0.1)[0]:
                output.extend(os.read(master, 65536))
            if not answered and prompt in output:
                if answer == secret:
                    require(
                        not termios.tcgetattr(slave)[3] & termios.ECHO,
                        "Token prompt enabled terminal echo",
                    )
                os.write(master, answer + b"\n")
                answered = True
            if process.poll() is not None:
                while select.select([master], [], [], 0)[0]:
                    output.extend(os.read(master, 65536))
                break
        require(process.poll() is not None, "Interactive command timed out")
        require(secret not in output, "Interactive command disclosed the token")
        require(answered, "Expected interactive prompt was not reached")
        require(process.returncode == 0, "Interactive command failed")
        restored_terminal_state = termios.tcgetattr(slave)
        # The kernel can set PENDIN when canonical input resumes.
        restored_terminal_state[3] &= ~termios.PENDIN
        require(
            restored_terminal_state == original_terminal_state,
            "Interactive command did not restore terminal settings",
        )
        return bytes(output)
    finally:
        if process.poll() is None:
            process.kill()
        process.wait(timeout=5)
        os.close(master)
        os.close(slave)


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--cli", type=Path, required=True)
    parser.add_argument("--project", type=Path, required=True)
    parser.add_argument("--token-file", type=Path, required=True)
    parser.add_argument("--backend-url", required=True)
    parser.add_argument("--app-url", required=True)
    parser.add_argument("--allow-connection-replacement", action="store_true")
    args = parser.parse_args()
    require(
        args.allow_connection_replacement,
        "Explicit connection replacement approval is required",
    )
    for address in (args.backend_url, args.app_url):
        parsed = urlparse(address)
        require(
            parsed.scheme == "http"
            and parsed.hostname in {"localhost", "127.0.0.1"}
            and parsed.username is None
            and not parsed.path
            and not parsed.query
            and not parsed.fragment
            and parsed.port is not None,
            "This test requires explicit local preview URLs with ports",
        )
    token_metadata = args.token_file.lstat()
    require(
        stat.S_ISREG(token_metadata.st_mode)
        and token_metadata.st_uid == os.getuid()
        and not token_metadata.st_mode & 0o077,
        "Token file must be a private regular file owned by the current user",
    )
    secret = args.token_file.read_bytes().rstrip(b"\r\n")
    require(
        0 < len(secret) <= 8192 and not any(chr(byte).isspace() for byte in secret),
        "Invalid token file",
    )
    environment: dict[str, str] = {
        **os.environ,
        "REVYL_BACKEND_URL": args.backend_url,
        "REVYL_APP_URL": args.app_url,
        "NO_COLOR": "1",
    }
    base = [str(args.cli.resolve()), "-C", str(args.project.resolve())]

    def run(*arguments: str, stdin: bytes = b"", success: bool = True) -> bytes:
        result = subprocess.run(
            [*base, *arguments],
            env=environment,
            input=stdin,
            capture_output=True,
            timeout=90,
            check=False,
        )
        require(secret not in result.stdout + result.stderr, "CLI disclosed the token")
        require(
            (result.returncode == 0) == success,
            f"Unexpected exit status for {' '.join(arguments)}",
        )
        return result.stdout

    def status() -> dict[str, object]:
        report = decode_object(run("gitlab", "status", "--json"))
        require(
            report.get("enabled") is True and report.get("connected") is True,
            "Live API access failed",
        )
        repository = report.get("repository")
        project = report.get("project")
        webhook = report.get("webhook")
        require(
            isinstance(repository, dict) and repository.get("access_granted") is True,
            "Live repository access failed",
        )
        require(
            isinstance(project, dict)
            and project.get("status") == "enabled"
            and project.get("authority") == "manual",
            "Fixture must have a complete manual project configuration",
        )
        if not isinstance(webhook, dict) or not webhook.get("verified_at"):
            raise RuntimeError("Fixture needs authenticated webhook delivery")
        report["webhook"] = {
            "configured": webhook.get("configured"),
            "verified": bool(webhook.get("verified_at")),
        }
        return report

    baseline = status()
    print(
        "PASS: live API, repository, project and authenticated delivery status",
        flush=True,
    )
    output = run_terminal(
        [*base, "gitlab", "connect"],
        environment,
        b"GitLab automation token (hidden):",
        secret,
        secret=secret,
    )
    require(
        b"Connected to GitLab at" in output,
        "Hidden-prompt connection was not confirmed",
    )
    print(
        "PASS: hidden terminal token entry, echo disabled, real connection saved",
        flush=True,
    )
    connection = decode_object(
        run("gitlab", "connect", "--token-stdin", "--json", stdin=secret + b"\n")
    )
    require(connection.get("connected") is True, "Stdin connection was not confirmed")
    connected_status = status()
    require(
        all(
            connected_status.get(key) == baseline.get(key)
            for key in ("instance_url", "repository", "project", "webhook")
        ),
        "Connecting changed the fixture repository, project or webhook",
    )
    baseline = connected_status
    print("PASS: token-stdin connection and secret-free JSON", flush=True)
    run(
        "gitlab",
        "connect",
        "--token-stdin",
        stdin=b"intentionally-invalid-token\n",
        success=False,
    )
    require(
        status() == baseline,
        "Rejected replacement changed the saved connection or project",
    )
    print(
        "PASS: GitLab rejected invalid replacement and retained healthy connection",
        flush=True,
    )
    for attempt in range(2):
        output = run_terminal(
            [*base, "gitlab", "setup"],
            environment,
            b"Publish this complete project configuration to Revyl?",
            b"y",
            secret=secret,
        )
        require(
            b"Revyl already has this project configuration" in output,
            "Setup changed the existing configuration",
        )
        require(
            b"GitLab webhook delivery verified" in output,
            "Setup did not verify delivery",
        )
        require(status() == baseline, "Setup changed the existing fixture state")
        print(
            f"PASS: interactive setup {attempt + 1}, existing config and verified webhook",
            flush=True,
        )


if __name__ == "__main__":
    main()
