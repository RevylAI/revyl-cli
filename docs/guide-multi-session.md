<!-- mintlify
title: "Guide: Multi-Session Devices"
description: "Run multiple cloud devices simultaneously for cross-platform testing, parallel workflows, and side-by-side comparisons"
target: device/multi-session.mdx
-->

# Multi-Session Guide

Run multiple cloud devices at the same time. Start an Android and an iOS session, interact with each one independently, and tear them down when you're done.

## When to use multiple sessions

- **Cross-platform parity** — verify the same flow works on both iOS and Android
- **Side-by-side comparison** — compare different OS versions or device models
- **Parallel test steps** — run independent test flows concurrently to save time

## Session model

Each call to `revyl device start` (or `start_device_session` via MCP) provisions one cloud device and assigns it a **session index** — an integer starting at 0:

```bash
revyl device start --platform android    # → session 0 (active)
revyl device start --platform ios        # → session 1
```

One session is always the **active session**. Commands that omit `-s` (CLI) or `session_index` (MCP/SDK) target the active session. You can switch active with `revyl device use <index>` or `switch_device_session`.

### Stable indices

Session indices are **stable**. Stopping session 0 does not renumber session 1 — it stays at index 1. The index you received at start time remains valid for the lifetime of that session.

```bash
revyl device start --platform android    # → session 0 (active)
revyl device start --platform ios        # → session 1
revyl device stop -s 0                   # stop Android
revyl device list                        # session 1 still at index 1
revyl device tap --target "button" -s 1  # works — index unchanged
```

When all sessions are stopped (or `--all`), the index counter resets to 0 for the next start.

### Active session auto-switching

If you stop the active session, the CLI auto-switches to the **lowest remaining index**:

```bash
revyl device start --platform android    # → session 0 (active)
revyl device start --platform ios        # → session 1
revyl device stop -s 0                   # active switches to session 1
```

## CLI workflow

```bash
# 1. Start two devices
revyl device start --platform android    # session 0
revyl device start --platform ios        # session 1

# 2. Install an app on each
revyl device install --app-url "https://example.com/app.apk" -s 0
revyl device install --app-url "https://example.com/app.ipa" -s 1

# 3. Interact with each device using -s
revyl device tap --target "Get Started" -s 0
revyl device tap --target "Get Started" -s 1

revyl device type --target "Email field" --text "user@test.com" -s 0
revyl device type --target "Email field" --text "user@test.com" -s 1

# 4. Take screenshots from each
revyl device screenshot --out android-home.png -s 0
revyl device screenshot --out ios-home.png -s 1

# 5. Tear down
revyl device stop --all
```

### Useful session management commands

| Command | Description |
|---------|-------------|
| `revyl device list` | Show all active sessions with index, platform, and uptime |
| `revyl device use <index>` | Switch the active session |
| `revyl device info -s <index>` | Show details for a specific session |
| `revyl device stop -s <index>` | Stop one session |
| `revyl device stop --all` | Stop all sessions |

## MCP workflow (AI agents)

Every device action, control, and live-step tool accepts an optional `session_index` parameter. When omitted, the active session is used.

```
# Start two sessions
start_device_session(platform="android")   → session_index: 0
start_device_session(platform="ios")       → session_index: 1

# Install apps
install_app(app_url="https://example.com/app.apk", session_index=0)
install_app(app_url="https://example.com/app.ipa", session_index=1)

# Target each session explicitly
device_tap(target="Sign In", session_index=0)
device_tap(target="Sign In", session_index=1)

screenshot(session_index=0)
screenshot(session_index=1)

# Check which sessions are running
list_device_sessions()

# Stop everything
stop_device_session(all=true)
```

<Callout type="tip" title="Store the session_index">
  When working with multiple sessions, always store the `session_index` returned by `start_device_session` and pass it explicitly to subsequent tool calls. Indices are stable and won't change even if other sessions are stopped.
</Callout>

## Python SDK workflow

### Sequential — target by session_index

```python
from revyl import DeviceClient

android = DeviceClient.start(platform="android")  # session 0
ios = DeviceClient.start(platform="ios")           # session 1

android.tap(target="Get Started")
ios.tap(target="Get Started")

android.screenshot(out="android.png")
ios.screenshot(out="ios.png")

android.stop_session()
ios.stop_session()
```

### Parallel — threads

```python
from revyl import DeviceClient
import threading

def test_flow(platform, out_prefix):
    with DeviceClient.start(platform=platform) as device:
        device.tap(target="Get Started")
        device.type_text(target="Email field", text="user@test.com")
        device.tap(target="Sign In")
        device.screenshot(out=f"{out_prefix}-result.png")

ios_thread = threading.Thread(target=test_flow, args=("ios", "ios"))
android_thread = threading.Thread(target=test_flow, args=("android", "android"))

ios_thread.start()
android_thread.start()
ios_thread.join()
android_thread.join()
```

## Session limits

| Limit | Value |
|-------|-------|
| **Idle timeout** | 5 minutes (configurable with `--timeout`) |
| **Concurrent sessions** | Depends on your plan (typically 1–3) |

Sessions auto-terminate after the idle timeout expires. Any action (tap, type, screenshot, etc.) resets the idle timer.

## Cross-client sync

Sessions are shared across CLI, MCP, and the web dashboard. A session started in Cursor (via MCP) appears in `revyl device list` and vice versa. The local state file is `.revyl/device-sessions.json` in your project root.

If session lists look out of sync, the CLI auto-syncs with the backend on most commands. You can also force a sync by running `revyl device list`.

## Troubleshooting

### "no session at index N"

The session was stopped (manually or by idle timeout). Run `revyl device list` to see which sessions are still alive, then start a new one if needed.

### "must specify session_index or call list_device_sessions"

You have multiple sessions but no active session set. Run `revyl device use <index>` or pass `-s <index>` / `session_index` explicitly.

### Actions hitting the wrong device

You may be targeting the active session unintentionally. Pass `-s` (CLI) or `session_index` (MCP) explicitly when working with multiple sessions.

---

## What's Next

- [Device Commands](/cli/devices) — full CLI reference for all device commands
- [MCP Server Setup](/cli/mcp-setup) — connect device tools to your AI coding agent
- [Device Scripting Guide](/device/scripting-guide) — write Python scripts that control devices
- [Device Troubleshooting](/device/troubleshooting) — common issues and fixes
