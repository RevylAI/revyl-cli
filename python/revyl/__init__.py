"""Python launcher for the native Revyl CLI."""

from __future__ import annotations

import sys

from ._binary import (
    __version__,
    ensure_binary,
    get_binary_path,
    get_platform_info,
    main,
)

__all__ = [
    "__version__",
    "main",
    "get_platform_info",
    "get_binary_path",
    "ensure_binary",
]


if __name__ == "__main__":
    sys.exit(main())
