"""Validate that DeviceClient matches the shared device-sdk-methods.json spec.

The spec is the single source of truth for all SDK implementations. This test
ensures the Python SDK stays in sync: every spec method must exist on
DeviceClient with matching parameter names, and DeviceClient must not expose
public device methods absent from the spec.
"""

from __future__ import annotations

import inspect
import json
import unittest
from pathlib import Path

from revyl.sdk import DeviceClient

SPEC_PATH = Path(__file__).resolve().parents[2] / "device-sdk-methods.json"

LANGUAGE_SPECIFIC_METHODS = frozenset({
    "start",
    "close",
    "__init__",
    "__enter__",
    "__exit__",
    "report",
    "targets",
    "history",
    "wait_for_stream",
    "wait_for_device_ready",
    "wait_for_report",
})


def _load_spec() -> list[dict]:
    with open(SPEC_PATH) as fh:
        data = json.load(fh)
    return data["methods"]


class SpecParityTests(unittest.TestCase):
    """Verify DeviceClient implements exactly the methods in the spec."""

    @classmethod
    def setUpClass(cls) -> None:
        cls.spec_methods = _load_spec()
        cls.spec_names = {m["name"] for m in cls.spec_methods}

    def test_spec_file_exists(self) -> None:
        self.assertTrue(SPEC_PATH.exists(), f"Spec not found at {SPEC_PATH}")

    def test_every_spec_method_exists_on_device_client(self) -> None:
        for method_spec in self.spec_methods:
            name = method_spec["name"]
            self.assertTrue(
                hasattr(DeviceClient, name),
                f"DeviceClient is missing spec method '{name}'",
            )
            self.assertTrue(
                callable(getattr(DeviceClient, name)),
                f"DeviceClient.{name} exists but is not callable",
            )

    def test_parameter_names_match_spec(self) -> None:
        for method_spec in self.spec_methods:
            name = method_spec["name"]
            if not hasattr(DeviceClient, name):
                continue

            sig = inspect.signature(getattr(DeviceClient, name))
            actual_params = [
                p for p in sig.parameters if p != "self"
            ]
            expected_params = [p["name"] for p in method_spec["params"]]

            self.assertEqual(
                actual_params,
                expected_params,
                f"DeviceClient.{name} params {actual_params} "
                f"don't match spec {expected_params}",
            )

    def test_no_extra_public_methods_outside_spec(self) -> None:
        """DeviceClient should not have public device methods absent from spec."""
        actual_public = {
            name
            for name in dir(DeviceClient)
            if not name.startswith("_") and callable(getattr(DeviceClient, name))
        }
        extra = actual_public - self.spec_names - LANGUAGE_SPECIFIC_METHODS
        self.assertEqual(
            extra,
            set(),
            f"DeviceClient has public methods not in spec: {sorted(extra)}",
        )

    def test_spec_method_count(self) -> None:
        """Sanity check: spec should have a reasonable number of methods."""
        self.assertGreaterEqual(len(self.spec_methods), 25)


if __name__ == "__main__":
    unittest.main()
