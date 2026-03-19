#!/usr/bin/env python3
"""Exercise every DeviceClient action type against a live device.

Runs through taps, swipes, typing, gestures, device controls, live steps,
and app lifecycle in a single session. A comprehensive reference for the full
SDK surface and a sanity check that all actions work.

Usage:
    python examples/sdk/all_actions.py
    python examples/sdk/all_actions.py --platform android
    python examples/sdk/all_actions.py --dev
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

TOTAL_STEPS = 44

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


def _section(title: str) -> None:
    """Print a colored section divider."""
    print(f"\n  {_cyan('── ' + title + ' ──')}")


# ---------------------------------------------------------------------------


@dataclass
class StepResult:
    """Outcome of a single action step."""

    name: str
    passed: bool
    elapsed: float = 0.0
    detail: str = ""


def run_step(name: str, fn, results: list[StepResult]) -> Optional[dict]:
    """Run a named step, record pass/fail with timing, and return the result.

    Args:
        name: Human-readable label for the step.
        fn: Zero-arg callable that performs the action and returns a dict.
        results: Accumulator list to append the step outcome to.

    Returns:
        The dict returned by *fn* on success, or ``None`` on failure.
    """
    idx = len(results) + 1
    print(f"  {_dim(f'[{idx}/{TOTAL_STEPS}]')}  {name}", end="", flush=True)
    t0 = time.monotonic()
    try:
        result = fn()
        elapsed = time.monotonic() - t0
        print(f"  {_green('✓')}  {_dim(f'({elapsed:.1f}s)')}")
        results.append(StepResult(name=name, passed=True, elapsed=elapsed))
        return result
    except (RevylError, Exception) as exc:
        elapsed = time.monotonic() - t0
        print(f"  {_red('✗')}  {_dim(f'({elapsed:.1f}s)')}")
        print(f"         {_red(str(exc))}")
        results.append(StepResult(name=name, passed=False, elapsed=elapsed, detail=str(exc)))
        return None


def skip_step(name: str, reason: str, results: list[StepResult]) -> None:
    """Record a skipped step with a reason.

    Args:
        name: Human-readable label for the step.
        reason: Why it was skipped.
        results: Accumulator list to append the skip to.
    """
    idx = len(results) + 1
    print(f"  {_dim(f'[{idx}/{TOTAL_STEPS}]')}  {name}  {_yellow('skip')}  {_dim(reason)}")
    results.append(StepResult(name=name, passed=True, detail=f"skipped: {reason}"))


def _screenshot(
    device: DeviceClient,
    filename: str,
    run_dir: Path,
    screenshots: list[str],
    results: list[StepResult],
) -> None:
    """Take a screenshot, save to the run directory, and record the step."""
    path = str(run_dir / "screenshots" / filename)

    def _take():
        device.screenshot(out=path)
        return {"saved": filename}

    result = run_step(f"screenshot → {filename}", _take, results)
    if result:
        screenshots.append(filename)
        print(f"         {_dim('→')} screenshots/{filename}")


def run_all_actions(
    device: DeviceClient,
    platform: str,
    run_dir: Path,
) -> tuple[list[StepResult], list[str]]:
    """Execute every action type on the device and collect results.

    Args:
        device: An active DeviceClient session.
        platform: "ios" or "android", used to skip platform-specific actions.
        run_dir: Output directory for screenshots.

    Returns:
        A tuple of (step results, screenshot filenames).
    """
    results: list[StepResult] = []
    screenshots: list[str] = []

    # -- Session info & streaming --
    _section("Session Info & Streaming")
    run_step("info", lambda: device.info(), results)
    run_step("list_sessions", lambda: device.list_sessions(), results)
    run_step(
        "wait_for_stream",
        lambda: device.wait_for_stream(timeout=30) or "no stream",
        results,
    )

    # -- Screenshot --
    _section("Screenshot")
    _screenshot(device, "01_initial.png", run_dir, screenshots, results)

    # -- Tap variants --
    _section("Tap Actions")
    run_step(
        "tap (grounded)",
        lambda: device.tap(target="first product card or visible element"),
        results,
    )
    run_step("tap (coordinates)", lambda: device.tap(x=200, y=400), results)
    run_step("double_tap", lambda: device.double_tap(x=200, y=400), results)
    run_step(
        "long_press",
        lambda: device.long_press(x=200, y=400, duration_ms=1000),
        results,
    )

    # -- Text input --
    _section("Text Input")
    run_step(
        "instruction: open search",
        lambda: device.instruction("Tap the search tab in the bottom navigation"),
        results,
    )
    run_step(
        "type_text (grounded)",
        lambda: device.type_text(target="search bar", text="Beetle"),
        results,
    )
    run_step(
        "clear_text",
        lambda: device.clear_text(target="search bar"),
        results,
    )
    run_step(
        "type_text (append)",
        lambda: device.type_text(target="search bar", text="Moth", clear_first=False),
        results,
    )

    # -- Swipe / scroll --
    _section("Swipe & Gestures")
    run_step(
        "swipe down (grounded)",
        lambda: device.swipe(target="search results", direction="down"),
        results,
    )
    run_step(
        "swipe up (coordinates)",
        lambda: device.swipe(x=200, y=600, direction="up", duration_ms=300),
        results,
    )
    run_step(
        "drag",
        lambda: device.drag(start_x=200, start_y=500, end_x=200, end_y=300),
        results,
    )
    run_step(
        "pinch (zoom in)",
        lambda: device.pinch(x=200, y=400, scale=2.0, duration_ms=300),
        results,
    )
    run_step(
        "pinch (zoom out)",
        lambda: device.pinch(x=200, y=400, scale=0.5, duration_ms=300),
        results,
    )

    # -- Keyboard --
    _section("Keyboard")
    run_step("key ENTER", lambda: device.key("ENTER"), results)
    run_step("key BACKSPACE", lambda: device.key("BACKSPACE"), results)

    # -- Device controls --
    _section("Device Controls")
    run_step("wait", lambda: device.wait(duration_ms=1000), results)
    run_step("shake", lambda: device.shake(), results)

    if platform == "android":
        run_step("back", lambda: device.back(), results)
    else:
        skip_step("back", "Android-only action", results)

    run_step("go_home", lambda: device.go_home(), results)
    _screenshot(device, "02_home.png", run_dir, screenshots, results)

    # -- Navigation --
    _section("Navigation")
    run_step("open_app (settings)", lambda: device.open_app("settings"), results)
    run_step(
        "navigate (URL)",
        lambda: device.navigate("https://example.com"),
        results,
    )
    run_step(
        "set_location (SF)",
        lambda: device.set_location(37.7749, -122.4194),
        results,
    )

    # -- Live steps --
    _section("Live Steps")
    run_step("go_home (reset)", lambda: device.go_home(), results)
    run_step(
        "navigate (deep link)",
        lambda: device.navigate("bug-bazaar://"),
        results,
    )
    run_step("wait (app load)", lambda: device.wait(duration_ms=2000), results)
    run_step(
        "instruction",
        lambda: device.instruction("Tap on the first product card in the shop"),
        results,
    )
    run_step(
        "validation",
        lambda: device.validation("A product detail page is visible with a price"),
        results,
    )
    run_step(
        "extract",
        lambda: device.extract(
            "Read the product name displayed on screen",
            variable_name="product_name",
        ),
        results,
    )
    run_step(
        "code_execution (inline)",
        lambda: device.code_execution(
            code='print("hello from device")',
            runtime="python",
        ),
        results,
    )
    _screenshot(device, "03_product.png", run_dir, screenshots, results)

    # -- App lifecycle (install / launch / kill / reinstall) --
    _section("App Lifecycle")
    app_url = BUG_BAZAAR_BUILDS.get(platform)
    run_step(
        "install_app",
        lambda: device.install_app(app_url=app_url),
        results,
    )
    run_step(
        "launch_app",
        lambda: device.launch_app(bundle_id="com.revyl.bugbazaar"),
        results,
    )
    run_step("wait (app load)", lambda: device.wait(duration_ms=2000), results)
    run_step("kill_app", lambda: device.kill_app(), results)
    run_step("go_home (after kill)", lambda: device.go_home(), results)
    run_step(
        "navigate (relaunch)",
        lambda: device.navigate("bug-bazaar://"),
        results,
    )
    run_step("wait (relaunch)", lambda: device.wait(duration_ms=2000), results)
    _screenshot(device, "04_relaunch.png", run_dir, screenshots, results)

    # -- Session report --
    _section("Session Report")
    run_step("report", lambda: device.report(), results)

    return results, screenshots


def print_summary(
    results: list[StepResult],
    duration: float,
    screenshots: list[str],
    run_dir: Path,
) -> None:
    """Print the final colored results summary.

    Args:
        results: All step outcomes from the run.
        duration: Total wall-clock time in seconds.
        screenshots: List of screenshot filenames saved.
        run_dir: Output directory path.
    """
    skipped = sum(1 for r in results if r.detail.startswith("skipped:"))
    failed = sum(1 for r in results if not r.passed)
    passed = len(results) - failed - skipped

    bar = _cyan("─" * 52)
    print(f"\n  {bar}")

    if failed == 0:
        print(f"  {_green('✓')} {_bold('All actions passed')}  ·  {_dim(f'{duration:.1f}s')}\n")
    else:
        print(f"  {_red('✗')} {_bold(f'{failed} action(s) failed')}  ·  {_dim(f'{duration:.1f}s')}\n")

    parts = [_green(f"{passed} passed")]
    if failed:
        parts.append(_red(f"{failed} failed"))
    if skipped:
        parts.append(_yellow(f"{skipped} skipped"))
    parts.append(_dim(f"{len(results)} total"))
    print(f"    {'  '.join(parts)}\n")

    failures = [r for r in results if not r.passed]
    if failures:
        print(f"    {_dim('Failures:')}")
        for r in failures:
            print(f"      {_red('✗')} {r.name}: {r.detail}")
        print()

    if screenshots:
        print(f"    {_dim('Screenshots:')}")
        for s in screenshots:
            print(f"      {s}")
        print()

    print(f"    {_dim('Output:'.ljust(12))}{run_dir}")
    print(f"  {bar}\n")


def main() -> int:
    parser = argparse.ArgumentParser(
        description="Run every DeviceClient action type against a live device.",
    )
    parser.add_argument("--platform", default="ios", choices=["ios", "android"])
    parser.add_argument("--app-url", default=None, help="Direct URL to an .apk or .app archive.")
    parser.add_argument("--open", action="store_true", help="Open the live viewer in the browser.")
    parser.add_argument("--dev", action="store_true", help="Target local dev backend.")
    parser.add_argument("--output-dir", default=None, help="Custom output directory for assets.")
    args = parser.parse_args()

    app_url = args.app_url or BUG_BAZAAR_BUILDS[args.platform]
    cli = RevylCLI(dev_mode=args.dev)
    run_dir = _make_run_dir("all-actions", override=args.output_dir)
    t_run = time.monotonic()

    _banner("Revyl SDK  ·  All Actions", {
        "Platform": args.platform,
        "App": app_url.rsplit("/", 1)[-1],
        "Actions": f"{TOTAL_STEPS} steps across 11 categories",
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

            results, screenshots = run_all_actions(device, args.platform, run_dir)

        duration = time.monotonic() - t_run
        print_summary(results, duration, screenshots, run_dir)

        failed = sum(1 for r in results if not r.passed)
        if failed > 0:
            return 1

    except RevylError as exc:
        print(f"\n  {_red('✗')} {_bold('Error:')} {exc}", file=sys.stderr)
        return 1

    return 0


if __name__ == "__main__":
    raise SystemExit(main())
