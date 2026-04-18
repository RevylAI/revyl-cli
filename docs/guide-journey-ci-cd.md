<!-- mintlify
title: "CI/CD Pipeline"
description: "Run Revyl tests on every pull request with GitHub Actions or any CI provider"
target: cli/journey-ci-cd.mdx
-->

# CI/CD Pipeline

Gate pull requests with automated mobile tests. This guide takes you from zero to a working pipeline in about 10 minutes.

<Callout type="tip" title="Already set up?">
  This guide assumes you have at least one test or workflow created. See [Your First Test](/cli/journey-first-test) if you haven't done that yet.
</Callout>

## Step 1: Store your API key

Add `REVYL_API_KEY` as a repository secret in GitHub:

**Settings → Secrets and variables → Actions → New repository secret**

- Name: `REVYL_API_KEY`
- Value: your API key from [Account → API Keys](https://auth.revyl.ai/account/api_keys)

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
        run: pip install revyl

      - name: Run smoke tests
        env:
          REVYL_API_KEY: ${{ secrets.REVYL_API_KEY }}
        run: revyl workflow run smoke-tests
```

The CLI binary downloads automatically on first use. The CLI exits with code `0` on pass, `1` on failure — standard for CI.

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
        run: pip install revyl

      - name: Build, upload, and run workflow
        env:
          REVYL_API_KEY: ${{ secrets.REVYL_API_KEY }}
        run: revyl run smoke-tests -w --platform android
```

`revyl run <workflow> -w` builds your app, uploads the artifact, and runs all tests in the workflow. Use `--no-build` to skip the build step.

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

## Step 3: Sync tests from CI

Keep remote tests in sync with your repo after merge to main:

```yaml
- name: Sync tests
  if: github.event_name == 'push' && github.ref == 'refs/heads/main'
  env:
    REVYL_API_KEY: ${{ secrets.REVYL_API_KEY }}
  run: revyl test push --force
```

## Useful CI patterns

### Retries

```bash
revyl workflow run smoke-tests --retries 2
```

### Async execution

Queue without blocking:

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

## Exit codes

| Code | Meaning |
|------|---------|
| `0` | Test/workflow passed |
| `1` | Test/workflow failed or error |

## Environment variables

| Variable | Description |
|----------|-------------|
| `REVYL_API_KEY` | API key (required) |
| `REVYL_BACKEND_URL` | Override backend URL |

## GitLab CI

```yaml
test:
  script:
    - pip install revyl
    - revyl workflow run smoke-tests
  variables:
    REVYL_API_KEY: $REVYL_API_KEY
```

---

## What's Next

<CardGroup cols={2}>
  <Card title="GitHub Actions Reference" icon="github" href="/ci-cd/github-actions">
    Detailed GitHub Action configuration
  </Card>
  <Card title="Test Suite at Scale" icon="layer-group" href="/cli/journey-test-suite">
    Workflows, modules, and team sync patterns
  </Card>
  <Card title="Full Command Reference" icon="book" href="/cli/reference">
    Every CLI command, flag, and option
  </Card>
</CardGroup>
