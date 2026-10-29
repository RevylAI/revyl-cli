# Use Revyl as device infrastructure

Build your app in the cloud, start an iOS simulator or Android emulator, and
control it from your own tools. No Revyl test or AI agent is required.

Before you start, install the [Revyl CLI](https://docs.revyl.com/cli) and authenticate with
`revyl auth login`. Sessions and remote builds use your organization's cloud
resources. Use test data and keep API keys and screenshots private.

## 1. Build remotely

From your app's project directory, run `revyl init` once. Review the detected
recipe and its Revyl app ID using the [remote build setup](https://docs.revyl.com/remote-builds/quickstart),
then run the command for your platform:

```bash iOS
revyl build --remote --platform ios
```

```bash Android
revyl build --remote --platform android
```

The build runs on Revyl's cloud infrastructure and uploads the artifact to the
Revyl branch named after your Git branch when automatic detection is on, which
the upload creates if needed; with detection off it uses the app's default branch
([Build branches](https://docs.revyl.com/builds/branches)). No local Xcode or Android SDK is needed.
If more than one build profile matches, select one when prompted;
for unattended CI, follow the [remote build configuration guide](https://docs.revyl.com/remote-builds/configuration).

Already uploaded a build? Skip this step. iOS needs a simulator `.app` or zipped
`.app`; Android needs a compatible `.apk`. For project setup, dependencies, and
framework recipes, see [Remote Builds](https://docs.revyl.com/remote-builds/quickstart) and
[Framework Guides](https://docs.revyl.com/builds/frameworks).

## 2. Start a device with your app

Use the matching `app_id` from your app in Revyl or your project's
`.revyl/config.yaml`:

```bash iOS
revyl device start --platform ios --app-id YOUR_IOS_APP_ID
```

```bash Android
revyl device start --platform android --app-id YOUR_ANDROID_APP_ID
```

Revyl selects a default device and installs the default build of
[your Git branch](https://docs.revyl.com/builds/branches#git-branch-detection)
when automatic detection is on and it has a usable build,
otherwise of the app's default branch. If your build is on another branch, add
`--branch <name>`. For the other build choices, see
[Build branches from the CLI](build-branches.md#run-options). The command prints the session ID and a
live viewer link. Keep the session ID for the next steps; use the viewer to
watch the device.

When `REVYL_COMPUTER_ID` is set, mobile `device start` uses that computer and
the configured app's detected branch build. Pass `--app-id` or
`--build-version-id` to choose explicitly, `--computer=none` to start a
standalone device, or unset `REVYL_COMPUTER_ID` to disable the default.
Implicit computer starts skip `session.before_script` and `session.auth_bypass`
and use the app's defaults; standalone starts keep those session settings.

The start waits until the device is ready, which takes longer when devices are
queued. As soon as Revyl accepts it, the command names the session on stderr,
and `revyl device list` shows that session as `queued` or `starting`, with no
local index, until its device is ready; target it by session ID in the
meantime.

`revyl device list` shows sessions whose owner email matches the authenticated
CLI identity, sessions started in this directory, and the session last selected
with `revyl device attach` or `revyl device use`. Other sessions, including those
with an unknown owner email, are hidden and cannot become default targets.
`revyl device stop --all` stops the ready sessions in that set, not every session
in the organization; it names any listed sessions that are not ready and were
not stopped. `--session-id` and `REVYL_SESSION_ID` still target any session the
backend authorizes without adding it to local discovery.

If a device start fails after the backend assigns its session ID, the error
names that ID and includes the backend failure reason when available. Reasons
are length-limited and redact URLs and recognized credentials.

For a repeatable CI run, select one exact build with `--build-version-id`
instead of `--app-id`. A build ID is different from a remote build job ID. If you
pass both, the build must belong to that app; otherwise `device start` names the
app it belongs to and stops.

Device commands do not require a local config or a Git checkout. With no
`.revyl/config.yaml`, `device start` uses its command defaults and explicit
flags. In a Git checkout, it finds the nearest config within that worktree;
outside Git, it checks only the current directory. Use `-C <project-dir>` to
select a different directory.

An existing legacy config does not need migration for device commands.
`device start` reads canonical `session` settings or legacy `defaults.timeout`,
`before_session`, and `auth_bypass` settings without rewriting the file or
validating unrelated build and PR-review configuration. Explicit `--timeout`
still overrides the config. Invalid session settings, malformed YAML, and
unsafe config or script paths still fail before provisioning. Actions on an
existing session do not parse the config. `revyl build`, `revyl config validate`,
and `revyl config push` continue to require the canonical format.

## 3. Control the device and capture evidence

### Web sessions

Web sessions require a Web app and a URL or managed bundle build. Start the app's latest build,
or create or reuse one from an HTTPS URL before starting:

```bash
revyl app create --name "My Web App" --platform web --json
revyl device start --platform web --app YOUR_WEB_APP_ID --json
revyl device start --platform web --app YOUR_WEB_APP_ID --url https://example.com/ --json
revyl device start --platform web --app YOUR_WEB_APP_ID --build-version-id YOUR_BUILD_ID --viewport 1280x800 --json
```

`--app-id` remains an alias for the app selection. Without either, the app comes
from `build.profiles.<name>.web.app_id` in `.revyl/config.yaml`. To register a
URL build from CI without starting a session, use
`revyl build upload --platform web --url <https-url>` (see
[Web builds](web-builds.md)). `--commit` optionally identifies the URL build
when used with `--url`. URLs cannot contain credentials, query
strings, or fragments. No project input or local build is required.

Managed Web builds run inside the session's app container. Revyl verifies the
bundle, starts its configured command (or a static server), and waits for the
configured health check before opening Chrome. Runtime `run.env_keys` and
`run.secret_names` reference organization launch variables; their values are
available only to the app process, not the browser or automation worker.

The default viewport is 1280x800 and the default Web idle timeout is 600 seconds, unless session configuration or
`--timeout` overrides it.

#### Signed-in web starts

For apps with a test-only sign-in route, configure a worker-minted link:

```yaml
session:
  auth_bypass:
    sign_in:
      path: /__e2e__/login
      secret: REVYL_SIGN_IN_SECRET
```

`secret` names a secret key-value launch variable in the session's organization;
never put its value in the config. The CLI and MCP send only its name. The
backend binds the session to that variable, and the trusted worker reads its
value and opens `<build-origin><path>?code=…` before handing over the
session. Web starts do not run `session.before_script` or the mobile
launch-variable or deep-link preparation pipeline. Use
`session.auth_bypass.sign_in` for Web sign-in; any data a Web proof needs must
already exist in the preview environment, for example seeded by the preview's
own deploy or a test-login route.

A pull request comment's Open preview link signs its Web preview in with
the project's published `sign_in`, as mobile previews apply
`auth_bypass`; the pull request's own config cannot change it.

A Web session started without a sign-in of its own, such as one started from
the dashboard, signs in with the `sign_in` its app's project configuration
authors, as Atlas explorations and Web test runs do. A start that passes
environment overrides never falls back to it.

`path` must begin with a single `/` and cannot contain a query, fragment,
backslash, or whitespace. It is limited to 512 characters. Every code expires
after 120 seconds. The app's sign-in handler chooses the destination after exchanging
the code; Revyl does not send a redirect parameter.

The code is an HS256 compact JWS, signed with the UTF-8 secret. Its header is
`{"alg":"HS256","typ":"JWT"}` and its claims are `iss: "revyl"`, `aud: <build
origin>` (lowercase, without a default port), `sub: "default"`, integer `iat`
and `exp: iat + 120`, and `jti` containing 16 random bytes encoded as
unpadded base64url. The app must verify the signature, issuer, audience, and
expiry, consume each `jti` only once, and exchange the code for its own session.

The worker waits up to 30 seconds for navigation to leave the sign-in path on
the same origin. Failure stops startup with `sign_in_failed`. The signing
secret and code stay out of workflow inputs, CLI output, and reports; recording
starts after the redirect. Only the selected variable ID is retained in session
metadata to authorize the worker's secret read.

The config block is accepted in shared project configuration, but only Web
sessions act on it. Existing mobile `launch_vars`, `before_script`, and
`deep_link` behavior is unchanged.

#### Per-session environment overrides

A managed Web session can replace or add to its app's environment for that
session only, for example to point the app at a pull request's backend behind a
public tunnel. Pass each value with `--launch-env`, the same flag mobile starts
use, and no configuration change is needed:

```bash
revyl device start --platform web \
  --launch-env BACKEND_URL=https://pr-123.example.dev \
  --launch-env REVYL_SIGN_IN_SECRET
```

`--launch-env KEY=VALUE` is a plain override. `--launch-env NAME` with no `=`
is a secret override: the CLI reads its value from the local environment
variable `NAME`, so the value is never a command argument, never printed, and
never written to session metadata. MCP `launch_env` takes plain values only,
because a secret value would pass through the agent's tool arguments.

An override replaces the organization's value for one of the run's `env_keys`
or adds a new variable to the app process. Names must match
`[A-Za-z_][A-Za-z0-9_]{0,127}`. A session takes at most 32 overrides, each
value is at most 4,096 bytes with no control characters, and names plus values
together stay within the runtime's 128 KiB environment limit. A pull request
preview start (`scm_preview_id`) takes no overrides. The runtime owns
`PATH`, `HOME`, `LANG`, `PORT`, `HOSTNAME`, and `WEB_APP_BOOTSTRAP`, and
names starting with `DYLD_`, `SIMCTL_CHILD_`, or `COGNISIM_` are reserved, so
overrides can't use them. A plain override can't use a name in the run's
`secret_names` or an encrypted organization launch variable; pass those as
secret overrides.

A session with any override gets none of the organization's secrets: no
`secret_names` values and no organization sign-in secret. Secret overrides
reach the app process as its secrets instead. If one is named by
`session.auth_bypass.sign_in.secret`, it becomes the session's sign-in secret:
the worker signs the sign-in code with it, and it never enters the app's
environment. That name can't be a plain override. Without it, sign-in is off for
that session. Revyl stores override values encrypted for the session only and
deletes them when the session ends. Sessions without overrides are unchanged,
and overrides need a managed Web bundle. The session report lists each
overridden name, marks secret ones, and shows only the host of plain values that
parse as URLs.

While a Web session is running, organization members who can control it can
click, scroll, type, and press Enter, Tab, Backspace, Escape, and the arrow keys
in the live viewer. Each input is recorded as a Manual control step, and typed
text is never written to the report. Viewers and shared report links get a
view-only player. Drag and hover are not available in the viewer; use CLI or
MCP actions for them.

Use the returned `session_id` for screenshots, navigation, taps, typing, reports,
and stopping. Web screenshots include a local `screen_token`; grounded actions
carry the matching observation token automatically. A stale target requires a
fresh screenshot and target resolution. Do not automatically repeat an action
reported as applied or uncertain.

For private text, use `revyl device type --session-id YOUR_SESSION_ID --target
"password field" --text-env PASSWORD_ENV_NAME`. The value stays out of command
arguments and CLI output; on Web, this option only types into a focused password
field. Web does not support phone-only actions such as app installation, Home, or
pinch.

#### Keyboard, scrolling, and native selects

Use keys to dismiss dialogs and test keyboard reachability, rather than assuming
a clickable element is keyboard-accessible:

```bash
revyl device key Escape --session-id YOUR_SESSION_ID --json
revyl device key Tab --session-id YOUR_SESSION_ID
revyl device key Enter --session-id YOUR_SESSION_ID
revyl device key "Shift+Tab" --session-id YOUR_SESSION_ID
revyl device key "Meta+K" --session-id YOUR_SESSION_ID
revyl device scroll down --amount 600 --session-id YOUR_SESSION_ID --json
revyl device scroll up --amount 300 --target "Results list" --session-id YOUR_SESSION_ID
revyl device scroll right --amount 200 --x 400 --y 300 --session-id YOUR_SESSION_ID
revyl device select "Canada" --target "Country" --session-id YOUR_SESSION_ID --json
```

Keys are case-insensitive: Enter (Return), Backspace, Tab, Escape (Esc), Delete,
Space, ArrowUp, ArrowDown, ArrowLeft, ArrowRight, Home, End, PageUp, and PageDown.
Chords use `+` with distinct Ctrl, Alt, Meta, or Shift modifiers and a named key
or one ASCII letter/digit. Bare letters/digits, function keys, and arbitrary
text are rejected; use `device type` for text. On iOS and Android only the
existing Enter and Backspace keys are supported; Return and Delete remain
their existing aliases.

`scroll` accepts `up`, `down`, `left`, or `right`, either positionally or through
`--direction`. `--amount` is 1–16384 pixels (default 600). Down and right move
the viewport toward later content. Web `swipe` uses the same page directions;
native iOS and Android swipes use finger-motion directions instead. With no
target, `scroll` acts at the viewport center. To scroll a nested region, supply
either `--target` or both `--x` and `--y`; never combine them.

`select` chooses one enabled, visible option by its exact label in a native
HTML `<select>`, not by its value attribute. Supply `--target` or both coordinates.
On desktop Web, opening a native single-choice dropdown temporarily uses the
browser's in-page picker so option taps reach the dropdown instead of the page
behind it. The original appearance returns when the picker closes. This also
applies to native month and year dropdowns within a page's date picker, not
operating-system date dialogs.
Custom dropdowns need normal taps or keys. Scroll and select are Web-only and
fail before dispatch on phones. All three commands accept `-s`/`--session-id`
and `--json`; successful output does not echo the key, option label, or target.

### Phone sessions

Replace the placeholder with your session ID:

```bash
SESSION_ID="YOUR_SESSION_ID"
revyl device screenshot --session-id "$SESSION_ID" --out app.png
revyl device home --session-id "$SESSION_ID"
revyl device screenshot --session-id "$SESSION_ID" --out home.png
```

Inspect the screenshots to confirm the app opened and the home command changed
the screen. Your harness owns the assertions; a saved screenshot alone does
not prove a test passed. See the [device command reference](https://docs.revyl.com/cli/command-reference)
for taps, typing, app lifecycle, and other actions.

For supported foldable devices, see [live fold-state control](https://docs.revyl.com/reference/cli#live-fold-state).

## 4. Stop the device

```bash
revyl device stop --session-id "$SESSION_ID"
```

Always release the session your job created, including when a test fails. Do
not stop every session in a shared organization.

## Appium and Maestro

This quickstart uses Revyl's native device commands. An unchanged Appium or
Maestro suite needs a framework connection layer; a viewer URL is not an
automation endpoint. The [Appium driver](https://docs.revyl.com/infrastructure/appium)
and [Maestro runner](https://docs.revyl.com/infrastructure/maestro) are
source-installed integrations that run a native command subset against existing
device sessions. Get them from
[RevylAI/appium-revyl-driver](https://github.com/RevylAI/appium-revyl-driver)
and [RevylAI/maestro-revyl-driver](https://github.com/RevylAI/maestro-revyl-driver).
The Maestro runner embeds the Maestro runtime; it is not a plugin for stock
`maestro test`. Neither provides standard UiAutomator2/XCUITest or full Maestro
compatibility; each page lists the commands it supports.

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
`bash smoke.sh android`. Add a platform-matching build ID as the second
argument to open your own app instead of Settings.

The script sets a 600-second idle timeout as a fallback; an abrupt runner
termination can still prevent its cleanup trap from running. Give CI jobs their
own timeout, keep the returned session ID, and collect private screenshots even
when a job fails. Review the images before treating them as visual proof.
