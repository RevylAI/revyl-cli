# Device Prod Validation

> [Back to README](../README.md) | [Commands](COMMANDS.md) | [SDK](SDK.md)

Use this runbook to validate the recent device parity work from your local branch against production.

The default path is:
- local binary from this checkout
- authenticated with your prod API key
- no `--dev`
- backend relay through `https://backend.revyl.ai`

## Requirements

- `REVYL_API_KEY=rev_...`
- `jq`
- `curl`
- `go`
- `uv` for the Python SDK smoke

Use a disposable org/user and a disposable app build if you want to exercise install/launch/kill-app in production.

## Automated CLI Smoke

Run the mirrored platform suites explicitly:

```bash
cd revyl-cli
make device-prod-smoke-ios
make device-prod-smoke-android
```

Useful variants:

```bash
make device-prod-smoke-ios ARGS="--grounded-text"
make device-prod-smoke-ios ARGS="--app-url https://... --bundle-id com.example.app"
make device-prod-smoke-android ARGS="--grounded-text"
make device-prod-smoke-android ARGS="--keep-session"
```

The generic target still exists for ad hoc runs:

```bash
make device-prod-smoke ARGS="--platform ios"
make device-prod-smoke ARGS="--platform android"
```

What each mirrored CLI suite validates:
- local binary can auth against prod
- `device start` returns a real `workflow_run_id`
- direct relay checks for `/health`, `/screenshot`, and `/tap`
- CLI control commands succeed through the backend relay
- negative validation checks fail as expected
- `click` is not a valid CLI alias
- Android additionally validates `device back`
- iOS intentionally skips `device back` because the worker treats it as Android-only

The script is fail-fast and writes temporary artifacts under `/tmp/revyl-device-prod-smoke-*`.

## Automated SDK Smoke

Run the mirrored Python SDK suites against the local binary:

```bash
cd revyl-cli
make device-prod-sdk-smoke-ios
make device-prod-sdk-smoke-android
```

Useful variants:

```bash
make device-prod-sdk-smoke-ios ARGS="--grounded-text"
make device-prod-sdk-smoke-ios ARGS="--app-url https://... --bundle-id com.example.app"
make device-prod-sdk-smoke-android ARGS="--grounded-text"
make device-prod-sdk-smoke-android ARGS="--keep-session"
```

The generic target still exists for ad hoc runs:

```bash
make device-prod-sdk-smoke ARGS="--platform ios"
make device-prod-sdk-smoke ARGS="--platform android"
```

What each mirrored SDK suite validates:
- `DeviceClient` wraps the local binary, not a stale installed build
- core device actions succeed against prod
- live-step helpers like `instruction` and `validation` resolve through the same CLI JSON contract
- `click` is not exposed on `DeviceClient`
- optional grounded text flow and install flow still work from the SDK surface
- Android additionally validates `back`
- iOS intentionally skips `back` because it is not a cross-platform device action

## Manual MCP Validation

MCP still needs a manual smoke because the normal consumer is an MCP host:

```bash
cd revyl-cli
make build
./build/revyl mcp serve
```

Run the same tool set once with an iOS session and once with an Android session. Validate these tools from your MCP host or inspector:
- `start_device_session`
- `device_tap`
- `device_navigate`
- `device_instruction`
- `device_validation`
- `device_extract`
- `device_code_execution`
- `device_doctor`
- `list_device_sessions`
- `switch_device_session`
- `install_app`
- `launch_app`
- `get_session_info`
- `stop_device_session`

Expected:
- the above tools exist
- no `device_click` tool exists
- outputs match the CLI behavior for the same action
- session info and session listings expose `whep_url` when the backend provides it
- Android MCP smoke should include `device_back`
- iOS MCP smoke should not rely on `device_back`

## Manual Multi-Session Validation

The smoke scripts intentionally keep this separate because it is easier to inspect manually:

```bash
./build/revyl device start --platform ios --json
./build/revyl device start --platform android --json
./build/revyl device list --json
./build/revyl device use 1
./build/revyl device info --json
./build/revyl device instruction "Open Settings" --json
./build/revyl device validation "Verify Settings is visible" --json
./build/revyl device tap -s 0 --x 200 --y 400 --json
./build/revyl device tap -s 1 --x 220 --y 420 --json
./build/revyl device stop --all
```

Expected:
- `device use` switches the active session correctly
- `-s` targets the requested session
- `device info --json` includes `whep_url` when streaming is available
- live-step commands return structured `step_type`, `step_id`, and `step_output`
- stopping one session does not break the other

## Manual Raw-Session URL Validation

Use a disposable artifact URL that matches the platform you are validating:
- iOS: `.ipa`
- Android: `.apk`

```bash
ARTIFACT_URL="https://example.com/path/to/app.ipa"

# Preinstall from URL while starting the raw session.
./build/revyl device start --platform ios --app-url "$ARTIFACT_URL" --json

# Install from URL into an already running raw session.
./build/revyl device start --platform ios --json
./build/revyl device install --app-url "$ARTIFACT_URL" --json

# Download a remote file without installing it.
./build/revyl device download-file --url https://example.com/report.pdf --filename report.pdf --json
./build/revyl device stop --all
```

Expected:
- `device start --app-url` forwards the artifact URL and the session still reaches `running`
- `device install --app-url` fails if the worker reports `success: false`; it should not print a false-positive success message
- `device download-file --url` fails if the worker reports `success: false`; it should not print a false-positive success message
- `device download-file` returns `device_path` in JSON when the download succeeds
- `device install` returns `bundle_id` in JSON when the worker can detect it

## Optional Prod-Side Checks

If you have backend and worker log access, also confirm:
- each local action hits `/api/v1/execution/device-proxy/{workflow_run_id}/{action}`
- worker logs only show canonical action names like `tap`, `swipe`, `navigate`, and `execute_step`
- no `click` alias appears anywhere
- non-idempotent actions do not show obvious duplicate execution
