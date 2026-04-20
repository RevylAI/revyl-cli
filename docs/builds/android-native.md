# Android Native (Kotlin/Java): Zero to Tested

Build a native Android app, upload it to Revyl, and run your first test.

## Prerequisites

- Android SDK installed
- A project with `build.gradle` or `build.gradle.kts`
- Revyl CLI installed and authenticated (`revyl auth login`)

## Build and Upload

```bash
# 1. Build a debug APK
./gradlew assembleDebug

# 2. Upload
revyl build upload --file app/build/outputs/apk/debug/app-debug.apk --platform android

# 3. Run a test
revyl test run login-smoke
```

## When Do You Need a New Build?

Every code change requires a new build. The APK **is** the app.

During `revyl dev`, press `[r]` to rebuild, upload, and reinstall without restarting the device session. Typical incremental rebuild: ~30-90s (first build takes longer).

Android reinstalls preserve app data (the `-r` flag is used).

## CI Integration

```yaml
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - name: Setup Java
        uses: actions/setup-java@v4
        with:
          distribution: temurin
          java-version: 17

      - name: Build APK
        run: ./gradlew assembleDebug

      - name: Upload and test
        env:
          REVYL_API_KEY: ${{ secrets.REVYL_API_KEY }}
        run: |
          pip install revyl
          revyl build upload --file app/build/outputs/apk/debug/app-debug.apk --platform android --yes
          revyl workflow run smoke-tests
```

No macOS runner needed. Android builds run on `ubuntu-latest`.

## Next Steps

- [Dev Loop Setup](../developer_loop/dev-setup.md) -- configure the rebuild-based dev loop for Android
- [CI Build Patterns](ci-builds.md) -- advanced CI workflows
