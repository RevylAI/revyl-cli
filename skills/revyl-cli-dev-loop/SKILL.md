---
name: revyl-cli-dev-loop
description: Generic CLI-first Revyl dev loop for hot reload, rebuild-loop, and device exploration.
---

# Revyl CLI Dev Loop Skill

Use this skill when the user wants the generic Revyl CLI dev loop instead of MCP tool-by-tool orchestration. Start from the app's real stack, keep the session running, and use the device as the source of truth.

## Detect and Start

```bash
# Initialize or refresh project detection.
revyl init --detect

# Start the dev loop for the default platform (iOS is the default).
revyl dev
```

When platform matters, make it explicit:

```bash
revyl dev --platform ios
revyl dev --platform android
```

If detection picks the wrong provider, force the provider during init instead of editing around a bad config:

```bash
revyl init --provider expo
revyl init --provider react-native
```

In monorepos, run Revyl from the actual app directory, not the workspace root. For example, use `apps/mobile` for an Expo app even if the repo root also has a `.revyl/` directory.

## Start or Attach

Use normal `revyl dev` for a new loop. Attach only when you are intentionally
reusing an already-running device session.

```bash
# Discover existing state before reusing a device.
revyl dev list
revyl device list

# New loop: let Revyl provision the device.
revyl dev --context <name>

# Reuse the current active session only when it is unambiguous.
revyl dev attach active --context <name>
revyl dev --context <name>

# If multiple sessions exist, attach by explicit session id or index.
revyl dev attach <session-id> --context <name>
revyl dev attach <index> --context <name>
revyl dev --context <name>
```

`active` is a convenience shortcut for the current running device session. Do
not use it when multiple sessions exist or when the desired session is unclear.
After attaching a session to a context, run `revyl dev --context <name>` to
start the loop on that session.

## Framework Guidance

- Expo: use the Revyl-managed relay and Expo dev client for JS/TS hot
  reload. Rebuild only when native config, native modules, SDK/native
  dependencies, permissions, or URL scheme registration changes. Try an
  external Expo tunnel only after screenshots or reports show the Revyl relay,
  app load, or HMR path failed. If repeated web auth is slowing stable Expo
  testing, install and use `revyl-cli-auth-bypass`; let it detect the stack and
  delegate to `revyl-cli-auth-bypass-expo` for Expo app code. Implement the
  test-only bypass in the app first, start `revyl dev` with
  `--launch-var REVYL_AUTH_BYPASS_ENABLED --launch-var REVYL_AUTH_BYPASS_TOKEN`,
  wait for the normal Expo app UI, then open the app-specific `revyl-auth`
  deep link.
- React Native bare: use the Metro relay. No `app_scheme` is needed because
  the device loads the JS bundle over the Revyl relay. JS/TS changes hot
  reload; native dependency, Podfile, Gradle, or native source changes need a
  rebuild. Do not use Expo tunnel fallback unless the app is actually an Expo
  dev-client flow.
- Flutter: use a rebuild-first loop. There is no Metro/Expo dev server for
  cloud hot reload; the binary is the app. `revyl dev` installs and runs the
  current build, and Dart file saves may auto-trigger rebuilds. In agent
  shells, use `revyl dev rebuild --wait` when an explicit rebuild is needed,
  then verify on the device.
- Native iOS/Swift and native Android/Kotlin: use a rebuild-first loop. Xcode,
  Gradle, Kotlin, Swift, resource, manifest, or native dependency changes must
  build, upload or delta-push, reinstall, and relaunch in the cloud session.
  Expo tunnels do not fix these stacks.
- KMP, Bazel, and other native artifact flows: treat the configured
  `build.platforms.<key>.output` artifact as the app. Iterate through the
  configured build command, `revyl dev rebuild --wait`, and device verification.
- Monorepos: if detection is confused by hoisted dependencies or nested native folders, run from the app directory and force the provider only when needed.

If repeated login slows exploration on any stack, use `revyl-cli-auth-bypass`
first. It detects the app stack and delegates to the matching platform leaf
before this dev-loop skill starts the session with bypass launch vars.

## Observe, Act, Verify

Use screenshots and reports to decide what happened before changing strategy.

```bash
revyl device screenshot --out before.png
revyl device tap --target "Sign In button"
revyl device type --target "Email field" --text "user@example.com"
revyl device swipe --target "Product list" --direction down
revyl device instruction "Open the checkout screen"
revyl device screenshot --out after.png
revyl device report --session-id <session-id> --json
```

During exploration, capture the exact path that worked. Describe actions with visible target language and keep the path at intent level.

## Expo Auth Bypass

For apps that have implemented the `revyl-cli-auth-bypass` contract, start the
dev loop with the bypass launch vars and then navigate after the app loads
normally:

```bash
# One-time setup if the launch vars do not already exist.
export REVYL_AUTH_BYPASS_TOKEN="<test-only-token>"
revyl global launch-var create REVYL_AUTH_BYPASS_ENABLED=true
revyl global launch-var create REVYL_AUTH_BYPASS_TOKEN="$REVYL_AUTH_BYPASS_TOKEN"

export REVYL_CONTEXT="${USER:-agent}-expo-auth-$$"

revyl dev --context "$REVYL_CONTEXT" --no-build \
  --launch-var REVYL_AUTH_BYPASS_ENABLED \
  --launch-var REVYL_AUTH_BYPASS_TOKEN
```

After `Dev loop ready`, confirm the device is on the app UI with a screenshot,
then open the auth-bypass link from a separate shell:

```bash
revyl device navigate \
  --url "myapp://revyl-auth?token=$REVYL_AUTH_BYPASS_TOKEN&role=buyer&redirect=%2Fcheckout"
revyl device screenshot --out /tmp/revyl-auth-bypass.png
```

If the app has not implemented the handler yet, install/use
`revyl-cli-auth-bypass` first. It will delegate to `revyl-cli-auth-bypass-expo`
when the app is Expo or Expo Router. If the device session was reused, launch
vars may not have applied; run `revyl dev stop --context "$REVYL_CONTEXT"` and
start a fresh loop with the launch vars.

## Guardrails

1. Start from the detected app stack, then override only when detection is wrong.
2. Keep the dev loop running while using separate short-lived `revyl device` commands for interaction.
3. Prefer user-visible outcomes over implementation details.
4. Stop local loop cleanly with `Ctrl+C` or `revyl dev stop` when done.

## Agent Execution

`revyl dev` is a persistent process. In agent shells, run it in a background or non-blocking terminal and keep it alive while you inspect the device from separate commands.

1. **Background long-running loops** -- use the agent environment's non-blocking shell mode for `revyl dev`.
2. **Poll for readiness** -- `Hot reload ready` means the Expo/Metro transport
   is up; `Dev loop ready`, a viewer URL, or successful `revyl device` evidence
   means the full device loop is ready. If output stalls on device provisioning
   after `Hot reload ready`, debug the worker/device session path instead of
   changing the relay/tunnel strategy.
3. **Detect failures early** -- if the process exits or output contains
   `Error:` before the ready line, stop and report the error to the user.
4. **Device commands in a separate terminal** -- `revyl device tap`,
   `screenshot`, `type`, and `swipe` are short-lived. Run them in a
   different Shell call, not the dev-loop terminal.
5. **Do not interact with TTY prompts** -- the dev loop prints
   `[r] rebuild native + reinstall` and `[q] quit`. These require a real
   TTY. In agent shells, use `revyl dev rebuild`, `revyl dev stop`, or restart
   the loop instead.
6. **Attaching to an existing session** -- if no suitable session exists, run
   `revyl dev` normally. If exactly one relevant current session exists, attach
   it with `revyl dev attach active --context <name>`, then start the loop with
   `revyl dev --context <name>`. If multiple sessions exist, use an explicit
   session id or index; do not guess.
7. **Keep logs concise** -- use `revyl dev --debug` only for relay/HMR
   troubleshooting. When reporting results, summarize the state transitions and
   include only the first actionable error, relevant relay/session IDs, and a
   small log tail. Do not paste long spinner output or full debug streams unless
   the user asks for raw logs.

## Cloud Agent Relay Note

In Cursor or similar cloud-agent environments, start with the Revyl-managed
relay:

```bash
export REVYL_CONTEXT="${REVYL_CONTEXT:-revyl-dev-loop}"
revyl dev --context "$REVYL_CONTEXT" --no-build --app-id <app-id>
```

If you need to define the context yourself, set or export it before the
`revyl dev` command. Avoid inline shell assignment such as
`REVYL_CONTEXT=name revyl dev --context "$REVYL_CONTEXT"` because many shells
expand `$REVYL_CONTEXT` before the inline assignment takes effect.

For Expo and bare React Native, this lets Revyl own Metro/Expo startup, relay
creation, dev-client install, and the deep link opened on the cloud device. If
the app does not load or hot reload does not apply changes, gather device
evidence before changing transport.

If startup fails with `failed to create relay session: unauthorized` after
`Backend relay connectivity OK`, do not assume the developer needs to log in
again. First run `revyl auth status` and `revyl ping`; if both pass, capture a
`revyl dev --debug` run and treat it as a Revyl relay/backend issue. For Expo
dev-client projects, try the external tunnel fallback to keep the user moving.

After `Dev loop ready`, keep the process running. Treat `Viewer:` and the
relay/deep-link host printed by `revyl dev` as the active relay session; in
production this may be `relay.revyl.ai`, while local or branch environments may
use a generated relay/ngrok host. Do not stop the relay because of HMR
diagnostic warnings; normal runs hide advisory HMR diagnostics, and
`revyl dev --debug` is for relay/HMR troubleshooting.

Before switching to Expo tunnel fallback, gather device evidence:

```bash
revyl device screenshot -s <session-index>
revyl device report --session-id <session-id> --json
```

Continue using the relay if screenshots show the app downloading/loading or the
report/network evidence shows successful fetches from the relay host printed by
`revyl dev`. Only fall back for Expo/React Native dev-client projects if the
device remains on a dev-client error screen, the report shows no successful
relay fetches, or the app loads but hot reload is not applying changes.

For Flutter, Swift/iOS, native Android, KMP, Bazel, and other rebuild-first
stacks, do not switch to an Expo tunnel. These stacks do not use the
Metro/Expo transport path. If changes do not appear, inspect `revyl dev status`,
the last build output, and the device screenshot. Then trigger
`revyl dev rebuild --wait` from a separate shell, or stop and restart the loop
if the rebuild session is unhealthy.

If fallback is needed, start Expo in a long-running terminal and pass the full
dev-client link that Expo prints, not just the raw `*.exp.direct` URL:

```bash
CURSOR_AGENT=1 npx expo start --tunnel --dev-client
revyl dev --no-build --app-id <app-id> --tunnel '<full Expo dev-client link>'
```

```
Shell(command="export REVYL_CONTEXT=\"${REVYL_CONTEXT:-revyl-dev-loop}\" && revyl dev --context \"$REVYL_CONTEXT\" --no-build --app-id <app-id>", block_until_ms=0)
AwaitShell(pattern="Dev loop ready", block_until_ms=120000)

# Or attach to an existing context
Shell(command="revyl dev list")
Shell(command="revyl dev attach active --context default")
Shell(command="revyl dev --context default", block_until_ms=0)
AwaitShell(pattern="Dev loop ready", block_until_ms=120000)

Shell(command="revyl device screenshot")
Shell(command="revyl device tap --target 'Login button'")
```
