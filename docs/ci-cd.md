# CI/CD Integration

Run Revyl tests automatically on every pull request. This guide covers GitHub Actions, the Revyl CLI in CI, and integrating test results into your workflow.

## Prerequisites

- Revyl account with an API key
- App and tests already created (see [Your First Test](tests/first-test.md))

## Step 1: Store your API key

Add `REVYL_API_KEY` as a repository secret in GitHub:

**Settings → Secrets and variables → Actions → New repository secret**

Name: `REVYL_API_KEY`
Value: your API key from [Account → API Keys](https://auth.revyl.ai/account/api_keys)

## Step 2: Add a workflow file

### Option A: Run tests against the latest build

The simplest setup. Runs your test workflow on every PR using the most recently uploaded build.

```yaml
name: Revyl Tests

on:
  pull_request:
    branches: [main]

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - name: Install Revyl CLI
        run: |
          curl -fsSL https://revyl.com/install.sh | sh
          echo "$HOME/.revyl/bin" >> "$GITHUB_PATH"

      - name: Run smoke tests
        env:
          REVYL_API_KEY: ${{ secrets.REVYL_API_KEY }}
        run: revyl workflow run smoke-tests
```

The installer downloads the native CLI binary. The CLI exits with code `0` on pass, `1` on failure.

### Option B: Build, upload, and test

Build your app fresh, upload the artifact, and run the full suite against it:

```yaml
name: Build and Test

on:
  pull_request:
    branches: [main]

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - name: Install Revyl CLI
        run: |
          curl -fsSL https://revyl.com/install.sh | sh
          echo "$HOME/.revyl/bin" >> "$GITHUB_PATH"

      - name: Build, upload, and run workflow
        env:
          REVYL_API_KEY: ${{ secrets.REVYL_API_KEY }}
        run: revyl run smoke-tests -w --platform android
```

`revyl run <workflow> -w` builds your app, uploads the artifact, and runs all tests. Use `--no-build` to skip the build step.

### Option C: Use the Revyl GitHub Action

```yaml
- uses: RevylAI/revyl-gh-action/run-workflow@v1
  with:
    api-key: ${{ secrets.REVYL_API_KEY }}
    workflow: smoke-tests
```

Other actions available:

```yaml
# Upload a build
- uses: RevylAI/revyl-gh-action/upload-build@v1
  with:
    api-key: ${{ secrets.REVYL_API_KEY }}
    platform: android

# Run a single test
- uses: RevylAI/revyl-gh-action/run-test@v1
  with:
    api-key: ${{ secrets.REVYL_API_KEY }}
    test: login-flow
```

### Build-to-test pipeline (GitHub Action)

Upload a build and test it in the same workflow:

```yaml
- name: Upload Build
  id: upload
  uses: RevylAI/revyl-gh-action/upload-build@v1
  with:
    build-var-id: ${{ vars.BUILD_VAR_ID }}
    version: ${{ github.sha }}
    file-path: ./build/app.apk
  env:
    REVYL_API_KEY: ${{ secrets.REVYL_API_KEY }}

- name: Run Workflow on New Build
  uses: RevylAI/revyl-gh-action/run-workflow@v1
  with:
    workflow-id: ${{ vars.WORKFLOW_ID }}
    build-version-id: ${{ steps.upload.outputs.version-id }}
  env:
    REVYL_API_KEY: ${{ secrets.REVYL_API_KEY }}
```

## Step 3: Sync tests from CI

Keep remote tests in sync with your repo after merge to main:

```yaml
- name: Sync tests
  if: github.event_name == 'push' && github.ref == 'refs/heads/main'
  env:
    REVYL_API_KEY: ${{ secrets.REVYL_API_KEY }}
  run: revyl test push --force
```

## CLI in CI (without GitHub Action)

Install and run the CLI directly in any CI:

```bash
curl -fsSL https://revyl.com/install.sh | sh
export PATH="$HOME/.revyl/bin:$PATH"
export REVYL_API_KEY=${{ secrets.REVYL_API_KEY }}

revyl test run login-flow --no-wait --json    # Queue and exit immediately
revyl workflow run smoke-tests --no-wait      # Queue a workflow
```

### Build upload from CI

```bash
revyl build upload --platform ios --skip-build --yes    # Upload pre-built artifact
revyl build upload --platform android --yes             # Build and upload
```

## Useful CI Patterns

### Retries

```bash
revyl workflow run smoke-tests --retries 2
```

### Async execution

```bash
revyl workflow run regression --no-wait
```

### JSON output for downstream processing

```yaml
- name: Run with JSON output
  env:
    REVYL_API_KEY: ${{ secrets.REVYL_API_KEY }}
  run: |
    result=$(revyl test run login-flow --json)
    echo "Report: $(echo $result | jq -r '.report_link')"
```

### Version-tagged builds

```bash
revyl build upload --platform android --version $GITHUB_SHA
```

## GitLab CI

```yaml
test:
  script:
    - curl -fsSL https://revyl.com/install.sh | sh
    - export PATH="$HOME/.revyl/bin:$PATH"
    - revyl workflow run smoke-tests
  variables:
    REVYL_API_KEY: $REVYL_API_KEY
```

## CI-Friendly Flags

| Flag | Effect |
|------|--------|
| `--json` | Machine-readable JSON output |
| `--no-wait` | Queue the run and exit without waiting for results |
| `--quiet` / `-q` | Suppress non-essential output |
| `--yes` | Skip interactive confirmations |

## Exit Codes

| Code | Meaning |
|------|---------|
| `0` | Test/workflow passed |
| `1` | Test/workflow failed or error |

## Environment Variables

| Variable | Description |
|----------|-------------|
| `REVYL_API_KEY` | API key for authentication (required in CI) |
| `REVYL_BACKEND_URL` | Override the backend URL (for staging/preview environments) |
| `REVYL_APP_URL` | Override the frontend app URL |

---

## What's Next

- [Build Guides](builds/expo.md) -- framework-specific build and upload instructions
- [CI Build Patterns](builds/ci-builds.md) -- advanced patterns for building in CI
- [Running Tests](tests/running-tests.md) -- CLI test execution reference
