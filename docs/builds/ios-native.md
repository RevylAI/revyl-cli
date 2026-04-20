# iOS Native (Swift/Xcode): Zero to Tested

Build a native iOS app, upload it to Revyl, and run your first test.

## Prerequisites

- Xcode installed
- A project with `.xcodeproj` or `.xcworkspace`
- Revyl CLI installed and authenticated (`revyl auth login`)

## Build and Upload

```bash
# 1. Build for simulator
xcodebuild \
  -project YourApp.xcodeproj \
  -scheme YourApp \
  -configuration Debug \
  -sdk iphonesimulator \
  -derivedDataPath build \
  -quiet

# 2. Zip and upload
cd build/Build/Products/Debug-iphonesimulator
zip -r ../../../../build/app.zip YourApp.app
cd ../../../../
revyl build upload --file build/app.zip --platform ios

# 3. Run a test
revyl test run login-smoke
```

For workspace-based projects, replace `-project` with `-workspace YourApp.xcworkspace`.

Builds must target `iphonesimulator` SDK. Revyl runs on cloud simulators, not physical devices.

## Auto-Detect from DerivedData

If you build in Xcode normally (Cmd+B), `revyl dev` automatically finds the most recent simulator `.app` from DerivedData:

```bash
# Build in Xcode as usual (Cmd+B), then:
revyl dev --platform ios
```

The CLI scans `~/Library/Developer/Xcode/DerivedData/` for the most recently modified `.app` matching your project. Test runner bundles (`*Tests.app`) are excluded automatically.

## When Do You Need a New Build?

Every code change requires a new build. The binary **is** the app.

During `revyl dev`, press `[r]` to rebuild, upload, and reinstall without restarting the device session. Typical incremental rebuild: ~20-60s.

## CI Integration

```yaml
jobs:
  test:
    runs-on: macos-latest
    steps:
      - uses: actions/checkout@v4

      - name: Build simulator app
        run: |
          xcodebuild -project YourApp.xcodeproj -scheme YourApp \
            -configuration Debug -sdk iphonesimulator \
            -derivedDataPath build -quiet
          cd build/Build/Products/Debug-iphonesimulator
          zip -r ../../../../build/app.zip YourApp.app

      - name: Upload and test
        env:
          REVYL_API_KEY: ${{ secrets.REVYL_API_KEY }}
        run: |
          pip install revyl
          revyl build upload --file build/app.zip --platform ios --yes
          revyl workflow run smoke-tests
```

GitHub Actions includes macOS runners on all paid plans.

## Next Steps

- [Dev Loop Setup](../developer_loop/dev-setup.md) -- configure the rebuild-based dev loop for Swift
- [CI Build Patterns](ci-builds.md) -- advanced CI workflows
