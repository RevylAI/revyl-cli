<!-- mintlify
title: "Your First Test"
description: "Install the CLI, authenticate, create a YAML test, run it, and view the report — all in one guide"
target: cli/journey-first-test.mdx
-->

# Your First Test

This guide takes you from zero to a passing test. By the end you'll have the CLI installed, a test written in YAML, and a report you can share.

**Time:** ~10 minutes

## Step 1: Install the CLI

<CodeGroup>

```bash Homebrew (macOS)
brew install RevylAI/tap/revyl
```

```bash pipx (cross-platform)
pipx install revyl
```

```bash uv
uv tool install revyl
```

```bash pip
pip install revyl
```

</CodeGroup>

Verify it worked:

```bash
revyl version
```

## Step 2: Authenticate

```bash
revyl auth login
```

You'll be prompted for an API key. Get one from [Account → API Keys](https://auth.revyl.ai/account/api_keys).

```
Enter your API key: rvl_xxxxxxxxxxxxxxxxxxxx
✓ Authenticated as user@example.com
✓ Organization: My Company
✓ Credentials saved to ~/.revyl/credentials.json
```

<Callout type="tip" title="CI/CD?">
  For automated environments, set `REVYL_API_KEY` as an environment variable instead. See [CI/CD Pipeline](/cli/journey-ci-cd).
</Callout>

## Step 3: Initialize your project

```bash
cd your-app
revyl init
```

The interactive wizard:

1. Detects your build system (Expo, Gradle, Xcode, Flutter, React Native)
2. Creates `.revyl/config.yaml`
3. Walks you through creating apps and uploading a build

To skip the wizard and configure manually:

```bash
revyl init -y
```

## Step 4: Upload a build

If `revyl init` didn't upload a build for you, do it now:

```bash
revyl build upload --platform android
```

<Callout type="warning" title="iOS builds">
  iOS builds must be simulator `.app` bundles (or a zipped `.app`), not `.ipa` device builds. Use a Debug-iphonesimulator build from Xcode or a simulator profile from EAS/Expo. See [iOS build guides](/builds/xcode) for details.
</Callout>

## Step 5: Write a YAML test

Create a file called `login-smoke.yaml`:

```yaml
test:
  metadata:
    name: login-smoke
    platform: ios
    tags:
      - smoke
  build:
    name: my-ios-app
  blocks:
    - type: instructions
      step_description: Tap the Sign In button.
    - type: instructions
      step_description: Type "user@example.com" in the email field.
    - type: instructions
      step_description: Type "password123" in the password field.
    - type: instructions
      step_description: Tap Continue.
    - type: validation
      step_description: The home screen is visible.
```

Key fields:

- `test.metadata.name` — must be unique in your org
- `test.metadata.platform` — `ios` or `android`
- `test.build.name` — must match a Revyl app name exactly. Check with `revyl app list`.
- `test.blocks` — ordered list of steps (one action per instruction, assertions in `validation` blocks)

## Step 6: Validate the YAML

```bash
revyl test validate ./login-smoke.yaml
```

Fix any errors before proceeding. Use `--json` for machine-readable output.

## Step 7: Create the test

```bash
revyl test create login-smoke --from-file ./login-smoke.yaml
```

This validates the YAML, copies it to `.revyl/tests/login-smoke.yaml`, creates the remote test, and writes config if it doesn't exist yet.

## Step 8: Run the test

```bash
revyl test run login-smoke --open
```

The CLI queues the test, streams progress to your terminal, and opens the report in your browser:

```
Running test: login-smoke
Platform: ios
Build: v1.2.3 (current)

Step 1/5: Tap the Sign In button ✓
Step 2/5: Type "user@example.com" in the email field ✓
Step 3/5: Type "password123" in the password field ✓
Step 4/5: Tap Continue ✓
Step 5/5: The home screen is visible ✓

✓ Test passed in 45s
Report: https://app.revyl.ai/report/abc123
```

### Useful run flags

| Flag | Description |
|------|-------------|
| `--open` | Open report in browser when complete |
| `--retries <n>` | Retry attempts (default: 1) |
| `--timeout <seconds>` | Maximum execution time (default: 600) |
| `--no-wait` | Queue and exit immediately |
| `--json` | Structured JSON output |
| `--build-version-id <id>` | Pin a specific build version |

## Step 9: Iterate

Edit `.revyl/tests/login-smoke.yaml`, then push and re-run:

```bash
revyl test push login-smoke --force
revyl test run login-smoke
```

## Commit your tests to git

The `.revyl/tests/` directory is **not gitignored** by default. These YAML files are your source of truth — commit them.

```bash
git add .revyl/tests/
git commit -m "Add login-smoke test"
```

## Quick reference

| Task | Command |
|------|---------|
| See available tests | `revyl test list` |
| View a test report | `revyl test report login-smoke` |
| Open in browser editor | `revyl test open login-smoke` |
| Cancel a running test | `revyl test cancel <task_id>` |
| Check sync status | `revyl test list` (shows synced/modified/outdated) |

---

## What's Next

<CardGroup cols={2}>
  <Card title="Dev Loop" icon="bolt" href="/cli/journey-dev-loop">
    Connect a live device to your local code and iterate in real time
  </Card>
  <Card title="Test Suite at Scale" icon="layer-group" href="/cli/journey-test-suite">
    Add modules, scripts, workflows, and team sync patterns
  </Card>
  <Card title="CI/CD Pipeline" icon="rotate" href="/cli/journey-ci-cd">
    Run tests automatically on every pull request
  </Card>
  <Card title="YAML Schema" icon="code" href="/yaml/yaml-schema">
    Full reference for all block types and fields
  </Card>
</CardGroup>
