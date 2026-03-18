#!/usr/bin/env python3
"""Detect the intentional Orchid Mantis cart bug in Bug Bazaar.

Bug Bazaar has a known defect: adding "Orchid Mantis" to the cart silently
substitutes "Gold Tortoise Beetle" instead. This script exercises that flow
and uses ``extract`` + ``validation`` to surface the mismatch automatically.

Usage:
    python examples/sdk/bug_detection.py
    python examples/sdk/bug_detection.py --platform android
    python examples/sdk/bug_detection.py --dev
"""

from __future__ import annotations

import argparse
import os
import sys
import time
from datetime import datetime
from pathlib import Path
from typing import Optional

from revyl import DeviceClient, RevylCLI, RevylError

BUG_BAZAAR_BUILDS = {
    "android": "https://pub-b03f222a53c447c18ef5f8d365a2f00e.r2.dev/bug-bazaar/bug-bazaar-preview.apk",
    "ios": "https://pub-b03f222a53c447c18ef5f8d365a2f00e.r2.dev/bug-bazaar/bug-bazaar-preview-simulator.tar.gz",
}

EXPECTED_PRODUCT = "Orchid Mantis"
EXPECTED_PRICE = "$62.00"
TOTAL_STEPS = 6

# ---------------------------------------------------------------------------
# Output helpers (zero deps, color auto-disabled outside a tty)
# ---------------------------------------------------------------------------

_NO_COLOR = not hasattr(sys.stdout, "isatty") or not sys.stdout.isatty() or os.environ.get("NO_COLOR")


def _c(code: str, text: str) -> str:
    """Wrap *text* in an ANSI escape sequence, unless color is disabled."""
    return text if _NO_COLOR else f"\033[{code}m{text}\033[0m"


def _green(t: str) -> str: return _c("32", t)
def _red(t: str) -> str: return _c("31", t)
def _cyan(t: str) -> str: return _c("36", t)
def _dim(t: str) -> str: return _c("2", t)
def _bold(t: str) -> str: return _c("1", t)


def _make_run_dir(name: str, override: Optional[str] = None) -> Path:
    """Create a timestamped output directory for screenshots and reports."""
    d = Path(override) if override else Path("revyl-runs") / name / datetime.now().strftime("%Y-%m-%d_%H-%M-%S")
    (d / "screenshots").mkdir(parents=True, exist_ok=True)
    return d


def _banner(title: str, fields: dict[str, str]) -> None:
    """Print a styled run header with key-value metadata."""
    bar = _cyan("─" * 52)
    print(f"\n  {bar}")
    print(f"  {_bold(title)}\n")
    for k, v in fields.items():
        label = f"{k}:".ljust(14)
        print(f"    {_dim(label)}{v}")
    print(f"  {bar}\n")


# ---------------------------------------------------------------------------


def detect_cart_bug(
    device: DeviceClient,
    run_dir: Path,
) -> tuple[bool, str, list[str]]:
    """Add Orchid Mantis to the cart and check if the correct item appears.

    Args:
        device: An active DeviceClient session with Bug Bazaar installed.
        run_dir: Output directory for screenshots.

    Returns:
        Tuple of (bug_found, cart_product_name, screenshot_filenames).

    Raises:
        RevylError: If any device command fails.
    """
    screenshots: list[str] = []
    step_num = 0

    def _step(label: str) -> float:
        """Print a step header and return the start timestamp."""
        nonlocal step_num
        step_num += 1
        print(f"  {_dim(f'[{step_num}/{TOTAL_STEPS}]')}  {label}", end="", flush=True)
        return time.monotonic()

    def _done(t0: float, asset: str = "") -> None:
        """Print step completion with elapsed time and optional saved asset."""
        elapsed = time.monotonic() - t0
        print(f"  {_green('✓')}  {_dim(f'({elapsed:.1f}s)')}")
        if asset:
            print(f"         {_dim('→')} {asset}")

    t0 = _step("Verifying shop screen is visible")
    device.validation("The shop screen is visible with product cards")
    path = str(run_dir / "screenshots" / "01_shop.png")
    device.screenshot(out=path)
    screenshots.append("01_shop.png")
    _done(t0, "screenshots/01_shop.png")

    t0 = _step("Scrolling to find Orchid Mantis")
    device.instruction(
        "Scroll down slowly to find the Orchid Mantis product card, then tap on it"
    )
    _done(t0)

    t0 = _step("Verifying product detail page")
    device.validation(
        f'The product detail page shows "{EXPECTED_PRODUCT}" with a price of "{EXPECTED_PRICE}"'
    )
    path = str(run_dir / "screenshots" / "02_product_detail.png")
    device.screenshot(out=path)
    screenshots.append("02_product_detail.png")
    _done(t0, "screenshots/02_product_detail.png")

    t0 = _step("Adding to cart")
    device.instruction("Tap the ADD TO CART button")
    _done(t0)

    t0 = _step("Extracting cart item name")
    result = device.extract(
        "Read the product name shown in the cart",
        variable_name="cart_product_name",
    )
    cart_product = result.get("value", "")
    path = str(run_dir / "screenshots" / "03_cart.png")
    device.screenshot(out=path)
    screenshots.append("03_cart.png")
    _done(t0, "screenshots/03_cart.png")

    t0 = _step("Checking for product mismatch")
    bug_found = EXPECTED_PRODUCT.lower() not in str(cart_product).lower()
    _done(t0)

    return bug_found, str(cart_product), screenshots


def main() -> int:
    parser = argparse.ArgumentParser(
        description="Detect the Orchid Mantis cart substitution bug in Bug Bazaar.",
    )
    parser.add_argument("--platform", default="ios", choices=["ios", "android"])
    parser.add_argument("--app-url", default=None, help="Direct URL to the Bug Bazaar build.")
    parser.add_argument("--open", action="store_true", help="Open the live viewer in the browser.")
    parser.add_argument("--dev", action="store_true", help="Target local dev backend.")
    parser.add_argument("--output-dir", default=None, help="Custom output directory for assets.")
    args = parser.parse_args()

    app_url = args.app_url or BUG_BAZAAR_BUILDS[args.platform]
    cli = RevylCLI(dev_mode=args.dev)
    run_dir = _make_run_dir("bug-detection", override=args.output_dir)
    t_run = time.monotonic()

    _banner("Revyl SDK  ·  Bug Detection", {
        "Platform": args.platform,
        "App": app_url.rsplit("/", 1)[-1],
        "Target": f"Cart substitution ({EXPECTED_PRODUCT})",
        "Output": str(run_dir),
    })

    try:
        print(f"  {_dim('Provisioning device...')}", end="", flush=True)
        t0_prov = time.monotonic()
        with DeviceClient.start(
            platform=args.platform,
            app_url=app_url,
            timeout=600,
            open_viewer=args.open,
            cli=cli,
            verbose=False,
            auto_report=False,
        ) as device:
            prov_elapsed = time.monotonic() - t0_prov
            print(f"  {_green('✓')}  {_dim(f'({prov_elapsed:.1f}s)')}")

            try:
                rd = device.report()
                url = rd.get("report_url", "")
                if url:
                    print(f"  {_dim('Live view:'.ljust(14))}{url}\n")
                else:
                    print()
            except RevylError:
                print()

            bug_found, cart_product, screenshots = detect_cart_bug(device, run_dir)

        duration = time.monotonic() - t_run
        bar = _cyan("─" * 52)
        print(f"\n  {bar}")

        if bug_found:
            print(f"  {_red('✗')} {_bold('BUG DETECTED')}  ·  {_dim(f'{duration:.1f}s')}\n")
            print(f"    Expected:  {_bold(EXPECTED_PRODUCT)}")
            print(f"    Got:       {_red(_bold(cart_product))}")
            print("\n    The cart silently substituted the wrong product.")
        else:
            print(f"  {_green('✓')} {_bold('No bug found')}  ·  {_dim(f'{duration:.1f}s')}\n")
            print(f"    Cart correctly shows {_bold(EXPECTED_PRODUCT)}.")

        print()
        if screenshots:
            print(f"    {_dim('Screenshots:')}")
            for s in screenshots:
                print(f"      {s}")
            print()

        print(f"    {_dim('Output:'.ljust(12))}{run_dir}")
        print(f"\n    {_dim('Next up →')} try {_bold('exploratory_testing.py')} for catalog exploration")
        print(f"  {bar}\n")

        return 1 if bug_found else 0

    except RevylError as exc:
        print(f"\n  {_red('✗')} {_bold('Error:')} {exc}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
