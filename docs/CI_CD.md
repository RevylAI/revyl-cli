# CI/CD Integration

> [Back to README](../README.md) | [Commands](COMMANDS.md) | [Configuration](CONFIGURATION.md)

## GitHub Actions

The official GitHub Action supports test runs, workflow runs, and build uploads:

```yaml
name: Revyl Tests

on:
  pull_request:
    branches: [main]

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - name: Run Revyl Test
        uses: RevylAI/revyl-gh-action/run-test@main
        with:
          test-id: "your-test-id"
        env:
          REVYL_API_KEY: ${{ secrets.REVYL_API_KEY }}
```

### Run a workflow (recommended for CI)

Workflows run multiple tests and provide richer status reporting:

```yaml
- name: Run Revyl Workflow
  uses: RevylAI/revyl-gh-action/run-workflow@main
  with:
    workflow-id: "your-workflow-id"
    retries: 2
  env:
    REVYL_API_KEY: ${{ secrets.REVYL_API_KEY }}
```

### Build-to-test pipeline

Upload a build and test it in the same workflow:

```yaml
- name: Upload Build
  id: upload
  uses: RevylAI/revyl-gh-action/upload-build@main
  with:
    build-var-id: ${{ vars.BUILD_VAR_ID }}
    version: ${{ github.sha }}
    file-path: ./build/app.apk
  env:
    REVYL_API_KEY: ${{ secrets.REVYL_API_KEY }}

- name: Run Workflow on New Build
  uses: RevylAI/revyl-gh-action/run-workflow@main
  with:
    workflow-id: ${{ vars.WORKFLOW_ID }}
    build-version-id: ${{ steps.upload.outputs.version-id }}
  env:
    REVYL_API_KEY: ${{ secrets.REVYL_API_KEY }}
```

---

## CLI in CI

You can also run the CLI directly in CI without the GitHub Action:

```bash
pip install revyl
export REVYL_API_KEY=${{ secrets.REVYL_API_KEY }}

revyl test run login-flow --no-wait --json    # Queue and exit immediately
revyl workflow run smoke-tests --no-wait      # Queue a workflow
```

### Build upload from CI

```bash
revyl build upload --platform ios --skip-build --yes    # Upload pre-built artifact
revyl build upload --platform android --yes             # Build and upload
```

### Full pipeline example

```bash
# 1) Upload the build
revyl build upload --platform ios --skip-build --yes

# 2) Run tests
revyl workflow run smoke-tests --json

# 3) Get results
revyl workflow report smoke-tests --json
```

### CI-friendly flags

| Flag | Effect |
|------|--------|
| `--json` | Machine-readable JSON output |
| `--no-wait` | Queue the run and exit without waiting for results |
| `--quiet` / `-q` | Suppress non-essential output |
| `--yes` | Skip interactive confirmations |

---

## TestFlight Publishing from CI

Publish to TestFlight without interactive prompts using environment variables:

```bash
export REVYL_ASC_KEY_ID=ABC123DEF4
export REVYL_ASC_ISSUER_ID=00000000-0000-0000-0000-000000000000
export REVYL_ASC_PRIVATE_KEY_PATH=/secure/path/AuthKey_ABC123DEF4.p8
export REVYL_ASC_APP_ID=6758900172
export REVYL_TESTFLIGHT_GROUPS="Internal,External"

revyl publish testflight --ipa ./build/MyApp.ipa
```

Or pass the key content directly (useful in CI where file paths are ephemeral):

```bash
export REVYL_ASC_PRIVATE_KEY='-----BEGIN PRIVATE KEY-----\n...\n-----END PRIVATE KEY-----'
```

---

## Environment Variables

| Variable | Description |
|----------|-------------|
| `REVYL_API_KEY` | API key for authentication (required in CI) |
| `REVYL_BACKEND_URL` | Override the backend URL (for staging/preview environments) |
| `REVYL_APP_URL` | Override the frontend app URL |
| `REVYL_PROJECT_DIR` | Override the project directory for MCP |
| `REVYL_ASC_KEY_ID` | App Store Connect API key ID (TestFlight) |
| `REVYL_ASC_ISSUER_ID` | App Store Connect issuer ID (TestFlight) |
| `REVYL_ASC_PRIVATE_KEY_PATH` | Path to App Store Connect private key file |
| `REVYL_ASC_PRIVATE_KEY` | App Store Connect private key content (alternative to path) |
| `REVYL_ASC_APP_ID` | App Store Connect app ID |
| `REVYL_TESTFLIGHT_GROUPS` | Comma-separated TestFlight group names |

Note: there is no `REVYL_DEBUG` environment variable. Use the `--debug` CLI flag instead.
