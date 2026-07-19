"""
Binary management for the Revyl Python package.

The pip package is a thin launcher for the Revyl CLI (a Go binary). Each
wheel version keeps its own copy of the binary at ``~/.revyl/bin/v<version>/``
and downloads it from the matching GitHub release on first run, so upgrading
the package always upgrades the binary.
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

__version__ = "0.1.60"
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


def get_binary_path() -> Path:
    """
    Return the local path of the binary matching this wheel version.
    """
    return Path.home() / ".revyl" / "bin" / f"v{__version__}" / _binary_name()


def _sha256_file(path: Path) -> str:
    hasher = hashlib.sha256()
    with path.open("rb") as file_handle:
        for chunk in iter(lambda: file_handle.read(_HASH_CHUNK_SIZE), b""):
            hasher.update(chunk)
    return hasher.hexdigest()


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


def _fetch_expected_checksum(binary_name: str, tag: str) -> str | None:
    """
    Fetch the expected SHA-256 checksum for a release binary.

    Returns None (with a stderr warning) when checksums.txt is unavailable
    so callers can still proceed with an unverified download.
    """
    checksum_url = f"https://github.com/{REPO}/releases/download/{tag}/checksums.txt"
    try:
        with urllib.request.urlopen(checksum_url) as response:
            raw_checksums = response.read().decode("utf-8", errors="replace")
    except Exception as exc:
        print(
            f"Warning: Could not download checksums ({exc}). "
            "Skipping integrity verification.",
            file=sys.stderr,
        )
        return None

    expected = _parse_checksums(raw_checksums).get(binary_name)
    if not expected:
        print(
            f"Warning: No checksum found for '{binary_name}' in release checksums. "
            "Skipping integrity verification.",
            file=sys.stderr,
        )
        return None

    return expected


def download_binary() -> Path:
    """
    Download the release binary matching this wheel version.

    The download is verified against the release's checksums.txt when
    available; if the checksum file is unavailable the binary is still
    installed, with a warning printed to stderr.

    Returns:
        Path to the downloaded binary.

    Raises:
        RuntimeError: If the download fails or the checksum does not match.
    """
    tag = f"v{__version__}"
    binary_name = _binary_name()
    binary_url = f"https://github.com/{REPO}/releases/download/{tag}/{binary_name}"
    expected_checksum = _fetch_expected_checksum(binary_name, tag)

    binary_path = get_binary_path()
    binary_path.parent.mkdir(parents=True, exist_ok=True)
    print(f"Downloading Revyl CLI from {binary_url}...")

    temp_path: Path | None = None
    try:
        # Download next to the destination so os.replace is an atomic rename.
        with urllib.request.urlopen(binary_url) as response, tempfile.NamedTemporaryFile(
            delete=False, dir=binary_path.parent
        ) as tmp:
            shutil.copyfileobj(response, tmp)
            temp_path = Path(tmp.name)
        if expected_checksum is not None:
            actual_checksum = _sha256_file(temp_path)
            if actual_checksum != expected_checksum:
                raise RuntimeError(
                    f"Checksum verification failed for {binary_name} "
                    f"(expected {expected_checksum}, got {actual_checksum})"
                )
    except Exception as exc:
        if temp_path is not None:
            temp_path.unlink(missing_ok=True)
        raise RuntimeError(f"Failed to download binary: {exc}") from exc

    if platform.system() != "Windows":
        temp_path.chmod(0o755)
    os.replace(temp_path, binary_path)

    # Drop binaries kept for other wheel versions, including the legacy
    # flat layout (bin/revyl-<platform>-<arch> + .sha256 sidecar).
    bin_dir = binary_path.parent.parent
    for stale_dir in bin_dir.glob("v*"):
        if stale_dir != binary_path.parent:
            shutil.rmtree(stale_dir, ignore_errors=True)
    for legacy in bin_dir.glob("revyl-*"):
        if legacy.is_file():
            try:
                legacy.unlink()
            except OSError:
                pass

    print(f"Downloaded to {binary_path}")
    return binary_path


def ensure_binary() -> Path:
    """
    Return the path of the binary to run, downloading it on first use.

    ``REVYL_BINARY`` overrides everything (local dev / CI).

    Raises:
        RuntimeError: If the binary cannot be downloaded, or if
            ``REVYL_BINARY`` points to a non-existent file.
    """
    env_override = os.environ.get("REVYL_BINARY")
    if env_override:
        resolved = Path(env_override).expanduser().resolve()
        if not resolved.exists():
            raise RuntimeError(
                f"REVYL_BINARY points to non-existent path: {resolved}"
            )
        return resolved

    binary_path = get_binary_path()
    if binary_path.exists():
        return binary_path
    return download_binary()


def run_binary(args: Sequence[str]) -> int:
    """
    Run the Revyl binary with the provided args.
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
