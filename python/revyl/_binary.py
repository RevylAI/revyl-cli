"""
Binary management helpers for the Revyl Python package.
"""

from __future__ import annotations

import hashlib
import os
import platform
import shutil
import subprocess
import sys
import tempfile
import urllib.request
from pathlib import Path
from typing import Sequence

__version__ = "0.1.4"
REPO = "RevylAI/revyl-cli"
_HASH_CHUNK_SIZE = 1024 * 1024


def get_platform_info() -> tuple[str, str, str]:
    """
    Return platform info used to resolve release binary asset names.
    """
    system = platform.system().lower()
    machine = platform.machine().lower()

    if system == "darwin":
        platform_str = "darwin"
    elif system == "linux":
        platform_str = "linux"
    elif system == "windows":
        platform_str = "windows"
    else:
        raise RuntimeError(f"Unsupported platform: {system}")

    if machine in ("x86_64", "amd64"):
        arch_str = "amd64"
    elif machine in ("arm64", "aarch64"):
        arch_str = "arm64"
    else:
        raise RuntimeError(f"Unsupported architecture: {machine}")

    ext = ".exe" if system == "windows" else ""
    return platform_str, arch_str, ext


def _binary_name() -> str:
    platform_str, arch_str, ext = get_platform_info()
    return f"revyl-{platform_str}-{arch_str}{ext}"


def _release_asset_url(asset_name: str, version: str = "latest") -> str:
    if version == "latest":
        return f"https://github.com/{REPO}/releases/latest/download/{asset_name}"
    return f"https://github.com/{REPO}/releases/download/{version}/{asset_name}"


def _checksum_path(binary_path: Path) -> Path:
    return Path(f"{binary_path}.sha256")


def _parse_checksums(raw_checksums: str) -> dict[str, str]:
    checksums: dict[str, str] = {}
    for line in raw_checksums.splitlines():
        line = line.strip()
        if not line or line.startswith("#"):
            continue

        parts = line.split(maxsplit=1)
        if len(parts) != 2:
            continue

        digest, filename = parts
        filename = filename.lstrip("*").strip()
        digest = digest.strip().lower()
        if digest and filename:
            checksums[filename] = digest

    return checksums


def _sha256_file(path: Path) -> str:
    hasher = hashlib.sha256()
    with path.open("rb") as file_handle:
        for chunk in iter(lambda: file_handle.read(_HASH_CHUNK_SIZE), b""):
            hasher.update(chunk)
    return hasher.hexdigest()


def _fetch_expected_checksum(binary_name: str, version: str = "latest") -> str:
    checksum_url = _release_asset_url("checksums.txt", version)
    try:
        with urllib.request.urlopen(checksum_url) as response:
            raw_checksums = response.read().decode("utf-8", errors="replace")
    except Exception as exc:
        raise RuntimeError(f"Failed to download checksums: {exc}") from exc

    checksums = _parse_checksums(raw_checksums)
    expected = checksums.get(binary_name)
    if not expected:
        raise RuntimeError(f"No checksum found for asset '{binary_name}' in release checksums")

    return expected


def _download_to_temp(url: str, suffix: str) -> Path:
    with urllib.request.urlopen(url) as response, tempfile.NamedTemporaryFile(delete=False, suffix=suffix) as tmp:
        shutil.copyfileobj(response, tmp)
        return Path(tmp.name)


def _write_checksum_sidecar(binary_path: Path, digest: str) -> None:
    _checksum_path(binary_path).write_text(digest.strip().lower() + "\n", encoding="utf-8")


def _is_verified_binary(binary_path: Path) -> bool:
    if not binary_path.exists():
        return False

    checksum_file = _checksum_path(binary_path)
    if not checksum_file.exists():
        return False

    try:
        expected = checksum_file.read_text(encoding="utf-8").strip().lower()
        if not expected:
            return False
        actual = _sha256_file(binary_path)
    except Exception:
        return False

    return actual == expected


def get_binary_path() -> Path:
    """
    Return the expected local path for the downloaded Revyl binary.
    """
    revyl_dir = Path.home() / ".revyl" / "bin"
    revyl_dir.mkdir(parents=True, exist_ok=True)

    return revyl_dir / _binary_name()


def download_binary(version: str = "latest") -> Path:
    """
    Download the Revyl binary for the current platform.
    """
    platform_str, arch_str, ext = get_platform_info()
    binary_name = f"revyl-{platform_str}-{arch_str}{ext}"
    binary_url = _release_asset_url(binary_name, version)
    expected_checksum = _fetch_expected_checksum(binary_name, version)

    binary_path = get_binary_path()
    print(f"Downloading Revyl CLI from {binary_url}...")

    temp_path: Path | None = None
    try:
        temp_suffix = ext if ext else ".tmp"
        temp_path = _download_to_temp(binary_url, suffix=temp_suffix)
        actual_checksum = _sha256_file(temp_path)
        if actual_checksum != expected_checksum:
            raise RuntimeError(
                f"Checksum verification failed for {binary_name} "
                f"(expected {expected_checksum}, got {actual_checksum})"
            )
    except Exception as exc:
        if temp_path is not None:
            try:
                temp_path.unlink(missing_ok=True)
            except Exception:
                pass
        raise RuntimeError(f"Failed to download binary: {exc}") from exc

    # Ensure executable mode before moving into final path.
    if platform.system() != "Windows":
        temp_path.chmod(0o755)

    os.replace(temp_path, binary_path)
    _write_checksum_sidecar(binary_path, expected_checksum)

    print(f"Downloaded to {binary_path}")
    return binary_path


def ensure_binary() -> Path:
    """
    Ensure the Revyl binary exists locally and return its path.
    """
    binary_path = get_binary_path()
    if _is_verified_binary(binary_path):
        return binary_path
    return download_binary()


def run_binary(args: Sequence[str]) -> int:
    """
    Run the downloaded Revyl binary with the provided args.
    """
    binary_path = ensure_binary()
    result = subprocess.run([str(binary_path), *args], check=False)
    return result.returncode


def main() -> int:
    """
    Entry point used by the `revyl` console script.
    """
    try:
        return run_binary(sys.argv[1:])
    except KeyboardInterrupt:
        return 130
    except RuntimeError as exc:
        print(f"Error: {exc}", file=sys.stderr)
        print(f"\nYou can manually download from: https://github.com/{REPO}/releases")
        return 1
    except Exception as exc:
        print(f"Error running Revyl: {exc}", file=sys.stderr)
        return 1
