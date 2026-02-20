"""
Revyl Python package.

- `revyl` console script proxies to the Revyl CLI binary.
- `DeviceClient` provides a thin Python SDK over device CLI commands.
"""

from __future__ import annotations

import sys

from ._binary import (
    __version__,
    download_binary,
    ensure_binary,
    get_binary_path,
    get_platform_info,
    main,
)
from .sdk import DeviceClient, RevylCLI, RevylError

__all__ = [
    "__version__",
    "main",
    "get_platform_info",
    "get_binary_path",
    "download_binary",
    "ensure_binary",
    "RevylCLI",
    "DeviceClient",
    "RevylError",
]


if __name__ == "__main__":
    sys.exit(main())
