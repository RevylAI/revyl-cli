# Revyl Device SDK

Python SDK for programmatic control of Revyl cloud devices. Start sessions, interact with elements, run live test steps, and capture screenshots.

## Install

```bash
pip install revyl
```

## Authenticate

```bash
revyl auth login                # Browser-based login
export REVYL_API_KEY="rev_..."  # Or set an API key
```

## Quick Start

```python
from revyl import DeviceClient

device = DeviceClient.start(platform="ios", timeout=600)

device.tap(target="Login button")
device.type_text(target="Email", text="user@example.com")
device.type_text(target="Password", text="secret123")
device.tap(target="Submit")
device.validation("Verify the dashboard title is visible")
device.screenshot(out="after-login.png")

device.stop_session()
```

## Context Manager (Auto Stop)

```python
from revyl import DeviceClient

with DeviceClient.start(platform="android") as device:
    device.tap(target="Get Started")
    device.swipe(target="feed", direction="down")
```

## Binary Resolution

The SDK resolves the CLI binary in this order:

1. SDK-managed binary at `~/.revyl/bin/` (with valid checksum)
2. `revyl` on `PATH`
3. Auto-download from GitHub releases

Use `RevylCLI(binary_path="...")` to override.

## API Reference

### RevylCLI

```python
from revyl import RevylCLI

cli = RevylCLI()                                    # Auto-resolved binary
cli = RevylCLI(binary_path="/usr/local/bin/revyl")  # Explicit path

version = cli.run("version")                        # Returns stdout string
sessions = cli.run("device", "list", json_output=True)  # Returns parsed JSON
```

Raises `RevylError` on non-zero exit code.

### DeviceClient

#### Session Management

| Method | Signature | Description |
|--------|-----------|-------------|
| `start` | `(cls, platform, timeout=None, open_viewer=False, app_id=None, build_version_id=None, app_url=None, app_link=None, cli=None) -> DeviceClient` | Class method. Start a session and return a client. |
| `start_session` | `(platform, timeout=None, open_viewer=False, app_id=None, build_version_id=None, app_url=None, app_link=None) -> dict` | Start a device session. |
| `stop_session` | `(session_index=None) -> dict` | Stop a session. |
| `stop_all` | `() -> dict` | Stop all sessions. |
| `list_sessions` | `() -> list[dict]` | List active sessions. |
| `use_session` | `(index: int) -> str` | Switch active session. |
| `info` | `(session_index=None) -> dict` | Get session details. |
| `doctor` | `(session_index=None) -> str` | Run session diagnostics (text output). |
| `close` | `() -> None` | Best-effort stop for tracked session. |

#### Actions

| Method | Signature | Description |
|--------|-----------|-------------|
| `tap` | `(target=None, x=None, y=None, session_index=None) -> dict` | Tap an element. |
| `double_tap` | `(target=None, x=None, y=None, session_index=None) -> dict` | Double-tap. |
| `long_press` | `(target=None, x=None, y=None, duration_ms=1500, session_index=None) -> dict` | Long press. |
| `type_text` | `(text, target=None, x=None, y=None, clear_first=True, session_index=None) -> dict` | Type text. |
| `swipe` | `(direction, target=None, x=None, y=None, duration_ms=500, session_index=None) -> dict` | Swipe gesture. |
| `drag` | `(start_x, start_y, end_x, end_y, session_index=None) -> dict` | Drag (coordinates only). |
| `pinch` | `(target=None, x=None, y=None, scale=2.0, duration_ms=300, session_index=None) -> dict` | Pinch/zoom. |
| `clear_text` | `(target=None, x=None, y=None, session_index=None) -> dict` | Clear text in a field. |

#### Controls

| Method | Signature | Description |
|--------|-----------|-------------|
| `back` | `(session_index=None) -> dict` | Android back button. |
| `key` | `(key: str, session_index=None) -> dict` | Press ENTER or BACKSPACE. |
| `shake` | `(session_index=None) -> dict` | Shake gesture. |
| `wait` | `(duration_ms=1000, session_index=None) -> dict` | Fixed wait. |
| `go_home` | `(session_index=None) -> dict` | Return to home screen. |
| `open_app` | `(app: str, session_index=None) -> dict` | Open a system app. |
| `navigate` | `(url: str, session_index=None) -> dict` | Open URL or deep link. |
| `set_location` | `(latitude: float, longitude: float, session_index=None) -> dict` | Set GPS location. |
| `download_file` | `(url: str, filename=None, session_index=None) -> dict` | Download file to device. |

#### App Management

| Method | Signature | Description |
|--------|-----------|-------------|
| `install_app` | `(app_url: str, bundle_id=None, session_index=None) -> dict` | Install from URL. |
| `launch_app` | `(bundle_id: str, session_index=None) -> dict` | Launch by bundle ID. |
| `kill_app` | `(session_index=None) -> dict` | Kill the current app. |

#### Live Steps

| Method | Signature | Description |
|--------|-----------|-------------|
| `instruction` | `(description: str, session_index=None) -> dict` | Execute one instruction step. |
| `validation` | `(description: str, session_index=None) -> dict` | Execute one validation step. |
| `extract` | `(description: str, variable_name=None, session_index=None) -> dict` | Execute one extract step. |
| `code_execution` | `(script_id: str, session_index=None) -> dict` | Execute one code-execution step. |

#### Capture

| Method | Signature | Description |
|--------|-----------|-------------|
| `screenshot` | `(out=None, session_index=None) -> dict` | Take a screenshot. |

## Beyond Device Control

For test execution, workflow management, and builds, use the `revyl` CLI directly or `RevylCLI.run()`:

```python
cli = RevylCLI()
cli.run("test", "run", "login-flow", json_output=True)
cli.run("workflow", "run", "smoke-tests", json_output=True)
```

## Targeting

All action methods support either:

- **Grounded targeting** via `target="..."` (recommended), or
- **Raw coordinates** via `x=...` and `y=...`

Provide one or the other, not both.

## Error Handling

```python
from revyl import DeviceClient, RevylError

try:
    device = DeviceClient.start(platform="ios")
    device.tap(target="Login")
except RevylError as e:
    print(f"Failed: {e}")
```

## Repo Smoke Script

From the repo root:

```bash
make device-prod-sdk-smoke-ios
make device-prod-sdk-smoke-android
```

Useful variants:

```bash
make device-prod-sdk-smoke-ios ARGS="--grounded-text"
make device-prod-sdk-smoke-ios ARGS="--app-url https://... --bundle-id com.example.app"
make device-prod-sdk-smoke-android ARGS="--grounded-text"
make device-prod-sdk-smoke ARGS="--platform android"
```
