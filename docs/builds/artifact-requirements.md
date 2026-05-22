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

Two requirements:

1. **APK must include `x86_64` in its ABI set.** Revyl's cloud emulators are `x86_64`; `arm64-v8a`-only APKs won't install.
2. **APK must be debuggable** (`android:debuggable="true"` in the merged manifest).

### Why x86_64

Cloud emulators run a Google Play `x86_64` system image. Install fails immediately if the APK has no `x86_64` native libs. Default debug builds from every framework include all ABIs in one fat APK, so this only bites if you've narrowed `abiFilters` or `android.ndkAbiFilters`.

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

### What about per-ABI APK splits?

Fine as long as the APK you upload to Revyl contains `x86_64`. Universal / fat APKs are simplest. If you're uploading a single-ABI APK, it must be the `x86_64` variant.

## FAQ

**Can I use a physical-device archive (`.ipa` / non-debuggable release APK)?**
No. `.ipa` won't install on simulators. Release APKs will install but lose the State tab and other debugging-dependent paths. Use a debug build for Revyl.

**Why does Revyl insist on the simulator slice / x86_64?**
Cloud test infra runs on shared hosts. Simulators (iOS) and emulators (Android) give us isolation and snapshot-restore. Physical-device farms exist but aren't what Revyl is.

**What about Apple Silicon `arm64` simulators?**
Xcode produces a fat simulator `.app` (`x86_64` + `arm64`) by default. Both slices are accepted; Revyl picks the right one at install time.

**My CI only produces release/production builds. What now?**
Add a parallel debug job for Revyl, or accept that release-build runs lose State-tab features.

**My build uses `abiFilters` for size reasons. Help.**
Add `x86_64` to the filter set, or produce a separate Revyl-targeted variant that includes it. The full ABI set is only ~10–20 MB larger.

## Related

- [Build Guides](index.md) — framework-specific build commands
- [Dev Loop](../developer_loop/dev-loop.md) — `revyl dev` rebuild model
- [Running Tests](../tests/running-tests.md) — how artifacts are resolved at test-run time
