#!/usr/bin/env python3

from __future__ import annotations

import argparse
import json
import os
from pathlib import Path
from tempfile import TemporaryDirectory

from revyl import DeviceClient, RevylCLI


def pretty(value: object) -> str:
    return json.dumps(value, sort_keys=True)


def require(condition: bool, message: str) -> None:
    if not condition:
        raise RuntimeError(message)


def run_step(name: str, fn):
    print(f"RUN: {name}")
    result = fn()
    print(f"OK: {name}: {pretty(result)}")
    return result


def skip_step(name: str, reason: str) -> None:
    print(f"SKIP: {name}: {reason}")


def main() -> int:
    parser = argparse.ArgumentParser(
        description="Run a thin Revyl Python SDK smoke suite against production."
    )
    parser.add_argument("--platform", default="ios", choices=["ios", "android"])
    parser.add_argument(
        "--binary",
        default=None,
        help="Path to the local revyl binary to wrap (defaults to installed/bundled binary).",
    )
    parser.add_argument(
        "--grounded-text",
        action="store_true",
        help="Also test type_text/clear_text via Settings search grounding.",
    )
    parser.add_argument(
        "--app-url",
        default=None,
        help="Also test install_app using this disposable build URL.",
    )
    parser.add_argument(
        "--bundle-id",
        default=None,
        help="Bundle ID for launch_app after install_app.",
    )
    parser.add_argument(
        "--keep-session",
        action="store_true",
        help="Leave the started session running for debugging.",
    )
    args = parser.parse_args()

    require(bool(os.getenv("REVYL_API_KEY")), "REVYL_API_KEY is required")
    require(
        os.getenv("REVYL_BACKEND_URL", "https://backend.revyl.ai")
        == "https://backend.revyl.ai",
        "SDK prod smoke must target production backend",
    )
    require(
        not args.bundle_id or args.app_url,
        "--bundle-id requires --app-url",
    )

    repo_root = Path(__file__).resolve().parents[2]
    binary_path = args.binary
    if binary_path is None:
        local_build = repo_root / "build" / "revyl"
        if local_build.exists():
            binary_path = str(local_build)

    cli = RevylCLI(binary_path=binary_path)
    require(not hasattr(DeviceClient, "click"), "DeviceClient must not expose click")

    with TemporaryDirectory(prefix="revyl-sdk-prod-smoke-") as temp_dir:
        shot_path = str(Path(temp_dir) / "sdk-smoke.png")
        device = DeviceClient.start(platform=args.platform, timeout=600, cli=cli)

        try:
            info = run_step("info", lambda: device.info())
            require("workflow_run_id" in info, "info missing workflow_run_id")

            run_step("list_sessions", lambda: device.list_sessions())
            doctor_output = device.doctor()
            print("OK: doctor")
            print(doctor_output)

            shot = run_step("screenshot", lambda: device.screenshot(out=shot_path))
            require(Path(shot_path).exists(), "screenshot file missing")
            require(Path(shot_path).stat().st_size > 0, "screenshot file is empty")
            require("path" in shot or "bytes" in shot, "screenshot output missing fields")

            run_step("tap", lambda: device.tap(x=200, y=400))
            run_step("double_tap", lambda: device.double_tap(x=200, y=400))
            run_step("long_press", lambda: device.long_press(x=200, y=400, duration_ms=1500))
            run_step("swipe", lambda: device.swipe(x=200, y=560, direction="down", duration_ms=500))
            run_step("drag", lambda: device.drag(start_x=200, start_y=400, end_x=320, end_y=420))
            run_step("wait", lambda: device.wait(duration_ms=1000))
            run_step("pinch", lambda: device.pinch(x=200, y=400, scale=1.5, duration_ms=300))
            if args.platform == "android":
                run_step("back", lambda: device.back())
            else:
                skip_step("back", "Android-only action")
            run_step("key", lambda: device.key("ENTER"))
            run_step("shake", lambda: device.shake())
            run_step("go_home", lambda: device.go_home())
            run_step("open_app", lambda: device.open_app("settings"))
            run_step("navigate", lambda: device.navigate("https://example.com"))
            run_step(
                "set_location",
                lambda: device.set_location(37.7749, -122.4194),
            )
            run_step(
                "download_file",
                lambda: device.download_file("https://example.com"),
            )

            if args.grounded_text:
                run_step("tap Search", lambda: device.tap(target="Search"))
                run_step(
                    "type_text Search",
                    lambda: device.type_text(target="Search", text="wifi"),
                )
                run_step(
                    "clear_text Search",
                    lambda: device.clear_text(target="Search"),
                )

            if args.app_url:
                run_step(
                    "install_app",
                    lambda: device.install_app(
                        app_url=args.app_url,
                        bundle_id=args.bundle_id,
                    ),
                )
                if args.bundle_id:
                    run_step(
                        "launch_app",
                        lambda: device.launch_app(bundle_id=args.bundle_id),
                    )
                    run_step("kill_app", lambda: device.kill_app())

            print(f"PASS: SDK prod smoke completed for platform={args.platform}")
        finally:
            if args.keep_session:
                print(f"Leaving session {device.session_index} running (--keep-session)")
            elif device.session_index is not None:
                try:
                    cli.run("device", "stop", "-s", str(device.session_index))
                except Exception as exc:  # pragma: no cover - best effort cleanup
                    print(f"WARNING: failed to stop session cleanly: {exc}")

    return 0


if __name__ == "__main__":
    raise SystemExit(main())
