"""
Tests for the pip wrapper's binary management: each wheel version keeps its
own copy of the Go binary and downloads it (checksum-verified) on first run.
"""

from __future__ import annotations

import hashlib
import io
import os
import tempfile
import unittest
import urllib.error
from pathlib import Path
from unittest import mock

import revyl._binary as binary


class _FakeResponse(io.BytesIO):
    def __enter__(self) -> "_FakeResponse":
        return self

    def __exit__(self, exc_type, exc, tb) -> bool:
        self.close()
        return False


def _urlopen_serving(payload: bytes, checksums: str):
    def _fake_urlopen(url, *args, **kwargs):
        target = getattr(url, "full_url", str(url))
        assert "/download/v0.2.0/" in target, f"expected URL pinned to v0.2.0, got {target}"
        if target.endswith("/checksums.txt"):
            return _FakeResponse(checksums.encode("utf-8"))
        return _FakeResponse(payload)

    return _fake_urlopen


class BinaryTests(unittest.TestCase):
    def setUp(self) -> None:
        tmp = tempfile.TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        self.bin_dir = Path(tmp.name)
        self.binary_path = self.bin_dir / "v0.2.0" / "revyl-linux-amd64"

        for patch in (
            mock.patch.object(binary, "__version__", "0.2.0"),
            mock.patch.object(binary, "get_platform_info", return_value=("linux", "amd64", "")),
            mock.patch.object(binary, "get_binary_path", return_value=self.binary_path),
            mock.patch.dict(os.environ, {"REVYL_BINARY": ""}),
        ):
            patch.start()
            self.addCleanup(patch.stop)

    def test_ensure_binary_uses_existing_binary_without_network(self) -> None:
        self.binary_path.parent.mkdir(parents=True)
        self.binary_path.write_bytes(b"cached")

        with mock.patch("urllib.request.urlopen") as mocked_urlopen:
            self.assertEqual(binary.ensure_binary(), self.binary_path)

        mocked_urlopen.assert_not_called()

    def test_ensure_binary_downloads_release_matching_wheel_version(self) -> None:
        payload = b"pinned-binary"
        digest = hashlib.sha256(payload).hexdigest()

        with mock.patch(
            "urllib.request.urlopen",
            side_effect=_urlopen_serving(payload, f"{digest}  revyl-linux-amd64\n"),
        ):
            resolved = binary.ensure_binary()

        self.assertEqual(resolved, self.binary_path)
        self.assertEqual(self.binary_path.read_bytes(), payload)

    def test_download_binary_fails_on_checksum_mismatch(self) -> None:
        checksums = "0" * 64 + "  revyl-linux-amd64\n"

        with mock.patch(
            "urllib.request.urlopen", side_effect=_urlopen_serving(b"payload", checksums)
        ):
            with self.assertRaises(RuntimeError):
                binary.download_binary()

        self.assertFalse(self.binary_path.exists())

    def test_download_binary_proceeds_when_checksums_unavailable(self) -> None:
        def _fake_urlopen(url, *args, **kwargs):
            target = getattr(url, "full_url", str(url))
            if target.endswith("/checksums.txt"):
                raise urllib.error.HTTPError(target, 404, "Not Found", {}, None)
            return _FakeResponse(b"unverified-binary")

        with mock.patch("urllib.request.urlopen", side_effect=_fake_urlopen):
            path = binary.download_binary()

        self.assertEqual(path.read_bytes(), b"unverified-binary")

    def test_download_binary_removes_binaries_of_other_versions(self) -> None:
        old_dir = self.bin_dir / "v0.1.0"
        old_dir.mkdir(parents=True)
        (old_dir / "revyl-linux-amd64").write_bytes(b"old")
        legacy_binary = self.bin_dir / "revyl-linux-amd64"
        legacy_binary.write_bytes(b"legacy")
        legacy_sidecar = self.bin_dir / "revyl-linux-amd64.sha256"
        legacy_sidecar.write_text("deadbeef\n", encoding="utf-8")

        payload = b"pinned-binary"
        digest = hashlib.sha256(payload).hexdigest()
        with mock.patch(
            "urllib.request.urlopen",
            side_effect=_urlopen_serving(payload, f"{digest}  revyl-linux-amd64\n"),
        ):
            binary.download_binary()

        self.assertFalse(old_dir.exists())
        self.assertFalse(legacy_binary.exists())
        self.assertFalse(legacy_sidecar.exists())
        self.assertEqual(self.binary_path.read_bytes(), payload)


if __name__ == "__main__":
    unittest.main()
