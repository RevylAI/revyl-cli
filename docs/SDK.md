# Device SDK Reference

> [Back to README](../README.md) | [Commands](COMMANDS.md) | [MCP Setup](MCP_SETUP.md)

Use the Revyl Device SDK (`pip install revyl`) for programmatic device control and live test step execution.

## Install

```bash
pip install revyl    # pip
uv pip install revyl # uv
```

The package includes a bundled CLI binary. On first use the SDK resolves the binary in this order:

1. SDK-managed binary at `~/.revyl/bin/` (with valid checksum sidecar)
2. `revyl` on `PATH`
3. Auto-download from GitHub releases

## Authenticate

```bash
revyl auth login                # Browser-based login (stores credentials locally)
export REVYL_API_KEY=rev_...    # Or set an API key
```

The SDK uses the same credentials as the CLI. No additional auth setup is needed.

---

## RevylCLI

Low-level runner for arbitrary CLI commands.

```python
from revyl import RevylCLI, RevylError

cli = RevylCLI()                                        # Uses auto-resolved binary
cli = RevylCLI(binary_path="/usr/local/bin/revyl")      # Explicit path

version = cli.run("version")                            # Returns stdout as string
tests = cli.run("test", "list", json_output=True)       # Returns parsed JSON
```

### `RevylCLI(binary_path=None)`

| Parameter | Type | Description |
|-----------|------|-------------|
| `binary_path` | `Optional[str]` | Path to the `revyl` binary. If `None`, auto-resolved via `ensure_binary()`. |

### `cli.run(*args, json_output=False)`

Run a CLI command. Returns parsed JSON when `json_output=True`, otherwise returns stdout as a string.

Raises `RevylError` on non-zero exit code.

---

## DeviceClient

High-level helper for device interaction. Every action method returns a `dict` with the CLI's JSON response.

### Quick Start

```python
from revyl import DeviceClient

device = DeviceClient.start(platform="ios", timeout=600)
device.tap(target="Login button")
device.type_text(target="Email", text="user@test.com")
device.screenshot(out="screen.png")
device.stop_session()
```

### Context Manager

```python
from revyl import DeviceClient

with DeviceClient.start(platform="android") as device:
    device.tap(target="Get Started")
    device.swipe(target="feed", direction="down")
# Session is stopped automatically on exit
```

### Reusing an Existing Session

```python
device = DeviceClient(session_index=1)
device.tap(target="Settings tab")
```

---

## Grounded Targets vs Coordinates

Most action methods accept either a grounded `target` (natural language element description) or raw `x, y` coordinates. Provide one or the other, not both.

```python
device.tap(target="Sign In button")           # Grounded (recommended)
device.tap(x=540, y=960)                      # Coordinates
device.type_text(target="Email", text="...")   # Grounded
device.type_text(x=540, y=400, text="...")     # Coordinates
```

Grounded targeting uses AI vision to resolve coordinates automatically. Use specific descriptions like `"blue 'Sign In' button"` for better accuracy.

---

## Session Management

### `DeviceClient.start(platform, timeout=None, open_viewer=False, app_id=None, build_version_id=None, app_url=None, app_link=None, cli=None) -> DeviceClient`

Class method. Start a device session and return a connected client.

| Parameter | Type | Description |
|-----------|------|-------------|
| `platform` | `str` | `"ios"` or `"android"` |
| `timeout` | `Optional[int]` | Idle timeout in seconds |
| `open_viewer` | `bool` | Open the live viewer in the browser |
| `app_id` | `Optional[str]` | Revyl app ID to preinstall |
| `build_version_id` | `Optional[str]` | Specific build version to install |
| `app_url` | `Optional[str]` | URL to an `.ipa` or `.apk` to preinstall |
| `app_link` | `Optional[str]` | Deep link to open after launch |
| `cli` | `Optional[RevylCLI]` | Custom CLI instance |

### `start_session(platform, timeout=None, open_viewer=False, app_id=None, build_version_id=None, app_url=None, app_link=None) -> dict`

Start a device session. Same parameters as `start()`. Returns session info including the session `index`.

### `stop_session(session_index=None) -> dict`

Stop a device session. Defaults to the tracked session.

### `stop_all() -> dict`

Stop all active sessions.

### `list_sessions() -> list[dict]`

List all active device sessions.

### `use_session(index) -> str`

Switch the active session. Returns confirmation text.

### `info(session_index=None) -> dict`

Get session details including `whep_url` when streaming is available.

### `doctor(session_index=None) -> str`

Run diagnostics on auth, session, worker, and grounding health. Returns text output.

### `close() -> None`

Best-effort stop for the tracked session. Called automatically when using the context manager.

---

## Actions

### `tap(target=None, x=None, y=None, session_index=None) -> dict`

Tap an element by target description or coordinates.

### `double_tap(target=None, x=None, y=None, session_index=None) -> dict`

Double-tap an element.

### `long_press(target=None, x=None, y=None, duration_ms=1500, session_index=None) -> dict`

Long press an element. `duration_ms` controls hold duration.

### `type_text(text, target=None, x=None, y=None, clear_first=True, session_index=None) -> dict`

Type text into a field. Set `clear_first=False` to append instead of replace.

### `swipe(direction, target=None, x=None, y=None, duration_ms=500, session_index=None) -> dict`

Swipe in a direction (`"up"`, `"down"`, `"left"`, `"right"`) from a target or point.

### `drag(start_x, start_y, end_x, end_y, session_index=None) -> dict`

Drag from one point to another (coordinates only).

### `pinch(target=None, x=None, y=None, scale=2.0, duration_ms=300, session_index=None) -> dict`

Pinch/zoom gesture. `scale > 1` zooms in, `scale < 1` zooms out.

### `clear_text(target=None, x=None, y=None, session_index=None) -> dict`

Clear text in a field.

---

## Controls

### `back(session_index=None) -> dict`

Android back button. Not supported on iOS.

### `key(key, session_index=None) -> dict`

Press a key. Supported values: `"ENTER"`, `"BACKSPACE"`.

### `shake(session_index=None) -> dict`

Trigger a shake gesture.

### `wait(duration_ms=1000, session_index=None) -> dict`

Wait for a fixed duration.

### `go_home(session_index=None) -> dict`

Return to the home screen.

### `open_app(app, session_index=None) -> dict`

Open a system app by name (e.g. `"settings"`).

### `navigate(url, session_index=None) -> dict`

Open a URL or deep link on the device.

### `set_location(latitude, longitude, session_index=None) -> dict`

Set the device GPS location.

### `download_file(url, filename=None, session_index=None) -> dict`

Download a file to the device. Returns `device_path` in the response.

---

## App Management

### `install_app(app_url, bundle_id=None, session_index=None) -> dict`

Install an app from a URL (`.ipa` or `.apk`).

### `launch_app(bundle_id, session_index=None) -> dict`

Launch an installed app by bundle ID.

### `kill_app(session_index=None) -> dict`

Kill the currently running app.

---

## Live Steps

Execute individual test steps against an active device session without creating a full test.

### `instruction(description, session_index=None) -> dict`

Execute one instruction step. The description is a natural-language action like `"Open Settings and tap Wi-Fi"`.

### `validation(description, session_index=None) -> dict`

Execute one validation step. The description is an assertion like `"Verify the inbox is visible"`.

### `extract(description, variable_name=None, session_index=None) -> dict`

Execute one extract step. Returns extracted data from the screen. Use `variable_name` to tag the result for downstream use.

### `code_execution(script_id, session_index=None) -> dict`

Execute a code execution step by script ID.

---

## Capture

### `screenshot(out=None, session_index=None) -> dict`

Take a screenshot. If `out` is provided, the image is saved to that file path.

---

## Error Handling

All CLI failures raise `RevylError`:

```python
from revyl import DeviceClient, RevylError

try:
    device = DeviceClient.start(platform="ios")
    device.tap(target="Login button")
except RevylError as e:
    print(f"Command failed: {e}")
```

Common causes:

| Error | Cause |
|-------|-------|
| `RevylError` on first command | Not authenticated. Run `revyl auth login` or set `REVYL_API_KEY`. |
| `ValueError: Provide target OR x/y` | Passed both `target` and coordinates to an action method. |
| `ValueError: ... must not be empty` | Empty string passed to a live step method. |

---

## TypeScript Device SDK (CLI JSON Wrapper)

There is no published TypeScript Device SDK. The `revyl` npm package is a binary wrapper only. For TypeScript projects, shell out to the CLI with `--json` and parse the response. The pattern below mirrors the Python `DeviceClient`:

```typescript
import { execFileSync } from "node:child_process";

class RevylDevice {
  constructor(platform: string) {
    this.run("device", "start", "--platform", platform);
  }

  private run(...args: string[]): Record<string, unknown> {
    const out = execFileSync("revyl", [...args, "--json"], {
      encoding: "utf-8",
    });
    return out.trim() ? JSON.parse(out) : {};
  }

  tap(target: string) {
    return this.run("device", "tap", "--target", target);
  }

  typeText(target: string, text: string) {
    return this.run("device", "type", "--target", target, "--text", text);
  }

  swipe(target: string, direction: "up" | "down" | "left" | "right") {
    return this.run("device", "swipe", "--target", target, "--direction", direction);
  }

  instruction(step: string) {
    return this.run("device", "instruction", step);
  }

  validation(step: string) {
    return this.run("device", "validation", step);
  }

  screenshot(out?: string) {
    const args = ["device", "screenshot"];
    if (out) args.push("--out", out);
    return this.run(...args);
  }

  stop() {
    return this.run("device", "stop");
  }
}
```
