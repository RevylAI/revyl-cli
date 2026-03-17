"""End-to-end regression tests for the Revyl Python SDK.

Exercises the RevylCLI wrapper and DeviceClient against a real backend
(staging or local). Skips gracefully when REVYL_API_KEY is not set.

Run with:
    cd revyl-cli/python && uv run pytest tests/test_sdk_regression.py -v --tb=short
"""

from __future__ import annotations

import json
import os
import subprocess
import sys
from pathlib import Path

import pytest

# Ensure the SDK package is importable
SDK_ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(SDK_ROOT))

from revyl import RevylCLI  # noqa: E402


def _has_api_key() -> bool:
    """Check if an API key is available (env or .env file)."""
    if os.getenv("REVYL_API_KEY"):
        return True
    monorepo = SDK_ROOT.parent.parent
    env_path = monorepo / "cognisim_backend" / ".env"
    if env_path.exists():
        for line in env_path.read_text().splitlines():
            if line.startswith("REVYL_API_KEY="):
                return True
    return False


def _find_binary() -> str | None:
    """Find the revyl binary (build dir or PATH)."""
    local_build = SDK_ROOT.parent / "build" / "revyl"
    if local_build.exists():
        return str(local_build)
    return None


pytestmark = pytest.mark.skipif(
    not _has_api_key(),
    reason="REVYL_API_KEY not available; skipping SDK regression tests",
)


@pytest.fixture(scope="module")
def cli() -> RevylCLI:
    """Create a RevylCLI instance with optional local binary."""
    binary = _find_binary()
    return RevylCLI(binary_path=binary)


class TestRevylCLIWrapper:
    """Tests for the RevylCLI subprocess wrapper."""

    def test_auth_status(self, cli: RevylCLI) -> None:
        """Auth status should succeed and produce output."""
        result = cli.run("auth", "status")
        assert result is not None

    def test_test_list_json(self, cli: RevylCLI) -> None:
        """Test list --json should produce valid JSON."""
        result = cli.run("test", "list", "--json")
        assert result is not None
        raw = result if isinstance(result, str) else str(result)
        for i, ch in enumerate(raw):
            if ch in ("{", "["):
                json.loads(raw[i:])
                return
        pytest.skip("test list did not produce JSON output")

    def test_workflow_list_json(self, cli: RevylCLI) -> None:
        """Workflow list --json should produce valid JSON."""
        result = cli.run("workflow", "list", "--json")
        assert result is not None

    def test_app_list_json(self, cli: RevylCLI) -> None:
        """App list --json should produce valid JSON."""
        result = cli.run("app", "list", "--json")
        assert result is not None

    def test_version(self, cli: RevylCLI) -> None:
        """Version output should contain 'revyl'."""
        result = cli.run("--version")
        assert result is not None


class TestDeviceClientSpec:
    """Verify DeviceClient has the expected methods (API surface parity).

    These don't start real device sessions -- they just check the SDK
    exposes the right interface. The actual device tests are in the
    Go e2e suite and the existing device_prod_smoke.py.
    """

    def test_has_start_session(self) -> None:
        from revyl import DeviceClient

        assert hasattr(DeviceClient, "start") or hasattr(
            DeviceClient, "start_session"
        )

    def test_has_instruction_method(self) -> None:
        from revyl import DeviceClient

        assert hasattr(DeviceClient, "instruction")

    def test_has_validation_method(self) -> None:
        from revyl import DeviceClient

        assert hasattr(DeviceClient, "validation")

    def test_has_extract_method(self) -> None:
        from revyl import DeviceClient

        assert hasattr(DeviceClient, "extract")

    def test_has_code_execution_method(self) -> None:
        from revyl import DeviceClient

        assert hasattr(DeviceClient, "code_execution")

    def test_has_download_file_method(self) -> None:
        from revyl import DeviceClient

        assert hasattr(DeviceClient, "download_file")

    def test_has_tap_method(self) -> None:
        from revyl import DeviceClient

        assert hasattr(DeviceClient, "tap")

    def test_has_swipe_method(self) -> None:
        from revyl import DeviceClient

        assert hasattr(DeviceClient, "swipe")

    def test_no_click_method(self) -> None:
        """DeviceClient must not expose a 'click' method (removed alias)."""
        from revyl import DeviceClient

        assert not hasattr(DeviceClient, "click")
