from __future__ import annotations

import importlib.util
import json
import os
import re
import subprocess
import tempfile
import unittest
from importlib.machinery import ModuleSpec
from pathlib import Path
from types import ModuleType
from unittest.mock import patch


spec: ModuleSpec | None = importlib.util.spec_from_file_location(
    "maintain_atlas", Path(__file__).with_name("maintain-atlas.py")
)
assert spec and spec.loader
recipe: ModuleType = importlib.util.module_from_spec(spec)
spec.loader.exec_module(recipe)


class RecipeTests(unittest.TestCase):
    def test_workflow_summaries_use_the_helper_checkpoint(self) -> None:
        for workflow_path in Path(__file__).parent.glob("refresh-*.yml"):
            with self.subTest(workflow=workflow_path.name):
                workflow = workflow_path.read_text()
                summary = workflow.split("      - name: Summarize\n", 1)[1]
                script = summary.split("        run: |\n", 1)[1]
                script = "\n".join(
                    line.removeprefix("          ") for line in script.splitlines()
                )
                filenames = re.findall(r"[A-Za-z0-9_-]+\.json", script)
                self.assertEqual(filenames, [recipe.CHECKPOINT.name] * 2)
                with tempfile.TemporaryDirectory() as directory:
                    checkpoint_path = Path(directory) / recipe.CHECKPOINT.name
                    checkpoint_path.write_text('{"build_id":"exact-build-proof"}\n')
                    summary_path = Path(directory) / "summary.md"
                    result = subprocess.run(
                        ["bash", "-e", "-c", script],
                        cwd=directory,
                        env={**os.environ, "GITHUB_STEP_SUMMARY": str(summary_path)},
                        capture_output=True,
                        text=True,
                        timeout=5,
                        check=False,
                    )
                    self.assertEqual(result.returncode, 0, result.stderr)
                    self.assertIn("exact-build-proof", summary_path.read_text())

    def run_recipe(
        self,
        *,
        artifact: bool,
        outcome: str = "completed",
        broken_poll: bool = False,
        cleanup_failure: bool = False,
    ) -> tuple[list[tuple[str, ...]], dict[str, object]]:
        calls: list[tuple[str, ...]] = []
        status_reads = 0
        cancelled = False

        def fake_cli(*args: str, timeout_seconds: float = 180) -> dict[str, object]:
            nonlocal status_reads, cancelled
            calls.append(args)
            if args[:2] == ("build", "upload"):
                self.assertEqual(args[args.index("--branch") + 1], "main")
                return {
                    "success": True,
                    "count": 1,
                    "build": {"app_id": "app", "build_id": "build"},
                }
            if args[0] == "build" and args[1] == "--remote":
                self.assertIn("--detach", args)
                return {"build_job_id": "job", "status": "pending"}
            if args[:2] == ("build", "status"):
                return {"status": "success", "app_id": "app", "version_id": "build"}
            if args[:2] == ("explore", "run"):
                self.assertEqual(args[args.index("--build-id") + 1], "build")
                self.assertIn("--no-wait", args)
                self.assertNotIn("--branch", args)
                return {
                    "run_id": "run",
                    "build_id": "build",
                    "execution_status": "queued",
                    "success": False,
                }
            if args[:2] == ("explore", "cancel"):
                cancelled = True
                return {"execution_status": "cancelling"}
            if args[:2] == ("explore", "status"):
                status_reads += 1
                if cancelled:
                    if cleanup_failure:
                        raise RuntimeError("cleanup read failed")
                    return {"execution_status": "cancelled", "success": False}
                if broken_poll:
                    raise RuntimeError("network request failed")
                if status_reads == 1:
                    return {
                        "execution_status": "completed",
                        "atlas_status": "running",
                        "outcome": "completed",
                        "success": False,
                    }
                return {
                    "execution_status": "completed",
                    "atlas_status": "partial" if outcome == "partial" else "completed",
                    "outcome": outcome,
                    "success": True,
                    "build_id": "build",
                    "branch_name": "main",
                    "report_url": "https://app.example/report/run",
                }
            self.fail(f"Unexpected command {args}")

        with tempfile.TemporaryDirectory() as directory:
            receipt = Path(directory) / "result.json"
            env = {
                "REVYL_APP_ID": "app",
                "REVYL_DESTINATION": "main",
                "REVYL_PROFILE": "preview",
                "REVYL_PLATFORM": "android",
                "SOURCE_SHA": "source",
            }
            if artifact:
                env["REVYL_ARTIFACT"] = "app.apk"
            with (
                patch.dict(os.environ, env, clear=True),
                patch.object(recipe, "CHECKPOINT", receipt),
                patch.object(recipe, "cli", fake_cli),
                patch.object(recipe.time, "sleep"),
            ):
                if broken_poll:
                    with self.assertRaisesRegex(
                        RuntimeError, "cleanup" if cleanup_failure else "network"
                    ):
                        recipe.main()
                else:
                    recipe.main()
            saved = json.loads(receipt.read_text())
        return calls, saved

    def test_artifact_exact_handoff_waits_for_atlas(self) -> None:
        calls, saved = self.run_recipe(artifact=True)
        self.assertEqual(saved["build_id"], "build")
        self.assertEqual(saved["outcome"], "completed")
        self.assertEqual(calls.count(("explore", "status", "run")), 2)

    def test_remote_job_id_is_not_used_as_build_id(self) -> None:
        _, saved = self.run_recipe(artifact=False)
        self.assertEqual(saved["build_job_id"], "job")
        self.assertEqual(saved["build_id"], "build")

    def test_partial_map_is_explicit_success(self) -> None:
        _, saved = self.run_recipe(artifact=True, outcome="partial")
        self.assertEqual(saved["outcome"], "partial")
        self.assertTrue(saved["success"])

    def test_poll_failure_cancels_and_observes_terminal_state(self) -> None:
        calls, saved = self.run_recipe(artifact=True, broken_poll=True)
        self.assertIn(("explore", "cancel", "run"), calls)
        self.assertEqual(calls[-1], ("explore", "status", "run"))
        self.assertNotIn("unsettled_operation", saved)

    def test_cleanup_failure_preserves_operation_for_recovery(self) -> None:
        _, saved = self.run_recipe(
            artifact=True, broken_poll=True, cleanup_failure=True
        )
        self.assertEqual(saved["unsettled_operation"], {"kind": "explore", "id": "run"})

    def test_wait_deadline_bounds_poll_and_sleep(self) -> None:
        with (
            patch.object(recipe.time, "monotonic", side_effect=[100, 100, 119, 120]),
            patch.object(recipe.time, "sleep") as sleep,
            patch.object(recipe, "cli", return_value={"status": "running"}) as cli,
        ):
            with self.assertRaises(TimeoutError):
                recipe.wait_for(
                    ("build", "status", "job"),
                    lambda row: row["status"] == "success",
                    20,
                )
        cli.assert_called_once_with("build", "status", "job", timeout_seconds=20)
        sleep.assert_called_once_with(1)

    def test_failed_explore_status_keeps_terminal_receipt(self) -> None:
        receipt = {
            "execution_status": "failed",
            "atlas_status": "not_started",
            "success": False,
        }
        result = subprocess.CompletedProcess([], 1, json.dumps(receipt))
        with patch.object(recipe.subprocess, "run", return_value=result):
            self.assertEqual(recipe.cli("explore", "status", "run"), receipt)
            with self.assertRaises(RuntimeError):
                recipe.cli("explore", "run", "app")
        self.assertTrue(recipe.exploration_finished(receipt))

    def test_remote_timeout_is_terminal(self) -> None:
        self.assertIn("timeout", recipe.TERMINAL_BUILDS)

    def test_workflows_serialize_current_source_by_destination(self) -> None:
        for branch, filename in [
            ("main", "refresh-main.yml"),
            ("staging", "refresh-staging-weekly.yml"),
        ]:
            with self.subTest(branch=branch):
                workflow = Path(__file__).with_name(filename).read_text()
                self.assertIn(
                    "group: revyl-atlas-${{ vars.REVYL_APP_ID }}-" + branch, workflow
                )
                self.assertIn("cancel-in-progress: false", workflow)
                self.assertIn("ref: " + branch, workflow)
                self.assertIn("REVYL_DESTINATION: " + branch, workflow)
                self.assertNotIn("github.sha", workflow)
                self.assertNotIn("github.run_id", workflow)


if __name__ == "__main__":
    unittest.main()
