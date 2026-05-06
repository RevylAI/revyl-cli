# Flutter: Zero to Tested

Build a Flutter app, upload it to Revyl, and run your first test.

## Prerequisites

- Flutter SDK installed (`flutter doctor` passes)
- Xcode (for iOS) or Android SDK (for Android)
- Revyl CLI installed and authenticated (`revyl auth login`)

## iOS

```bash
# 1. Build a simulator app
flutter build ios --simulator --debug

# 2. Zip and upload
cd build/ios/iphonesimulator
zip -r ../../../build/app.zip Runner.app
cd ../../../
revyl build upload --file build/app.zip --platform ios

# 3. Run a test
revyl test run login-smoke
```

iOS builds must target the simulator (`--simulator`). Revyl runs on cloud simulators, not physical devices.

## Android

```bash
# 1. Build a debug APK
flutter build apk --debug

# 2. Upload
revyl build upload --file build/app/outputs/flutter-apk/app-debug.apk --platform android

# 3. Run a test
revyl test run login-smoke
```

## When Do You Need a New Build?

Flutter compiles all Dart code into the native binary. Every code change requires a new build. There is no hot reload equivalent for cloud testing -- the binary **is** the app.

During `revyl dev`, press `[r]` to rebuild, upload, and reinstall without restarting the device session.

## CI Integration

### iOS (GitHub Actions)

```yaml
jobs:
  test:
    runs-on: macos-latest
    steps:
      - uses: actions/checkout@v4

      - name: Setup Flutter
        uses: subosito/flutter-action@v2
        with:
          flutter-version: '3.x'

      - name: Build and upload
        env:
          REVYL_API_KEY: ${{ secrets.REVYL_API_KEY }}
        run: |
          flutter build ios --simulator --debug
          cd build/ios/iphonesimulator && zip -r ../../../build/app.zip Runner.app && cd ../../../
          curl -fsSL https://revyl.com/install.sh | sh
          export PATH="$HOME/.revyl/bin:$PATH"
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

      - name: Setup Flutter
        uses: subosito/flutter-action@v2
        with:
          flutter-version: '3.x'

      - name: Build and upload
        env:
          REVYL_API_KEY: ${{ secrets.REVYL_API_KEY }}
        run: |
          flutter build apk --debug
          curl -fsSL https://revyl.com/install.sh | sh
          export PATH="$HOME/.revyl/bin:$PATH"
          revyl build upload --file build/app/outputs/flutter-apk/app-debug.apk --platform android --yes
          revyl workflow run smoke-tests
```

## Next Steps

- [Dev Loop Setup](../developer_loop/dev-setup.md) -- configure the rebuild-based dev loop for Flutter
- [CI Build Patterns](ci-builds.md) -- advanced CI workflows
