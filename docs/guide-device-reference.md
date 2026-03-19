<!-- mintlify
title: "Device Commands"
description: "Provision cloud-hosted Android and iOS devices and interact with them from the CLI"
target: cli/devices.mdx
-->

The `revyl device` command group lets you provision cloud-hosted mobile devices, install apps, and interact with them directly from your terminal. Every command has an equivalent [MCP tool](/cli/mcp-setup) for AI agent integration.

<Callout type="info" title="Start here for onboarding">
  If you want task-oriented onboarding first, use [Device Quickstart](/device/quickstart), then [Device Scripting Guide](/device/scripting-guide) or [Device Troubleshooting](/device/troubleshooting).
</Callout>

## Overview

Device sessions are cloud-hosted Android or iOS devices that you can control remotely. The typical lifecycle is:

1. **Start** a session with `revyl device start`
2. **Install** an app with `revyl device install`
3. **Interact** using `tap`, `type`, `swipe`, and other action commands
4. **Stop** the session with `revyl device stop`

Sessions auto-terminate after 5 minutes of inactivity (configurable with `--timeout`).

### AI-Powered Grounding

Action commands like `tap`, `type`, and `swipe` support **natural language targeting**. Instead of specifying pixel coordinates, describe the element you want to interact with:

```bash
revyl device tap --target "Sign In button"
revyl device type --target "email input field" --text "user@example.com"
revyl device swipe --target "product list" --direction down
```

The CLI takes a screenshot, sends your description to the AI grounding model, and resolves it to pixel coordinates automatically. You can also pass raw `--x` and `--y` coordinates as an override.

### Multi-Session Support

You can run multiple device sessions simultaneously. Sessions are indexed starting at 0, and one session is always marked as "active":

```bash
revyl device start --platform android    # Session 0 (active)
revyl device start --platform ios        # Session 1
revyl device use 1                       # Switch active to session 1
revyl device tap --target "button" -s 0  # Target session 0 explicitly
```

Session indices are **stable** — stopping session 0 does not renumber session 1. The first started session is automatically set as the active session. If the active session is stopped, the CLI switches to the lowest remaining index.

For detailed workflows (CLI, MCP, Python SDK), cross-client sync, and troubleshooting, see the [Multi-Session Guide](/device/multi-session).

### Common Flags

These flags are shared across most device commands:

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--json` | | `false` | Output results as JSON |
| `-s` | | `-1` | Session index to target (`-1` = active session) |

---

## Session Management

### `revyl device start`

Provision and start a new cloud device session.

```bash
revyl device start [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--platform` | `ios` | Platform: `ios` or `android` |
| `--timeout` | `300` | Idle timeout in seconds |
| `--open` | `false` | Open the live viewer in your browser after the device is ready |
| `--app-id` | | App ID to resolve latest build from |
| `--app-url` | | Direct app artifact URL (`.apk`/`.ipa`/`.zip`) |
| `--build-version-id` | | Build version ID to install |
| `--app-link` | | Deep link to launch after app start |
| `--device` | `false` | Interactively select device model and OS version |
| `--device-model` | | Target device model (e.g. `"iPhone 16"`) |
| `--os-version` | | Target OS version (e.g. `"iOS 18.5"`) |
| `--json` | `false` | Output as JSON |

**Examples:**

```bash
revyl device start
revyl device start --platform android --open --timeout 600
revyl device start --platform ios --device-model "iPhone 16" --os-version "iOS 18.5"
```

The response includes a `viewer_url` you can open in your browser to watch the device live.

### `revyl device stop`

Stop one or all device sessions.

```bash
revyl device stop [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `-s` | `-1` | Session index to stop (`-1` = active session) |
| `--all` | `false` | Stop all sessions |
| `--json` | `false` | Output as JSON |

**Examples:**

```bash
revyl device stop              # Stop active session
revyl device stop -s 2         # Stop session at index 2
revyl device stop --all        # Stop all sessions
```

### `revyl device list`

List all active device sessions.

```bash
revyl device list [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--json` | `false` | Output as JSON |

Output columns: `#` (index, `*` marks active), `PLATFORM`, `STATUS`, `SESSION ID`, `UPTIME`.

### `revyl device use`

Switch the active session to a different index.

```bash
revyl device use <index>
```

**Example:**

```bash
revyl device use 2    # Make session 2 the active session
```

### `revyl device info`

Show details about a device session: session ID, platform, viewer URL, and uptime.

```bash
revyl device info [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `-s` | `-1` | Session index (`-1` = active session) |
| `--json` | `false` | Output as JSON |

### `revyl device doctor`

Run diagnostics on auth, session health, worker reachability, and device connectivity.

```bash
revyl device doctor [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `-s` | `-1` | Session index (`-1` = active session) |
| `--json` | `false` | Output as JSON |

**Checks performed:**

1. Authentication (API key / login status)
2. Active session existence
3. Worker reachability
4. Device connectivity (via health endpoint)
5. Lists all active sessions with markers

---

## Device Actions

All action commands support two targeting modes:

- **Grounded** (default): Pass `--target "element description"` and coordinates are resolved via AI
- **Raw coordinates**: Pass `--x` and `--y` for direct pixel targeting

<Callout type="tip" title="Writing good targets">
  Describe what you **see** on screen. Prefer visible text/labels (`"the 'Sign In' button"`), visual characteristics (`"blue rounded rectangle"`), or spatial anchors (`"text area below the 'Subject:' line"`). Avoid abstract UI jargon.
</Callout>

### `revyl device tap`

Tap an element by description or coordinates.

```bash
revyl device tap [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--target` | | Element description (AI grounded) |
| `--x` | `0` | X coordinate (raw) |
| `--y` | `0` | Y coordinate (raw) |
| `-s` | `-1` | Session index |
| `--json` | `false` | Output as JSON |

**Examples:**

```bash
revyl device tap --target "Sign In button"
revyl device tap --x 540 --y 960
```

### `revyl device double-tap`

Double-tap an element by description or coordinates.

```bash
revyl device double-tap [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--target` | | Element description (AI grounded) |
| `--x` | `0` | X coordinate (raw) |
| `--y` | `0` | Y coordinate (raw) |
| `-s` | `-1` | Session index |
| `--json` | `false` | Output as JSON |

### `revyl device long-press`

Long press an element with configurable duration.

```bash
revyl device long-press [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--target` | | Element description (AI grounded) |
| `--x` | `0` | X coordinate (raw) |
| `--y` | `0` | Y coordinate (raw) |
| `--duration` | `1500` | Press duration in milliseconds |
| `-s` | `-1` | Session index |
| `--json` | `false` | Output as JSON |

**Example:**

```bash
revyl device long-press --target "message bubble" --duration 2000
```

### `revyl device type`

Type text into an element. Taps the target first, then inputs the text.

```bash
revyl device type [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--target` | | Element description (AI grounded) |
| `--x` | `0` | X coordinate (raw) |
| `--y` | `0` | Y coordinate (raw) |
| `--text` | | Text to type **(required)** |
| `--clear-first` | `true` | Clear the field before typing |
| `-s` | `-1` | Session index |
| `--json` | `false` | Output as JSON |

**Examples:**

```bash
revyl device type --target "email input field" --text "user@example.com"
revyl device type --x 540 --y 400 --text "hello" --clear-first=false
```

### `revyl device swipe`

Swipe from an element in a direction.

```bash
revyl device swipe [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--target` | | Element description (AI grounded) |
| `--x` | `0` | X coordinate (raw) |
| `--y` | `0` | Y coordinate (raw) |
| `--direction` | | Direction: `up`, `down`, `left`, `right` **(required)** |
| `--duration` | `500` | Swipe duration in milliseconds |
| `-s` | `-1` | Session index |
| `--json` | `false` | Output as JSON |

**Swipe direction semantics:**

| Direction | Finger Movement | Content Effect |
|-----------|----------------|----------------|
| `up` | Finger moves up | Scrolls content down (reveals below) |
| `down` | Finger moves down | Scrolls content up (reveals above) |
| `left` | Finger moves left | Scrolls content right |
| `right` | Finger moves right | Scrolls content left |

**Example:**

```bash
revyl device swipe --target "product list" --direction down --duration 1000
```

### `revyl device drag`

Drag from one point to another using raw coordinates.

```bash
revyl device drag [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--start-x` | `0` | Starting X coordinate |
| `--start-y` | `0` | Starting Y coordinate |
| `--end-x` | `0` | Ending X coordinate |
| `--end-y` | `0` | Ending Y coordinate |
| `-s` | `-1` | Session index |
| `--json` | `false` | Output as JSON |

<Callout type="info">
  `drag` only supports raw coordinates, not AI grounding. Take a screenshot first and validate your start/end points before running the drag.
</Callout>

**Example:**

```bash
revyl device drag --start-x 100 --start-y 500 --end-x 100 --end-y 200
```

### `revyl device pinch`

Pinch or zoom an element.

```bash
revyl device pinch [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--target` | | Element description (AI grounded) |
| `--x` | `0` | X coordinate (raw) |
| `--y` | `0` | Y coordinate (raw) |
| `--scale` | `2` | Zoom scale (`>1` zooms in, `<1` zooms out) |
| `--duration` | `300` | Pinch duration in milliseconds |
| `-s` | `-1` | Session index |
| `--json` | `false` | Output as JSON |

**Examples:**

```bash
revyl device pinch --target "map" --scale 1.5
revyl device pinch --target "photo" --scale 0.5   # Zoom out
```

### `revyl device clear-text`

Clear text in an input element.

```bash
revyl device clear-text [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--target` | | Element description (AI grounded) |
| `--x` | `0` | X coordinate (raw) |
| `--y` | `0` | Y coordinate (raw) |
| `-s` | `-1` | Session index |
| `--json` | `false` | Output as JSON |

**Example:**

```bash
revyl device clear-text --target "Search field"
```

---

## Control Commands

### `revyl device wait`

Pause for a fixed duration.

```bash
revyl device wait [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--duration-ms` | `1000` | Wait duration in milliseconds |
| `-s` | `-1` | Session index |
| `--json` | `false` | Output as JSON |

**Example:**

```bash
revyl device wait --duration-ms 2000
```

### `revyl device back`

Press the Android back button.

```bash
revyl device back [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `-s` | `-1` | Session index |
| `--json` | `false` | Output as JSON |

<Callout type="info">
  This command only works on Android devices. It will return an error on iOS.
</Callout>

### `revyl device key`

Send a non-printable key to the focused field.

```bash
revyl device key [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--key` | | Key to send: `ENTER` or `BACKSPACE` |
| `-s` | `-1` | Session index |
| `--json` | `false` | Output as JSON |

**Examples:**

```bash
revyl device key --key ENTER
revyl device key --key BACKSPACE
```

### `revyl device shake`

Trigger a shake gesture on the device.

```bash
revyl device shake [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `-s` | `-1` | Session index |
| `--json` | `false` | Output as JSON |

### `revyl device home`

Return to the device home screen.

```bash
revyl device home [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `-s` | `-1` | Session index |
| `--json` | `false` | Output as JSON |

### `revyl device kill-app`

Kill the installed app on the device.

```bash
revyl device kill-app [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `-s` | `-1` | Session index |
| `--json` | `false` | Output as JSON |

### `revyl device open-app`

Open a system app by name or bundle ID.

```bash
revyl device open-app [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--app` | | App name (e.g. `settings`, `safari`, `chrome`) or raw bundle ID **(required)** |
| `-s` | `-1` | Session index |
| `--json` | `false` | Output as JSON |

**Examples:**

```bash
revyl device open-app --app settings
revyl device open-app --app safari
revyl device open-app --app com.google.chrome.ios
```

### `revyl device navigate`

Open a URL or deep link on the device.

```bash
revyl device navigate [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--url` | | URL or deep link to open **(required)** |
| `-s` | `-1` | Session index |
| `--json` | `false` | Output as JSON |

**Examples:**

```bash
revyl device navigate --url https://example.com
revyl device navigate --url "myapp://settings/profile"
```

### `revyl device set-location`

Set the device GPS coordinates.

```bash
revyl device set-location [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--lat` | `0` | Latitude (`-90` to `90`) **(required)** |
| `--lon` | `0` | Longitude (`-180` to `180`) **(required)** |
| `-s` | `-1` | Session index |
| `--json` | `false` | Output as JSON |

**Example:**

```bash
revyl device set-location --lat 37.7749 --lon -122.4194
```

### `revyl device download-file`

Download a file to the device from a URL.

```bash
revyl device download-file [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--url` | | URL to download from **(required)** |
| `--filename` | | Optional destination filename on the device |
| `-s` | `-1` | Session index |
| `--json` | `false` | Output as JSON |

**Example:**

```bash
revyl device download-file --url https://example.com/report.pdf --filename report.pdf
```

---

## Vision

### `revyl device screenshot`

Capture a screenshot of the device screen.

```bash
revyl device screenshot [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--out` | | Output file path (e.g. `screen.png`) |
| `-s` | `-1` | Session index |
| `--json` | `false` | Output as JSON |

**Examples:**

```bash
revyl device screenshot --out screen.png
revyl device screenshot -s 1 --out ~/Desktop/ios-screen.png
```

---

## App Management

### `revyl device install`

Install an app on the device from a remote URL or a previously uploaded build.

```bash
revyl device install [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--app-url` | | URL to download app from (`.apk` or `.ipa`) |
| `--build-version-id` | | Build version ID from a previous upload; download URL is resolved automatically |
| `--bundle-id` | | Bundle ID (optional, auto-detected from the binary) |
| `-s` | `-1` | Session index |
| `--json` | `false` | Output as JSON |

Provide either `--app-url` or `--build-version-id`, not both.

**Examples:**

```bash
revyl device install --app-url "https://example.com/app.apk"
revyl device install --build-version-id bv_abc123
revyl device install --app-url "https://example.com/app.ipa" --bundle-id com.example.app
```

<Callout type="tip" title="Using uploaded builds">
  If you've already uploaded a build with `revyl build upload`, use `--build-version-id` so you don't need to track URLs manually. The MCP `install_app` tool also accepts this flag. See [MCP Setup](/cli/mcp-setup) for details.
</Callout>

### `revyl device launch`

Launch an installed app by bundle ID.

```bash
revyl device launch [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--bundle-id` | | App bundle ID to launch **(required)** |
| `-s` | `-1` | Session index |
| `--json` | `false` | Output as JSON |

**Example:**

```bash
revyl device launch --bundle-id com.example.app
```

---

## Live Steps

Execute individual test steps against an active device session without creating a full test. These commands use the same AI-powered step execution engine used in `revyl test run`.

### `revyl device instruction`

Run one natural-language instruction step on the active device. The AI agent interprets the description and performs the necessary actions (tap, type, swipe, etc.) to complete it.

```bash
revyl device instruction <description> [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `-s` | `-1` | Session index |
| `--json` | `false` | Output as JSON |

**Examples:**

```bash
revyl device instruction "Open Settings and tap Wi-Fi"
revyl device instruction "Log in with test@example.com and password secret123"
revyl device instruction "Scroll down and tap the 'Save Changes' button"
```

### `revyl device validation`

Run one natural-language validation step. Asserts that a condition is true on the current screen.

```bash
revyl device validation <description> [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `-s` | `-1` | Session index |
| `--json` | `false` | Output as JSON |

**Examples:**

```bash
revyl device validation "Verify the Settings title is visible"
revyl device validation "The inbox shows at least one unread message"
revyl device validation "The price displayed is $9.99"
```

### `revyl device extract`

Run one natural-language extract step. Pulls data from the current screen and returns it.

```bash
revyl device extract <description> [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--variable-name` | | Optional variable name to tag the extracted value for downstream use |
| `-s` | `-1` | Session index |
| `--json` | `false` | Output as JSON |

**Examples:**

```bash
revyl device extract "Extract the visible account email"
revyl device extract "Extract the total price" --variable-name total_price
```

### `revyl device code-execution`

Run a code execution step on the active device session.

Three modes:

1. **By script ID:** `revyl device code-execution <script-id>`
2. **From local file:** `revyl device code-execution --file ./script.py --runtime python`
3. **Inline code:** `revyl device code-execution --code "print('hello')" --runtime python`

Modes 2 and 3 create an ephemeral script on the backend, execute it, then clean up.

```bash
revyl device code-execution [script-id] [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--file` | | Run code from a local file (creates ephemeral script) |
| `--code` | | Run inline code string (creates ephemeral script) |
| `--runtime` | | Script runtime for `--file`/`--code`: `python`, `javascript`, `typescript`, or `bash` |
| `-s` | `-1` | Session index |
| `--json` | `false` | Output as JSON |

**Examples:**

```bash
revyl device code-execution script_abc123
revyl device code-execution --file ./tests/check_state.py --runtime python
revyl device code-execution --code "console.log('ok')" --runtime javascript
```

---

## Utilities

### `revyl device report`

View the report for the active device session, including steps executed, actions taken, video URL, and status.

```bash
revyl device report [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `-s` | `-1` | Session index |
| `--json` | `false` | Output as JSON |

### `revyl device targets`

List available device models and OS versions for each platform.

```bash
revyl device targets [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--platform` | | Filter to a specific platform: `ios` or `android` |
| `--json` | `false` | Output as JSON |

**Examples:**

```bash
revyl device targets
revyl device targets --platform ios
```

### `revyl device history`

Show recent device session history from the server.

```bash
revyl device history [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--limit` | `20` | Maximum number of sessions to show |
| `--json` | `false` | Output as JSON |

---

## MCP Tool Mapping

Every CLI device command has a corresponding MCP tool for AI agent integration:

| CLI Command | MCP Tool |
|-------------|----------|
| `device start` | `start_device_session` |
| `device stop` | `stop_device_session` |
| `device list` | `list_device_sessions` |
| `device use` | `switch_device_session` |
| `device info` | `get_session_info` |
| `device doctor` | `device_doctor` |
| `device tap` | `device_tap` |
| `device double-tap` | `device_double_tap` |
| `device long-press` | `device_long_press` |
| `device type` | `device_type` |
| `device swipe` | `device_swipe` |
| `device drag` | `device_drag` |
| `device pinch` | `device_pinch` |
| `device clear-text` | `device_clear_text` |
| `device screenshot` | `screenshot` |
| `device install` | `install_app` |
| `device launch` | `launch_app` |
| `device wait` | `device_wait` |
| `device back` | `device_back` |
| `device key` | `device_key` |
| `device shake` | `device_shake` |
| `device home` | `device_go_home` |
| `device kill-app` | `device_kill_app` |
| `device open-app` | `device_open_app` |
| `device navigate` | `device_navigate` |
| `device set-location` | `device_set_location` |
| `device download-file` | `device_download_file` |
| `device instruction` | `device_instruction` |
| `device validation` | `device_validation` |
| `device extract` | `device_extract` |
| `device code-execution` | `device_code_execution` |
| `device report` | `get_session_report` |

See [MCP Server Setup](/cli/mcp-setup) for configuration and usage with AI coding tools.

---

## Examples

### Start a device, install an app, and interact

```bash
revyl device start --platform android --open
revyl device install --app-url "https://example.com/myapp.apk"
revyl device launch --bundle-id com.example.myapp
revyl device screenshot --out before.png
revyl device tap --target "Sign In button"
revyl device type --target "email field" --text "user@test.com"
revyl device type --target "password field" --text "secret123"
revyl device tap --target "Log In"
revyl device screenshot --out after.png
revyl device stop
```

### Use live steps for a complex flow

```bash
revyl device start --platform ios --open
revyl device install --app-url "https://example.com/myapp.ipa"
revyl device instruction "Log in with test@example.com and navigate to the Settings page"
revyl device validation "The Settings page header is visible"
revyl device extract "Extract the current plan name" --variable-name plan
revyl device stop
```

### Multi-session testing

```bash
revyl device start --platform android    # Session 0
revyl device start --platform ios        # Session 1

# Interact with Android (session 0)
revyl device tap --target "Get Started" -s 0

# Interact with iOS (session 1)
revyl device tap --target "Get Started" -s 1

revyl device stop --all
```

### Target an element before acting

```bash
revyl device screenshot --out before-submit.png
revyl device tap --target "Submit button"
revyl device screenshot --out after-submit.png
```

---

## Troubleshooting

### Device won't start

1. Check authentication: `revyl auth status`
2. Run diagnostics: `revyl device doctor`
3. Verify your account has available device slots

### "no active device session"

Sessions auto-terminate after the idle timeout (default 5 minutes). Start a new session with `revyl device start`.

### Grounding can't find the element

1. Take a screenshot to see what's actually on screen: `revyl device screenshot --out debug.png`
2. Use more specific descriptions: `"blue 'Next' button"` instead of `"button"`
3. Fall back to raw coordinates with `--x`/`--y` when needed

### Actions seem to miss their target

The device resolution may differ from what you expect. Capture a fresh screenshot and switch to raw `--x`/`--y` targeting for precise control.

---

## Next Steps

<CardGroup cols={2}>
  <Card title="Device Quickstart" icon="rocket" href="/device/quickstart">
    Task-oriented onboarding with your first device session.
  </Card>
  <Card title="MCP Server Setup" icon="robot" href="/cli/mcp-setup">
    Connect device tools to your AI coding agent.
  </Card>
  <Card title="Interactive Mode" icon="terminal" href="/cli/interactive">
    Build tests step-by-step with live device feedback.
  </Card>
  <Card title="Command Reference" icon="book" href="/cli/reference">
    Full reference for all CLI commands.
  </Card>
</CardGroup>
