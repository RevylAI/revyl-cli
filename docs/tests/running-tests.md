Run individual tests or complete workflows directly from your terminal. Get real-time feedback and integrate testing into your development workflow.

## Run a Single Test

Execute a test by name or ID:

```bash
revyl test run login-flow
```

The CLI will:
1. Resolve the test name (from aliases or API)
2. Queue the test for execution
3. Stream progress to your terminal
4. Report the final result

### Example Output

```
Running test: login-flow
Platform: android
Build: v1.2.3 (current)

Step 1/5: Tap the login button ✓
Step 2/5: Enter email address ✓
Step 3/5: Enter password ✓
Step 4/5: Tap Submit ✓
Step 5/5: Dashboard is visible ✓

✓ Test passed in 45s
Report: https://app.revyl.ai/report/abc123
```

### Run Options

| Flag | Description |
|------|-------------|
| `--build-version-id <id>` | Run against a specific build version |
| `--retries <n>` | Number of retry attempts (default: 1) |
| `--timeout <seconds>` | Maximum execution time (default: 3600) |
| `--no-wait` | Queue test and exit immediately |
| `--open` | Open report in browser when complete |
| `--json` | Output results as JSON |
| `--verbose` | Show detailed execution logs |

## Run a Workflow

Execute a workflow (multiple tests). The recommended way to build and run a workflow in one command:

```bash
revyl run smoke-tests -w
```

This builds your app, uploads it, then runs all tests in the workflow. Use `--no-build` to run without rebuilding:

```bash
revyl run smoke-tests -w --no-build
```

You can also use the explicit workflow command:

```bash
revyl workflow run smoke-tests
```

### Example Output

```
Running workflow: smoke-tests
Tests: 5

[1/5] login-flow ✓ (32s)
[2/5] browse-products ✓ (28s)
[3/5] add-to-cart ✓ (41s)
[4/5] checkout ✗ (55s) - Validation failed
[5/5] logout ✓ (12s)

Results: 4 passed, 1 failed
Total time: 2m 48s
Report: https://app.revyl.ai/workflow-report/xyz789
```

### Workflow Options

Same as `test run`, plus:

| Flag | Description |
|------|-------------|
| `--github-actions` | Format output for GitHub Actions |

## Full Pipeline: Build → Upload → Test

Use the `--build` flag with `revyl test run` to run the complete pipeline:

```bash
revyl test run login-flow --build --platform android
```

This executes:
1. **Build** - Run your build command
2. **Upload** - Upload the artifact to Revyl
3. **Test** - Run the test against the new build

### Pipeline Options

| Flag | Description |
|------|-------------|
| `--build` | Build and upload before running |
| `--platform <name>` | Build platform to use (requires `--build`) |
| `--retries <n>` | Retry attempts for the test |
| `--no-wait` | Don't wait for test completion |
| `--open` | Open report when complete |
| `--timeout <seconds>` | Test timeout |
| `--json` | JSON output |

### Example

```bash
# Build Android, upload, and run test
revyl test run checkout-flow --build --platform android

# Run test without building (against existing build)
revyl test run checkout-flow
```

<Callout type="info" title="Early Validation">
  The CLI validates that the test exists before starting the build process. If you specify a test name that doesn't exist, you'll get an immediate error with a list of available tests.
</Callout>

## Full Pipeline: Build → Upload → Workflow

The recommended way to build and run a workflow in one command:

```bash
revyl run smoke-tests -w
revyl run smoke-tests -w --platform android
```

This executes:
1. **Build** - Run your build command
2. **Upload** - Upload the artifact to Revyl
3. **Workflow** - Run all tests in the workflow against the new build

Use `--no-build` to run the workflow without rebuilding (e.g. `revyl run smoke-tests -w --no-build`).

### Workflow pipeline options (with `revyl run <name> -w`)

| Flag | Description |
|------|-------------|
| `--no-build` | Skip build step; run against last uploaded build |
| `--platform <name>` | Build platform to use |
| `--retries <n>` | Retry attempts for each test |
| `--no-wait` | Don't wait for workflow completion |
| `--open` | Open report when complete |
| `--timeout <seconds>` | Workflow timeout |
| `--json` | JSON output |

### Alternative: `revyl workflow run`

You can also use `revyl workflow run` with the `--build` flag:

```bash
revyl workflow run smoke-tests --build --platform android
```

| Flag | Description |
|------|-------------|
| `--build` | Build and upload before running workflow |
| `--platform <name>` | Build platform to use (requires `--build`) |

## Build and Upload Only

Upload a build without running tests:

```bash
revyl build upload --platform android
```

### Upload Options

| Flag | Description |
|------|-------------|
| `--platform <name>` | Build platform to use |
| `--skip-build` | Upload existing artifact without building |
| `--version <string>` | Version label (default: `<branch-slug>-<timestamp>`) |
| `--set-current` | Set as the current/default build |
| `--platform <ios\|android>` | Override detected platform |
| `--name <string>` | Custom build name |
| `--yes` | Skip confirmation prompts |
| `--dry-run` | Show what would be uploaded |
| `--json` | JSON output |

### Example Output

```
Building Android platform...
Running: ./gradlew assembleDebug
✓ Build completed in 45s

Uploading app-debug.apk...
✓ Uploaded 24.5 MB in 8s

Build Version: bv_abc123
Package ID: com.example.app
Version: feature-new-login-20260227-153000 (branch-aware default)
```

<Callout type="warning" title="iOS Build Requirements">
  iOS builds must be simulator `.app` bundles (or a zipped `.app`), not `.ipa` device builds. Use a Debug-iphonesimulator build from Xcode or a simulator profile from EAS/Expo. See [iOS build guides](/builds/xcode) for details.
</Callout>

## List Builds

View uploaded build versions:

```bash
revyl build list
```

Options:

| Flag | Description |
|------|-------------|
| `--platform <ios\|android>` | Filter by platform |
| `--build-var <id>` | Filter by build variant |
| `--json` | JSON output |

## Exit Codes

The CLI uses standard exit codes for scripting:

| Code | Meaning |
|------|---------|
| `0` | Test/workflow passed |
| `1` | Test/workflow failed or error |

### CI/CD Example

```bash
#!/bin/bash
revyl workflow run smoke-tests

if [ $? -eq 0 ]; then
  echo "All tests passed!"
  ./deploy.sh
else
  echo "Tests failed, aborting deployment"
  exit 1
fi
```

## JSON Output

For programmatic use, get structured JSON output:

```bash
revyl test run login-flow --json
```

```json
{
  "success": true,
  "task_id": "task_abc123",
  "test_name": "login-flow",
  "platform": "android",
  "execution_time": 45,
  "total_steps": 5,
  "completed_steps": 5,
  "report_link": "https://app.revyl.ai/report/abc123"
}
```

## Open Results in Browser

Automatically open the report when a test completes:

```bash
revyl test run login-flow --open
```

Or open a previous report:

```bash
revyl test open login-flow
```

## Background Execution

Queue a test without waiting for results:

```bash
revyl test run login-flow --no-wait
```

Output:

```
Test queued: login-flow
Task ID: task_abc123
Track progress: https://app.revyl.ai/task/abc123
```

## Common Workflows

### Quick Smoke Test

```bash
# Build, upload, and run workflow (recommended)
revyl run smoke-tests -w

# Or run workflow without rebuilding
revyl run smoke-tests -w --no-build
```

### Test New Changes

```bash
# Build, upload, and test (recommended)
revyl run login-flow --platform android
```

### CI/CD Pipeline

```bash
# In your CI script
export REVYL_API_KEY=${{ secrets.REVYL_API_KEY }}

# Build and upload
revyl build upload --platform android --version $GITHUB_SHA

# Run workflow, fail pipeline if tests fail
revyl workflow run regression --retries 2
```

### Local Development Loop

```bash
# Make code changes
# ...

# Quick test without rebuilding
revyl run login-flow --no-build

# Full rebuild and test
revyl run login-flow --platform android
```

## Troubleshooting

### Cancelling a Running Test

Use `revyl test cancel` or `revyl workflow cancel` to stop a running test or workflow:

```bash
# Cancel a running test
revyl test cancel <task_id>

# Cancel a running workflow (and all its tests)
revyl workflow cancel <task_id>
```

**Where to find the task ID:**
- CLI output when starting a test: `Task ID: abc123-def456...`
- Report URL: `https://app.revyl.ai/tests/report?taskId=abc123...`

<Callout type="info" title="Note">
  If the test has already completed, failed, or been cancelled, the command will return an error with the current status.
</Callout>

<Callout type="warning" title="Ctrl+C vs Cancel">
  Pressing Ctrl+C only stops the CLI from monitoring - the test continues running on the server. Use `revyl test cancel <task_id>` to actually stop the test.
</Callout>

### Test Times Out

- Increase timeout: `--timeout 900`
- Check device availability in Revyl dashboard
- Verify the test runs manually in the web UI

### Build Upload Fails

- Check your build command produces output at the expected path
- Verify file permissions on the artifact
- Try `--dry-run` to see what would be uploaded

### "Test not found" Error

- Run `revyl test list` to see available tests
- Check spelling and case sensitivity
- Ensure the test exists in your `config.yaml` aliases or on remote

## Next Steps

<CardGroup cols={2}>
  <Card title="Command Reference" icon="book" href="/cli/reference">
    Full command documentation
  </Card>
  <Card title="CI/CD Integration" icon="rotate" href="/ci-cd/github-actions">
    Automate with GitHub Actions
  </Card>
</CardGroup>
