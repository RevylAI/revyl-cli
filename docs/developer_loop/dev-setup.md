# Dev Setup Guide

This guide covers hot reload and rebuild-loop configuration for `revyl dev`. For build and upload instructions, see [Build Guides](../builds/).

| Framework | Hot Reload | Rebuild Dev Loop | Provider Name |
|-----------|-----------|-----------------|---------------|
| Expo | Fully supported | `[r]` for native changes | `expo` |
| React Native (bare) | Fully supported | `[r]` for native changes | `react-native` |
| Flutter | — | `[r]` rebuild + reinstall | — |
| Swift/iOS | — | `[r]` rebuild + reinstall | `swift` |
| Android Native | — | `[r]` rebuild + reinstall | `android` |
| Kotlin Multiplatform | — | `[r]` rebuild + reinstall | — |
| Bazel | — | `[r]` rebuild + reinstall | — |

---

## Expo

Expo projects use full hot reload via a Revyl relay to your local Metro server. JS/TS changes appear on the device within seconds. Press `[r]` only when native dependencies change.

For build and upload steps, see [Expo Build Guide](../builds/expo.md).

### How detection works

The CLI looks for two things in the current directory:

1. `"expo"` in `package.json` dependencies or devDependencies
2. At least one project indicator: `app.json`, `app.config.js`, `app.config.ts`, `eas.json`, or `.expo/` directory

If both are present, the Expo provider matches at **confidence 0.9** — the highest of any provider. This means Expo wins over Swift (0.7) and Android (0.6) when all three detect the same directory.

If either condition fails (common in monorepos), the Expo provider returns nil and lower-confidence providers may match instead. See the monorepo section below.

### URL schemes

Hot reload deep-links the dev client to your local Metro server via a custom URL scheme:

```
myapp://expo-development-client/?url=https://hr-abc123.revyl.ai
```

This requires a **custom URL scheme** (`myapp://`) registered in the dev client binary.

#### Where to find your scheme

Check `app.json` under `expo.scheme`:

```json
{
  "expo": {
    "scheme": "myapp"
  }
}
```

Or in `app.config.js` / `app.config.ts`:

```javascript
export default {
  expo: {
    scheme: "myapp",
  }
};
```

The CLI reads this value during `revyl init` and stores it as `hotreload.providers.expo.app_scheme` in `.revyl/config.yaml`.

#### Why universal links don't work

Apps that only use universal links (`https://example.com/...`) for deep linking cannot use those for hot reload. Apple's associated domains system requires a live HTTPS domain serving an `apple-app-site-association` file. Revyl hot reload uses short-lived relay hosts that are not suitable as stable associated domains. Custom URL schemes (`myapp://`) bypass this entirely — no server verification needed.

#### No URL scheme in the app

If your app has no `expo.scheme` (common in apps that only use universal links), you need to add one. The name is arbitrary and only affects the dev client:

```json
{
  "expo": {
    "scheme": "myapp-dev"
  }
}
```

After adding the scheme, **rebuild the dev client** — the scheme is baked into the native binary at build time:

```bash
revyl build upload --platform ios-dev
```

Then set `app_scheme: myapp-dev` in `.revyl/config.yaml`.

#### Checking existing schemes without rebuilding

The dev client may already have a scheme registered by `expo-dev-client`. Check the iOS build:

```bash
grep -r "CFBundleURLSchemes" ios/ --include="*.plist" -A3
```

If this returns something like `exp+myslug`, you can use that scheme in your config without rebuilding.

#### The `use_exp_prefix` option

Expo dev clients can register URL schemes in two formats:

- **Base scheme**: `myapp://` — registered when `expo.scheme` is set in app.json
- **Prefixed scheme**: `exp+myapp://` — auto-registered by `expo-dev-client` based on the app slug

The CLI defaults to the base scheme. If deep links fail with "No application is registered to handle this URL scheme", the binary may only have the prefixed variant. Toggle `use_exp_prefix` in your config:

```yaml
hotreload:
  providers:
    expo:
      app_scheme: myapp
      use_exp_prefix: true
```

**Why isn't this automatic?** The CLI runs on your local machine while the dev client runs on a remote cloud simulator. The CLI cannot introspect which URL schemes the installed binary has registered. The `expo.scheme` field in `app.json` defines the base scheme, but whether `expo-dev-client` also registers `exp+<scheme>://` depends on the Expo SDK version and the `addGeneratedScheme` setting at build time. Since there's no way to query this at runtime, the config toggle is the escape hatch.

### Monorepo setup

In monorepos (Turborepo, Nx, pnpm workspaces), the Expo app typically lives in a subdirectory like `apps/native/`, `apps/mobile/`, or `packages/app/`.

#### Run from the Expo app directory

All Revyl commands must run from the directory containing the Expo app's `package.json` — **not the monorepo root**.

```bash
cd apps/native
revyl init --provider expo
revyl dev --platform ios
```

The CLI resolves the working directory by calling `FindRepoRoot`, which walks up from the current directory looking for `.revyl/`. If the monorepo root also has a `.revyl/` directory (common for CI test configs), make sure you're in the correct subdirectory.

#### Why detection fails in monorepos

Two things go wrong:

1. **Hoisted dependencies** — Package managers like pnpm, Yarn, and npm may hoist `expo` to the root `node_modules/` or use `workspace:*` protocol in the local `package.json`. The Expo provider reads the local `package.json` and checks for `"expo"` in `dependencies` or `devDependencies`. If it's not there, the provider returns nil (no match).

2. **Native directories match other providers** — Every Expo project with prebuild has `ios/` containing `.xcodeproj` files (triggering the Swift provider at confidence 0.7) and `android/` containing `build.gradle` (triggering the Android provider at confidence 0.6). When the Expo provider returns nil, these become the top matches.

The result: `revyl init` detects "Swift/iOS (coming soon)" and "Android (coming soon)" instead of Expo.

**Fix**: Use `--provider expo` to bypass auto-detection:

```bash
revyl init --provider expo
```

Or, if `expo` is genuinely missing from the local `package.json`, add it:

```bash
npx expo install expo
```

#### Example hotreload config for a monorepo app

```yaml
# apps/native/.revyl/config.yaml
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

### What to expect when the deep link opens

After `revyl dev` starts, the CLI opens a deep link to connect the dev client to your local Metro server. You may notice:

1. **A confirmation dialog** -- iOS shows "Open in [Your App]?" with Cancel and Open buttons. This is a standard iOS security prompt for URL scheme handoffs. Tap **Open** to proceed. (On cloud simulators this currently requires a manual tap; future CLI versions will auto-accept it.)

2. **The app briefly restarts** -- The dev client may close and reopen as it disconnects from its cached state and reconnects to the tunnel URL. This looks jarring but is normal Expo dev client behavior. The app is reloading to fetch the JS bundle from your local Metro server instead of its built-in bundle.

3. **Sometimes no restart at all** -- If the dev client is already in a fresh state (first launch after install), it may connect seamlessly without closing. The behavior varies by Expo SDK version and whether the app was previously running.

Once the app is back up and showing your app's UI, hot reload is active. Edit a file locally, save, and the change appears on the device within seconds.

If the app closes and **doesn't come back**, check the Metro/Expo terminal output for JavaScript errors. A crash during bundle loading usually means a missing native module or environment variable issue -- not a Revyl problem.

### Dynamic config (app.config.js / app.config.ts)

The CLI reads the URL scheme from `app.json` at `expo.scheme`. If the project uses `app.config.js` or `app.config.ts` instead, the CLI cannot auto-detect the scheme because evaluating arbitrary JavaScript is out of scope.

The scheme is the custom URL prefix baked into the Expo dev client, such as
`myapp-dev://`. Revyl uses it to open the installed dev client on the cloud
device and connect it to Metro for hot reload.

Provide the scheme explicitly during init:

```bash
revyl init --provider expo --hotreload-app-scheme myapp
```

Or set it manually in `.revyl/config.yaml` after init:

```yaml
hotreload:
  providers:
    expo:
      app_scheme: myapp
```

If you add or change the scheme, rebuild the Expo dev client once. After that,
JS/TS changes can hot reload through `revyl dev` without another build.

### Custom dev server port

If Metro runs on a non-default port (common in monorepos with multiple Metro instances), set it in config or as a flag:

```yaml
hotreload:
  providers:
    expo:
      port: 8082
```

```bash
revyl dev --platform ios --port 8082
```

---

## React Native (bare)

Bare React Native projects use full hot reload via Metro, similar to Expo but without deep links. JS/TS changes appear on the device within seconds. Press `[r]` only when native dependencies change.

For build and upload steps, see [React Native Build Guide](../builds/react-native.md).

### How detection works

The CLI looks for `react-native` in `package.json` dependencies **without** `expo` also being present. If both `react-native` and `expo` exist, the Expo provider takes precedence.

Detection confidence is **0.8** — higher than Swift (0.7) and Android (0.6), so bare RN projects are correctly identified even when `ios/` and `android/` directories are present.

Additional indicators that boost detection: `metro.config.js`, `metro.config.ts`, `react-native.config.js`, `react-native.config.ts`.

### No app_scheme needed

Unlike Expo, bare React Native hot reload does not use deep links. The dev client loads the JavaScript bundle directly from the tunnel URL. No `app_scheme` or `use_exp_prefix` configuration is needed.

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

### Monorepo considerations

The same directory rules apply as Expo:

- Run all commands from the React Native app directory, not the monorepo root
- If `react-native` is hoisted, use `--provider react-native` to force detection
- Watch for multiple `.revyl/` directories in the monorepo

---

## Flutter

Flutter uses a rebuild-based dev loop (no hot reload). The CLI detects `pubspec.yaml` and configures build commands for both platforms. During `revyl dev`, press `[r]` to rebuild, upload, and reinstall on the cloud device. Typical rebuild cycle: ~30-60s.

For build and upload steps, see [Flutter Build Guide](../builds/flutter.md).

---

## Swift/iOS

Native iOS uses a rebuild-based dev loop. The CLI detects `.xcodeproj` / `.xcworkspace` and configures `xcodebuild`. During `revyl dev`, press `[r]` to rebuild and reinstall. Typical rebuild cycle: ~20-60s.

If your project is incorrectly detected as Swift when it's actually Expo or React Native (common in monorepos), use `--provider expo` or `--provider react-native` to override.

For build and upload steps, see [iOS Build Guide](../builds/ios-native.md).

---

## Android Native

Native Android uses a rebuild-based dev loop. The CLI detects `build.gradle` / `build.gradle.kts` and configures `./gradlew assembleDebug`. During `revyl dev`, press `[r]` to rebuild and reinstall. Typical rebuild cycle: ~30-90s. Reinstalls preserve app data.

If your project is incorrectly detected as Android Native when it's actually Expo or React Native, use `--provider expo` or `--provider react-native` to override.

For build and upload steps, see [Android Build Guide](../builds/android-native.md).

---

## Kotlin Multiplatform (KMP)

KMP projects share business logic in Kotlin across iOS and Android while building native binaries for each platform. Revyl detects the KMP layout and routes you into the rebuild-based dev loop using native iOS and Android build commands underneath.

The CLI detects KMP projects by looking for a `shared` module alongside native shell directories (`iosApp`, `androidApp`, or `composeApp`) and KMP-specific Gradle markers. It generates `build.platforms.ios` and `build.platforms.android` entries using the underlying native build systems (Xcode for iOS, Gradle for Android).

KMP does not have its own hot reload runtime. The dev loop uses the same rebuild model as native iOS/Android: press `[r]` to rebuild the full binary including shared Kotlin code, upload it, and reinstall.

### What `revyl init` shows

When KMP is detected, the CLI explains the mapping:

```
Detected: Kotlin Multiplatform
  Shared module: shared/
  Platforms: ios, android
  Note: Shared KMP logic compiles into native iOS/Android binaries.
        The dev loop uses native build commands underneath.
```

### Manual configuration

If auto-detection does not trigger (e.g. a non-standard project layout), configure the native builds manually:

```yaml
build:
  system: Kotlin Multiplatform
  platforms:
    ios:
      command: "cd iosApp && xcodebuild -workspace iosApp.xcworkspace -scheme iosApp -configuration Debug -sdk iphonesimulator -derivedDataPath build"
      output: "iosApp/build/Build/Products/Debug-iphonesimulator/*.app"
    android:
      command: "cd androidApp && ./gradlew assembleDebug"
      output: "androidApp/build/outputs/apk/debug/androidApp-debug.apk"
```

---

## Bazel

Bazel-based mobile projects use a rebuild-based dev loop. Revyl detects Bazel workspaces by looking for `MODULE.bazel`, `WORKSPACE.bazel`, or `WORKSPACE` files. Because Bazel build targets and artifact paths vary per project, `revyl init` creates placeholder platform entries that you fill in with your specific build targets.

### What `revyl init` shows

```
Detected: Bazel
  Workspace: MODULE.bazel
  Note: Revyl detected a Bazel workspace but cannot infer build targets automatically.
        Configure build.platforms.ios and/or build.platforms.android in .revyl/config.yaml.
```

### Example configuration

```yaml
build:
  system: Bazel
  platforms:
    ios:
      command: "bazel build //ios:MyApp -c dbg"
      output: "bazel-bin/ios/MyApp.app"
    android:
      command: "bazel build //android:app -c dbg"
      output: "bazel-bin/android/app.apk"
```

---

## Troubleshooting

| Symptom | Cause | Fix |
|---------|-------|-----|
| "Detected Swift/iOS instead of Expo" | Monorepo: `ios/` triggers Swift provider; `expo` not in local `package.json` | `revyl init --provider expo` |
| "Detected Android instead of Expo" | Monorepo: `android/` triggers Android provider | `revyl init --provider expo` |
| "No providers detected" | Missing `package.json`, wrong directory, or no framework indicators | `cd` to the app directory; verify `package.json` has `expo` or `react-native` |
| "App scheme empty" | Using `app.config.js` instead of `app.json` | `--hotreload-app-scheme myapp` or edit config |
| "Hot reload is not configured" | `hotreload:` section missing from config | Re-run `revyl init --provider expo` or add manually |
| "Port 8081 is already in use" | Another Metro instance running | `lsof -ti:8081 \| xargs kill` or `--port 8082` |
| "No application is registered to handle this URL scheme" | Dev client doesn't have the scheme registered | Toggle `use_exp_prefix: true/false` in config; if neither works, add `scheme` to app.json and rebuild |
| No URL scheme in the app | App only uses universal links | Add `"scheme": "myapp-dev"` to app.json, rebuild dev client |
| "Build platform 'ios' not found" | `build.platforms.ios` missing from config | Run `revyl init --force` to re-detect, or add manually |
| Deep link fails with `LSApplicationWorkspaceErrorDomain error 115` | The URL scheme in the deep link doesn't match any installed app | Same as "No application is registered" above |
| App closes briefly after tapping "Open" in the dialog | Normal: dev client is reloading to connect to Metro via tunnel | Wait for it to reopen; if it doesn't, check Metro output for JS errors |
| "Open in [App Name]?" confirmation dialog | iOS system prompt for URL scheme handoffs | Tap "Open" to proceed; this is expected behavior |

---

## Older CLI versions (v0.1.11 and earlier)

If `--provider` is not recognized, add the hotreload config manually:

1. Run `revyl init` (without `--provider`)
2. Open `.revyl/config.yaml`
3. Add the `hotreload:` section (see the Expo or React Native examples above)
4. Set `app_scheme` to the value from `app.json` > `expo.scheme`
5. Run `revyl dev --platform ios`

---

## What's next

- [Dev Loop workflow](dev-loop.md) — running tests in dev mode
- [Agent Prompt Pack](../integrations/skills.md) — copy-paste prompt templates for coding agents
