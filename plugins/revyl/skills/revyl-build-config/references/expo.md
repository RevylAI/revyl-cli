# Expo recipes

`init` writes two profiles for an Expo app:

- `preview` runs `expo prebuild` when the native directory is missing, then
  Xcode or Gradle in Release. It makes a standalone simulator `.app` or
  installable APK with no Metro server and no Expo credentials. Use it for
  `revyl build --profile preview`, pull-request builds, tests, and proofs.
- `development` makes EAS dev-client builds locally
  (`npx --yes eas-cli build --platform ios --profile <eas-profile> --local --output build/app.tar.gz`).
  `revyl dev` uses it for hot reload.

A typical `preview` iOS recipe:

```yaml
build:
  profiles:
    preview:
      ios:
        app_id: <ios-app-uuid>
        setup_commands:
          - npm ci
          - if [ ! -d ios ]; then npx expo prebuild --platform ios; fi
        build_commands:
          - cd ios && if [ ! -d Pods ]; then pod install; fi && xcodebuild -workspace MyApp.xcworkspace -scheme MyApp -configuration Release -sdk iphonesimulator -destination 'generic/platform=iOS Simulator' -derivedDataPath build
        output_path: ios/build/Build/Products/Release-iphonesimulator/MyApp.app
      android:
        app_id: <android-app-uuid>
        setup_commands:
          - npm ci
          - if [ ! -d android ]; then npx expo prebuild --platform android; fi
        build_commands: [cd android && ./gradlew assembleRelease]
        output_path: android/app/build/outputs/apk/release/app-release.apk
```

- Check the Xcode scheme. Without an `ios/` directory it is the project name
  `expo prebuild` derives from an ASCII `expo.name`. For any other name, run
  `npx expo prebuild --platform ios`, then `init --detect` to read the real
  scheme.
- `EXPO_PUBLIC_*` values the app reads at build time go in the recipe's `env`.
- To use builds EAS already made instead of building on Revyl, upload the EAS
  artifact by URL with `build upload --url <artifact-url> --platform ios`; see
  the `revyl-build-ci-upload` skill. EAS iOS simulator builds (`.tar.gz`) are
  accepted as they are.
