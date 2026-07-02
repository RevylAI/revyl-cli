# Building in CI

Patterns for getting your app binary into Revyl from any CI system.

## Pattern 1: Upload from a URL (simplest)

If your build system produces a downloadable artifact URL (EAS, Bitrise, AppCenter, S3, GCS, GitHub Actions artifacts), skip the local download entirely:

```bash
revyl build upload --url https://your-ci.com/artifacts/app-latest.apk --app "My Android App"
```

The Revyl backend downloads, validates, and stores the artifact server-side. Your CI runner never touches the binary.

For authenticated URLs:

```bash
revyl build upload \
  --url https://artifacts.internal.company.com/builds/app.ipa \
  --header "Authorization: Bearer $ARTIFACT_TOKEN" \
  --app "My iOS App"
```

## Pattern 2: Revyl CLI in GitHub Actions

Use the CLI after your workflow builds the app. In GitHub Actions, pass the PR
head SHA as the build version so Revyl can match the artifact back to the pull
request.

```yaml
- name: Install Revyl CLI
  run: curl -fsSL https://revyl.com/install.sh | sh

- name: Upload Build
  id: upload
  run: |
    revyl build upload \
      --file ./build/app.apk \
      --app "$REVYL_ANDROID_APP_ID" \
      --platform android \
      --version "$REVYL_PR_HEAD_SHA" \
      --yes \
      --json
  env:
    REVYL_API_KEY: ${{ secrets.REVYL_API_KEY }}
    REVYL_ANDROID_APP_ID: ${{ vars.REVYL_ANDROID_APP_ID }}
    REVYL_PR_HEAD_SHA: ${{ github.event.pull_request.head.sha || github.sha }}

- name: Run Tests
  run: revyl workflow run smoke-tests --android-app "$REVYL_ANDROID_APP_ID"
  env:
    REVYL_API_KEY: ${{ secrets.REVYL_API_KEY }}
    REVYL_ANDROID_APP_ID: ${{ vars.REVYL_ANDROID_APP_ID }}
```

## Pattern 3: Build on CI, Upload File

Build on the runner and upload the artifact directly:

```bash
curl -fsSL https://revyl.com/install.sh | sh
export PATH="$HOME/.revyl/bin:$PATH"
revyl build upload --file ./build/app.apk --platform android --yes
revyl workflow run smoke-tests
```

### iOS on macOS runners

```yaml
jobs:
  test:
    runs-on: macos-latest
    steps:
      - uses: actions/checkout@v4
      - uses: oven-sh/setup-bun@v2
      - run: |
          bun install
          bunx expo prebuild --platform ios
          cd ios && pod install
          cd ios && xcodebuild -workspace YourApp.xcworkspace -scheme YourApp -configuration Release -sdk iphonesimulator -destination 'generic/platform=iOS Simulator' -derivedDataPath build ARCHS=arm64
      - env:
          REVYL_API_KEY: ${{ secrets.REVYL_API_KEY }}
        run: |
          curl -fsSL https://revyl.com/install.sh | sh
          export PATH="$HOME/.revyl/bin:$PATH"
          revyl build upload --file ios/build/Build/Products/Release-iphonesimulator/*.app --platform ios --yes
          revyl workflow run smoke-tests
```

GitHub Actions includes macOS runners on all paid plans.

### Android on Linux runners

```yaml
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: oven-sh/setup-bun@v2
      - run: |
          bun install
          bunx expo prebuild --platform android
          cd android && ./gradlew assembleRelease
      - env:
          REVYL_API_KEY: ${{ secrets.REVYL_API_KEY }}
        run: |
          curl -fsSL https://revyl.com/install.sh | sh
          export PATH="$HOME/.revyl/bin:$PATH"
          revyl build upload --file android/app/build/outputs/apk/release/*.apk --platform android --yes
          revyl workflow run smoke-tests
```

## Pattern 4: EAS Cloud + URL Upload (no Mac, no local build)

For Expo teams, delegate the build to EAS and just ingest the result:

```bash
ARTIFACT_URL=$(npx eas-cli build --profile development --platform ios --non-interactive --json | jq -r '.[0].artifacts.buildUrl')
revyl build upload --url "$ARTIFACT_URL" --app "My iOS App"
```

Zero Mac needed on the customer side. Expo's cloud does the build, Revyl just ingests the artifact.

## Pattern 5: revyl build (config-driven)

If `.revyl/config.yaml` has build commands configured, the CLI can build and upload in one step:

```bash
revyl build --platform ios --json
```

This runs `build.platforms.ios.commands` in order when configured, otherwise `build.platforms.ios.command`, then uploads the output artifact.

## CI-Friendly Flags

| Flag | Effect |
|------|--------|
| `--json` | Machine-readable JSON output |
| `--no-wait` | Queue the run and exit without waiting for results |
| `--quiet` / `-q` | Suppress non-essential output |
| `--version <string>` | Tag the build with a version (default: `<branch>-<timestamp>`) |

## Next Steps

- [Expo Build Guide](expo.md) -- Expo-specific build and upload
- [CI/CD Integration](../ci-cd.md) -- full CI pipeline setup
