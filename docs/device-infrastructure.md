# Use Revyl as device infrastructure

Build your app in the cloud, start an iOS simulator or Android emulator, and
control it from your own tools. No Revyl test or AI agent is required.

Before you start, install the [Revyl CLI](https://docs.revyl.ai/cli) and authenticate with
`revyl auth login`. Sessions and remote builds use your organization's cloud
resources. Use test data and keep API keys and screenshots private.

## 1. Build remotely

From your app's project directory, run `revyl init` once. Review the detected
recipe and its Revyl app ID using the [remote build setup](https://docs.revyl.ai/remote-builds/quickstart),
then run the command for your platform:

```bash iOS
revyl build --remote --platform ios
```

```bash Android
revyl build --remote --platform android
```

The build runs on Revyl's cloud infrastructure, uploads the artifact, and sets
it as the app's current build by default. No local Xcode or Android SDK is
needed. If more than one build profile matches, select one when prompted;
for unattended CI, follow the [remote build configuration guide](https://docs.revyl.ai/remote-builds/configuration).

Already uploaded a build? Skip this step. iOS needs a simulator `.app` or zipped
`.app`; Android needs a compatible `.apk`. For project setup, dependencies, and
framework recipes, see [Remote Builds](https://docs.revyl.ai/remote-builds/quickstart) and
[Framework Guides](https://docs.revyl.ai/builds/frameworks).

## 2. Start a device with your app

Use the matching `app_id` from your project's `.revyl/config.yaml`:

```bash iOS
revyl device start --platform ios --app-id YOUR_IOS_APP_ID
```

```bash Android
revyl device start --platform android --app-id YOUR_ANDROID_APP_ID
```

Revyl selects a default device and installs the app's latest build. The command
prints the session ID and a live viewer link. Keep the session ID for the next
steps; use the viewer to watch the device.

For a repeatable CI run, select a specific uploaded version with
`--build-version-id` instead of `--app-id`. A build version ID is different from
a remote build job ID.

## 3. Control the device and capture evidence

Replace the placeholder with your session ID:

```bash
SESSION_ID="YOUR_SESSION_ID"
revyl device screenshot --session-id "$SESSION_ID" --out app.png
revyl device home --session-id "$SESSION_ID"
revyl device screenshot --session-id "$SESSION_ID" --out home.png
```

Inspect the screenshots to confirm the app opened and the home command changed
the screen. Your harness owns the assertions; a saved screenshot alone does
not prove a test passed. See the [device command reference](https://docs.revyl.ai/cli/command-reference)
for taps, typing, app lifecycle, and other actions.

## 4. Stop the device

```bash
revyl device stop --session-id "$SESSION_ID"
```

Always release the session your job created, including when a test fails. Do
not stop every session in a shared organization.

## Appium and Maestro

This quickstart uses Revyl's native device commands. Connecting an unchanged
Appium or Maestro suite still needs a framework connection layer; a viewer URL
is not an automation endpoint. The [Appium driver](https://docs.revyl.ai/infrastructure/appium)
and [Maestro runner](https://docs.revyl.ai/infrastructure/maestro)
implement attach-only native subsets over existing APIs and are available as
private source repositories. Synthetic-app checks verified native lookup and
gestures for Appium, and native actions, focused deletion, appearance, and deep
links for Maestro on staging Android emulators and iOS simulators. Maestro's
input/paste uses the existing viewer control channel, with the
[input limitations](https://docs.revyl.ai/infrastructure/maestro#text-input-through-the-viewer-channel)
documented separately; that integration has loopback coverage, not live-device
verification. Duration-preserving swipe/scroll still requires undeployed worker
capabilities and fails closed on current staging. Broader compatibility is not
established.
Get the [Appium driver](https://github.com/RevylAI/appium-revyl-driver) or
[Maestro driver](https://github.com/RevylAI/maestro-revyl-driver) for setup and
runnable samples; repository access is required. The Maestro runner embeds
the genuine Maestro runtime; it is not a plugin for stock `maestro test`.
Neither integration enables standard UiAutomator2/XCUITest or full Maestro
compatibility. See [the remaining requirements](https://docs.revyl.ai/infrastructure#what-needs-to-be-added-for-appium-and-maestro).

## Optional: a complete script for CI

The script below starts one device, opens Settings or an uploaded build,
saves screenshots, and stops only that session on exit. It needs Bash and `jq`.
It is a lifecycle smoke check, not an Appium or Maestro adapter.

<details>
<summary>Show the complete smoke script</summary>

```bash
#!/usr/bin/env bash
set -euo pipefail
umask 077

platform="${1:-}"
case "$platform" in
  ios|android) ;;
  *) printf 'Usage: bash smoke.sh <ios|android> [build-version-id]\n' >&2; exit 2 ;;
esac
if (( $# > 2 )); then
  printf 'Usage: bash smoke.sh <ios|android> [build-version-id]\n' >&2
  exit 2
fi
for dependency in revyl jq; do
  if ! command -v "$dependency" >/dev/null 2>&1; then
    printf 'Missing required command: %s\n' "$dependency" >&2
    exit 2
  fi
done

session_id=""
cleanup() {
  local exit_status=$?
  trap - EXIT
  if [[ -n "$session_id" ]]; then
    if ! revyl device stop --session-id "$session_id" >/dev/null; then
      printf 'Device cleanup failed; stop the session explicitly.\n' >&2
      if (( exit_status == 0 )); then exit_status=1; fi
    fi
  fi
  exit "$exit_status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

session_json=$(revyl device start --platform "$platform" --timeout 600 --open=false --json)
session_id=$(jq -er '.session_id | select(type == "string" and length > 0)' <<<"$session_json")
artifact_dir="artifacts/revyl-${platform}-${session_id}"
mkdir -p "$artifact_dir"
printf 'Session: %s\n' "$session_id"
jq -r 'select(.viewer_url != null and .viewer_url != "") | "Live viewer: \(.viewer_url)"' <<<"$session_json"
unset session_json

if [[ -n "${2:-}" ]]; then
  revyl device install --session-id "$session_id" --build-version-id "$2" >/dev/null
  revyl device launch --session-id "$session_id" >/dev/null
else
  revyl device open-app --session-id "$session_id" --app settings >/dev/null
fi
revyl device screenshot --session-id "$session_id" --out "$artifact_dir/app.png" >/dev/null
test -s "$artifact_dir/app.png"
revyl device home --session-id "$session_id" >/dev/null
revyl device screenshot --session-id "$session_id" --out "$artifact_dir/home.png" >/dev/null
test -s "$artifact_dir/home.png"
printf 'Device commands completed. Inspect screenshots in %s\n' "$artifact_dir"
```

</details>

Save the script as `smoke.sh`, then run `bash smoke.sh ios` or
`bash smoke.sh android`. Add a platform-matching build version ID as the second
argument to open your own app instead of Settings.

The script sets a 600-second idle timeout as a fallback; an abrupt runner
termination can still prevent its cleanup trap from running. Give CI jobs their
own timeout, keep the returned session ID, and collect private screenshots even
when a job fails. Review the images before treating them as visual proof.
