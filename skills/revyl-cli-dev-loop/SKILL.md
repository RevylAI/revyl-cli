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

# Start the dev loop for the default platform.
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

Use normal `revyl dev` for a new loop. Use attach when a device session or context already exists.

```bash
revyl dev list
revyl device start --platform ios
revyl dev attach active
revyl dev attach <session-id>
revyl dev --context <name>
```

After attaching a session to a context, run `revyl dev --context <name>` to reuse it.

## Framework Guidance

- Expo: use the Revyl-managed relay and Expo dev client for JS/TS hot reload. Rebuild only when native config, native modules, SDK/native dependencies, or URL scheme registration changes. Use external Expo tunnel only after screenshots or reports show the Revyl relay did not load.
- React Native bare: use the Metro relay. No `app_scheme` is needed. JS/TS changes hot reload; native dependency, Podfile, Gradle, or native source changes need a rebuild.
- Flutter: use a rebuild-first loop. `revyl dev` installs and runs the current build; code changes need `revyl dev rebuild` or a restarted loop after rebuilding.
- Native iOS, native Android, KMP, and Bazel: use a rebuild-first loop. The binary is the app, so source changes need rebuild, upload, and reinstall instead of hot reload.
- Monorepos: if detection is confused by hoisted dependencies or nested native folders, run from the app directory and force the provider only when needed.

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

## Guardrails

1. Start from the detected app stack, then override only when detection is wrong.
2. Keep the dev loop running while using separate short-lived `revyl device` commands for interaction.
3. Prefer user-visible outcomes over implementation details.
4. Stop local loop cleanly with `Ctrl+C` or `revyl dev stop` when done.

## Agent Execution

`revyl dev` is a persistent process. In agent shells, run it in a background or non-blocking terminal and keep it alive while you inspect the device from separate commands.

1. **Background long-running loops** -- use the agent environment's non-blocking shell mode for `revyl dev`.
2. **Poll for readiness** -- wait for `Dev loop ready` or `Hot reload ready`
   with a generous timeout (~120 s) to confirm startup succeeded.
3. **Detect failures early** -- if the process exits or output contains
   `Error:` before the ready line, stop and report the error to the user.
4. **Device commands in a separate terminal** -- `revyl device tap`,
   `screenshot`, `type`, and `swipe` are short-lived. Run them in a
   different Shell call, not the dev-loop terminal.
5. **Do not interact with TTY prompts** -- the dev loop prints
   `[r] rebuild native + reinstall` and `[q] quit`. These require a real
   TTY. In agent shells, use `revyl dev rebuild`, `revyl dev stop`, or restart
   the loop instead.
6. **Attaching to an existing dev context** -- if a dev loop is already
   running, use `revyl dev attach <context>` instead of starting a new one.
   This can also be long-running; background it the same way and poll for
   readiness. Use `revyl dev list` (short-lived) to discover
   active contexts first.

## Cloud Agent Relay Note

In Cursor or similar cloud-agent environments, prefer the Revyl-managed relay before using an external Expo tunnel:

```bash
revyl dev --context "$REVYL_CONTEXT" --no-build --app-id <app-id>
```

This lets Revyl own Metro/Expo startup, relay creation, dev-client install, and the deep link opened on the cloud device. Expo `--tunnel` is only a fallback when device evidence proves the relay path did not load.

After `Dev loop ready`, keep the process running. Treat `Viewer:` and a
`Deep Link:` containing `relay.revyl.ai` as a started relay session. Do not stop
the relay because of HMR diagnostic warnings; normal runs hide advisory HMR
diagnostics, and `revyl dev --debug` is for relay/HMR troubleshooting.

Before switching to Expo tunnel fallback, gather device evidence:

```bash
revyl device screenshot -s <session-index>
revyl device report --session-id <session-id> --json
```

Continue using the relay if screenshots show the app downloading/loading or the
report/network evidence shows successful `relay.revyl.ai` manifest/assets. Only
fall back if the device remains on a dev-client error screen or the report shows
no successful relay fetches.

If fallback is needed, start Expo in a long-running terminal and pass the full
dev-client link that Expo prints, not just the raw `*.exp.direct` URL:

```bash
CURSOR_AGENT=1 npx expo start --tunnel --dev-client
revyl dev --no-build --app-id <app-id> --tunnel '<full Expo dev-client link>'
```

```
Shell(command="revyl dev --context \"$REVYL_CONTEXT\" --no-build --app-id <app-id>", block_until_ms=0)
AwaitShell(pattern="Dev loop ready", block_until_ms=120000)

# Or attach to an existing context
Shell(command="revyl dev list")
Shell(command="revyl dev attach default", block_until_ms=0)
AwaitShell(pattern="Dev loop ready", block_until_ms=120000)

Shell(command="revyl device screenshot")
Shell(command="revyl device tap --target 'Login button'")
```
