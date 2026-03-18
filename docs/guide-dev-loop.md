<!-- mintlify
title: "Guide: Dev Loop"
description: "Set up revyl dev for fast local verification with hot reload on cloud devices"
target: cli/dev-loop-guide.mdx
-->

# Dev Loop Guide

`revyl dev` gives you a live cloud device connected to your local dev server. Change code locally, see it on the device instantly, and convert successful flows into regression tests.

## Prerequisites

- Revyl CLI installed and authenticated (`revyl auth login`)
- An Expo or React Native project with a dev client build

## Step 1: Configure hot reload

```bash
revyl init --hotreload
```

Select your provider when prompted. This updates `.revyl/config.yaml` with the hot reload settings.

### Expo configuration

```yaml
hotreload:
  default: expo
  providers:
    expo:
      app_scheme: myapp
      port: 8081
      platform_keys:
        ios: ios-dev
        android: android-dev
```

### React Native (bare) configuration

```yaml
hotreload:
  default: react-native
  providers:
    react-native:
      port: 8081
      platform_keys:
        ios: ios-dev
        android: android-dev
```

## Step 2: Upload a dev build

Upload a development client build for the branch you're working on:

```bash
revyl build upload --platform ios-dev
```

For Expo, this is a dev client build. For bare React Native, it's your debug build.

`revyl dev` automatically picks the build that matches your current git branch. If no match is found, it falls back to the latest available build.

## Step 3: Start the dev loop

```bash
revyl dev
```

This:

1. Starts your local dev server (Expo via `npx expo start --dev-client`, or Metro via `npx react-native start`)
2. Creates a Cloudflare tunnel to expose it to cloud devices
3. Installs the dev client build on a cloud device
4. Opens the device session in your browser

Now edit code locally and see changes reflected on the device instantly.

### Platform and build overrides

```bash
revyl dev --platform android              # Explicit platform
revyl dev --no-open                       # Don't open browser (headless/SSH)
revyl dev --platform ios --build          # Force a fresh dev build first
revyl dev --build-version-id <id>         # Pin a specific build
```

## Step 4: Run tests in dev mode

While the dev loop is active, run existing tests against your local code:

```bash
revyl dev test run login-flow
```

## Step 5: Create tests from the dev loop

After verifying a flow manually, convert it to a regression test:

```bash
revyl dev test create checkout-flow --platform ios
revyl dev test open checkout-flow
```

## Step 6: Promote to regression

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
revyl build upload --platform ios-dev     # Tagged with your branch
revyl dev --platform ios                  # Auto-picks the branch build
```

## When do you need a new build?

- **Expo / React Native:** Only when native dependencies change (new native modules, Podfile changes, Gradle dependency changes). JS/TS changes are served live via the tunnel.
- **Swift / Kotlin (native):** Every code change requires a new build.

## Team sharing

All developers push builds to a shared app container (the `app_id` in config). Each developer gets their own cloud device session, tunnel, and local server. For JS projects, multiple developers can share the same dev build and still see their own code changes.

---

## What's Next

- [Hot Reload Setup](/cli/hot-reload) — provider configuration details
- [Device Scripting](/device/scripting-guide) — Python SDK for programmatic device control
- [Agent Journeys](/cli/agent-journeys) — AI agent workflows with dev loop
