<!-- mintlify
title: "CLI Device Commands"
description: "Use revyl device commands for direct session and action control"
target: device/cli-commands.mdx
-->

Use `revyl device` when you want direct, terminal-first control of cloud devices.

For full flag-level reference, see [CLI Device Commands (detailed)](/cli/devices) and [Command Reference](/cli/reference).

## Core Lifecycle

```bash
# Start
revyl device start --platform ios --timeout 600

# Inspect
revyl device info
revyl device list

# Stop
revyl device stop
revyl device stop --all
```

## Install and Launch App

```bash
revyl device install --app-url "https://example.com/app.apk"
revyl device launch --bundle-id com.example.app
revyl device kill-app
```

## Action Loop (Recommended)

```bash
revyl device screenshot --out before.png
revyl device tap --target "Sign In button"
revyl device type --target "Email field" --text "user@example.com"
revyl device tap --target "Continue"
revyl device screenshot --out after.png
```

Use this rhythm:

1. Re-observe (`screenshot`)
2. One best action (`tap`, `type`, `swipe`, etc.)
3. Verify (`screenshot`)

## Action Commands

All actions support either `--target "..."` for grounded targeting or `--x` / `--y` for raw coordinates.

```bash
revyl device tap --target "Login button"
revyl device tap --x 200 --y 400
revyl device double-tap --target "item"
revyl device long-press --target "icon" --duration 1500
revyl device type --target "Email field" --text "user@test.com"
revyl device swipe --target "list" --direction down
revyl device drag --start-x 100 --start-y 200 --end-x 300 --end-y 400
revyl device pinch --target "map" --scale 1.5
revyl device clear-text --target "Search"
```

## Control Commands

```bash
revyl device wait --duration-ms 1000
revyl device back                              # Android back button
revyl device key --key ENTER                   # ENTER or BACKSPACE
revyl device shake                             # Trigger shake gesture
revyl device home                              # Return to home screen
revyl device kill-app                          # Kill the installed app
revyl device open-app --app settings           # Open a system app
revyl device navigate --url https://example.com
revyl device set-location --lat 37.77 --lon -122.42
revyl device download-file --url https://example.com/report.pdf --filename report.pdf
```

## Live Steps

Execute individual test steps against an active session without creating a full test:

```bash
revyl device instruction "Open Settings and tap Wi-Fi"
revyl device validation "Verify the Settings title is visible"
revyl device extract "Extract the visible account email" --variable-name account_email
revyl device code-execution script_123
revyl device code-execution --file ./check.py --runtime python
revyl device code-execution --code "print('ok')" --runtime python
```

## Utilities

```bash
revyl device report                            # View session report
revyl device targets                           # List available devices/OS versions
revyl device targets --platform ios            # Filter by platform
revyl device history                           # Show recent session history
```

## Multi-Session Pattern

```bash
revyl device start --platform android   # session 0
revyl device start --platform ios       # session 1

revyl device use 1
revyl device tap --target "Get Started" # active session
revyl device tap --target "Get Started" -s 0   # target specific session

revyl device stop --all
```

### Session Flags

| Flag | Description |
|------|-------------|
| `-s <index>` | Target a specific session (default: active) |
| `--json` | Output as JSON (useful for scripting) |
| `--timeout <secs>` | Idle timeout for start (default: 300) |

## Health and Recovery

When anything looks off:

```bash
revyl device doctor
```

Then use [Device Troubleshooting](/device/troubleshooting).
