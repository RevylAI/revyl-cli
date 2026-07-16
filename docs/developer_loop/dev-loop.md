---
title: "Guide: Dev Loop"
description: "Set up revyl dev for fast local verification with hot reload on cloud devices"
---

<!-- AUTO-GENERATED from revyl-cli/docs/guide-dev-loop.md — do not edit manually -->

# Dev Loop Guide

`revyl dev` gives you a live cloud device connected to your local dev server. Change code locally, see it on the device instantly, and convert successful flows into regression tests.

## Prerequisites

- Revyl CLI installed and authenticated (`revyl auth login`)
- An Expo or React Native project with a dev client build

## Step 1: Configure hot reload

```bash
revyl init
```

Hot reload is configured automatically during init. If detection picks the wrong provider (common in monorepos), use `revyl init --provider expo` — that updates `.revyl/config.yaml` with the right settings.

For the provider config schema (`expo`, `react-native`, `app_scheme`, `platform_keys`, etc.), see [Configuration › Hot Reload](../CONFIGURATION.md#hot-reload-configuration). For per-framework setup nuances (monorepos, KMP, Flutter), see [Dev Setup Guide](dev-setup.md).

## Step 2: Upload a dev build

Upload a development client build for the branch you're working on:

```bash
revyl build --platform ios-dev
```

For Expo, this is a dev client build. For bare React Native, it's your debug build.

`revyl dev` automatically picks the build that matches your current git branch. If no match is found, it falls back to the latest available build.

## Step 3: Start the dev loop

```bash
revyl dev
```

This:

1. Starts your local dev server (Expo via `npx expo start --dev-client`, or Metro via `npx react-native start`)
2. Creates a Revyl relay to expose it to cloud devices
3. Installs the dev client build on a cloud device
4. Opens the device session in your browser

Normal runs keep advisory HMR diagnostics quiet so they do not look like startup
failures. If you are troubleshooting relay or HMR internals, run with
`revyl dev --debug` to print the diagnostic probes.

Now edit code locally and see changes reflected on the device instantly.

### Platform and build overrides

```bash
revyl dev --platform android              # Explicit platform
revyl dev --no-open                       # Don't open browser (headless/SSH)
revyl dev --platform ios --build          # Force a fresh dev build first
revyl dev --build-version-id <id>         # Pin a specific build
revyl dev --context ios-main              # Named context for parallel loops
revyl dev --force-hot-reload              # Diagnostic launch after Expo relay transport
revyl dev --ready-timeout 120             # Large apps / slow Metro: extend relay readiness wait
revyl dev --prewarm-timeout 600           # Very large Expo apps: extend cold bundle prewarm (max 600s)
revyl dev --no-build --tunnel "<expo-dev-client-link>"  # Reuse an Expo tunnel
```

If startup fails because the relay transport is not ready yet (slow Metro
startup on large apps), raise `--ready-timeout` (or set `REVYL_READY_TIMEOUT`;
seconds, default 60). If Revyl starts Expo and verifies relay transport but
times out proving the first Expo manifest, retry with `--force-hot-reload`
first. This launches after
the relay and dev server start, skipping only the manifest and bundle proof. If
the app loads, keep working; if the dev client shows a project load error,
restart Expo/Metro or capture `revyl device report --session-id <id> --json`.
If Revyl reaches the bundle prewarm stage but the first cold JavaScript
transform times out, raise `--prewarm-timeout` (or set
`REVYL_PREWARM_TIMEOUT`; seconds, default 300, maximum 600).

If you already run Expo with its own tunnel, you can collapse the manual device
start + deep-link step into one command:

```bash
npx expo start --tunnel --dev-client
revyl dev --no-build --app-id <app-id> --tunnel "<deep-link-from-expo>"
```

`--tunnel` accepts either the full Expo dev-client link or the raw `https://...`
tunnel URL. Passing the full dev-client link works even when the local Revyl
config does not have an Expo `app_scheme`. Prefer the Revyl relay first in
cloud-agent environments; use Expo tunnel fallback only after device screenshots
or `revyl device report --session-id <id> --json` show force mode still did not
load through the relay.

## Step 4: Interact with the device

Use `revyl device` commands to observe, act, and verify in a tight loop.

```bash
# Observe
revyl device screenshot --out before.png

# Act
revyl device tap --target "Sign In button"
revyl device type --target "email field" --text "user@example.com"
revyl device type --target "password field" --text "secret123"
revyl device tap --target "Log In"

# Verify
revyl device screenshot --out after.png
```

Always follow the **observe-act-verify** pattern: screenshot before an action, take the action, then screenshot again to confirm the result.

Scroll through content with swipe:

```bash
revyl device swipe --target "product list" --direction down
```

## Step 5: Run tests in dev mode

While the dev loop is active, run existing tests against your local code:

```bash
revyl dev test run login-flow
```

To reuse a running dev loop's relay from another terminal:

```bash
# Terminal 1: dev loop running
revyl dev --context ios-main

# Terminal 2: reuse the tunnel via --context
revyl dev test run login-flow --context ios-main
```

## Step 6: Create tests from the dev loop

After verifying a flow manually, convert it to a regression test:

```bash
revyl dev test create checkout-flow --platform ios
revyl dev test open checkout-flow
```

## Step 7: Promote to regression

Once the test is stable, push and run it outside dev mode:

```bash
revyl test push checkout-flow --force
revyl test run checkout-flow
```

---

## New branch workflow

When you create a new branch and want `revyl dev` to use that branch's build:

```bash
git checkout -b feature/new-login
revyl build --platform ios-dev            # Tagged with your branch
revyl dev --platform ios                  # Auto-picks the branch build
```

If you already have a local artifact and want to skip the build step:

```bash
revyl build upload --file build/dev-ios.tar.gz --platform ios
revyl dev --platform ios
```

## Device-first flow

Start a plain device session first, then attach and run the dev loop on it:

```bash
revyl device start --platform ios
revyl dev attach active --context checkout
revyl dev --context checkout            # reuses the attached session
```

When the dev loop exits, the attached session stays running. Run `revyl dev --context checkout` again to resume, or `revyl dev stop` to detach.

## Context management

```bash
revyl dev list                         # List dev contexts in the current worktree
revyl dev use ios-main                 # Switch the current context
revyl dev status                       # Show context status (JSON)
revyl dev rebuild                      # Trigger a rebuild
revyl dev stop                         # Stop the current context
revyl dev stop --all                   # Stop all contexts
```

## Rebuild model

Whether a code change needs a new binary depends on the framework. Inside an active `revyl dev` session, press **`[r]`** to rebuild the artifact, upload it, and reinstall on the cloud device — without ending the session.

| Framework | What `[r]` does | When you need it |
|-----------|------------------|------------------|
| **Expo** | Re-runs the EAS / native rebuild command and re-uploads | Only when native dependencies change (new native modules, Podfile / Gradle dependency changes, `app.json` native config). JS/TS reaches the device via the Metro relay. |
| **React Native (bare)** | Re-runs the Xcode / Gradle build | Same — only native changes. JS/TS is live via Metro. |
| **Flutter** | Re-runs `flutter build` for the active platform | Every code change. Dart compiles into the binary; there is no hot reload over a cloud relay. |
| **Swift / Xcode** | Re-runs `xcodebuild` | Every code change. The binary **is** the app. |
| **Kotlin / Gradle** | Re-runs `./gradlew assembleDebug` | Every code change. The binary **is** the app. |

For JS-based frameworks, `revyl dev` keeps the JS bundle live via Metro and the Revyl relay; the uploaded build is a "dev client shell." For native frameworks, every change becomes a rebuild — `[r]` is the keyboard shortcut for "do that now."

Typical rebuild times: Android Gradle ~30–90s, Xcode ~20–60s, Flutter Dart-only ~15–30s. First builds are longer.

## Team sharing

All developers push builds to a shared app container (the `app_id` in config). Each developer gets their own cloud device session, relay URL, and local server. For JS projects, multiple developers can share the same dev build and still see their own code changes.

---

## What's Next

- [Dev Setup Guide](dev-setup.md) — provider configuration details
- [Device Management](../device/index.md) — device session management and control
- [Agent Skills](../integrations/skills.md) — AI agent workflows with dev loop
