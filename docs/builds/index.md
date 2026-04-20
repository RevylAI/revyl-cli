# Build Guides

Pick your framework to see the exact commands for building, uploading, and running your first test.

<CardGroup cols={2}>
  <Card title="Expo" icon="bolt" href="/builds/expo">
    Local build or EAS cloud. Two commands to test.
  </Card>
  <Card title="React Native (bare)" icon="react" href="/builds/react-native">
    xcodebuild or Gradle, then upload.
  </Card>
  <Card title="Flutter" icon="mobile" href="/builds/flutter">
    flutter build for iOS simulator or Android APK.
  </Card>
  <Card title="iOS Native (Swift)" icon="apple" href="/builds/ios-native">
    xcodebuild for simulator, or auto-detect from DerivedData.
  </Card>
  <Card title="Android Native" icon="android" href="/builds/android-native">
    Gradle assembleDebug, then upload.
  </Card>
  <Card title="Building in CI" icon="rotate" href="/builds/ci-builds">
    URL uploads, GitHub Action, EAS cloud patterns.
  </Card>
</CardGroup>

## Hot Reload vs Rebuild

| Framework | Dev Loop Model | When to Rebuild |
|-----------|---------------|-----------------|
| Expo | Hot reload (JS/TS changes are live) | Only when native dependencies change |
| React Native (bare) | Hot reload (JS/TS changes are live) | Only when native dependencies change |
| Flutter | Rebuild on every change | Every code change (Dart compiles into the binary) |
| Swift/iOS | Rebuild on every change | Every code change (binary is the app) |
| Android Native | Rebuild on every change | Every code change (binary is the app) |

For Expo and React Native, the uploaded build is a "dev client shell" -- your JS/TS code is served live from your local Metro server via a Revyl relay. You only need a new build when native modules, Podfile, or Gradle dependencies change.

For Flutter, Swift, and Android, the binary **is** the app. During `revyl dev`, press `[r]` to rebuild, upload, and reinstall without restarting the device session.
