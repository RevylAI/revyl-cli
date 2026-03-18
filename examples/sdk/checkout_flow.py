#!/usr/bin/env python3
"""End-to-end checkout flow on the Bug Bazaar demo app.

Walks through product selection, cart, shipping, payment, and order
confirmation — validating each step along the way. A realistic e-commerce
test flow you can adapt for your own app.

Usage:
    python examples/sdk/checkout_flow.py
    python examples/sdk/checkout_flow.py --platform android
    python examples/sdk/checkout_flow.py --dev
"""

from __future__ import annotations

import argparse
import os
import sys
import time
from dataclasses import dataclass
from datetime import datetime
from pathlib import Path
from typing import Optional

from revyl import DeviceClient, RevylCLI, RevylError

BUG_BAZAAR_BUILDS = {
    "android": "https://pub-b03f222a53c447c18ef5f8d365a2f00e.r2.dev/bug-bazaar/bug-bazaar-preview.apk",
    "ios": "https://pub-b03f222a53c447c18ef5f8d365a2f00e.r2.dev/bug-bazaar/bug-bazaar-preview-simulator.tar.gz",
}

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


@dataclass(frozen=True)
class CheckoutStep:
    """A single step in the checkout flow."""

    label: str
    action: str
    validation: Optional[str] = None
    screenshot: Optional[str] = None


CHECKOUT_STEPS: list[CheckoutStep] = [
    CheckoutStep(
        label="Verify shop screen",
        action="",
        validation="The shop screen is visible with product cards and the BUG BAZAAR header",
        screenshot="01_shop_screen.png",
    ),
    CheckoutStep(
        label="Select product",
        action="Tap on the Hercules Beetle product card",
    ),
    CheckoutStep(
        label="Verify product detail",
        action="",
        validation='The product detail page shows "Hercules Beetle" with a price and an ADD TO CART button',
        screenshot="02_product_detail.png",
    ),
    CheckoutStep(
        label="Add to cart",
        action="Tap the ADD TO CART button",
    ),
    CheckoutStep(
        label="Verify cart",
        action="",
        validation="The cart screen shows Hercules Beetle with the correct price",
        screenshot="03_cart.png",
    ),
    CheckoutStep(
        label="Begin checkout",
        action="Tap the CHECKOUT button",
    ),
    CheckoutStep(
        label="Fill shipping",
        action="Tap 'Use saved address' to autofill the shipping fields, then tap CONTINUE TO PAYMENT",
    ),
    CheckoutStep(
        label="Verify payment form",
        action="",
        validation="The payment method form is visible with card number, expiry, CVV, and name fields",
    ),
    CheckoutStep(
        label="Fill payment",
        action="Tap 'Use demo card' to autofill the payment fields, then tap REVIEW ORDER",
    ),
    CheckoutStep(
        label="Verify order review",
        action="",
        validation="The order review shows Hercules Beetle, the shipping address, and a masked card number",
        screenshot="04_order_review.png",
    ),
    CheckoutStep(
        label="Place order",
        action="Tap the PLACE ORDER button",
    ),
    CheckoutStep(
        label="Verify confirmation",
        action="",
        validation='The confirmation screen shows "Order Placed!" with an order ID',
        screenshot="05_order_confirmed.png",
    ),
    CheckoutStep(
        label="View orders",
        action="Tap VIEW ORDERS",
    ),
    CheckoutStep(
        label="Verify order history",
        action="",
        validation='The Account page shows an order with "Processing" status',
        screenshot="06_order_history.png",
    ),
]


def run_checkout(device: DeviceClient, run_dir: Path) -> list[str]:
    """Execute every step in the checkout flow with progress tracking.

    Args:
        device: An active DeviceClient session.
        run_dir: Output directory for screenshots.

    Returns:
        List of screenshot filenames saved during the flow.

    Raises:
        RevylError: If any device command fails.
    """
    total = len(CHECKOUT_STEPS)
    screenshots: list[str] = []

    for i, step in enumerate(CHECKOUT_STEPS, 1):
        print(f"  {_dim(f'[{i}/{total}]')}  {step.label}", end="", flush=True)
        t0 = time.monotonic()

        if step.action:
            device.instruction(step.action)

        if step.validation:
            device.validation(step.validation)

        if step.screenshot:
            path = str(run_dir / "screenshots" / step.screenshot)
            device.screenshot(out=path)
            screenshots.append(step.screenshot)

        elapsed = time.monotonic() - t0
        print(f"  {_green('✓')}  {_dim(f'({elapsed:.1f}s)')}")

        if step.screenshot:
            print(f"         {_dim('→')} screenshots/{step.screenshot}")

    return screenshots


def main() -> int:
    parser = argparse.ArgumentParser(
        description="Run a full checkout flow on Bug Bazaar and validate each stage.",
    )
    parser.add_argument("--platform", default="ios", choices=["ios", "android"])
    parser.add_argument("--app-url", default=None, help="Direct URL to the Bug Bazaar build.")
    parser.add_argument("--open", action="store_true", help="Open the live viewer in the browser.")
    parser.add_argument("--dev", action="store_true", help="Target local dev backend.")
    parser.add_argument("--output-dir", default=None, help="Custom output directory for assets.")
    args = parser.parse_args()

    app_url = args.app_url or BUG_BAZAAR_BUILDS[args.platform]
    cli = RevylCLI(dev_mode=args.dev)
    run_dir = _make_run_dir("checkout-flow", override=args.output_dir)
    t_run = time.monotonic()

    _banner("Revyl SDK  ·  Checkout Flow", {
        "Platform": args.platform,
        "App": app_url.rsplit("/", 1)[-1],
        "Steps": f"{len(CHECKOUT_STEPS)} checkout stages",
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

            screenshots = run_checkout(device, run_dir)

        duration = time.monotonic() - t_run
        bar = _cyan("─" * 52)
        print(f"\n  {bar}")
        print(f"  {_green('✓')} {_bold(f'Checkout completed')}  ·  {_dim(f'{duration:.1f}s')}\n")

        if screenshots:
            print(f"    {_dim('Screenshots:')}")
            for s in screenshots:
                print(f"      {s}")
            print()

        print(f"    {_dim('Output:'.ljust(12))}{run_dir}")
        print(f"\n    {_dim('Next up →')} try {_bold('bug_detection.py')} to catch a real bug")
        print(f"  {bar}\n")

    except RevylError as exc:
        print(f"\n  {_red('✗')} {_bold('Checkout failed:')} {exc}", file=sys.stderr)
        return 1

    return 0


if __name__ == "__main__":
    raise SystemExit(main())
