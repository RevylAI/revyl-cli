"""Unit tests for ScriptClient, ModuleClient, and BuildClient.

Uses the same _FakeCLI pattern as test_sdk_device_client.py to verify that
each client method maps to the correct CLI arguments.
"""

from __future__ import annotations

import unittest

from revyl.sdk import BuildClient, ModuleClient, ScriptClient


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


class ScriptClientTests(unittest.TestCase):
    def setUp(self) -> None:
        self.cli = _FakeCLI()
        self.client = ScriptClient(cli=self.cli)

    def _last_call(self) -> tuple[tuple[str, ...], bool]:
        self.assertTrue(self.cli.calls, "no CLI calls recorded")
        return self.cli.calls[-1]

    def _assert_last_json_call(self, expected_args: tuple[str, ...]) -> None:
        args, json_output = self._last_call()
        self.assertEqual(args, expected_args)
        self.assertTrue(json_output)

    def test_list_no_filter(self) -> None:
        self.cli.json_responses.append([{"id": "s1", "name": "seed"}])
        result = self.client.list()
        self.assertEqual(len(result), 1)
        self._assert_last_json_call(("script", "list"))

    def test_list_with_runtime_filter(self) -> None:
        self.cli.json_responses.append([])
        self.client.list(runtime="python")
        self._assert_last_json_call(("script", "list", "--runtime", "python"))

    def test_get(self) -> None:
        self.cli.json_responses.append(
            {"id": "s1", "name": "seed", "code": "..."}
        )
        result = self.client.get("seed")
        self.assertEqual(result["name"], "seed")
        self._assert_last_json_call(("script", "get", "seed"))

    def test_create(self) -> None:
        self.cli.json_responses.append({"id": "s1"})
        self.client.create(
            "seed-db", file_path="seed.py", runtime="python"
        )
        self._assert_last_json_call(
            (
                "script", "create", "seed-db",
                "--file", "seed.py",
                "--runtime", "python",
            )
        )

    def test_create_with_description(self) -> None:
        self.cli.json_responses.append({"id": "s1"})
        self.client.create(
            "seed-db",
            file_path="seed.py",
            runtime="python",
            description="Seeds the database",
        )
        self._assert_last_json_call(
            (
                "script", "create", "seed-db",
                "--file", "seed.py",
                "--runtime", "python",
                "--description", "Seeds the database",
            )
        )

    def test_update(self) -> None:
        self.cli.json_responses.append({"id": "s1"})
        self.client.update(
            "seed-db", file_path="seed_v2.py", name="seed-v2"
        )
        self._assert_last_json_call(
            (
                "script", "update", "seed-db",
                "--file", "seed_v2.py",
                "--name", "seed-v2",
            )
        )

    def test_delete(self) -> None:
        self.cli.text_responses.append("Deleted seed-db")
        result = self.client.delete("seed-db")
        self.assertEqual(result, "Deleted seed-db")
        args, json_output = self._last_call()
        self.assertEqual(args, ("script", "delete", "seed-db", "--force"))
        self.assertFalse(json_output)

    def test_delete_without_force(self) -> None:
        self.cli.text_responses.append("Deleted")
        self.client.delete("seed-db", force=False)
        args, _ = self._last_call()
        self.assertEqual(args, ("script", "delete", "seed-db"))

    def test_usage(self) -> None:
        self.cli.json_responses.append([{"test_id": "t1", "name": "login"}])
        result = self.client.usage("seed-db")
        self.assertEqual(len(result), 1)
        self._assert_last_json_call(("script", "usage", "seed-db"))


class ModuleClientTests(unittest.TestCase):
    def setUp(self) -> None:
        self.cli = _FakeCLI()
        self.client = ModuleClient(cli=self.cli)

    def _last_call(self) -> tuple[tuple[str, ...], bool]:
        self.assertTrue(self.cli.calls, "no CLI calls recorded")
        return self.cli.calls[-1]

    def _assert_last_json_call(self, expected_args: tuple[str, ...]) -> None:
        args, json_output = self._last_call()
        self.assertEqual(args, expected_args)
        self.assertTrue(json_output)

    def test_list_no_filter(self) -> None:
        self.cli.json_responses.append([{"id": "m1"}])
        result = self.client.list()
        self.assertEqual(len(result), 1)
        self._assert_last_json_call(("module", "list"))

    def test_list_with_search(self) -> None:
        self.cli.json_responses.append([])
        self.client.list(search="login")
        self._assert_last_json_call(("module", "list", "--search", "login"))

    def test_get(self) -> None:
        self.cli.json_responses.append({"id": "m1", "name": "login-flow"})
        result = self.client.get("login-flow")
        self.assertEqual(result["name"], "login-flow")
        self._assert_last_json_call(("module", "get", "login-flow"))

    def test_create(self) -> None:
        self.cli.json_responses.append({"id": "m1"})
        self.client.create(
            "login-flow", blocks_file="modules/login.yaml"
        )
        self._assert_last_json_call(
            (
                "module", "create", "login-flow",
                "--from-file", "modules/login.yaml",
            )
        )

    def test_create_with_description(self) -> None:
        self.cli.json_responses.append({"id": "m1"})
        self.client.create(
            "login-flow",
            blocks_file="modules/login.yaml",
            description="Standard login",
        )
        self._assert_last_json_call(
            (
                "module", "create", "login-flow",
                "--from-file", "modules/login.yaml",
                "--description", "Standard login",
            )
        )

    def test_update(self) -> None:
        self.cli.json_responses.append({"id": "m1"})
        self.client.update(
            "login-flow", name="login-v2",
            blocks_file="v2.yaml",
        )
        self._assert_last_json_call(
            (
                "module", "update", "login-flow",
                "--name", "login-v2",
                "--from-file", "v2.yaml",
            )
        )

    def test_delete(self) -> None:
        self.cli.text_responses.append("Deleted login-flow")
        result = self.client.delete("login-flow")
        self.assertEqual(result, "Deleted login-flow")
        args, json_output = self._last_call()
        self.assertEqual(args, ("module", "delete", "login-flow", "--force"))
        self.assertFalse(json_output)

    def test_delete_without_force(self) -> None:
        self.cli.text_responses.append("Deleted")
        self.client.delete("login-flow", force=False)
        args, _ = self._last_call()
        self.assertEqual(args, ("module", "delete", "login-flow"))

    def test_usage(self) -> None:
        self.cli.json_responses.append([{"test_id": "t1"}])
        result = self.client.usage("login-flow")
        self.assertEqual(len(result), 1)
        self._assert_last_json_call(("module", "usage", "login-flow"))


class BuildClientTests(unittest.TestCase):
    def setUp(self) -> None:
        self.cli = _FakeCLI()
        self.client = BuildClient(cli=self.cli)

    def _last_call(self) -> tuple[tuple[str, ...], bool]:
        self.assertTrue(self.cli.calls, "no CLI calls recorded")
        return self.cli.calls[-1]

    def _assert_last_json_call(self, expected_args: tuple[str, ...]) -> None:
        args, json_output = self._last_call()
        self.assertEqual(args, expected_args)
        self.assertTrue(json_output)

    def test_upload_minimal(self) -> None:
        self.cli.json_responses.append(
            {"app_id": "a1", "build_version_id": "bv1"}
        )
        result = self.client.upload()
        self.assertEqual(result["app_id"], "a1")
        self._assert_last_json_call(("build", "upload", "--yes"))

    def test_upload_with_all_options(self) -> None:
        self.cli.json_responses.append({"app_id": "a1"})
        self.client.upload(
            app_name="my-app",
            platform="android",
            skip_build=True,
            version="v1.2.3",
            set_current=True,
        )
        self._assert_last_json_call(
            (
                "build", "upload",
                "--name", "my-app",
                "--platform", "android",
                "--skip-build",
                "--version", "v1.2.3",
                "--set-current",
                "--yes",
            )
        )

    def test_list_no_filter(self) -> None:
        self.cli.json_responses.append([{"id": "bv1"}])
        result = self.client.list()
        self.assertEqual(len(result), 1)
        self._assert_last_json_call(("build", "list"))

    def test_list_with_filters(self) -> None:
        self.cli.json_responses.append([])
        self.client.list(app_name="my-app", platform="ios")
        self._assert_last_json_call(
            ("build", "list", "--app", "my-app", "--platform", "ios")
        )

    def test_delete(self) -> None:
        self.cli.text_responses.append("Deleted my-app")
        result = self.client.delete("my-app")
        self.assertEqual(result, "Deleted my-app")
        args, json_output = self._last_call()
        self.assertEqual(args, ("build", "delete", "my-app", "--force"))
        self.assertFalse(json_output)

    def test_delete_specific_version(self) -> None:
        self.cli.text_responses.append("Deleted")
        self.client.delete("my-app", version="v1.0.0")
        args, _ = self._last_call()
        self.assertEqual(
            args,
            (
                "build", "delete", "my-app",
                "--version", "v1.0.0", "--force",
            ),
        )

    def test_delete_without_force(self) -> None:
        self.cli.text_responses.append("Deleted")
        self.client.delete("my-app", force=False)
        args, _ = self._last_call()
        self.assertEqual(args, ("build", "delete", "my-app"))


if __name__ == "__main__":
    unittest.main()
