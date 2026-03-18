#!/usr/bin/env python3
"""Programmatic exploratory testing on the Bug Bazaar product catalog.

Demonstrates how to combine Python control flow with the Revyl SDK to
dynamically explore an app: iterate over products, extract prices, validate
detail pages, and exercise device controls like home, kill, and relaunch.

Usage:
    python examples/sdk/exploratory_testing.py
    python examples/sdk/exploratory_testing.py --platform android
    python examples/sdk/exploratory_testing.py --dev
"""

from __future__ import annotations

import argparse
import json
import os
import sys
import time
from dataclasses import dataclass, field
from datetime import datetime
from pathlib import Path
from typing import Optional

from revyl import DeviceClient, RevylCLI, RevylError

BUG_BAZAAR_BUILDS = {
    "android": "https://pub-b03f222a53c447c18ef5f8d365a2f00e.r2.dev/bug-bazaar/bug-bazaar-preview.apk",
    "ios": "https://pub-b03f222a53c447c18ef5f8d365a2f00e.r2.dev/bug-bazaar/bug-bazaar-preview-simulator.tar.gz",
}

PRODUCTS_TO_CHECK = [
    "Hercules Beetle",
    "Blue Morpho",
    "Goliath Beetle",
    "Atlas Moth",
]

# ---------------------------------------------------------------------------
# Output helpers (zero deps, color auto-disabled outside a tty)
# ---------------------------------------------------------------------------

_NO_COLOR = not hasattr(sys.stdout, "isatty") or not sys.stdout.isatty() or os.environ.get("NO_COLOR")


def _c(code: str, text: str) -> str:
    """Wrap *text* in an ANSI escape sequence, unless color is disabled."""
    return text if _NO_COLOR else f"\033[{code}m{text}\033[0m"


def _green(t: str) -> str: return _c("32", t)
def _red(t: str) -> str: return _c("31", t)
def _yellow(t: str) -> str: return _c("33", t)
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


@dataclass
class ProductResult:
    """Holds extracted data for a single product."""

    name: str
    price: str = ""
    detail_ok: bool = False
    elapsed: float = 0.0
    screenshot: str = ""


@dataclass
class ExplorationReport:
    """Aggregated results from the exploratory run."""

    products: list[ProductResult] = field(default_factory=list)
    errors: list[str] = field(default_factory=list)
    screenshots: list[str] = field(default_factory=list)


def explore_product(
    device: DeviceClient,
    product_name: str,
    run_dir: Path,
) -> ProductResult:
    """Navigate to a product, extract its price, and validate the detail page.

    Args:
        device: An active DeviceClient session.
        product_name: Display name of the product to find and inspect.
        run_dir: Output directory for screenshots.

    Returns:
        A ProductResult with the extracted price and validation outcome.

    Raises:
        RevylError: If a device command fails.
    """
    result = ProductResult(name=product_name)
    t0 = time.monotonic()

    device.instruction(f"Tap on the {product_name} product card")

    price_data = device.extract(
        f"Read the price displayed on the {product_name} detail page",
        variable_name="product_price",
    )
    result.price = str(price_data.get("value", ""))

    try:
        device.validation(
            f'The product detail page shows "{product_name}" with a visible price and an ADD TO CART button'
        )
        result.detail_ok = True
    except RevylError:
        result.detail_ok = False

    filename = f"product_{product_name.lower().replace(' ', '_')}.png"
    path = str(run_dir / "screenshots" / filename)
    device.screenshot(out=path)
    result.screenshot = filename

    device.instruction("Navigate back to the shop screen")

    result.elapsed = time.monotonic() - t0
    return result


def run_exploration(device: DeviceClient, run_dir: Path) -> ExplorationReport:
    """Iterate over a list of products, inspect each, and build a report.

    Args:
        device: An active DeviceClient session with Bug Bazaar installed.
        run_dir: Output directory for screenshots.

    Returns:
        An ExplorationReport summarizing findings for each product.

    Raises:
        RevylError: If a critical device command fails.
    """
    report = ExplorationReport()
    total_phases = len(PRODUCTS_TO_CHECK) + 2

    print(f"  {_dim(f'[1/{total_phases}]')}  Verifying shop screen", end="", flush=True)
    t0 = time.monotonic()
    device.validation("The shop screen is visible with product cards")
    elapsed = time.monotonic() - t0
    print(f"  {_green('✓')}  {_dim(f'({elapsed:.1f}s)')}")

    for i, product_name in enumerate(PRODUCTS_TO_CHECK, 2):
        print(f"  {_dim(f'[{i}/{total_phases}]')}  Exploring: {_bold(product_name)}", end="", flush=True)
        try:
            product_result = explore_product(device, product_name, run_dir)
            report.products.append(product_result)
            report.screenshots.append(product_result.screenshot)

            icon = _green("✓") if product_result.detail_ok else _red("✗")
            print(f"  {icon}  {_dim(f'({product_result.elapsed:.1f}s)')}")
            print(f"         {_dim('price:')} {product_result.price}  {_dim('→')} screenshots/{product_result.screenshot}")
        except RevylError as exc:
            report.errors.append(f"{product_name}: {exc}")
            print(f"  {_red('✗')}  {_dim(str(exc))}")

    lifecycle_idx = len(PRODUCTS_TO_CHECK) + 2
    print(f"\n  {_dim(f'[{lifecycle_idx}/{total_phases}]')}  App lifecycle: home → kill → relaunch", end="", flush=True)
    t0 = time.monotonic()
    device.go_home()
    device.kill_app()
    device.navigate("bug-bazaar://")
    device.wait(duration_ms=2000)
    device.validation("The app has relaunched and the shop screen is visible")
    filename = "lifecycle_relaunch.png"
    path = str(run_dir / "screenshots" / filename)
    device.screenshot(out=path)
    report.screenshots.append(filename)
    elapsed = time.monotonic() - t0
    print(f"  {_green('✓')}  {_dim(f'({elapsed:.1f}s)')}")
    print(f"         {_dim('→')} screenshots/{filename}")

    return report


def main() -> int:
    parser = argparse.ArgumentParser(
        description="Explore the Bug Bazaar product catalog and extract data from each product.",
    )
    parser.add_argument("--platform", default="ios", choices=["ios", "android"])
    parser.add_argument("--app-url", default=None, help="Direct URL to the Bug Bazaar build.")
    parser.add_argument("--json-report", action="store_true", help="Save a JSON report to the output directory.")
    parser.add_argument("--open", action="store_true", help="Open the live viewer in the browser.")
    parser.add_argument("--dev", action="store_true", help="Target local dev backend.")
    parser.add_argument("--output-dir", default=None, help="Custom output directory for assets.")
    args = parser.parse_args()

    app_url = args.app_url or BUG_BAZAAR_BUILDS[args.platform]
    cli = RevylCLI(dev_mode=args.dev)
    run_dir = _make_run_dir("exploratory-testing", override=args.output_dir)
    t_run = time.monotonic()

    _banner("Revyl SDK  ·  Exploratory Testing", {
        "Platform": args.platform,
        "App": app_url.rsplit("/", 1)[-1],
        "Products": f"{len(PRODUCTS_TO_CHECK)} to explore",
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

            report = run_exploration(device, run_dir)

        duration = time.monotonic() - t_run

        if args.json_report:
            report_path = run_dir / "report.json"
            payload = {
                "products": [
                    {
                        "name": p.name,
                        "price": p.price,
                        "detail_ok": p.detail_ok,
                        "elapsed_s": round(p.elapsed, 1),
                    }
                    for p in report.products
                ],
                "errors": report.errors,
                "duration_s": round(duration, 1),
            }
            report_path.write_text(json.dumps(payload, indent=2))

        bar = _cyan("─" * 52)
        has_issues = (
            any(not p.detail_ok for p in report.products)
            or report.errors
        )

        print(f"\n  {bar}")
        if not has_issues:
            print(f"  {_green('✓')} {_bold('Exploration complete')}  ·  {_dim(f'{duration:.1f}s')}\n")
        else:
            print(f"  {_yellow('!')} {_bold('Exploration complete with issues')}  ·  {_dim(f'{duration:.1f}s')}\n")

        print(f"    {_dim('Products:')}")
        for p in report.products:
            icon = _green("✓") if p.detail_ok else _red("✗")
            print(f"      {icon}  {p.name.ljust(20)} {_dim(p.price or 'no price')}")
        print()

        if report.errors:
            print(f"    {_dim('Errors:')}")
            for err in report.errors:
                print(f"      {_red('✗')} {err}")
            print()

        if report.screenshots:
            print(f"    {_dim('Screenshots:')}")
            for s in report.screenshots:
                print(f"      {s}")
            print()

        print(f"    {_dim('Output:'.ljust(12))}{run_dir}")
        if args.json_report:
            print(f"    {_dim('Report:'.ljust(12))}{run_dir / 'report.json'}")
        print(f"\n    {_dim('Next up →')} try {_bold('all_actions.py')} for the full SDK surface")
        print(f"  {bar}\n")

    except RevylError as exc:
        print(f"\n  {_red('✗')} {_bold('Error:')} {exc}", file=sys.stderr)
        return 1

    return 0


if __name__ == "__main__":
    raise SystemExit(main())
