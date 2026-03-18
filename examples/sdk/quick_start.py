#!/usr/bin/env python3
"""Minimal Revyl SDK session: start a device, tap an element, take a screenshot.

The fastest way to see the SDK in action. Just run it.

Usage:
    python examples/sdk/quick_start.py
    python examples/sdk/quick_start.py --platform android
    python examples/sdk/quick_start.py --dev
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


def main() -> int:
    parser = argparse.ArgumentParser(
        description="Start a device, tap, screenshot — the simplest SDK demo.",
    )
    parser.add_argument("--platform", default="ios", choices=["ios", "android"])
    parser.add_argument("--app-url", default=None, help="Direct URL to an .apk or .app archive.")
    parser.add_argument("--open", action="store_true", help="Open the live viewer in the browser.")
    parser.add_argument("--dev", action="store_true", help="Target local dev backend.")
    parser.add_argument("--output-dir", default=None, help="Custom output directory for assets.")
    args = parser.parse_args()

    app_url = args.app_url or BUG_BAZAAR_BUILDS[args.platform]
    cli = RevylCLI(dev_mode=args.dev)
    run_dir = _make_run_dir("quick-start", override=args.output_dir)
    screenshots: list[str] = []
    step_num = 0
    t_run = time.monotonic()

    _banner("Revyl SDK  ·  Quick Start", {
        "Platform": args.platform,
        "App": app_url.rsplit("/", 1)[-1],
        "Output": str(run_dir),
    })

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

    try:
        print(f"  {_dim('Provisioning device...')}", end="", flush=True)
        t0_prov = time.monotonic()
        with DeviceClient.start(
            platform=args.platform,
            app_url=app_url,
            timeout=300,
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

            t0 = _step("Taking screenshot")
            path = str(run_dir / "screenshots" / "01_app_launched.png")
            device.screenshot(out=path)
            screenshots.append("01_app_launched.png")
            _done(t0, "screenshots/01_app_launched.png")

            t0 = _step("Tapping first interactive element")
            device.tap(target="first visible button or link")
            _done(t0)

            t0 = _step("Taking screenshot")
            path = str(run_dir / "screenshots" / "02_after_tap.png")
            device.screenshot(out=path)
            screenshots.append("02_after_tap.png")
            _done(t0, "screenshots/02_after_tap.png")

            t0 = _step("Swiping up on main content")
            device.swipe(target="main content area", direction="up")
            _done(t0)

            t0 = _step("Taking screenshot")
            path = str(run_dir / "screenshots" / "03_after_scroll.png")
            device.screenshot(out=path)
            screenshots.append("03_after_scroll.png")
            _done(t0, "screenshots/03_after_scroll.png")

            t0 = _step("Fetching session report")
            report = device.report()
            _done(t0)

        total_time = time.monotonic() - t_run
        report_url = report.get("report_url", "")

        bar = _cyan("─" * 52)
        print(f"\n  {bar}")
        print(f"  {_green('✓')} {_bold(f'Done in {total_time:.1f}s')}  ·  {TOTAL_STEPS} steps\n")

        if screenshots:
            print(f"    {_dim('Screenshots:')}")
            for s in screenshots:
                print(f"      {s}")
            print()

        if report_url:
            print(f"    {_dim('Report:'.ljust(12))}{report_url}")
        print(f"    {_dim('Output:'.ljust(12))}{run_dir}")

        print(f"\n    {_dim('Next up →')} try {_bold('checkout_flow.py')} or {_bold('bug_detection.py')}")
        print(f"  {bar}\n")

    except RevylError as exc:
        print(f"\n  {_red('✗')} {_bold('Error:')} {exc}", file=sys.stderr)
        return 1

    return 0


if __name__ == "__main__":
    raise SystemExit(main())
