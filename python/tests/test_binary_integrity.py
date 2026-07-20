"""Tests for resolving the native CLI bundled in a platform wheel."""

from __future__ import annotations

import os
import tempfile
import unittest
from pathlib import Path
from unittest import mock

import revyl._binary as binary


class BinaryTests(unittest.TestCase):
    """Verify packaged and explicitly configured CLI resolution."""

    def setUp(self) -> None:
        """Create an isolated binary path for each test."""
        temporary_directory = tempfile.TemporaryDirectory()
        self.addCleanup(temporary_directory.cleanup)
        self.binary_path = Path(temporary_directory.name) / "revyl-linux-amd64"

        environment_patch = mock.patch.dict(os.environ, {"REVYL_BINARY": ""})
        binary_path_patch = mock.patch.object(
            binary,
            "get_binary_path",
            return_value=self.binary_path,
        )
        environment_patch.start()
        binary_path_patch.start()
        self.addCleanup(environment_patch.stop)
        self.addCleanup(binary_path_patch.stop)

    def test_ensure_binary_returns_packaged_executable(self) -> None:
        """Return the bundled CLI when it exists and is executable."""
        self.binary_path.write_bytes(b"binary")
        self.binary_path.chmod(0o755)

        self.assertEqual(binary.ensure_binary(), self.binary_path)

    def test_ensure_binary_rejects_missing_packaged_binary(self) -> None:
        """Fail clearly when a platform wheel lacks its native CLI."""
        with self.assertRaisesRegex(RuntimeError, "does not contain a CLI binary"):
            binary.ensure_binary()

    @unittest.skipIf(os.name == "nt", "Windows does not use POSIX executable bits")
    def test_ensure_binary_rejects_non_executable_packaged_binary(self) -> None:
        """Fail clearly when installation loses the CLI executable bit."""
        self.binary_path.write_bytes(b"binary")
        self.binary_path.chmod(0o644)

        with self.assertRaisesRegex(RuntimeError, "is not executable"):
            binary.ensure_binary()

    def test_ensure_binary_prefers_environment_override(self) -> None:
        """Resolve REVYL_BINARY before checking wheel package data."""
        override_path = self.binary_path.parent / "custom-revyl"
        override_path.write_bytes(b"override")

        with mock.patch.dict(
            os.environ,
            {"REVYL_BINARY": str(override_path)},
        ):
            self.assertEqual(binary.ensure_binary(), override_path.resolve())

    def test_ensure_binary_rejects_missing_environment_override(self) -> None:
        """Fail clearly when REVYL_BINARY points to a missing file."""
        missing_path = self.binary_path.parent / "missing-revyl"

        with mock.patch.dict(
            os.environ,
            {"REVYL_BINARY": str(missing_path)},
        ):
            with self.assertRaisesRegex(
                RuntimeError,
                "REVYL_BINARY points to non-existent path",
            ):
                binary.ensure_binary()


if __name__ == "__main__":
    unittest.main()
