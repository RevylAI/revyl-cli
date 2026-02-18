"""
Revyl CLI - AI-powered mobile app testing

This package provides a Python wrapper that downloads and runs
the Revyl CLI binary.
"""

import os
import platform
import subprocess
import sys
import urllib.request
from pathlib import Path

__version__ = "0.1.3"

REPO = "RevylAI/revyl-cli"


def get_platform_info() -> tuple[str, str, str]:
    """
    Get platform information for binary download.
    
    Returns:
        Tuple of (platform_str, arch_str, extension)
    
    Raises:
        RuntimeError: If platform or architecture is not supported
    """
    system = platform.system().lower()
    machine = platform.machine().lower()
    
    # Map system
    if system == "darwin":
        platform_str = "darwin"
    elif system == "linux":
        platform_str = "linux"
    elif system == "windows":
        platform_str = "windows"
    else:
        raise RuntimeError(f"Unsupported platform: {system}")
    
    # Map architecture
    if machine in ("x86_64", "amd64"):
        arch_str = "amd64"
    elif machine in ("arm64", "aarch64"):
        arch_str = "arm64"
    else:
        raise RuntimeError(f"Unsupported architecture: {machine}")
    
    ext = ".exe" if system == "windows" else ""
    
    return platform_str, arch_str, ext


def get_binary_path() -> Path:
    """
    Get the path where the binary should be stored.
    
    Returns:
        Path to the binary location
    """
    # Store in user's home directory
    home = Path.home()
    revyl_dir = home / ".revyl" / "bin"
    revyl_dir.mkdir(parents=True, exist_ok=True)
    
    platform_str, arch_str, ext = get_platform_info()
    binary_name = f"revyl-{platform_str}-{arch_str}{ext}"
    
    return revyl_dir / binary_name


def download_binary(version: str = "latest") -> Path:
    """
    Download the Revyl binary for the current platform.
    
    Args:
        version: Version to download (default: latest)
    
    Returns:
        Path to the downloaded binary
    
    Raises:
        RuntimeError: If download fails
    """
    platform_str, arch_str, ext = get_platform_info()
    binary_name = f"revyl-{platform_str}-{arch_str}{ext}"
    
    if version == "latest":
        url = f"https://github.com/{REPO}/releases/latest/download/{binary_name}"
    else:
        url = f"https://github.com/{REPO}/releases/download/{version}/{binary_name}"
    
    binary_path = get_binary_path()
    
    print(f"Downloading Revyl CLI from {url}...")
    
    try:
        urllib.request.urlretrieve(url, binary_path)
        
        # Make executable on Unix
        if platform.system() != "Windows":
            binary_path.chmod(0o755)
        
        print(f"Downloaded to {binary_path}")
        return binary_path
        
    except Exception as e:
        raise RuntimeError(f"Failed to download binary: {e}")


def ensure_binary() -> Path:
    """
    Ensure the binary exists, downloading if necessary.
    
    Returns:
        Path to the binary
    """
    binary_path = get_binary_path()
    
    if not binary_path.exists():
        return download_binary()
    
    return binary_path


def main() -> int:
    """
    Main entry point - runs the Revyl CLI binary.
    
    Returns:
        Exit code from the binary
    """
    try:
        binary_path = ensure_binary()
    except RuntimeError as e:
        print(f"Error: {e}", file=sys.stderr)
        print(f"\nYou can manually download from: https://github.com/{REPO}/releases")
        return 1
    
    # Run the binary with all arguments
    args = [str(binary_path)] + sys.argv[1:]
    
    try:
        result = subprocess.run(args)
        return result.returncode
    except KeyboardInterrupt:
        return 130
    except Exception as e:
        print(f"Error running Revyl: {e}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
