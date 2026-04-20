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
  For automated environments, set `REVYL_API_KEY` as an environment variable instead. See [CI/CD Integration](../ci-cd.md).
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
  iOS builds must be simulator `.app` bundles (or a zipped `.app`), not `.ipa` device builds. See [Build Guides](../builds/expo.md) for framework-specific instructions.
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

The CLI queues the test, streams progress to your terminal, and opens the report in your browser.

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

---

## YAML as Source of Truth

For teams that want test definitions version-controlled alongside code.

### Commit `.revyl/tests/` to git

The `.revyl/tests/` directory is **not gitignored** by default. These YAML files are your source of truth — commit them.

```bash
git add .revyl/tests/
git commit -m "Add login-smoke test"
```

### Daily sync pattern

```bash
# Start of day — pull any changes made in the browser editor
revyl test pull

# Work on tests locally in your IDE
# ...

# See what changed vs remote
revyl test diff login-smoke

# Push your changes
revyl test push
```

### Check sync status

```bash
revyl test list
```

```
NAME              STATUS      PLATFORM   LAST MODIFIED
login-smoke       synced      ios        2 hours ago
checkout          modified    ios        5 minutes ago
onboarding        outdated    android    1 day ago
```

| Status | Meaning |
|--------|---------|
| `synced` | Local and remote are identical |
| `modified` | Local changes not yet pushed |
| `outdated` | Remote has newer changes |
| `local-only` | Exists locally but not on remote |

### Reconcile when things drift

```bash
revyl sync --dry-run              # Preview what sync will change
revyl sync --tests --prune        # Reconcile and clean up stale mappings
```

### Resolve conflicts

```bash
revyl test diff checkout          # See the diff
revyl test pull checkout --force  # Keep remote version
revyl test push checkout --force  # Keep local version
```

---

## Quick Reference

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
  <Card title="Dev Loop" icon="bolt" href="/cli/dev-loop">
    Connect a live device to your local code and iterate in real time
  </Card>
  <Card title="Creating Tests" icon="layer-group" href="/cli/creating-tests">
    Modules, scripts, workflows, and team sync patterns
  </Card>
  <Card title="CI/CD Pipeline" icon="rotate" href="/ci-cd/pipeline">
    Run tests automatically on every pull request
  </Card>
  <Card title="YAML Schema" icon="code" href="/yaml/yaml-schema">
    Full reference for all block types and fields
  </Card>
</CardGroup>
