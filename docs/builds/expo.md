# Expo: Zero to Tested

Build an Expo dev client, upload it to Revyl, and run your first test.

## Prerequisites

- Node.js 18+
- Expo CLI (`npx expo`)
- EAS CLI (`npx eas-cli`) for local or cloud builds
- Revyl CLI installed and authenticated (`revyl auth login`)

## Option A: Local Build (recommended for first run)

Build on your machine and upload the artifact directly.

### iOS

```bash
# 1. Build a simulator dev client locally
npx eas-cli build --profile development --platform ios --local --output build/app.tar.gz

# 2. Upload to Revyl
revyl build upload --file build/app.tar.gz --platform ios

# 3. Run a test
revyl test run login-smoke
```

iOS builds must target the **simulator** (`ios.simulator: true` in `eas.json`). Revyl runs on cloud simulators, not physical devices.

### Android

```bash
# 1. Build a debug APK locally
npx eas-cli build --profile development --platform android --local --output build/app.apk

# 2. Upload to Revyl
revyl build upload --file build/app.apk --platform android

# 3. Run a test
revyl test run login-smoke
```

## Option B: EAS Cloud Build (no Mac needed)

Let Expo's cloud infrastructure handle the build. You just ingest the artifact URL.

```bash
# 1. Build on EAS cloud
npx eas-cli build --profile development --platform ios

# 2. Copy the artifact URL from EAS output, then upload to Revyl
revyl build upload --url https://expo.dev/artifacts/eas/... --app "My iOS App"

# 3. Run a test
revyl test run login-smoke
```

This works for both iOS and Android. EAS cloud builds don't require a Mac on your side.

## eas.json Setup

Your `eas.json` needs a `development` profile configured for Revyl:

```json
{
  "build": {
    "development": {
      "developmentClient": true,
      "distribution": "internal",
      "ios": {
        "simulator": true
      },
      "android": {
        "buildType": "apk"
      }
    }
  }
}
```

Key settings:

- `ios.simulator: true` -- produces a `.app` bundle (not `.ipa`) that runs on Revyl's cloud simulators
- `android.buildType: "apk"` -- produces a directly installable APK

## When Do You Need a New Build?

Expo dev clients serve your JS/TS code live from Metro via a Revyl relay. The binary is just a "dev client shell." You only need a new build when:

- Native dependencies change (new native modules, Podfile/Gradle changes)
- `app.json` native config changes (scheme, permissions, splash screen)
- Expo SDK version changes

For JS/TS changes, just use `revyl dev` -- hot reload handles everything.

## CI Integration

### GitHub Actions (local build on macOS runner)

```yaml
name: Build and Test

on:
  pull_request:
    branches: [main]

jobs:
  test:
    runs-on: macos-latest
    steps:
      - uses: actions/checkout@v4

      - name: Setup Node
        uses: actions/setup-node@v4
        with:
          node-version: 20

      - name: Install dependencies
        run: npm ci

      - name: Build iOS dev client
        run: npx eas-cli build --profile development --platform ios --local --output build/app.tar.gz --non-interactive

      - name: Upload and test
        env:
          REVYL_API_KEY: ${{ secrets.REVYL_API_KEY }}
        run: |
          pip install revyl
          revyl build upload --file build/app.tar.gz --platform ios --yes
          revyl workflow run smoke-tests
```

### GitHub Actions (EAS cloud build, no Mac needed)

```yaml
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - name: Build on EAS and upload to Revyl
        env:
          EXPO_TOKEN: ${{ secrets.EXPO_TOKEN }}
          REVYL_API_KEY: ${{ secrets.REVYL_API_KEY }}
        run: |
          npm ci
          ARTIFACT_URL=$(npx eas-cli build --profile development --platform ios --non-interactive --json | jq -r '.[0].artifacts.buildUrl')
          pip install revyl
          revyl build upload --url "$ARTIFACT_URL" --app "My iOS App"
          revyl workflow run smoke-tests
```

## Next Steps

- [Dev Loop Setup](../developer_loop/dev-setup.md) -- configure hot reload for Expo
- [Expo Dashboard Integration](../integrations/expo-dashboard.md) -- auto-import builds from EAS
- [CI Build Patterns](ci-builds.md) -- advanced CI workflows
