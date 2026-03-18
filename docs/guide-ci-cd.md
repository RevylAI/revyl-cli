<!-- mintlify
title: "Guide: CI/CD Pipeline"
description: "Run Revyl tests in GitHub Actions and other CI pipelines"
target: ci-cd/pipeline-guide.mdx
-->

# CI/CD Pipeline Guide

Run Revyl tests automatically on every pull request. This guide covers GitHub Actions setup, build-to-test pipelines, and integrating test results into your workflow.

## Prerequisites

- Revyl account with an API key
- App and tests already created (see [First Test Guide](/get-started/first-test-guide))

## Step 1: Store your API key

Add `REVYL_API_KEY` as a repository secret in GitHub:

**Settings → Secrets and variables → Actions → New repository secret**

Name: `REVYL_API_KEY`
Value: your API key from [Account → API Keys](https://auth.revyl.ai/account/api_keys)

## Step 2: Basic workflow

Run a test workflow on every PR:

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
        run: pip install revyl

      - name: Run smoke tests
        env:
          REVYL_API_KEY: ${{ secrets.REVYL_API_KEY }}
        run: revyl workflow run smoke-tests
```

The CLI binary downloads automatically on first use — no additional setup step is required.

The CLI exits with code `0` on pass, `1` on failure — standard for CI.

## Step 3: Build → Upload → Test pipeline

Build your app, upload it, and run the full test suite:

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
        run: pip install revyl

      - name: Build, upload, and run workflow
        env:
          REVYL_API_KEY: ${{ secrets.REVYL_API_KEY }}
        run: revyl run smoke-tests -w --platform android
```

`revyl run <workflow> -w` builds your app, uploads the artifact, and runs all tests in the workflow. Use `--no-build` to skip the build step and run against the latest uploaded build.

## Using the GitHub Action

Revyl provides a dedicated GitHub Action for common operations:

### Run a workflow

```yaml
- uses: RevylAI/revyl-gh-action/run-workflow@v1
  with:
    api-key: ${{ secrets.REVYL_API_KEY }}
    workflow: smoke-tests
```

### Upload a build

```yaml
- uses: RevylAI/revyl-gh-action/upload-build@v1
  with:
    api-key: ${{ secrets.REVYL_API_KEY }}
    platform: android
```

### Run a single test

```yaml
- uses: RevylAI/revyl-gh-action/run-test@v1
  with:
    api-key: ${{ secrets.REVYL_API_KEY }}
    test: login-flow
```

## JSON output for scripting

Use `--json` to get structured output for downstream processing:

```yaml
- name: Run tests with JSON output
  env:
    REVYL_API_KEY: ${{ secrets.REVYL_API_KEY }}
  run: |
    result=$(revyl test run login-flow --json)
    echo "Report: $(echo $result | jq -r '.report_link')"
```

## Async execution

Use `--no-wait` to queue the test and continue without blocking:

```yaml
- name: Queue tests
  env:
    REVYL_API_KEY: ${{ secrets.REVYL_API_KEY }}
  run: revyl workflow run regression --no-wait
```

## Retries

Retry failed tests automatically:

```bash
revyl workflow run smoke-tests --retries 2
```

## TestFlight from CI

Publish iOS builds to TestFlight after tests pass:

```yaml
- name: Publish to TestFlight
  if: success()
  env:
    REVYL_API_KEY: ${{ secrets.REVYL_API_KEY }}
    REVYL_ASC_KEY_ID: ${{ secrets.ASC_KEY_ID }}
    REVYL_ASC_ISSUER_ID: ${{ secrets.ASC_ISSUER_ID }}
    REVYL_ASC_PRIVATE_KEY: ${{ secrets.ASC_PRIVATE_KEY }}
  run: |
    revyl publish testflight \
      --ipa ./build/MyApp.ipa \
      --app-id 6758900172 \
      --group "Internal"
```

## Push tests from CI

Keep remote tests in sync with your repo after merge:

```yaml
- name: Sync tests
  env:
    REVYL_API_KEY: ${{ secrets.REVYL_API_KEY }}
  run: revyl test push --force
```

## Environment variables

| Variable | Description |
|----------|-------------|
| `REVYL_API_KEY` | API key (required) |
| `REVYL_BACKEND_URL` | Override backend URL |
| `REVYL_ASC_KEY_ID` | App Store Connect key ID |
| `REVYL_ASC_ISSUER_ID` | App Store Connect issuer ID |
| `REVYL_ASC_PRIVATE_KEY_PATH` | Path to ASC `.p8` private key |
| `REVYL_ASC_PRIVATE_KEY` | Raw ASC private key content |
| `REVYL_ASC_APP_ID` | App Store Connect app ID |
| `REVYL_TESTFLIGHT_GROUPS` | Comma-separated TestFlight groups |

---

## What's Next

- [GitHub Actions Reference](/ci-cd/github-actions) — detailed action configuration
- [Running Tests](/cli/running-tests) — CLI test execution reference
- [Advanced Tests](/yaml/advanced-tests-guide) — scripts, modules, and control flow
