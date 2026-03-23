# YAML Tests

Define, version-control, and sync your Revyl tests as YAML files.

## Quick start

```bash
revyl init                          # creates .revyl/config.yaml
revyl test create login-flow        # creates .revyl/tests/login-flow.yaml
# edit the YAML file
revyl test validate                 # check syntax
revyl test push                     # push to Revyl
revyl test run login-flow           # run it
```

## Creating a test from a file

Use `--from-file` to create a remote test directly from an existing YAML file:

```bash
revyl test create --from-file ./login-flow.yaml
```

The test name is inferred from `test.metadata.name` in the YAML. You can also pass it explicitly:

```bash
revyl test create login-flow --from-file ./login-flow.yaml
```

This command:

1. Validates the YAML
2. Copies it to `.revyl/tests/<name>.yaml`
3. Creates (or updates) the remote test
4. Writes `.revyl/config.yaml` if it doesn't exist yet
5. Stores sync metadata (`_meta.remote_id`, version, checksums) in the local YAML

Useful flags:

```bash
revyl test create --from-file ./test.yaml --force     # overwrite if name already exists
revyl test create --from-file ./test.yaml --no-open   # skip opening browser
revyl test create --from-file ./test.yaml --dry-run   # preview without creating
```

### Recommended loop

```bash
# 1. Validate
revyl test validate ./my-test.yaml

# 2. Create and bootstrap local state
revyl test create --from-file ./my-test.yaml

# 3. Iterate on the synced file
revyl test push my-test --force

# 4. Run and inspect
revyl test run my-test
revyl test report my-test --json
```

## Project structure

```
your-app/
  .revyl/
    config.yaml              # project config (see config.yaml example)
    tests/
      login-flow.yaml        # one file per test
      checkout.yaml
```

## Test format

Every test file has `test.metadata`, `test.build`, and `test.blocks`:

```yaml
test:
  metadata:
    name: my-test
    platform: ios            # ios or android
  build:
    name: my-app-build       # build name in Revyl
  blocks:
    - type: instructions
      step_description: "Tap the Login button"
    - type: validation
      step_description: "The home screen is visible"
```

## Block types

| Type | Purpose | Required fields |
|------|---------|-----------------|
| `instructions` | Perform an action | `step_description` |
| `validation` | Assert something is true | `step_description` |
| `extraction` | Extract data into a variable | `step_description`, `variable_name` |
| `manual` | Built-in actions (wait, navigate, etc.) | `step_type`, `step_description` |
| `if` | Conditional branch | `condition`, `then` (blocks) |
| `while` | Loop | `condition`, `body` (blocks) |
| `code_execution` | Run a script | `script` or `step_description` |
| `module_import` | Reuse a shared module | `module` or `module_id` |

## Variables

Use `{{variable-name}}` (kebab-case) in step descriptions:

```yaml
test:
  metadata:
    name: login
    platform: ios
  build:
    name: my-app
  variables:
    username: "testuser@example.com"
    password: "secret123"
  blocks:
    - type: instructions
      step_description: "Enter '{{username}}' in the email field"
```

## Syncing

```bash
revyl test push                   # push local YAML changes to Revyl
revyl test pull                   # pull remote changes into local YAML files
revyl test diff login-flow        # show diff between local and remote for a test
revyl test push --force           # overwrite remote with local (skip conflict check)
revyl test pull login-flow --force  # overwrite local with remote
```

### Check sync status

```bash
revyl test list
```

```
NAME              STATUS      PLATFORM   LAST MODIFIED
login-flow        synced      ios        2 hours ago
checkout          modified    ios        5 minutes ago
onboarding        outdated    android    1 day ago
```

| Status | Meaning |
|--------|---------|
| `synced` | Local and remote are identical |
| `modified` | Local changes not yet pushed |
| `outdated` | Remote has newer changes |
| `local-only` | Exists locally but not on remote |

### Daily workflow

```bash
# Start of day -- pull changes made in the browser editor
revyl test pull

# Work on tests locally ...

# See what changed vs remote
revyl test diff login-flow

# Push your changes
revyl test push
```

### Conflict resolution

```bash
# Keep the remote version
revyl test pull checkout --force

# Keep the local version
revyl test push checkout --force
```

### Full reconciliation

Reconcile all tests, workflows, and app links at once:

```bash
revyl sync --dry-run              # preview what will change
revyl sync --tests --prune        # reconcile and clean up stale mappings
```

## Using in CI/CD

Commit `.revyl/tests/` to git so test YAML is version-controlled alongside your code. Then sync and run tests in CI.

### Post-merge: push tests and run

After merging YAML changes to your main branch, push to Revyl and run:

```bash
revyl test push --force
revyl workflow run smoke-tests --json
```

### GitHub Actions

```yaml
name: Revyl Tests
on:
  push:
    branches: [main]
    paths: [".revyl/tests/**"]

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - name: Install Revyl
        run: pip install revyl

      - name: Push tests and run
        env:
          REVYL_API_KEY: ${{ secrets.REVYL_API_KEY }}
        run: |
          revyl test push --force
          revyl workflow run smoke-tests --json
```

You can also use the official GitHub Action for individual test or workflow runs:

```yaml
- uses: RevylAI/revyl-gh-action/run-workflow@main
  with:
    workflow-id: "your-workflow-id"
  env:
    REVYL_API_KEY: ${{ secrets.REVYL_API_KEY }}
```

### GitLab CI / generic CI

```yaml
test:
  image: alpine:latest
  before_script:
    - apk add --no-cache curl
    - curl -fsSL https://raw.githubusercontent.com/RevylAI/revyl-cli/main/scripts/install.sh | sh
  script:
    - revyl test push --force
    - revyl workflow run smoke-tests --json
```

### CI-friendly flags

| Flag | Effect |
|------|--------|
| `--json` | Machine-readable JSON output |
| `--no-wait` | Queue the run and exit without waiting for results |
| `--quiet` / `-q` | Suppress non-essential output |
| `--yes` | Skip interactive confirmations |
| `--force` | Overwrite remote tests without conflict checks |

## Examples

- [`config.yaml`](config.yaml) -- project configuration
- [`login-flow.yaml`](login-flow.yaml) -- simple login test
- [`checkout-with-variables.yaml`](checkout-with-variables.yaml) -- variables and env vars
- [`conditional-flow.yaml`](conditional-flow.yaml) -- if/while control flow
