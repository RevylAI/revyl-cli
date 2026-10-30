---
name: revyl-cli-dev-loop
description: Use when you need to build, run, view, or test a mobile app in a simulator or emulator.
---

# Revyl Dev Loop

Get the app running on a Revyl cloud device, share its live viewer, and verify
changes on the device. Infer the app and platform from the repository and the
request; ask only when the choice remains ambiguous.

## Start the app

Work from the app directory, such as `apps/mobile` in a monorepo. Check
`revyl auth status`; use `revyl auth login` if sign-in is needed. For a new
project or stale detection, run `revyl init --detect` from that directory.

Choose the profile and platform from `.revyl/config.yaml` and the request.
Without flags, Revyl chooses a unique development-like profile or the sole
eligible choice. Resolve ambiguity from the CLI's choices; don't assume a
profile named `development` exists.

Use normal `revyl dev` for new work. In agent shells, add `--detach --json`
so the loop runs in the background and returns the session details:

```bash
revyl dev --profile <profile> --platform <ios-or-android> --detach --json
```

- **Expo:** Revyl manages Metro, the Expo dev client, and its relay for JS/TS
  hot reload. Native code, dependencies, permissions, or app configuration
  changes need a rebuild. Use an Expo tunnel only as the documented fallback.
- **Bare React Native:** use the Revyl Metro relay; JS/TS changes hot reload,
  while native source, dependencies, Podfile, Gradle, permissions, or app
  configuration changes need a rebuild. Do not use an Expo tunnel.
- **Flutter:** use a rebuild-first loop; the binary is the app and Dart saves
  may trigger rebuilds. Use `revyl dev rebuild --wait` when needed.
- **Native Swift/iOS and Kotlin/Android:** use a rebuild-first loop. Xcode,
  Gradle, native source, resources, manifests, permissions, or native
  dependencies require a rebuild and reinstall. Expo tunnels do not apply.
- **KMP, Bazel, and other artifact-based builds:** treat the configured
  `build.profiles.<profile>.<platform>.output_path` artifact as the app and
  iterate through its build recipe plus `revyl dev rebuild --wait`.
- For Flutter and native builds, add `--remote` to build in the cloud. Use the
  configured recipe; do not invent a local build path.
- **Monorepos:** run from the app directory containing `.revyl/config.yaml` if
  hoisted dependencies or nested native folders confuse detection.

To use an existing uploaded build, use
`revyl dev --no-build --app-id <app-id> --detach --json`. This skips building
local changes. `--remote` cannot be combined with `--no-build` or `--tunnel`.

**Share `viewer_url` as a clickable link immediately.** The CLI opens it in the
user's browser when supported (`opened_browser` in the response). On a cloud
VM, share the link instead of opening a browser on the VM.

A live viewer or `dev status --wait-ready` means the device is available; the
build may still be running. Check `revyl dev status --context <context> --json`
for build completion and inspect the app before claiming the change is ready.
If `--seed-latest` was used, the visible app may still be the previous build.

## Reuse and target sessions

Save the returned `context` and `session_id`. Use that context for status,
rebuild, and stop, and `-s <session-id>` for device commands.

Contexts are worktree-local. Revyl auto-selects a safe branch/platform context name
if another loop is running in the same worktree.
Pass `--context <name>` only when deliberately targeting a known
loop. Inspect existing loops with `revyl dev list` before starting a duplicate.

Use `revyl dev use <name>` when deliberately switching the current named
context. To reuse an existing device, run `revyl dev attach <session-id> --context <name>`,
then `revyl dev --context <name> --detach --json`. Use `attach active` only when
exactly one active session is unambiguous; otherwise attach by server session ID
or local index.

Device commands act on the active session by default. `-s` accepts either a
local index from `revyl device list` or the server-issued `session_id`; use
`--session-id <uuid>` as the explicit spelling. `REVYL_SESSION_ID` scopes a
shell for commands that accept `-s`. Prefer server IDs when multiple sessions
are live: indexes are project-local and can change. `revyl device stop --all`
ignores `REVYL_SESSION_ID` but stops only sessions owned by this CLI or started
or explicitly selected here; it does not stop an unowned session. `revyl dev`
commands target their dev context instead.

## Iterate and verify

Keep the loop running and use separate commands to interact with the app:

```bash
revyl device screenshot -s <session-id> --out before.png
revyl device tap --target "Sign In button" -s <session-id>
revyl device type --target "Email field" --text "user@example.com" -s <session-id>
revyl device swipe --target "Product list" --direction down -s <session-id>
revyl device instruction "Open the checkout screen" -s <session-id>
revyl device validation "The checkout screen is visible" -s <session-id> --json
revyl device screenshot -s <session-id> --out after.png
revyl device report --session-id <session-id> --json
```

Inspect the screenshots before describing the result. A false or missing
validation verdict is a failed check, even if the device action completed.
In JSON output, inspect `success` and `step_output.validation_result`. Record
the path and outcome relevant to the user's request.

JS/TS edits hot reload on Expo and bare React Native. For changes that need a
build, or when an automatic rebuild has not applied the change, run:

```bash
revyl dev rebuild --context <context> --wait --timeout 600 --json
```

Verify again after the build succeeds. If it fails, inspect
`revyl dev logs --context <context> --build` and fix the reported error before
retrying. Keep status polling bounded by the build's configured timeout.

For a "let me see it" handoff, leave the loop running and share the viewer.
When verification is finished, stop the loop you started with
`revyl dev stop <context> --json`, unless the user wants it left running.

## Troubleshooting and other workflows

- **App or changes not visible:** inspect a screenshot and
  `revyl device report --session-id <session-id> --json` before changing the
  build or transport. Check build status for native apps.
- **Slow Metro startup:** increase `--ready-timeout`. For a slow first Expo
  bundle, increase `--prewarm-timeout` (up to 600 seconds). When the relay works
  but Expo manifest readiness cannot be confirmed, use `--force-hot-reload`
  and verify on the device. Use `revyl dev --help` for these options.
- **Expo relay failure:** use an Expo tunnel only after device evidence shows
  the app cannot load or hot reload through the Revyl relay. Start
  `npx expo start --tunnel --dev-client` in a persistent terminal and pass its
  full dev-client link to `revyl dev --no-build --app-id <app-id> --tunnel '<link>'
  --detach --json`. Bare React Native and native builds cannot use this fallback.
- Keep the Revyl relay when screenshots show the app loading or reports show
  successful relay fetches. Advisory HMR warnings alone are not a reason to
  stop it. `--force-hot-reload` skips only manifest/bundle proof; Expo startup
  and relay transport must still work. Switch to an Expo tunnel only when the
  device remains on an error screen, reports show no relay fetches, or hot
  reload is not applying changes.
- **Relay reports unauthorized:** check `revyl auth status` and `revyl ping`.
  If both pass, diagnose the relay with `--debug` on the startup command.
- **Repeated login:** use the
  [auth-bypass skill](https://github.com/RevylAI/revyl-cli/blob/main/skills/revyl-cli-auth-bypass/SKILL.md)
  when the task calls for test authentication. With `session.auth_bypass`
  configured, Revyl applies it on launch. Use `revyl dev auth refresh -s <session-id>` to
  restore a logged-out session; it re-fires the boot token and does not mint a
  new one. Restart the loop if its token has expired. Never paste launch-var
  values into code, logs, screenshots, or PRs.
- **Web projects:** follow the
  [Web setup guide](https://github.com/RevylAI/revyl-cli/blob/main/skills/revyl-cli-auth-bypass/references/web.md)
  for an explicit Web recipe or registered preview URL.
