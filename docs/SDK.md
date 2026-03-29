<!-- mintlify
title: "Python SDK Reference"
description: "Complete API reference for the Revyl Python SDK"
target: device/sdk-reference.mdx
-->

# Device SDK Reference

> [Back to README](../README.md) | [Commands](COMMANDS.md) | [MCP Setup](MCP_SETUP.md)

Use the Revyl Device SDK for programmatic device control and live test step execution.

## Install

```bash
pip install revyl[sdk]           # Python SDK (includes CLI)
```

If you installed the CLI via Homebrew, the SDK detects it on PATH and skips the binary download.

The CLI binary auto-downloads on first use. On first use the SDK resolves the binary in this order:

0. `REVYL_BINARY` env var -- explicit path for local dev or CI
1. SDK-managed binary at `~/.revyl/bin/` (with valid checksum sidecar)
2. `revyl` on `PATH`
3. Auto-download from GitHub releases

For local development against a locally-built CLI binary, set:

```bash
export REVYL_BINARY=./revyl-cli/tmp/revyl
```

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

### `RevylCLI(binary_path=None, dev_mode=False)`

| Parameter | Type | Description |
|-----------|------|-------------|
| `binary_path` | `Optional[str]` | Path to the `revyl` binary. If `None`, auto-resolved via `ensure_binary()`. |
| `dev_mode` | `bool` | When `True`, prepends `--dev` to every command for local development servers. |

### `cli.run(*args, json_output=False)`

Run a CLI command. Returns parsed JSON when `json_output=True`, otherwise returns stdout as a string.

Raises `RevylError` on non-zero exit code.

---

## DeviceClient

High-level helper for device interaction. Every action method returns a `dict` with the CLI's JSON response.

### Quick Start

```python
from revyl import DeviceClient

# start() blocks until the device is API-ready (default).
# Report URL auto-prints when the session closes.
with DeviceClient.start(platform="ios", app_url=url) as device:
    device.screenshot(out="screen.png")
    device.tap(target="Login button")
    device.type_text(target="Email", text="user@test.com")
```

### Context Manager

```python
from revyl import DeviceClient

with DeviceClient.start(platform="android") as device:
    device.tap(target="Get Started")
    device.swipe(target="feed", direction="down")
# Session is stopped automatically on exit; report URL is printed.
```

### Fire-and-Forget Start

```python
# For advanced users who want to do parallel setup work.
device = DeviceClient.start(platform="ios", app_url=url, wait_for_ready=False)
# ... do other setup ...
device.wait_for_device_ready()   # block when you need the device
device.screenshot(out="screen.png")
device.stop_session()
```

### Reusing an Existing Session

```python
device = DeviceClient(session_index=1)
device.tap(target="Settings tab")
```

### Constructor

`DeviceClient(cli=None, session_index=None, auto_report=True, verbose=True)`

| Parameter | Type | Description |
|-----------|------|-------------|
| `cli` | `Optional[RevylCLI]` | Custom CLI runner. If `None`, a default `RevylCLI()` is created. |
| `session_index` | `Optional[int]` | Attach to an existing session by index. |
| `auto_report` | `bool` | Auto-print the report URL when the session closes. |
| `verbose` | `bool` | Print status messages during session lifecycle. |

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

### `DeviceClient.start(platform, ..., wait_for_ready=True, ready_timeout=60, auto_report=True) -> DeviceClient`

Class method. Start a device session and return a connected client.

By default, blocks until the device is API-ready. Pass `wait_for_ready=False` for fire-and-forget provisioning.

| Parameter | Type | Description |
|-----------|------|-------------|
| `platform` | `str` | `"ios"` or `"android"` |
| `timeout` | `Optional[int]` | Idle timeout in seconds |
| `open_viewer` | `bool` | Open the live viewer in the browser |
| `app_id` | `Optional[str]` | Revyl app ID to preinstall |
| `build_version_id` | `Optional[str]` | Specific build version to install |
| `app_url` | `Optional[str]` | URL to an `.ipa` or `.apk` to preinstall |
| `app_link` | `Optional[str]` | Deep link to open after launch |
| `device_model` | `Optional[str]` | Target device model (e.g. `"iPhone 16"`). Must be paired with `os_version`. |
| `os_version` | `Optional[str]` | Target OS version (e.g. `"iOS 18.5"`). Must be paired with `device_model`. |
| `cli` | `Optional[RevylCLI]` | Custom CLI instance |
| `wait_for_ready` | `bool` | Block until device is API-ready (default `True`). Set `False` for fire-and-forget. |
| `ready_timeout` | `float` | Max seconds to wait for readiness when `wait_for_ready=True` (default `60`). |
| `auto_report` | `bool` | Auto-print report/video URLs on `close()` (default `True`). |
| `verbose` | `bool` | Show animated spinner during provisioning (default `True`). Set `False` for CI. |

### `start_session(platform, timeout=None, open_viewer=False, app_id=None, build_version_id=None, app_url=None, app_link=None, device_model=None, os_version=None) -> dict`

Start a device session. Same parameters as `start()` (except `cli`). Returns session info including the session `index`.

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

Run diagnostics on auth, session, device, and grounding health. Returns text output.

### `wait_for_device_ready(timeout=60, poll_interval=3) -> bool`

Poll `device doctor` until the device is reported as connected. Called automatically by `start(wait_for_ready=True)`. Returns `True` if the device became ready, `False` on timeout.

```python
device = DeviceClient.start(platform="ios", app_url=url, wait_for_ready=False)
# ... parallel setup ...
device.wait_for_device_ready(timeout=90)
```

### `wait_for_report(timeout=30, poll_interval=2) -> dict`

Poll until the session report is generated and return it. Raises `RevylError` if unavailable within timeout.

```python
report = device.wait_for_report(timeout=30)
print(report["report_url"])
```

### `close() -> None`

Best-effort stop for the tracked session. Called automatically when using the context manager. When `auto_report=True` (default), fetches and prints the report URL before stopping.

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

### `install_app(app_url=None, build_version_id=None, bundle_id=None, session_index=None) -> dict`

Install an app from a URL (`.ipa` or `.apk`) or a previously uploaded build version. Provide exactly one of `app_url` or `build_version_id`.

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

### `code_execution(script_id=None, file_path=None, code=None, runtime=None, session_index=None) -> dict`

Execute a code execution step. Provide exactly one of `script_id`, `file_path`, or `code`. When using `file_path` or `code`, a `runtime` is required (`"python"`, `"javascript"`, `"typescript"`, or `"bash"`).

```python
device.code_execution(script_id="seed-db")                           # saved script
device.code_execution(file_path="scripts/seed.py", runtime="python") # local file
device.code_execution(code="print('hello')", runtime="python")       # inline code
```

---

## Capture

### `screenshot(out=None, session_index=None) -> dict`

Take a screenshot. If `out` is provided, the image is saved to that file path.

---

## Reporting & Discovery

### `report(session_index=None) -> dict`

Fetch the session report including status, steps, video URL, and report URL.

```python
report = device.report()
print(report["report_url"])   # Browser link to the full report
print(report["video_url"])    # Recording of the session
print(report["total_steps"], report["passed_steps"], report["failed_steps"])
```

### `targets(platform=None, cli=None) -> dict` *(static method)*

List available device models and OS versions. Can be called without a session.

```python
all_targets = DeviceClient.targets()
ios_targets = DeviceClient.targets(platform="ios")
```

### `history(limit=20, cli=None) -> list[dict]` *(static method)*

Show recent device session history. Can be called without a session.

```python
recent = DeviceClient.history(limit=5)
```

### `wait_for_stream(timeout=30, poll_interval=2) -> str | None`

Poll `info()` until the WebRTC WHEP URL is available and return it. Use this when you need the stream URL for building live viewers or streaming integrations. Not called automatically by `start()`.

> **Note:** `wait_for_stream()` checks for the *stream URL*, not device API readiness. Use `wait_for_device_ready()` (built into `start()` by default) to ensure the device is ready for actions like `screenshot()` and `tap()`.

```python
with DeviceClient.start(platform="ios", app_url=url) as device:
    whep_url = device.wait_for_stream(timeout=30)
    if whep_url:
        print(f"Stream ready: {whep_url}")
```

---

## Live Streaming

Every active device session streams the live screen over WebRTC. The stream URL is a standard [WHEP](https://www.ietf.org/archive/id/draft-murillo-whep-03.html) (WebRTC-HTTP Egress Protocol) endpoint — you can feed it into any WHEP-compatible player to embed the device screen in your own dashboard, CI viewer, or internal tool.

### Retrieving the stream URL

Use `wait_for_stream()` for the simplest approach:

```python
whep_url = device.wait_for_stream(timeout=30)
```

Or call `device.info()` directly — the `whep_url` field contains the playback URL:

```python
session = device.info()
whep_url = session.get("whep_url")
```

The URL is also present on each item returned by `device.list_sessions()`.

### Using the stream

The WHEP URL works with any client that speaks the WHEP protocol. A few options:

- **Browser**: Use a WHEP JavaScript client (e.g. [`@AlexxIT/go2rtc`](https://github.com/AlexxIT/go2rtc) or Cloudflare's player SDK) to render a `<video>` element.
- **CLI**: `revyl device info --json | jq -r '.whep_url'` to pipe the URL into other tools.
- **Custom integration**: POST to the WHEP URL with an SDP offer to negotiate a WebRTC session — the response contains the SDP answer.

The stream stays live for the lifetime of the device session and stops when the session is stopped.

For copy-pasteable embedding examples (HTML, React, iframe), see [STREAMING.md](STREAMING.md).

---

## ScriptClient

Manage code-execution scripts (Python, JavaScript, TypeScript, Bash) used by `code_execution` blocks in tests.

```python
from revyl import ScriptClient

scripts = ScriptClient()              # Uses default CLI runner
scripts = ScriptClient(cli=my_cli)    # Custom CLI runner
```

### `list(runtime=None) -> list[dict]`

List all scripts, optionally filtered by runtime (`"python"`, `"javascript"`, `"typescript"`, `"bash"`).

### `get(name_or_id) -> dict`

Get a script by name or UUID, including its source code.

### `create(name, file_path, runtime, description=None) -> dict`

Create a new script from a local file.

```python
scripts.create("seed-db", file_path="scripts/seed.py", runtime="python", description="Seeds test data")
```

### `update(name_or_id, file_path=None, name=None, description=None) -> dict`

Update a script's code, name, or description.

### `delete(name_or_id, force=True) -> str`

Delete a script. Raises `RevylError` if the script is in use by tests.

### `usage(name_or_id) -> list[dict]`

List tests that reference this script.

---

## ModuleClient

Manage reusable test modules — shared groups of test blocks that can be imported via `module_import`.

```python
from revyl import ModuleClient

modules = ModuleClient()              # Uses default CLI runner
modules = ModuleClient(cli=my_cli)    # Custom CLI runner
```

### `list(search=None) -> list[dict]`

List all modules, optionally filtered by name or description substring.

### `get(name_or_id) -> dict`

Get a module by name or UUID, including its blocks.

### `create(name, blocks_file, description=None) -> dict`

Create a module from a YAML file containing a `blocks:` array.

```python
modules.create("login-flow", blocks_file="modules/login.yaml", description="Standard login sequence")
```

### `update(name_or_id, name=None, blocks_file=None, description=None) -> dict`

Update a module's blocks, name, or description.

### `delete(name_or_id, force=True) -> str`

Delete a module. Raises `RevylError` (HTTP 409) if still referenced by tests.

### `usage(name_or_id) -> list[dict]`

List tests that import this module.

---

## BuildClient

Upload and manage app builds on Revyl.

```python
from revyl import BuildClient

builds = BuildClient()              # Uses default CLI runner
builds = BuildClient(cli=my_cli)    # Custom CLI runner
```

### `upload(app_name=None, platform=None, skip_build=False, version=None, set_current=False) -> dict`

Build and upload an app. Uses the project's `.revyl/config.yaml` build commands by default.

```python
builds.upload(app_name="my-app", platform="android")
builds.upload(skip_build=True)  # upload existing artifact without rebuilding
```

### `list(app_name=None, platform=None) -> list[dict]`

List uploaded build versions, optionally filtered by app or platform.

### `delete(name_or_id, version=None, force=True) -> str`

Delete an app (and all versions) or a specific build version.

---

## Types

### `DeviceModel`

Union type of all supported device models. Auto-generated from `device-targets.json`.

```python
from revyl import DeviceModel

# iOS models
model: DeviceModel = "iPhone 16"
model: DeviceModel = "iPhone 17 Pro Max"
model: DeviceModel = "iPad Pro 13-inch (M4)"

# Android models
model: DeviceModel = "Pixel 7"
```

Use with `DeviceClient.start(device_model=..., os_version=...)` to target a specific device. Both parameters must be provided together. Use `DeviceClient.targets()` to list all available combinations.

### `OsVersion`

Union type of all supported OS versions. Auto-generated from `device-targets.json`.

```python
from revyl import OsVersion

version: OsVersion = "iOS 18.5"
version: OsVersion = "iOS 26.2"
version: OsVersion = "Android 14"
```

### Other Types

| Type | Definition | Description |
|------|-----------|-------------|
| `Platform` | `Literal["ios", "android"]` | Target platform |
| `Runtime` | `Literal["python", "javascript", "typescript", "bash"]` | Code execution runtime |
| `SwipeDirection` | `Literal["up", "down", "left", "right"]` | Swipe direction |
| `KeyInput` | `Literal["ENTER", "BACKSPACE"]` | Keyboard key input |

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
