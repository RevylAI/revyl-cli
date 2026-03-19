# SDK Examples

Runnable scripts that show the [Revyl Python SDK](../../docs/SDK.md) in action.
Pick one and run it — no config needed beyond auth.

## Quick start

```bash
pip install revyl[sdk]
revyl auth login

# Run any example (defaults to iOS + Bug Bazaar demo app)
python examples/sdk/quick_start.py --open
python examples/sdk/checkout_flow.py --open
python examples/sdk/bug_detection.py --open

# Android
python examples/sdk/checkout_flow.py --platform android --open

# Your own app
python examples/sdk/quick_start.py --open --app-url https://your-host.com/app.apk
```

## Examples

| Script | What it does |
|--------|-------------|
| `quick_start.py` | Start a device, tap, screenshot — the simplest possible session |
| `all_actions.py` | Every SDK action: taps, swipes, text, gestures, controls, live steps, code execution |
| `checkout_flow.py` | Full e-commerce checkout with validation at each stage |
| `bug_detection.py` | Catch a known cart bug using `extract` + `validation` |
| `exploratory_testing.py` | Loop-based product catalog exploration with data extraction |
| `app_lifecycle.py` | Full install / launch / interact / kill / reinstall cycle |

## Output

Each run saves screenshots to a timestamped directory:

```
revyl-runs/
  checkout-flow/
    2026-03-17_14-30-22/
      screenshots/
        01_shop_screen.png
        02_product_detail.png
        ...
      report.json          # with --json-report
```

Override the output location with `--output-dir ./my-artifacts`.

## Flags

All scripts accept:

| Flag | Description |
|------|-------------|
| `--platform ios\|android` | Target platform (default: `ios`) |
| `--app-url URL` | Use your own app build instead of Bug Bazaar |
| `--dev` | Target local dev backend |
| `--output-dir PATH` | Custom output directory for screenshots |

Color output is auto-disabled when stdout is not a tty (e.g. in CI).
Set `NO_COLOR=1` to force it off.
