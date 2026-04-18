<!-- mintlify
title: "Dev Loop"
description: "Connect a live cloud device to your local dev server, iterate in real time, and convert flows into regression tests"
target: cli/journey-dev-loop.mdx
-->

# Dev Loop

`revyl dev` gives you a live cloud device connected to your local dev server. Change code locally, see it on the device instantly, and convert successful flows into regression tests.

**Time:** ~5 minutes to start, then iterate as long as you need

## Prerequisites

- Revyl CLI installed and authenticated (`revyl auth login`)
- A mobile project (Expo, React Native, Flutter, Swift, Android, KMP, or Bazel)

<Callout type="tip" title="Already done this?">
  If you've already run `revyl init` and uploaded a build, skip to [Start the dev loop](#step-3-start-the-dev-loop).
</Callout>

## Step 1: Initialize your project

```bash
cd your-app
revyl init
```

The CLI detects your framework and writes `.revyl/config.yaml` with hot reload settings. If detection picks the wrong provider (common in monorepos), force it:

```bash
revyl init --provider expo
```

### Supported frameworks

| Framework | Hot Reload | Rebuild Loop | Provider Name |
|-----------|-----------|--------------|---------------|
| Expo | JS/TS changes live | `[r]` for native changes | `expo` |
| React Native (bare) | JS/TS changes live | `[r]` for native changes | `react-native` |
| Flutter | — | `[r]` rebuild + reinstall | — |
| Swift/iOS | — | `[r]` rebuild + reinstall | `swift` |
| Android Native | — | `[r]` rebuild + reinstall | `android` |
| Kotlin Multiplatform | — | `[r]` rebuild + reinstall | — |
| Bazel | — | `[r]` rebuild + reinstall | — |

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

Bare React Native does not require `app_scheme`. The device loads the JS bundle directly over the Revyl relay to Metro.

## Step 2: Upload a dev build

```bash
revyl build upload --platform ios-dev
```

For Expo, this is a dev client build. For bare React Native, it's your debug build. For native projects (Swift, Android, Flutter), it's a debug binary.

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

Now edit code locally and see changes reflected on the device instantly.

### Platform and build overrides

```bash
revyl dev --platform android              # Explicit platform
revyl dev --no-open                       # Don't open browser (headless/SSH)
revyl dev --platform ios --build          # Force a fresh dev build first
revyl dev --build-version-id <id>         # Pin a specific build
revyl dev --port 8082                     # Custom Metro port
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

## When do you need a new build?

| Project type | Rebuild when... |
|---|---|
| Expo / React Native | Native dependencies change (new native modules, Podfile changes, Gradle dependency changes). JS/TS changes are served live via the relay. |
| Swift / Kotlin / Flutter | Every code change requires a new build. Press `[r]` in the dev TUI to rebuild. |

## New branch workflow

```bash
git checkout -b feature/new-login
revyl build upload --platform ios-dev     # Tagged with your branch
revyl dev --platform ios                  # Auto-picks the branch build
```

## Team sharing

All developers push builds to a shared app container (the `app_id` in config). Each developer gets their own cloud device session, relay URL, and local server. For JS projects, multiple developers can share the same dev build and still see their own code changes.

---

## Expo-specific details

### URL schemes

Hot reload deep-links the dev client to your local Metro server via a custom URL scheme. This requires `expo.scheme` in your `app.json`:

```json
{
  "expo": {
    "scheme": "myapp"
  }
}
```

If your app has no scheme (common in apps that only use universal links), add one:

```json
{
  "expo": {
    "scheme": "myapp-dev"
  }
}
```

Then rebuild the dev client (`revyl build upload --platform ios-dev`), since the scheme is baked into the binary at build time.

### The `use_exp_prefix` option

If deep links fail with "No application is registered to handle this URL scheme", the binary may only have the prefixed variant (`exp+myapp://` instead of `myapp://`). Toggle in config:

```yaml
hotreload:
  providers:
    expo:
      app_scheme: myapp
      use_exp_prefix: true
```

### Dynamic config (app.config.js / app.config.ts)

The CLI reads the URL scheme from `app.json`. If your project uses `app.config.js` instead, provide the scheme explicitly:

```bash
revyl init --provider expo --hotreload-app-scheme myapp
```

### Monorepo setup

In monorepos, run all Revyl commands from the directory containing the Expo app's `package.json` — not the monorepo root.

```bash
cd apps/native
revyl init --provider expo
revyl dev --platform ios
```

If `expo` is hoisted out of the local `package.json`, use `--provider expo` to bypass auto-detection.

---

## Troubleshooting

| Symptom | Fix |
|---------|-----|
| "Detected Swift instead of Expo" | `revyl init --provider expo` |
| "No providers detected" | `cd` to the app directory; verify `package.json` has `expo` or `react-native` |
| "App scheme empty" | `--hotreload-app-scheme myapp` or edit config |
| "Port 8081 is already in use" | Kill the other Metro instance or use `--port 8082` |
| "No application is registered to handle this URL scheme" | Toggle `use_exp_prefix: true/false` in config; if neither works, add `scheme` to app.json and rebuild |
| App closes briefly after deep link | Normal. Dev client is reloading to connect to Metro via tunnel. Wait for it to reopen. |
| "Build platform 'ios' not found" | Run `revyl init --force` to re-detect, or add `build.platforms.ios` manually |

---

## What's Next

<CardGroup cols={2}>
  <Card title="Test Suite at Scale" icon="layer-group" href="/cli/journey-test-suite">
    Modules, scripts, workflows, and team sync patterns
  </Card>
  <Card title="Device Scripting" icon="code" href="/device/scripting-guide">
    Python SDK for programmatic device control
  </Card>
  <Card title="CI/CD Pipeline" icon="rotate" href="/cli/journey-ci-cd">
    Run tests automatically on every pull request
  </Card>
</CardGroup>
