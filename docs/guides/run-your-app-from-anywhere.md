# Run your app on a cloud device

Cloud development lets your local app run on a cloud iOS simulator or Android emulator. You keep editing code in your normal workspace while Revyl handles the device, install, stream, and network path.

Use it when a teammate, cloud agent, or worktree agent needs to inspect the app without setting up Xcode, Android Studio, Simulator, or Emulator locally.

## How it works

The loop has three pieces:

1. A **development-capable build** installed on the cloud device.
2. A **local dev server or build command** running in your workspace.
3. A **Revyl session** that connects the device to your local app through the CLI.

For Expo and React Native, most JavaScript and TypeScript changes hot reload through Metro. For Flutter, native iOS, and native Android, Revyl gives you a rebuild-and-reinstall loop instead.

## What you need

- A Revyl account with access to your workspace.
- The Revyl CLI installed and authenticated. See [Getting Started](../getting-started.md).
- A Revyl app with an uploaded development build.
- A project directory that Revyl can run locally.

For iOS, use a simulator `.app` or zipped `.app`, not an `.ipa`. For Android, use a debuggable APK with `x86_64` support. See [Artifact Requirements](../builds/artifact-requirements.md).

## 1. Upload a development build

The uploaded build is the shell Revyl installs on the cloud device. It must be able to load local code or be rebuilt quickly for local changes.

| Stack | What to upload |
|-------|----------------|
| Expo | A development client build. For iOS, use an EAS profile with `developmentClient: true` and `ios.simulator: true`. For Android, use a development APK. |
| React Native | A debug simulator `.app` for iOS or debug APK for Android. Metro provides the JavaScript bundle. |
| Flutter | A debug simulator `.app` or debug APK. Revyl rebuilds and reinstalls when code changes. |
| Native iOS | A Debug build for the `iphonesimulator` SDK. |
| Native Android | A debuggable APK, usually from `./gradlew assembleDebug`. |

You can upload from the dashboard in **Apps**, or from the CLI:

```bash
revyl init
revyl build
```

For framework-specific build commands, use the [build guides](../builds/index.md).

## 2. Initialize your project

Run this from the app directory, not the monorepo root unless the app itself lives there:

```bash
revyl init
```

`revyl init` creates `.revyl/config.yaml` and detects the local provider Revyl should use.

If detection picks the native project inside an Expo or React Native app, force the provider:

```bash
revyl init --provider expo
revyl init --provider react-native
```

Expo projects also need a custom URL scheme in the development client so Revyl can open the app on the remote simulator and point it at Metro. See the [Dev Setup Guide](../developer_loop/dev-setup.md) for monorepos, custom ports, schemes, and provider config.

## 3. Start the loop

You can start from the terminal or from a live session in the UI.

### Start from the terminal

```bash
revyl dev
```

Revyl starts the local dev server, creates a relay, installs your development build, and opens the cloud device session.

Use platform or build flags when needed:

```bash
revyl dev --platform ios
revyl dev --platform android
revyl dev --build-version-id <id>
```

### Start from the UI, then attach

If you already launched a session from the dashboard, attach your local dev loop to it:

```bash
revyl dev attach active --context checkout
revyl dev --context checkout
```

This is useful when a teammate or agent already has the right device state open and you want to connect local code to that session instead of starting over.

## 4. Iterate

For Expo and React Native, save a JavaScript or TypeScript file and wait for Metro to update the cloud device.

For Flutter, native iOS, and native Android, press **`r`** inside `revyl dev` to rebuild, upload, and reinstall on the same session.

Use the browser stream for visual checks, or drive the device from another terminal:

```bash
revyl device screenshot --out before.png
revyl device tap --target "Sign In button"
revyl device screenshot --out after.png
```

Work in small observe-act-verify loops: look at the current screen, take one action, then confirm the device did what you expected.

## 5. Handle login early

If the app needs authentication, decide how agents should reach a useful state before they start debugging.

Good options are:

- A stable test account with seeded data.
- A staging-only demo mode.
- A test-only auth bypass deep link.

For the deep link pattern, see [Auth Bypass Deep Links](../builds/auth-bypass-deeplinks.md).

## 6. Turn useful paths into tests

When the manual loop finds a flow worth keeping, create a regression test from the same setup:

```bash
revyl dev test create login-smoke --platform ios
revyl test run login-smoke
```

For the dashboard authoring path, see [Create AI-powered mobile tests](../tests/creating-tests.md).

## Troubleshooting

| Symptom | What to check |
|---------|---------------|
| iOS build will not install | Confirm it is a simulator `.app`, not an `.ipa`. |
| Android build will not install | Confirm the APK is debuggable and includes `x86_64`. |
| Expo opens but does not load local code | Check the URL scheme, Metro output, and dev client profile. |
| Revyl detects the wrong framework | Run from the app directory or re-run `revyl init --provider expo` / `react-native`. |
| Native changes do not appear | Press **`r`** in `revyl dev` to rebuild and reinstall. |

## Related

- [Dev Loop](../developer_loop/dev-loop.md)
- [Dev Setup](../developer_loop/dev-setup.md)
- [Build Guides](../builds/index.md)
- [Auth Bypass Deep Links](../builds/auth-bypass-deeplinks.md)
