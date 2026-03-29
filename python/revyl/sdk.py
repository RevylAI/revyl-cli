"""
Revyl SDK.

Programmatic control of Revyl cloud devices, code-execution scripts,
reusable test modules, and build uploads.  Wraps the Revyl CLI binary and
returns parsed JSON responses for commands that support ``--json``.
"""

from __future__ import annotations

import json
import subprocess
import sys
import threading
import time
from pathlib import Path
from typing import Any, Literal, Optional

from ._binary import ensure_binary
from ._device_targets import DeviceModel, OsVersion

JSONScalar = str | int | float | bool | None
JSONValue = JSONScalar | list["JSONValue"] | dict[str, "JSONValue"]
JSONObject = dict[str, JSONValue]

Runtime = Literal["python", "javascript", "typescript", "bash"]
Platform = Literal["ios", "android"]
SwipeDirection = Literal["up", "down", "left", "right"]
KeyInput = Literal["ENTER", "BACKSPACE"]


# ---------------------------------------------------------------------------
# Progress spinner
# ---------------------------------------------------------------------------


class _Spinner:
    """Threaded CLI spinner for long-running operations.

    Writes braille-pattern animation frames to *stderr* so progress is
    visible without interfering with captured *stdout* JSON output.

    Args:
        message: Initial status text displayed next to the spinner.

    Example::

        with _Spinner("Provisioning device...") as s:
            do_slow_work()
            s.update("Still going... (10s)")
    """

    _FRAMES = ("⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏")

    def __init__(self, message: str) -> None:
        self._message = message
        self._stop = threading.Event()
        self._thread: Optional[threading.Thread] = None
        self._lock = threading.Lock()

    def __enter__(self) -> "_Spinner":
        self.start()
        return self

    def __exit__(self, *_: Any) -> None:
        self.stop()

    def start(self) -> None:
        """Begin animating the spinner in a daemon thread."""
        if self._thread is not None:
            return
        self._stop.clear()
        self._thread = threading.Thread(target=self._animate, daemon=True)
        self._thread.start()

    def stop(self, final_message: Optional[str] = None) -> None:
        """Stop the spinner and optionally print a final status line.

        Args:
            final_message: If provided, printed as a completed-status line
                after clearing the spinner.
        """
        self._stop.set()
        if self._thread is not None:
            self._thread.join()
            self._thread = None
        self._clear_line()
        if final_message:
            sys.stderr.write(f"{final_message}\n")
            sys.stderr.flush()

    def update(self, message: str) -> None:
        """Replace the spinner text while it continues animating.

        Args:
            message: New status text to display.
        """
        with self._lock:
            self._message = message

    def _animate(self) -> None:
        idx = 0
        while not self._stop.is_set():
            frame = self._FRAMES[idx % len(self._FRAMES)]
            with self._lock:
                text = self._message
            sys.stderr.write(f"\r\033[K{frame} {text}")
            sys.stderr.flush()
            idx += 1
            self._stop.wait(0.08)

    @staticmethod
    def _clear_line() -> None:
        sys.stderr.write("\r\033[K")
        sys.stderr.flush()


class RevylError(RuntimeError):
    """Raised when a wrapped Revyl CLI command fails."""


class RevylCLI:
    """Low-level runner for the Revyl CLI binary.

    Args:
        binary_path: Explicit path to the ``revyl`` binary.
            Auto-resolved via ``ensure_binary()`` when omitted.
        dev_mode: When ``True``, prepend ``--dev`` to every command
            so the CLI targets local development servers.
    """

    def __init__(
        self,
        binary_path: Optional[str] = None,
        dev_mode: bool = False,
    ) -> None:
        resolved = (
            Path(binary_path) if binary_path else ensure_binary()
        )
        self.binary_path = str(resolved)
        self.dev_mode = dev_mode

    def run(self, *args: str, json_output: bool = False) -> Any:
        cmd = [self.binary_path]
        if self.dev_mode:
            cmd.append("--dev")
        cmd.extend(args)
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
    """Thin helper for Revyl ``device`` commands.

    Example::

        with DeviceClient.start(platform="ios", app_url=url) as device:
            device.screenshot(out="screen.png")
            device.tap(target="Sign In button")
            # Report URL auto-prints on exit.

    By default ``start()`` blocks until the device is API-ready.
    Pass ``wait_for_ready=False`` for fire-and-forget provisioning.
    """

    def __init__(
        self,
        cli: Optional[RevylCLI] = None,
        session_index: Optional[int] = None,
        auto_report: bool = True,
        verbose: bool = True,
    ) -> None:
        self.cli = cli or RevylCLI()
        self.session_index = session_index
        self._auto_report = auto_report
        self._verbose = verbose
        self._screen_width: int = 0
        self._screen_height: int = 0

    @property
    def screen_width(self) -> int:
        """Device screen width in pixels (0 when unknown)."""
        return self._screen_width

    @property
    def screen_height(self) -> int:
        """Device screen height in pixels (0 when unknown)."""
        return self._screen_height

    def device_info(self) -> JSONObject:
        """Return session metadata including screen dimensions.

        Returns:
            Dict with session_index, screen_width, screen_height, and
            the full ``device info`` JSON from the CLI.
        """
        args = ["device", "info", *self._session_args(self.session_index)]
        result = self.cli.run(*args, json_output=True)
        info = result if isinstance(result, dict) else {}
        if self._screen_width > 0:
            info["screen_width"] = self._screen_width
        if self._screen_height > 0:
            info["screen_height"] = self._screen_height
        return info

    @classmethod
    def start(
        cls,
        platform: Platform,
        timeout: Optional[int] = None,
        open_viewer: bool = False,
        app_id: Optional[str] = None,
        build_version_id: Optional[str] = None,
        app_url: Optional[str] = None,
        app_link: Optional[str] = None,
        device_model: Optional[DeviceModel] = None,
        os_version: Optional[OsVersion] = None,
        device_name: Optional[str] = None,
        cli: Optional[RevylCLI] = None,
        wait_for_ready: bool = True,
        ready_timeout: float = 60,
        auto_report: bool = True,
        verbose: bool = True,
    ) -> "DeviceClient":
        """Provision a cloud device and return a connected client.

        Args:
            platform: Target platform (ignored when *device_name* is set).
            timeout: Session timeout in seconds.
            open_viewer: Open the live viewer in a browser.
            app_id: Revyl app UUID to install.
            build_version_id: Specific build version UUID.
            app_url: Direct URL to an ``.apk`` or ``.app`` archive.
            app_link: Deep-link URL to open after install.
            device_model: Target device model. Must be paired with *os_version*.
            os_version: Target OS version. Must be paired with *device_model*.
            device_name: Named device preset (e.g. "revyl-android-phone").
                Overrides *platform*, *device_model*, and *os_version*.
            cli: Optional CLI instance (uses default if omitted).
            wait_for_ready: Block until the device is API-ready (default
                ``True``).  Set to ``False`` for fire-and-forget provisioning;
                call ``wait_for_device_ready()`` later when needed.
            ready_timeout: Maximum seconds to wait for device readiness when
                *wait_for_ready* is ``True``.
            auto_report: Automatically fetch and print the report URL when
                the session is closed via ``close()`` or the context manager.
            verbose: Show an animated spinner and status messages on *stderr*
                during provisioning and health checks.  Set to ``False`` for
                silent operation (e.g. CI pipelines).

        Returns:
            A connected ``DeviceClient`` with an active session.

        Raises:
            ValueError: If only one of *device_model* / *os_version* is given
                without using *device_name*.
            RevylError: If the underlying CLI command fails or the device does
                not become ready within *ready_timeout*.
        """
        client = cls(cli=cli, auto_report=auto_report, verbose=verbose)
        client.start_session(
            platform=platform,
            timeout=timeout,
            open_viewer=open_viewer,
            app_id=app_id,
            build_version_id=build_version_id,
            app_url=app_url,
            app_link=app_link,
            device_model=device_model,
            os_version=os_version,
            device_name=device_name,
        )
        if wait_for_ready:
            if not client.wait_for_device_ready(timeout=ready_timeout):
                raise RevylError(
                    "Device did not become ready within "
                    f"{ready_timeout}s. Run device_doctor() to diagnose."
                )
        return client

    def __enter__(self) -> "DeviceClient":
        return self

    def __exit__(self, _exc_type: Any, _exc: Any, _tb: Any) -> None:
        self.close()

    def close(self) -> None:
        """Best-effort stop for the tracked session.

        When *auto_report* is enabled, fetches and prints the report and
        video URLs before stopping the session.
        """
        if self.session_index is None:
            return
        if self._auto_report:
            try:
                report_data = self.report()
                report_url = report_data.get("report_url")
                video_url = report_data.get("video_url")
                if report_url:
                    print(f"\n  Report: {report_url}")
                if video_url:
                    print(f"  Video:  {video_url}")
            except RevylError:
                pass
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
        platform: Platform,
        timeout: Optional[int] = None,
        open_viewer: bool = False,
        app_id: Optional[str] = None,
        build_version_id: Optional[str] = None,
        app_url: Optional[str] = None,
        app_link: Optional[str] = None,
        device_model: Optional[DeviceModel] = None,
        os_version: Optional[OsVersion] = None,
        device_name: Optional[str] = None,
    ) -> JSONObject:
        """Start a new device session and track its index.

        Args:
            platform: Target platform (ignored when *device_name* is set).
            timeout: Session timeout in seconds.
            open_viewer: Open the live viewer in a browser.
            app_id: Revyl app UUID to install.
            build_version_id: Specific build version UUID.
            app_url: Direct URL to an ``.apk`` or ``.app`` archive.
            app_link: Deep-link URL to open after install.
            device_model: Target device model. Must be paired with *os_version*.
            os_version: Target OS version. Must be paired with *device_model*.
            device_name: Named device preset (e.g. "revyl-android-phone",
                "revyl-ios-iphone"). Overrides *platform*, *device_model*,
                and *os_version*.

        Returns:
            CLI JSON response with session metadata including
            ``screen_width`` and ``screen_height``.

        Raises:
            ValueError: If only one of *device_model* / *os_version* is given
                without using *device_name*.
        """
        if not device_name and bool(device_model) != bool(os_version):
            raise ValueError(
                "--device-model and --os-version must both be provided"
            )

        args = ["device", "start", "--platform", platform]
        if device_name:
            args.extend(["--device-name", device_name])
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
        if not device_name and device_model and os_version:
            args.extend(["--device-model", device_model, "--os-version", os_version])

        if self._verbose:
            with _Spinner(f"Provisioning {platform} device..."):
                result = self.cli.run(*args, json_output=True)
        else:
            result = self.cli.run(*args, json_output=True)

        if isinstance(result, dict):
            idx = result.get("index")
            if isinstance(idx, int):
                self.session_index = idx
            sw = result.get("screen_width")
            sh = result.get("screen_height")
            if isinstance(sw, int) and sw > 0:
                self._screen_width = sw
            if isinstance(sh, int) and sh > 0:
                self._screen_height = sh
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
        direction: SwipeDirection,
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
        app_url: Optional[str] = None,
        build_version_id: Optional[str] = None,
        bundle_id: Optional[str] = None,
        session_index: Optional[int] = None,
    ) -> JSONObject:
        """Install an app on the active device session.

        Exactly one of *app_url* or *build_version_id* must be provided.
        When *build_version_id* is used, the CLI resolves the download URL
        automatically from a previously uploaded build.

        Args:
            app_url: Direct URL to an ``.apk`` or ``.ipa`` archive.
            build_version_id: Build version UUID from a prior upload.
            bundle_id: Optional bundle/package ID (auto-detected if omitted).
            session_index: Optional device session index to target.

        Returns:
            The CLI JSON response for the install request.

        Raises:
            ValueError: If neither or both of *app_url* / *build_version_id*
                are provided.
        """
        if bool(app_url) == bool(build_version_id):
            raise ValueError(
                "Provide exactly one of app_url or build_version_id."
            )
        args: list[str] = ["device", "install"]
        if app_url:
            args.extend(["--app-url", app_url])
        if build_version_id:
            args.extend(["--build-version-id", build_version_id])
        args.extend(self._session_args(session_index))
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

    def key(self, key: KeyInput, session_index: Optional[int] = None) -> JSONObject:
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
        script_id: Optional[str] = None,
        file_path: Optional[str] = None,
        code: Optional[str] = None,
        runtime: Optional[Runtime] = None,
        session_index: Optional[int] = None,
    ) -> JSONObject:
        """Execute a code-execution step against the active device.

        Exactly one of *script_id*, *file_path*, or *code* must be provided.
        When using *file_path* or *code*, a *runtime* is required.

        Args:
            script_id: Saved script name or UUID.
            file_path: Path to a local source file to execute on device.
            code: Inline code string to execute on device.
            runtime: Runtime for *file_path* / *code*.
            session_index: Optional device session index to target.

        Returns:
            The CLI JSON response for the executed step.

        Raises:
            ValueError: If zero or more than one source is given, or if
                *runtime* is missing when required.
        """
        sources = sum(1 for s in (script_id, file_path, code) if s)
        if sources != 1:
            raise ValueError(
                "Provide exactly one of script_id, file_path, or code."
            )

        if script_id:
            args = self._live_step_args(
                "code-execution", script_id, session_index
            )
        elif file_path:
            if not runtime:
                raise ValueError("runtime is required when using file_path.")
            args = [
                "device", "code-execution",
                "--file", file_path,
                "--runtime", runtime,
                *self._session_args(session_index),
            ]
        else:
            if not runtime:
                raise ValueError("runtime is required when using code.")
            args = [
                "device", "code-execution",
                "--code", code,  # type: ignore[arg-type]
                "--runtime", runtime,
                *self._session_args(session_index),
            ]

        result = self.cli.run(*args, json_output=True)
        return result if isinstance(result, dict) else {}

    # -- Reporting & discovery ---------------------------------------------------

    def report(self, session_index: Optional[int] = None) -> JSONObject:
        """Fetch the session report (status, steps, video URL, report URL).

        Args:
            session_index: Optional device session index to target.

        Returns:
            Dict with keys like ``session_id``, ``session_status``,
            ``total_steps``, ``passed_steps``, ``failed_steps``,
            ``video_url``, ``report_url``, and ``steps``.
        """
        args = ["device", "report", *self._session_args(session_index)]
        result = self.cli.run(*args, json_output=True)
        return result if isinstance(result, dict) else {}

    @staticmethod
    def targets(platform: Optional[Platform] = None, cli: Optional["RevylCLI"] = None) -> JSONObject:
        """List available device models and OS versions.

        Args:
            platform: Optional platform filter.
            cli: Optional CLI instance (uses default if omitted).

        Returns:
            Dict mapping platform names to lists of device/OS pairs.
        """
        _cli = cli or RevylCLI()
        args = ["device", "targets"]
        if platform:
            args.extend(["--platform", platform])
        result = _cli.run(*args, json_output=True)
        return result if isinstance(result, dict) else {}

    @staticmethod
    def history(limit: int = 20, cli: Optional["RevylCLI"] = None) -> list[JSONObject]:
        """Show recent device session history from the server.

        Args:
            limit: Maximum number of sessions to return (default 20).
            cli: Optional CLI instance (uses default if omitted).

        Returns:
            List of session history entries.
        """
        _cli = cli or RevylCLI()
        args = ["device", "history", "--limit", str(limit)]
        result = _cli.run(*args, json_output=True)
        return result if isinstance(result, list) else []

    def wait_for_stream(self, timeout: float = 30, poll_interval: float = 2) -> Optional[str]:
        """Poll ``info()`` until ``whep_url`` is available.

        Use this when you need the WebRTC WHEP URL for building live
        viewers or streaming integrations.  Not called automatically by
        ``start()``; opt-in only.

        Args:
            timeout: Maximum seconds to wait before giving up.
            poll_interval: Seconds between polls.

        Returns:
            The WHEP stream URL, or ``None`` if the timeout expired.
        """
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            info = self.info()
            whep_url = info.get("whep_url")
            if whep_url:
                return str(whep_url)
            time.sleep(poll_interval)
        return None

    def wait_for_device_ready(self, timeout: float = 60, poll_interval: float = 3) -> bool:
        """Poll ``device doctor`` until the worker reports the device as connected.

        Called automatically by ``start(wait_for_ready=True)``.  Can also be
        called manually after fire-and-forget provisioning or to re-check
        readiness mid-session.

        Args:
            timeout: Maximum seconds to wait before giving up.
            poll_interval: Seconds between health probes.

        Returns:
            ``True`` if the device became ready, ``False`` if the timeout
            expired without the device passing all checks.
        """
        start_time = time.monotonic()
        deadline = start_time + timeout
        spinner: Optional[_Spinner] = None
        if self._verbose:
            spinner = _Spinner("Waiting for device to become ready...")
            spinner.start()
        try:
            while time.monotonic() < deadline:
                elapsed = time.monotonic() - start_time
                if spinner:
                    spinner.update(
                        f"Waiting for device to become ready... ({elapsed:.0f}s elapsed)"
                    )
                try:
                    result = self.cli.run(
                        "device", "doctor",
                        *self._session_args(self.session_index),
                        json_output=True,
                    )
                    if isinstance(result, dict) and result.get("all_passed"):
                        if spinner:
                            spinner.stop(
                                f"Device ready ({elapsed:.0f}s)"
                            )
                            spinner = None
                        return True
                except RevylError:
                    pass
                time.sleep(poll_interval)
            return False
        finally:
            if spinner:
                spinner.stop()

    def wait_for_report(self, timeout: float = 30, poll_interval: float = 2) -> JSONObject:
        """Poll until the session report is generated and return it.

        Useful for programmatic access to the report URL and step results
        after actions have completed.

        Args:
            timeout: Maximum seconds to wait for the report.
            poll_interval: Seconds between polls.

        Returns:
            Dict with ``report_url``, ``video_url``, ``steps``, etc.

        Raises:
            RevylError: If the report is not available within *timeout*.
        """
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            try:
                result = self.report()
                if result.get("report_url"):
                    return result
            except RevylError:
                pass
            time.sleep(poll_interval)
        raise RevylError(
            f"Report not available within {timeout}s"
        )


# ---------------------------------------------------------------------------
# Script management
# ---------------------------------------------------------------------------


class ScriptClient:
    """CRUD helper for Revyl code-execution scripts.

    Example::

        scripts = ScriptClient()
        scripts.create("seed-db", file_path="seed.py", runtime="python")
        all_scripts = scripts.list()
    """

    def __init__(self, cli: Optional[RevylCLI] = None) -> None:
        self.cli = cli or RevylCLI()

    def list(self, runtime: Optional[Runtime] = None) -> list[JSONObject]:
        """List all code-execution scripts, optionally filtered by runtime.

        Args:
            runtime: Filter by runtime.

        Returns:
            List of script summary dicts.
        """
        args = ["script", "list"]
        if runtime:
            args.extend(["--runtime", runtime])
        result = self.cli.run(*args, json_output=True)
        return result if isinstance(result, list) else []

    def get(self, name_or_id: str) -> JSONObject:
        """Get a script by name or ID, including its source code.

        Args:
            name_or_id: Script name or UUID.

        Returns:
            Dict with script details and ``code`` field.
        """
        result = self.cli.run("script", "get", name_or_id, json_output=True)
        return result if isinstance(result, dict) else {}

    def create(
        self,
        name: str,
        file_path: str,
        runtime: Runtime,
        description: Optional[str] = None,
    ) -> JSONObject:
        """Create a new code-execution script from a local file.

        Args:
            name: Display name for the script.
            file_path: Path to the source file.
            runtime: Runtime environment for the script.
            description: Optional human-readable description.

        Returns:
            Dict with the created script's metadata.
        """
        args = ["script", "create", name, "--file", file_path, "--runtime", runtime]
        if description:
            args.extend(["--description", description])
        result = self.cli.run(*args, json_output=True)
        return result if isinstance(result, dict) else {}

    def update(
        self,
        name_or_id: str,
        file_path: Optional[str] = None,
        name: Optional[str] = None,
        description: Optional[str] = None,
    ) -> JSONObject:
        """Update an existing script's name, description, or code.

        Args:
            name_or_id: Current script name or UUID.
            file_path: Path to a new source file.
            name: New display name.
            description: New description.

        Returns:
            Dict with the updated script's metadata.
        """
        args = ["script", "update", name_or_id]
        if file_path:
            args.extend(["--file", file_path])
        if name:
            args.extend(["--name", name])
        if description:
            args.extend(["--description", description])
        result = self.cli.run(*args, json_output=True)
        return result if isinstance(result, dict) else {}

    def delete(self, name_or_id: str, force: bool = True) -> str:
        """Delete a script by name or ID.

        Args:
            name_or_id: Script name or UUID.
            force: Skip confirmation prompt (default ``True``).

        Returns:
            CLI confirmation text.
        """
        args = ["script", "delete", name_or_id]
        if force:
            args.append("--force")
        return self.cli.run(*args, json_output=False)

    def usage(self, name_or_id: str) -> list[JSONObject]:
        """List tests that reference a script.

        Args:
            name_or_id: Script name or UUID.

        Returns:
            List of test summary dicts.
        """
        result = self.cli.run("script", "usage", name_or_id, json_output=True)
        return result if isinstance(result, list) else []


# ---------------------------------------------------------------------------
# Module management
# ---------------------------------------------------------------------------


class ModuleClient:
    """CRUD helper for Revyl reusable test modules.

    Example::

        modules = ModuleClient()
        modules.create("login-flow", blocks_file="modules/login.yaml")
        all_modules = modules.list()
    """

    def __init__(self, cli: Optional[RevylCLI] = None) -> None:
        self.cli = cli or RevylCLI()

    def list(self, search: Optional[str] = None) -> list[JSONObject]:
        """List all modules, optionally filtered by name or description.

        Args:
            search: Substring filter applied to name and description.

        Returns:
            List of module summary dicts.
        """
        args = ["module", "list"]
        if search:
            args.extend(["--search", search])
        result = self.cli.run(*args, json_output=True)
        return result if isinstance(result, list) else []

    def get(self, name_or_id: str) -> JSONObject:
        """Get a module by name or ID, including its blocks.

        Args:
            name_or_id: Module name or UUID.

        Returns:
            Dict with module details and ``blocks`` list.
        """
        result = self.cli.run("module", "get", name_or_id, json_output=True)
        return result if isinstance(result, dict) else {}

    def create(
        self,
        name: str,
        blocks_file: str,
        description: Optional[str] = None,
    ) -> JSONObject:
        """Create a reusable module from a YAML blocks file.

        Args:
            name: Display name for the module.
            blocks_file: Path to a YAML file with a top-level ``blocks`` array.
            description: Optional human-readable description.

        Returns:
            Dict with the created module's metadata.
        """
        args = ["module", "create", name, "--from-file", blocks_file]
        if description:
            args.extend(["--description", description])
        result = self.cli.run(*args, json_output=True)
        return result if isinstance(result, dict) else {}

    def update(
        self,
        name_or_id: str,
        name: Optional[str] = None,
        blocks_file: Optional[str] = None,
        description: Optional[str] = None,
    ) -> JSONObject:
        """Update a module's name, description, or blocks.

        Args:
            name_or_id: Current module name or UUID.
            name: New display name.
            blocks_file: Path to a YAML file with the new blocks array.
            description: New description.

        Returns:
            Dict with the updated module's metadata.
        """
        args = ["module", "update", name_or_id]
        if name:
            args.extend(["--name", name])
        if blocks_file:
            args.extend(["--from-file", blocks_file])
        if description:
            args.extend(["--description", description])
        result = self.cli.run(*args, json_output=True)
        return result if isinstance(result, dict) else {}

    def delete(self, name_or_id: str, force: bool = True) -> str:
        """Delete a module by name or ID.

        Args:
            name_or_id: Module name or UUID.
            force: Skip confirmation prompt (default ``True``).

        Returns:
            CLI confirmation text.

        Raises:
            RevylError: If the module is still referenced by tests (HTTP 409).
        """
        args = ["module", "delete", name_or_id]
        if force:
            args.append("--force")
        return self.cli.run(*args, json_output=False)

    def usage(self, name_or_id: str) -> list[JSONObject]:
        """List tests that import a module.

        Args:
            name_or_id: Module name or UUID.

        Returns:
            List of test summary dicts.
        """
        result = self.cli.run("module", "usage", name_or_id, json_output=True)
        return result if isinstance(result, list) else []


# ---------------------------------------------------------------------------
# Build management
# ---------------------------------------------------------------------------


class BuildClient:
    """Helper for uploading and managing Revyl app builds.

    Example::

        builds = BuildClient()
        builds.upload("my-app", platform="android")
        all_builds = builds.list()
    """

    def __init__(self, cli: Optional[RevylCLI] = None) -> None:
        self.cli = cli or RevylCLI()

    def upload(
        self,
        app_name: Optional[str] = None,
        platform: Optional[Platform] = None,
        skip_build: bool = False,
        version: Optional[str] = None,
        set_current: bool = False,
    ) -> JSONObject:
        """Build and upload an app to Revyl.

        Args:
            app_name: App name (used when creating a new app on first upload).
            platform: Target platform.
            skip_build: If ``True``, skip the local build step and upload
                the existing artifact.
            version: Explicit version string (auto-generated if omitted).
            set_current: Mark this version as the current version.

        Returns:
            Dict with upload result including ``app_id`` and ``build_version_id``.
        """
        args = ["build", "upload"]
        if app_name:
            args.extend(["--name", app_name])
        if platform:
            args.extend(["--platform", platform])
        if skip_build:
            args.append("--skip-build")
        if version:
            args.extend(["--version", version])
        if set_current:
            args.append("--set-current")
        args.append("--yes")
        result = self.cli.run(*args, json_output=True)
        return result if isinstance(result, dict) else {}

    def list(self, app_name: Optional[str] = None, platform: Optional[Platform] = None) -> list[JSONObject]:
        """List uploaded build versions.

        Args:
            app_name: Optional app ID or name to filter by.
            platform: Optional platform filter.

        Returns:
            List of build version dicts.
        """
        args = ["build", "list"]
        if app_name:
            args.extend(["--app", app_name])
        if platform:
            args.extend(["--platform", platform])
        result = self.cli.run(*args, json_output=True)
        return result if isinstance(result, list) else []

    def delete(self, name_or_id: str, version: Optional[str] = None, force: bool = True) -> str:
        """Delete an app or a specific build version.

        Args:
            name_or_id: App name or UUID.
            version: If provided, delete only this build version.
            force: Skip confirmation prompt (default ``True``).

        Returns:
            CLI confirmation text.
        """
        args = ["build", "delete", name_or_id]
        if version:
            args.extend(["--version", version])
        if force:
            args.append("--force")
        return self.cli.run(*args, json_output=False)
