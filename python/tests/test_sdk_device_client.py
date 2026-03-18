from __future__ import annotations

import subprocess
import unittest
from unittest import mock

from revyl.sdk import DeviceClient, RevylCLI, RevylError


class _FakeCLI:
    def __init__(self) -> None:
        self.calls: list[tuple[tuple[str, ...], bool]] = []
        self.json_responses: list[object] = []
        self.text_responses: list[str] = []

    def run(self, *args: str, json_output: bool = False):
        self.calls.append((args, json_output))
        if json_output:
            if self.json_responses:
                return self.json_responses.pop(0)
            return {}
        if self.text_responses:
            return self.text_responses.pop(0)
        return ""


class DeviceClientParityTests(unittest.TestCase):
    def setUp(self) -> None:
        self.cli = _FakeCLI()
        self.client = DeviceClient(cli=self.cli, session_index=7, verbose=False)

    def _last_call(self) -> tuple[tuple[str, ...], bool]:
        self.assertTrue(self.cli.calls, "no CLI calls recorded")
        return self.cli.calls[-1]

    def _assert_last_json_call(self, expected_args: tuple[str, ...]) -> None:
        args, json_output = self._last_call()
        self.assertEqual(args, expected_args)
        self.assertTrue(json_output)

    def test_start_classmethod_tracks_session_index(self) -> None:
        self.cli.json_responses.append({"index": 3})

        client = DeviceClient.start(
            platform="android",
            timeout=600,
            open_viewer=True,
            app_id="app_123",
            build_version_id="build_456",
            app_url="https://example.com/app.apk",
            app_link="myapp://deep-link",
            cli=self.cli,
            wait_for_ready=False,
            verbose=False,
        )

        self.assertEqual(client.session_index, 3)
        self._assert_last_json_call(
            (
                "device",
                "start",
                "--platform",
                "android",
                "--timeout",
                "600",
                "--open",
                "--app-id",
                "app_123",
                "--build-version-id",
                "build_456",
                "--app-url",
                "https://example.com/app.apk",
                "--app-link",
                "myapp://deep-link",
            )
        )

    def test_session_methods_map_to_cli(self) -> None:
        self.cli.json_responses.extend(
            [
                {"index": 11},
                {"stopped": True},
                {"stopped_all": True},
                [{"index": 2}],
                {"platform": "ios"},
            ]
        )
        self.cli.text_responses.append("Switched to session 4")

        result = self.client.start_session(platform="ios", timeout=300)
        self.assertEqual(result, {"index": 11})
        self.assertEqual(self.client.session_index, 11)
        self._assert_last_json_call(
            ("device", "start", "--platform", "ios", "--timeout", "300")
        )

        result = self.client.stop_session()
        self.assertEqual(result, {"stopped": True})
        self.assertIsNone(self.client.session_index)
        self._assert_last_json_call(("device", "stop", "-s", "11"))

        result = self.client.stop_all()
        self.assertEqual(result, {"stopped_all": True})
        self.assertIsNone(self.client.session_index)
        self._assert_last_json_call(("device", "stop", "--all"))

        result = self.client.list_sessions()
        self.assertEqual(result, [{"index": 2}])
        self._assert_last_json_call(("device", "list",))

        result = self.client.use_session(4)
        self.assertEqual(result, "Switched to session 4")
        self.assertEqual(self.client.session_index, 4)
        args, json_output = self._last_call()
        self.assertEqual(args, ("device", "use", "4"))
        self.assertFalse(json_output)

        result = self.client.info()
        self.assertEqual(result, {"platform": "ios"})
        self._assert_last_json_call(("device", "info", "-s", "4"))

        self.client.doctor()
        args, json_output = self._last_call()
        self.assertEqual(args, ("device", "doctor", "-s", "4"))
        self.assertFalse(json_output)

    def test_visual_and_interaction_methods_map_to_cli(self) -> None:
        self.client.screenshot(out="screen.png")
        self._assert_last_json_call(
            ("device", "screenshot", "-s", "7", "--out", "screen.png")
        )

        self.client.tap(target="Login button")
        self._assert_last_json_call(
            ("device", "tap", "--target", "Login button", "-s", "7")
        )

        self.client.double_tap(x=12, y=24)
        self._assert_last_json_call(
            ("device", "double-tap", "--x", "12", "--y", "24", "-s", "7")
        )

        self.client.long_press(target="Avatar", duration_ms=900)
        self._assert_last_json_call(
            (
                "device",
                "long-press",
                "--target",
                "Avatar",
                "--duration",
                "900",
                "-s",
                "7",
            )
        )

        self.client.type_text(
            target="Email field",
            text="user@example.com",
            clear_first=False,
        )
        self._assert_last_json_call(
            (
                "device",
                "type",
                "--target",
                "Email field",
                "--text",
                "user@example.com",
                "--clear-first=false",
                "-s",
                "7",
            )
        )

        self.client.swipe(direction="down", x=100, y=200, duration_ms=650)
        self._assert_last_json_call(
            (
                "device",
                "swipe",
                "--x",
                "100",
                "--y",
                "200",
                "--direction",
                "down",
                "--duration",
                "650",
                "-s",
                "7",
            )
        )

        self.client.drag(start_x=1, start_y=2, end_x=3, end_y=4)
        self._assert_last_json_call(
            (
                "device",
                "drag",
                "--start-x",
                "1",
                "--start-y",
                "2",
                "--end-x",
                "3",
                "--end-y",
                "4",
                "-s",
                "7",
            )
        )

        self.client.install_app(
            app_url="https://example.com/app.apk", bundle_id="com.example.app"
        )
        self._assert_last_json_call(
            (
                "device",
                "install",
                "--app-url",
                "https://example.com/app.apk",
                "-s",
                "7",
                "--bundle-id",
                "com.example.app",
            )
        )

        self.client.launch_app(bundle_id="com.example.app")
        self._assert_last_json_call(
            ("device", "launch", "--bundle-id", "com.example.app", "-s", "7")
        )

    def test_wait_maps_to_cli(self) -> None:
        self.client.wait(duration_ms=1500)
        self._assert_last_json_call(
            ("device", "wait", "--duration-ms", "1500", "-s", "7")
        )

    def test_pinch_maps_to_cli(self) -> None:
        self.client.pinch(x=10, y=20, scale=1.5, duration_ms=450)
        self._assert_last_json_call(
            (
                "device",
                "pinch",
                "--x",
                "10",
                "--y",
                "20",
                "--scale",
                "1.5",
                "--duration",
                "450",
                "-s",
                "7",
            )
        )

    def test_clear_text_maps_to_cli(self) -> None:
        self.client.clear_text(target="Email field")
        self._assert_last_json_call(
            ("device", "clear-text", "--target", "Email field", "-s", "7")
        )

    def test_back_key_shake_map_to_cli(self) -> None:
        self.client.back()
        args, _ = self._last_call()
        self.assertEqual(args, ("device", "back", "-s", "7"))

        self.client.key("ENTER")
        args, _ = self._last_call()
        self.assertEqual(args, ("device", "key", "--key", "ENTER", "-s", "7"))

        self.client.shake()
        args, _ = self._last_call()
        self.assertEqual(args, ("device", "shake", "-s", "7"))

    def test_manual_app_location_file_methods_map_to_cli(self) -> None:
        self.client.go_home()
        args, _ = self._last_call()
        self.assertEqual(args, ("device", "home", "-s", "7"))

        self.client.kill_app()
        args, _ = self._last_call()
        self.assertEqual(args, ("device", "kill-app", "-s", "7"))

        self.client.open_app("settings")
        args, _ = self._last_call()
        self.assertEqual(args, ("device", "open-app", "--app", "settings", "-s", "7"))

        self.client.navigate("https://example.com")
        args, _ = self._last_call()
        self.assertEqual(
            args, ("device", "navigate", "--url", "https://example.com", "-s", "7")
        )

        self.client.set_location(37.7749, -122.4194)
        args, _ = self._last_call()
        self.assertEqual(
            args,
            (
                "device",
                "set-location",
                "--lat",
                "37.7749",
                "--lon",
                "-122.4194",
                "-s",
                "7",
            ),
        )

        self.client.download_file(
            "https://example.com/file.pdf", filename="report.pdf"
        )
        args, _ = self._last_call()
        self.assertEqual(
            args,
            (
                "device",
                "download-file",
                "--url",
                "https://example.com/file.pdf",
                "--filename",
                "report.pdf",
                "-s",
                "7",
            ),
        )

    def test_live_step_methods_map_to_cli(self) -> None:
        self.client.instruction("Open Settings")
        args, _ = self._last_call()
        self.assertEqual(args, ("device", "instruction", "Open Settings", "-s", "7"))

        self.client.validation("Verify Settings is visible")
        args, _ = self._last_call()
        self.assertEqual(
            args, ("device", "validation", "Verify Settings is visible", "-s", "7")
        )

        self.client.extract("Extract the account email", variable_name="account_email")
        args, _ = self._last_call()
        self.assertEqual(
            args,
            (
                "device",
                "extract",
                "Extract the account email",
                "--variable-name",
                "account_email",
                "-s",
                "7",
            ),
        )

        self.client.code_execution("script_123")
        args, _ = self._last_call()
        self.assertEqual(
            args, ("device", "code-execution", "script_123", "-s", "7")
        )

    def test_click_alias_not_exposed(self) -> None:
        self.assertFalse(hasattr(self.client, "click"))

    def test_live_step_methods_reject_empty_values(self) -> None:
        with self.assertRaises(ValueError):
            self.client.instruction("   ")

        with self.assertRaises(ValueError):
            self.client.code_execution("")


class RevylCLITests(unittest.TestCase):
    @mock.patch("revyl.sdk.subprocess.run")
    def test_run_parses_trailing_json_after_info_lines(self, run_mock: mock.Mock) -> None:
        run_mock.return_value = subprocess.CompletedProcess(
            args=["/tmp/revyl", "device", "tap", "--json"],
            returncode=0,
            stdout="Resolved 'Search' -> (120, 340)\n{\n  \"x\": 120,\n  \"y\": 340\n}\n",
            stderr="",
        )

        cli = RevylCLI(binary_path="/tmp/revyl")
        result = cli.run("device", "tap", "--target", "Search", json_output=True)

        self.assertEqual(result, {"x": 120, "y": 340})

    @mock.patch("revyl.sdk.subprocess.run")
    def test_run_raises_for_non_json_output(self, run_mock: mock.Mock) -> None:
        run_mock.return_value = subprocess.CompletedProcess(
            args=["/tmp/revyl", "device", "doctor", "--json"],
            returncode=0,
            stdout="not json at all\n",
            stderr="",
        )

        cli = RevylCLI(binary_path="/tmp/revyl")
        with self.assertRaises(RevylError):
            cli.run("device", "doctor", json_output=True)


if __name__ == "__main__":
    unittest.main()
