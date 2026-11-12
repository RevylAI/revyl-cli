# React Native (bare) recipe

A React Native Debug build loads its JavaScript from Metro. `revyl dev`
provides Metro through its tunnel, so Debug builds suit a `development`
profile. Pull-request builds, tests, and proofs run without Metro, so give them
a Release build, which bundles the JavaScript:

```yaml
build:
  profiles:
    development:
      ios:
        app_id: <ios-app-uuid>
        setup_commands: [npm ci]
        build_commands:
          - cd ios && if [ ! -d Pods ]; then pod install; fi && xcodebuild -workspace MyApp.xcworkspace -scheme MyApp -configuration Debug -sdk iphonesimulator -destination 'generic/platform=iOS Simulator' -derivedDataPath build
        output_path: ios/build/Build/Products/Debug-iphonesimulator/MyApp.app
      android:
        app_id: <android-app-uuid>
        setup_commands: [npm ci]
        build_commands: [cd android && ./gradlew assembleDebug]
        output_path: android/app/build/outputs/apk/debug/app-debug.apk
    preview:
      ios:
        app_id: <ios-app-uuid>
        setup_commands: [npm ci]
        build_commands:
          - cd ios && if [ ! -d Pods ]; then pod install; fi && xcodebuild -workspace MyApp.xcworkspace -scheme MyApp -configuration Release -sdk iphonesimulator -destination 'generic/platform=iOS Simulator' -derivedDataPath build
        output_path: ios/build/Build/Products/Release-iphonesimulator/MyApp.app
      android:
        app_id: <android-app-uuid>
        setup_commands: [npm ci]
        build_commands: [cd android && ./gradlew assembleRelease]
        output_path: android/app/build/outputs/apk/release/app-release.apk
```

- Use the install command for the lockfile in the repository (`npm ci`,
  `yarn install --frozen-lockfile`, `pnpm install --frozen-lockfile`, or
  `bun install`).
- Environment variables that the JavaScript reads at build time (for example
  through `react-native-config`) go in the recipe's `env`, or in `secrets` when
  they are credentials.
- For a monorepo, set the commands relative to the directory that owns
  `.revyl/config.yaml`.
