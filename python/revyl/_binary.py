"""Resolve and run the native Revyl CLI bundled in this platform wheel."""

from __future__ import annotations

import os
import platform
import subprocess
import sys
from pathlib import Path
from typing import Sequence

__version__ = "0.1.64"


def get_platform_info() -> tuple[str, str, str]:
    """Return the operating system, architecture, and executable suffix."""
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
    """Return the release asset name for the current platform."""
    platform_str, arch_str, ext = get_platform_info()
    return f"revyl-{platform_str}-{arch_str}{ext}"


def get_binary_path() -> Path:
    """Return the native CLI path bundled in the installed wheel."""
    return Path(__file__).resolve().parent / "_bin" / _binary_name()


def ensure_binary() -> Path:
    """Return an executable Revyl CLI path.

    ``REVYL_BINARY`` overrides everything (local dev / CI).

    Returns:
        Path to the configured or wheel-bundled CLI binary.

    Raises:
        RuntimeError: If the configured or bundled binary is missing or not executable.
    """
    env_override = os.environ.get("REVYL_BINARY")
    if env_override:
        resolved = Path(env_override).expanduser().resolve()
        if not resolved.is_file():
            raise RuntimeError(
                f"REVYL_BINARY points to non-existent path: {resolved}"
            )
        return resolved

    binary_path = get_binary_path()
    if not binary_path.is_file():
        raise RuntimeError(
            "This Revyl package does not contain a CLI binary for "
            f"{platform.system()}/{platform.machine()}. "
            "Reinstall revyl with a supported platform wheel."
        )
    if platform.system() != "Windows" and not os.access(binary_path, os.X_OK):
        raise RuntimeError(
            f"The bundled Revyl CLI is not executable: {binary_path}. "
            "Reinstall the revyl package."
        )
    return binary_path


def run_binary(args: Sequence[str]) -> int:
    """Run the bundled Revyl CLI with the provided arguments."""
    binary_path = ensure_binary()
    result = subprocess.run([str(binary_path), *args], check=False)
    return result.returncode


def main() -> int:
    """Run the `revyl` console-script entry point."""
    try:
        return run_binary(sys.argv[1:])
    except KeyboardInterrupt:
        return 130
    except RuntimeError as exc:
        print(f"Error: {exc}", file=sys.stderr)
        return 1
    except Exception as exc:
        print(f"Error running Revyl: {exc}", file=sys.stderr)
        return 1
