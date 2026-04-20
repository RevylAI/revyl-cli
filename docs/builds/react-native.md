# React Native (Bare): Zero to Tested

Build a bare React Native app (no Expo), upload it to Revyl, and run your first test.

## Prerequisites

- Xcode (for iOS) or Android SDK (for Android)
- React Native CLI project with `react-native` in `package.json`
- Revyl CLI installed and authenticated (`revyl auth login`)

## iOS

```bash
# 1. Build a simulator .app
cd ios && pod install && cd ..
xcodebuild \
  -workspace ios/YourApp.xcworkspace \
  -scheme YourApp \
  -configuration Debug \
  -sdk iphonesimulator \
  -derivedDataPath build/ios \
  -quiet

# 2. Zip and upload
cd build/ios/Build/Products/Debug-iphonesimulator
zip -r ../../../../../build/app.zip YourApp.app
cd ../../../../../
revyl build upload --file build/app.zip --platform ios

# 3. Run a test
revyl test run login-smoke
```

iOS builds must target `iphonesimulator`, not `iphoneos`. Revyl runs on cloud simulators.

## Android

```bash
# 1. Build a debug APK
cd android && ./gradlew assembleDebug && cd ..

# 2. Upload
revyl build upload --file android/app/build/outputs/apk/debug/app-debug.apk --platform android

# 3. Run a test
revyl test run login-smoke
```

## When Do You Need a New Build?

Like Expo, bare React Native serves JS/TS live via Metro. You only need a new build when:

- Native code changes (new native modules, Podfile/Gradle changes)
- Build configuration changes

For JS/TS changes, use `revyl dev` with hot reload.

## CI Integration

### iOS (GitHub Actions)

```yaml
jobs:
  test:
    runs-on: macos-latest
    steps:
      - uses: actions/checkout@v4

      - name: Setup and build
        run: |
          npm ci
          cd ios && pod install && cd ..
          xcodebuild -workspace ios/YourApp.xcworkspace -scheme YourApp \
            -configuration Debug -sdk iphonesimulator \
            -derivedDataPath build/ios -quiet
          cd build/ios/Build/Products/Debug-iphonesimulator
          zip -r ../../../../../build/app.zip YourApp.app

      - name: Upload and test
        env:
          REVYL_API_KEY: ${{ secrets.REVYL_API_KEY }}
        run: |
          pip install revyl
          revyl build upload --file build/app.zip --platform ios --yes
          revyl workflow run smoke-tests
```

### Android (GitHub Actions)

```yaml
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - name: Build APK
        run: |
          npm ci
          cd android && ./gradlew assembleDebug

      - name: Upload and test
        env:
          REVYL_API_KEY: ${{ secrets.REVYL_API_KEY }}
        run: |
          pip install revyl
          revyl build upload --file android/app/build/outputs/apk/debug/app-debug.apk --platform android --yes
          revyl workflow run smoke-tests
```

## Next Steps

- [Dev Loop Setup](../developer_loop/dev-setup.md) -- configure hot reload for bare RN
- [CI Build Patterns](ci-builds.md) -- advanced CI workflows
