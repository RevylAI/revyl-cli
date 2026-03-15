"""
Revyl Device SDK.

Start cloud device sessions, interact with elements via AI-grounded targeting
or coordinates, and execute live test steps. Wraps the Revyl CLI binary and
returns parsed JSON responses for commands that support ``--json``.
"""

from __future__ import annotations

import json
import subprocess
from pathlib import Path
from typing import Any, Optional

from ._binary import ensure_binary

JSONScalar = str | int | float | bool | None
JSONValue = JSONScalar | list["JSONValue"] | dict[str, "JSONValue"]
JSONObject = dict[str, JSONValue]


class RevylError(RuntimeError):
    """Raised when a wrapped Revyl CLI command fails."""


class RevylCLI:
    """Low-level runner for the Revyl CLI binary."""

    def __init__(self, binary_path: Optional[str] = None) -> None:
        resolved = Path(binary_path) if binary_path else ensure_binary()
        self.binary_path = str(resolved)

    def run(self, *args: str, json_output: bool = False) -> Any:
        cmd = [self.binary_path, *args]
        if json_output:
            cmd.append("--json")

        result = subprocess.run(
            cmd,
            capture_output=True,
            text=True,
            check=False,
        )

        if result.returncode != 0:
            stderr = result.stderr.strip()
            stdout = result.stdout.strip()
            details = stderr or stdout or f"exit code {result.returncode}"
            raise RevylError(f"Command failed: {' '.join(cmd)}\n{details}")

        if not json_output:
            return result.stdout.strip()

        output = result.stdout.strip()
        if not output:
            return {}

        try:
            return json.loads(output)
        except json.JSONDecodeError as exc:
            parsed = self._parse_trailing_json(output)
            if parsed is not None:
                return parsed
            raise RevylError(
                f"Command returned non-JSON output: {' '.join(cmd)}\n{output}"
            ) from exc

    @staticmethod
    def _parse_trailing_json(output: str) -> Any:
        lines = output.strip().splitlines()
        for index, line in enumerate(lines):
            stripped = line.lstrip()
            if not stripped.startswith("{") and not stripped.startswith("["):
                continue
            candidate = "\n".join(lines[index:])
            try:
                return json.loads(candidate)
            except json.JSONDecodeError:
                continue
        return None


class DeviceClient:
    """
    Thin helper for Revyl `device` commands.

    Example:
        device = DeviceClient.start(platform="ios")
        device.tap(target="Sign In button")
        device.stop_session()
    """

    def __init__(self, cli: Optional[RevylCLI] = None, session_index: Optional[int] = None) -> None:
        self.cli = cli or RevylCLI()
        self.session_index = session_index

    @classmethod
    def start(
        cls,
        platform: str,
        timeout: Optional[int] = None,
        open_viewer: bool = False,
        app_id: Optional[str] = None,
        build_version_id: Optional[str] = None,
        app_url: Optional[str] = None,
        app_link: Optional[str] = None,
        cli: Optional[RevylCLI] = None,
    ) -> "DeviceClient":
        client = cls(cli=cli)
        client.start_session(
            platform=platform,
            timeout=timeout,
            open_viewer=open_viewer,
            app_id=app_id,
            build_version_id=build_version_id,
            app_url=app_url,
            app_link=app_link,
        )
        return client

    def __enter__(self) -> "DeviceClient":
        return self

    def __exit__(self, _exc_type: Any, _exc: Any, _tb: Any) -> None:
        self.close()

    def close(self) -> None:
        """Best-effort stop for the tracked session."""
        if self.session_index is None:
            return
        try:
            self.stop_session()
        except RevylError:
            pass

    def _session_args(self, session_index: Optional[int]) -> list[str]:
        idx = self.session_index if session_index is None else session_index
        if idx is None:
            return []
        return ["-s", str(idx)]

    def _target_or_coords_args(
        self,
        target: Optional[str],
        x: Optional[int],
        y: Optional[int],
    ) -> list[str]:
        if target and (x is not None or y is not None):
            raise ValueError("Provide target OR x/y, not both.")
        if target:
            return ["--target", target]
        if x is None or y is None:
            raise ValueError("Provide target or both x and y coordinates.")
        return ["--x", str(x), "--y", str(y)]

    def _live_step_args(
        self,
        command: str,
        value: str,
        session_index: Optional[int],
        *extra_args: str,
    ) -> list[str]:
        """Build CLI arguments for a live single-step device command.

        Args:
            command: Canonical device subcommand name to execute.
            value: Natural-language step text or script identifier.
            session_index: Optional device session index to target.
            *extra_args: Additional CLI flags appended before session selection.

        Returns:
            The full CLI argument vector for the live-step command.

        Raises:
            ValueError: If the provided step value is empty after trimming.
        """
        if not value.strip():
            raise ValueError(f"{command} value must not be empty.")
        return ["device", command, value, *extra_args, *self._session_args(session_index)]

    def start_session(
        self,
        platform: str,
        timeout: Optional[int] = None,
        open_viewer: bool = False,
        app_id: Optional[str] = None,
        build_version_id: Optional[str] = None,
        app_url: Optional[str] = None,
        app_link: Optional[str] = None,
    ) -> JSONObject:
        args = ["device", "start", "--platform", platform]
        if timeout is not None:
            args.extend(["--timeout", str(timeout)])
        if open_viewer:
            args.append("--open")
        if app_id:
            args.extend(["--app-id", app_id])
        if build_version_id:
            args.extend(["--build-version-id", build_version_id])
        if app_url:
            args.extend(["--app-url", app_url])
        if app_link:
            args.extend(["--app-link", app_link])

        result = self.cli.run(*args, json_output=True)
        if isinstance(result, dict):
            idx = result.get("index")
            if isinstance(idx, int):
                self.session_index = idx
            return result
        return {}

    def stop_session(self, session_index: Optional[int] = None) -> JSONObject:
        args = ["device", "stop", *self._session_args(session_index)]
        result = self.cli.run(*args, json_output=True)

        idx = self.session_index if session_index is None else session_index
        if idx is not None and idx == self.session_index:
            self.session_index = None
        return result if isinstance(result, dict) else {}

    def stop_all(self) -> JSONObject:
        self.session_index = None
        result = self.cli.run("device", "stop", "--all", json_output=True)
        return result if isinstance(result, dict) else {}

    def list_sessions(self) -> list[JSONObject]:
        result = self.cli.run("device", "list", json_output=True)
        return result if isinstance(result, list) else []

    def use_session(self, index: int) -> str:
        output = self.cli.run("device", "use", str(index), json_output=False)
        self.session_index = index
        return output

    def info(self, session_index: Optional[int] = None) -> JSONObject:
        args = ["device", "info", *self._session_args(session_index)]
        result = self.cli.run(*args, json_output=True)
        return result if isinstance(result, dict) else {}

    def doctor(self, session_index: Optional[int] = None) -> str:
        args = ["device", "doctor", *self._session_args(session_index)]
        return self.cli.run(*args, json_output=False)

    def screenshot(
        self,
        out: Optional[str] = None,
        session_index: Optional[int] = None,
    ) -> JSONObject:
        args = ["device", "screenshot", *self._session_args(session_index)]
        if out:
            args.extend(["--out", out])
        result = self.cli.run(*args, json_output=True)
        return result if isinstance(result, dict) else {}

    def tap(
        self,
        target: Optional[str] = None,
        x: Optional[int] = None,
        y: Optional[int] = None,
        session_index: Optional[int] = None,
    ) -> JSONObject:
        args = ["device", "tap", *self._target_or_coords_args(target, x, y), *self._session_args(session_index)]
        result = self.cli.run(*args, json_output=True)
        return result if isinstance(result, dict) else {}

    def double_tap(
        self,
        target: Optional[str] = None,
        x: Optional[int] = None,
        y: Optional[int] = None,
        session_index: Optional[int] = None,
    ) -> JSONObject:
        args = ["device", "double-tap", *self._target_or_coords_args(target, x, y), *self._session_args(session_index)]
        result = self.cli.run(*args, json_output=True)
        return result if isinstance(result, dict) else {}

    def long_press(
        self,
        target: Optional[str] = None,
        x: Optional[int] = None,
        y: Optional[int] = None,
        duration_ms: int = 1500,
        session_index: Optional[int] = None,
    ) -> JSONObject:
        args = [
            "device",
            "long-press",
            *self._target_or_coords_args(target, x, y),
            "--duration",
            str(duration_ms),
            *self._session_args(session_index),
        ]
        result = self.cli.run(*args, json_output=True)
        return result if isinstance(result, dict) else {}

    def type_text(
        self,
        text: str,
        target: Optional[str] = None,
        x: Optional[int] = None,
        y: Optional[int] = None,
        clear_first: bool = True,
        session_index: Optional[int] = None,
    ) -> JSONObject:
        args = [
            "device",
            "type",
            *self._target_or_coords_args(target, x, y),
            "--text",
            text,
            f"--clear-first={'true' if clear_first else 'false'}",
            *self._session_args(session_index),
        ]
        result = self.cli.run(*args, json_output=True)
        return result if isinstance(result, dict) else {}

    def swipe(
        self,
        direction: str,
        target: Optional[str] = None,
        x: Optional[int] = None,
        y: Optional[int] = None,
        duration_ms: int = 500,
        session_index: Optional[int] = None,
    ) -> JSONObject:
        args = [
            "device",
            "swipe",
            *self._target_or_coords_args(target, x, y),
            "--direction",
            direction,
            "--duration",
            str(duration_ms),
            *self._session_args(session_index),
        ]
        result = self.cli.run(*args, json_output=True)
        return result if isinstance(result, dict) else {}

    def drag(
        self,
        start_x: int,
        start_y: int,
        end_x: int,
        end_y: int,
        session_index: Optional[int] = None,
    ) -> JSONObject:
        args = [
            "device",
            "drag",
            "--start-x",
            str(start_x),
            "--start-y",
            str(start_y),
            "--end-x",
            str(end_x),
            "--end-y",
            str(end_y),
            *self._session_args(session_index),
        ]
        result = self.cli.run(*args, json_output=True)
        return result if isinstance(result, dict) else {}

    def install_app(
        self,
        app_url: str,
        bundle_id: Optional[str] = None,
        session_index: Optional[int] = None,
    ) -> JSONObject:
        args = ["device", "install", "--app-url", app_url, *self._session_args(session_index)]
        if bundle_id:
            args.extend(["--bundle-id", bundle_id])
        result = self.cli.run(*args, json_output=True)
        return result if isinstance(result, dict) else {}

    def launch_app(
        self,
        bundle_id: str,
        session_index: Optional[int] = None,
    ) -> JSONObject:
        args = ["device", "launch", "--bundle-id", bundle_id, *self._session_args(session_index)]
        result = self.cli.run(*args, json_output=True)
        return result if isinstance(result, dict) else {}

    def wait(
        self,
        duration_ms: int = 1000,
        session_index: Optional[int] = None,
    ) -> JSONObject:
        args = ["device", "wait", "--duration-ms", str(duration_ms), *self._session_args(session_index)]
        result = self.cli.run(*args, json_output=True)
        return result if isinstance(result, dict) else {}

    def pinch(
        self,
        target: Optional[str] = None,
        x: Optional[int] = None,
        y: Optional[int] = None,
        scale: float = 2.0,
        duration_ms: int = 300,
        session_index: Optional[int] = None,
    ) -> JSONObject:
        args = [
            "device",
            "pinch",
            *self._target_or_coords_args(target, x, y),
            "--scale",
            str(scale),
            "--duration",
            str(duration_ms),
            *self._session_args(session_index),
        ]
        result = self.cli.run(*args, json_output=True)
        return result if isinstance(result, dict) else {}

    def clear_text(
        self,
        target: Optional[str] = None,
        x: Optional[int] = None,
        y: Optional[int] = None,
        session_index: Optional[int] = None,
    ) -> JSONObject:
        args = ["device", "clear-text", *self._target_or_coords_args(target, x, y), *self._session_args(session_index)]
        result = self.cli.run(*args, json_output=True)
        return result if isinstance(result, dict) else {}

    def back(self, session_index: Optional[int] = None) -> JSONObject:
        args = ["device", "back", *self._session_args(session_index)]
        result = self.cli.run(*args, json_output=True)
        return result if isinstance(result, dict) else {}

    def key(self, key: str, session_index: Optional[int] = None) -> JSONObject:
        args = ["device", "key", "--key", key, *self._session_args(session_index)]
        result = self.cli.run(*args, json_output=True)
        return result if isinstance(result, dict) else {}

    def shake(self, session_index: Optional[int] = None) -> JSONObject:
        args = ["device", "shake", *self._session_args(session_index)]
        result = self.cli.run(*args, json_output=True)
        return result if isinstance(result, dict) else {}

    def go_home(self, session_index: Optional[int] = None) -> JSONObject:
        args = ["device", "home", *self._session_args(session_index)]
        result = self.cli.run(*args, json_output=True)
        return result if isinstance(result, dict) else {}

    def kill_app(self, session_index: Optional[int] = None) -> JSONObject:
        args = ["device", "kill-app", *self._session_args(session_index)]
        result = self.cli.run(*args, json_output=True)
        return result if isinstance(result, dict) else {}

    def open_app(
        self,
        app: str,
        session_index: Optional[int] = None,
    ) -> JSONObject:
        args = ["device", "open-app", "--app", app, *self._session_args(session_index)]
        result = self.cli.run(*args, json_output=True)
        return result if isinstance(result, dict) else {}

    def navigate(
        self,
        url: str,
        session_index: Optional[int] = None,
    ) -> JSONObject:
        args = ["device", "navigate", "--url", url, *self._session_args(session_index)]
        result = self.cli.run(*args, json_output=True)
        return result if isinstance(result, dict) else {}

    def set_location(
        self,
        latitude: float,
        longitude: float,
        session_index: Optional[int] = None,
    ) -> JSONObject:
        args = [
            "device",
            "set-location",
            "--lat",
            str(latitude),
            "--lon",
            str(longitude),
            *self._session_args(session_index),
        ]
        result = self.cli.run(*args, json_output=True)
        return result if isinstance(result, dict) else {}

    def download_file(
        self,
        url: str,
        filename: Optional[str] = None,
        session_index: Optional[int] = None,
    ) -> JSONObject:
        """Download a file onto the active device session.

        Args:
            url: Remote URL to download from.
            filename: Optional on-device filename override.
            session_index: Optional device session index to target.

        Returns:
            The CLI JSON response for the download request.
        """
        args = ["device", "download-file", "--url", url]
        if filename:
            args.extend(["--filename", filename])
        args.extend(self._session_args(session_index))
        result = self.cli.run(*args, json_output=True)
        return result if isinstance(result, dict) else {}

    def instruction(
        self,
        description: str,
        session_index: Optional[int] = None,
    ) -> JSONObject:
        """Execute one live instruction step against the active device.

        Args:
            description: Natural-language instruction to execute.
            session_index: Optional device session index to target.

        Returns:
            The CLI JSON response for the executed live step.

        Raises:
            ValueError: If the step description is empty after trimming.
        """
        args = self._live_step_args("instruction", description, session_index)
        result = self.cli.run(*args, json_output=True)
        return result if isinstance(result, dict) else {}

    def validation(
        self,
        description: str,
        session_index: Optional[int] = None,
    ) -> JSONObject:
        """Execute one live validation step against the active device.

        Args:
            description: Natural-language validation to execute.
            session_index: Optional device session index to target.

        Returns:
            The CLI JSON response for the executed live step.

        Raises:
            ValueError: If the step description is empty after trimming.
        """
        args = self._live_step_args("validation", description, session_index)
        result = self.cli.run(*args, json_output=True)
        return result if isinstance(result, dict) else {}

    def extract(
        self,
        description: str,
        variable_name: Optional[str] = None,
        session_index: Optional[int] = None,
    ) -> JSONObject:
        """Execute one live extract step against the active device.

        Args:
            description: Natural-language extraction instruction to execute.
            variable_name: Optional extraction variable name for downstream use.
            session_index: Optional device session index to target.

        Returns:
            The CLI JSON response for the executed live step.

        Raises:
            ValueError: If the step description is empty after trimming.
        """
        extra_args: list[str] = []
        if variable_name:
            extra_args.extend(["--variable-name", variable_name])
        args = self._live_step_args(
            "extract", description, session_index, *extra_args
        )
        result = self.cli.run(*args, json_output=True)
        return result if isinstance(result, dict) else {}

    def code_execution(
        self,
        script_id: str,
        session_index: Optional[int] = None,
    ) -> JSONObject:
        """Execute one live code-execution step against the active device.

        Args:
            script_id: Stable script identifier or code execution reference.
            session_index: Optional device session index to target.

        Returns:
            The CLI JSON response for the executed live step.

        Raises:
            ValueError: If the script identifier is empty after trimming.
        """
        args = self._live_step_args("code-execution", script_id, session_index)
        result = self.cli.run(*args, json_output=True)
        return result if isinstance(result, dict) else {}
