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

## Pattern 2: Revyl GitHub Action

The official GitHub Action handles upload and test in one step:

```yaml
- name: Upload Build
  id: upload
  uses: RevylAI/revyl-gh-action/upload-build@v1
  with:
    api-key: ${{ secrets.REVYL_API_KEY }}
    build-var-id: ${{ vars.BUILD_VAR_ID }}
    version: ${{ github.sha }}
    file-path: ./build/app.apk

- name: Run Tests
  uses: RevylAI/revyl-gh-action/run-workflow@v1
  with:
    api-key: ${{ secrets.REVYL_API_KEY }}
    workflow: smoke-tests
    build-version-id: ${{ steps.upload.outputs.version-id }}
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
      - run: |
          xcodebuild -scheme MyApp -sdk iphonesimulator -derivedDataPath build -quiet
          cd build/Build/Products/Debug-iphonesimulator && zip -r ../../../../app.zip MyApp.app
      - env:
          REVYL_API_KEY: ${{ secrets.REVYL_API_KEY }}
        run: |
          curl -fsSL https://revyl.com/install.sh | sh
          export PATH="$HOME/.revyl/bin:$PATH"
          revyl build upload --file app.zip --platform ios --yes
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
      - run: ./gradlew assembleDebug
      - env:
          REVYL_API_KEY: ${{ secrets.REVYL_API_KEY }}
        run: |
          curl -fsSL https://revyl.com/install.sh | sh
          export PATH="$HOME/.revyl/bin:$PATH"
          revyl build upload --file app/build/outputs/apk/debug/app-debug.apk --platform android --yes
          revyl workflow run smoke-tests
```

## Pattern 4: EAS Cloud + URL Upload (no Mac, no local build)

For Expo teams, delegate the build to EAS and just ingest the result:

```bash
ARTIFACT_URL=$(npx eas-cli build --profile development --platform ios --non-interactive --json | jq -r '.[0].artifacts.buildUrl')
revyl build upload --url "$ARTIFACT_URL" --app "My iOS App"
```

Zero Mac needed on the customer side. Expo's cloud does the build, Revyl just ingests the artifact.

## Pattern 5: revyl build upload (config-driven)

If `.revyl/config.yaml` has build commands configured, the CLI can build and upload in one step:

```bash
revyl build upload --platform ios --yes
```

This runs the `build.platforms.ios.command` from your config, then uploads the output artifact.

## CI-Friendly Flags

| Flag | Effect |
|------|--------|
| `--json` | Machine-readable JSON output |
| `--no-wait` | Queue the run and exit without waiting for results |
| `--quiet` / `-q` | Suppress non-essential output |
| `--yes` | Skip interactive confirmations |
| `--version <string>` | Tag the build with a version (default: `<branch>-<timestamp>`) |

## When Would Remote Builds Be Worth Building?

You don't need Revyl to build your app. But here's when a managed build service makes sense:

- **>50% of your customers can't produce a simulator build** -- they don't have Macs, don't know Xcode, or can't configure EAS. Today this is rare: Expo handles it with `eas build`, and GitHub Actions provides macOS runners.
- **Build times exceed 30 minutes** and customers are blocked waiting. Caching, parallelization, or dedicated build infra could help.
- **You need to patch or instrument the binary** before testing (e.g., inject a test harness, mock network layer). This requires control over the build step.

Until you hit one of these, the combination of customer CI + `revyl build upload` covers the workflow.

## Next Steps

- [Expo Build Guide](expo.md) -- Expo-specific build and upload
- [CI/CD Integration](../ci-cd.md) -- full CI pipeline setup
