# Build Artifact Requirements

What your `.app` or `.apk` needs to look like before it can run on Revyl. Read this once — every framework guide links here for the *why*.

Revyl runs tests on cloud **simulators (iOS)** and **emulators (Android)**, not physical devices. That single fact drives every requirement on this page.

## iOS

| Required | Recommended |
|----------|-------------|
| Simulator-slice `.app` (or zipped `.app`). Built against the `iphonesimulator` SDK, not `iphoneos`. | Debug configuration. |

### Why

Cloud simulators run a different CPU slice than physical iPhones. An `.ipa` produced for device distribution will not install on a simulator — `simctl install` rejects it with an arch mismatch. You need the `.app` bundle that Xcode/EAS produces when targeting the simulator.

### How to satisfy it

| Framework | Knob |
|-----------|------|
| Xcode / Swift | `xcodebuild -sdk iphonesimulator -configuration Debug` |
| Expo (EAS) | `ios.simulator: true` on your build profile in `eas.json` |
| React Native (bare) | `xcodebuild -sdk iphonesimulator` (Pods + workspace) |
| Flutter | `flutter build ios --simulator --debug` |

### What about `.ipa`?

Not supported. If your CI produces an `.ipa`, you need a parallel simulator-slice job to upload to Revyl.

## Android

Three requirements:

1. **Upload one installable `.apk` artifact.** `.aab`, `.apks`, APK Set archives, and split APK archives are not supported.
2. **Native libraries must include a supported 64-bit ABI.** Revyl accepts `x86_64` and `arm64-v8a`. APKs without native libraries are also supported.
3. **APK must be debuggable** (`android:debuggable="true"` in the merged manifest).

### Why a supported 64-bit ABI

Cloud emulators run `x86_64` Android images with Android's NDK translation layer enabled for `arm64-v8a` native code. APKs with either 64-bit ABI can run; APKs whose native libraries are only `x86` or `armeabi-v7a` cannot. Default debug builds from every framework usually include a compatible ABI, so this mainly matters if you've narrowed `abiFilters` or `android.ndkAbiFilters`.

### Why debuggable

Debuggable builds (`android:debuggable="true"` in the merged manifest) are what allow Revyl to walk the app sandbox via `run-as <pkg>` — the platform only permits that against debuggable apps on non-rooted (Play-image) emulators. That sandbox access powers the **State tab**: SharedPreferences, Jetpack DataStore, and your app's SQLite databases. It also keeps a number of test-execution paths fast and reliable.

The runtime *will* install a non-debuggable release APK — tests still execute, video records, logs flow. You lose the State tab, and a class of debugging-dependent features. Don't ship a release APK to Revyl by default; produce a debuggable variant for testing alongside whatever you ship to users.

### How to satisfy it

| Framework | Knob |
|-----------|------|
| Gradle / Kotlin / Java | `./gradlew assembleDebug` — debuggable + all ABIs by default |
| React Native (bare) | `cd android && ./gradlew assembleDebug` |
| Flutter | `flutter build apk --debug` |
| Expo (EAS) | The `development` profile (`developmentClient: true`, `android.buildType: "apk"`) is debuggable and ships all ABIs |

### What about release / production builds?

Don't upload them. Tests will run, but you lose the State tab (SharedPreferences / DataStore / SQLite inspection) and any debugging-dependent execution paths. Produce a debug-flavored variant for Revyl alongside your release pipeline — it's a one-line Gradle task in most projects.

### What about per-ABI APKs?

Upload one standalone APK. Universal / fat APKs are simplest, but single-ABI `x86_64` and `arm64-v8a` APKs are both supported. Do not upload `.apks` files or ZIPs containing multiple split APKs; Revyl does not install APK sets.

### Upload API validation

The Revyl web UI and CLI inspect uploaded artifacts before finalizing the build. Direct API integrations that upload with the presigned upload URL and then call `complete-upload` without first calling `extract-package-id` are still supported; `complete-upload` validates the uploaded artifact server-side before saving the build. For large mobile artifacts, that fallback can add one S3 download to the finalize request, so API clients that need lower finalize latency should call `extract-package-id` after the upload completes.

## FAQ

**Can I use a physical-device archive (`.ipa` / non-debuggable release APK)?**
No. `.ipa` won't install on simulators. Release APKs will install but lose the State tab and other debugging-dependent paths. Use a debug build for Revyl.

**Why does Revyl insist on a simulator slice / supported Android ABI?**
Cloud test infra runs on shared hosts. Simulators (iOS) and emulators (Android) give us isolation and snapshot-restore. Physical-device farms exist but aren't what Revyl is.

**What about Apple Silicon `arm64` simulators?**
Xcode produces a fat simulator `.app` (`x86_64` + `arm64`) by default. Both slices are accepted; Revyl picks the right one at install time.

**My CI only produces release/production builds. What now?**
Add a parallel debug job for Revyl, or accept that release-build runs lose State-tab features.

**My build uses `abiFilters` for size reasons. Help.**
Include `x86_64` or `arm64-v8a`, or produce a separate Revyl-targeted variant with at least one of them. You do not need both ABIs in the same APK.

## Related

- [Build Guides](index.md) — framework-specific build commands
- [Dev Loop](../developer_loop/dev-loop.md) — `revyl dev` rebuild model
- [Running Tests](../tests/running-tests.md) — how artifacts are resolved at test-run time
