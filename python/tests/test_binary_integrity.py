from __future__ import annotations

import hashlib
import io
import tempfile
import unittest
from pathlib import Path
from unittest import mock

import revyl._binary as binary


class _FakeResponse(io.BytesIO):
    def __enter__(self) -> "_FakeResponse":
        return self

    def __exit__(self, exc_type, exc, tb) -> bool:
        self.close()
        return False


def _build_urlopen(binary_payload: bytes, checksums_payload: str):
    def _fake_urlopen(url, *args, **kwargs):
        target = getattr(url, "full_url", str(url))
        if target.endswith("/checksums.txt"):
            return _FakeResponse(checksums_payload.encode("utf-8"))
        if target.endswith("/revyl-linux-amd64"):
            return _FakeResponse(binary_payload)
        raise AssertionError(f"unexpected URL requested: {target}")

    return _fake_urlopen


class BinaryIntegrityTests(unittest.TestCase):
    def test_download_binary_verifies_checksum_and_writes_sidecar(self) -> None:
        payload = b"verified-binary"
        digest = hashlib.sha256(payload).hexdigest()
        checksums = f"{digest}  revyl-linux-amd64\n"

        with tempfile.TemporaryDirectory() as tmpdir:
            binary_path = Path(tmpdir) / "revyl-linux-amd64"
            with (
                mock.patch.object(binary, "get_platform_info", return_value=("linux", "amd64", "")),
                mock.patch.object(binary, "get_binary_path", return_value=binary_path),
                mock.patch("urllib.request.urlopen", side_effect=_build_urlopen(payload, checksums)),
            ):
                path = binary.download_binary()

            self.assertEqual(path, binary_path)
            self.assertTrue(binary_path.exists())
            self.assertEqual(binary_path.read_bytes(), payload)
            self.assertEqual((Path(str(binary_path) + ".sha256")).read_text(encoding="utf-8").strip(), digest)

    def test_download_binary_fails_when_checksum_missing(self) -> None:
        payload = b"payload"
        checksums = "deadbeef  revyl-other-binary\n"

        with tempfile.TemporaryDirectory() as tmpdir:
            binary_path = Path(tmpdir) / "revyl-linux-amd64"
            with (
                mock.patch.object(binary, "get_platform_info", return_value=("linux", "amd64", "")),
                mock.patch.object(binary, "get_binary_path", return_value=binary_path),
                mock.patch("urllib.request.urlopen", side_effect=_build_urlopen(payload, checksums)),
            ):
                with self.assertRaises(RuntimeError):
                    binary.download_binary()

            self.assertFalse(binary_path.exists())

    def test_download_binary_fails_when_checksum_mismatch(self) -> None:
        payload = b"payload"
        checksums = "0" * 64 + "  revyl-linux-amd64\n"

        with tempfile.TemporaryDirectory() as tmpdir:
            binary_path = Path(tmpdir) / "revyl-linux-amd64"
            with (
                mock.patch.object(binary, "get_platform_info", return_value=("linux", "amd64", "")),
                mock.patch.object(binary, "get_binary_path", return_value=binary_path),
                mock.patch("urllib.request.urlopen", side_effect=_build_urlopen(payload, checksums)),
            ):
                with self.assertRaises(RuntimeError):
                    binary.download_binary()

            self.assertFalse(binary_path.exists())

    def test_ensure_binary_redownloads_when_sidecar_missing(self) -> None:
        old_payload = b"old"
        new_payload = b"new-verified"
        new_digest = hashlib.sha256(new_payload).hexdigest()
        checksums = f"{new_digest}  revyl-linux-amd64\n"

        with tempfile.TemporaryDirectory() as tmpdir:
            binary_path = Path(tmpdir) / "revyl-linux-amd64"
            binary_path.write_bytes(old_payload)

            with (
                mock.patch.object(binary, "get_platform_info", return_value=("linux", "amd64", "")),
                mock.patch.object(binary, "get_binary_path", return_value=binary_path),
                mock.patch("urllib.request.urlopen", side_effect=_build_urlopen(new_payload, checksums)),
            ):
                resolved = binary.ensure_binary()

            self.assertEqual(resolved, binary_path)
            self.assertEqual(binary_path.read_bytes(), new_payload)
            self.assertEqual((Path(str(binary_path) + ".sha256")).read_text(encoding="utf-8").strip(), new_digest)

    def test_ensure_binary_uses_verified_existing_binary(self) -> None:
        payload = b"already-verified"
        digest = hashlib.sha256(payload).hexdigest()

        with tempfile.TemporaryDirectory() as tmpdir:
            binary_path = Path(tmpdir) / "revyl-linux-amd64"
            binary_path.write_bytes(payload)
            Path(str(binary_path) + ".sha256").write_text(digest + "\n", encoding="utf-8")

            with (
                mock.patch.object(binary, "get_binary_path", return_value=binary_path),
                mock.patch("urllib.request.urlopen") as mocked_urlopen,
            ):
                resolved = binary.ensure_binary()

            self.assertEqual(resolved, binary_path)
            mocked_urlopen.assert_not_called()


if __name__ == "__main__":
    unittest.main()
