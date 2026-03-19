<!-- mintlify
title: "Managing Tests"
description: "Sync and manage tests between your local project and Revyl"
target: cli/tests.mdx
-->

# Creating Tests with the Revyl CLI

> [Back to README](../README.md) | [Commands](COMMANDS.md) | [Agent Skills](SKILLS.md)

This guide covers the full CLI authoring loop for Revyl tests:

- create tests from YAML
- scaffold a test and then edit it locally
- import reusable modules
- push, run, open, and troubleshoot tests

## Choose a Workflow

Use one of these flows depending on where your test starts:

1. YAML-first CLI (recommended)
   Start from a local YAML file, validate it, create the remote test, and bootstrap `.revyl/config.yaml` automatically.
2. Scaffold first
   Create an empty or module-seeded remote test with `revyl test create`, sync the generated YAML into `.revyl/tests/`, then edit and push locally.
3. Dev loop to regression
   Use `revyl dev test create` after an exploratory session, then refine the synced YAML and push it back as a stable regression.

## Prerequisites

Before creating tests, make sure you have:

- authenticated with `revyl auth login`
- an app/build available for the target platform
- the correct app name for `test.build.name`

Use these commands to confirm app names before you author YAML:

```bash
revyl app list
revyl app list --platform ios
revyl app list --platform android
```

`test.build.name` must match a Revyl app name for the test's platform. If the name does not resolve, `test push` will fail.

## Workflow 1: YAML-First CLI

This is the best path when you already know the flow you want to encode.

### 1. Write a YAML file

Example `smoke-login-ios.yaml`:

```yaml
test:
  metadata:
    name: smoke-login-ios
    platform: ios
    tags:
      - smoke
  build:
    name: ios-test
  blocks:
    - type: instructions
      step_description: Sign in with valid test credentials.
    - type: validation
      step_description: The inbox is visible.
```

### 2. Validate the YAML

```bash
revyl test validate ./smoke-login-ios.yaml
```

Use `--json` when you want machine-readable output in CI or scripts:

```bash
revyl test validate ./smoke-login-ios.yaml --json
```

### 3. Create the test from the YAML file

```bash
revyl test create smoke-login-ios --from-file ./smoke-login-ios.yaml
```

What this does:

- validates the YAML
- copies it to `.revyl/tests/smoke-login-ios.yaml`
- creates or updates the remote test through the normal push flow
- writes `.revyl/config.yaml` if it does not already exist
- stores `_meta.remote_id`, `_meta.remote_version`, checksum, and sync timestamps in the local YAML

This YAML-first bootstrap works even when the project does not already have `.revyl/config.yaml`.

### 4. Iterate locally and push changes

Edit the synced file:

```text
.revyl/tests/smoke-login-ios.yaml
```

Then push:

```bash
revyl test push smoke-login-ios --force
```

Use `--force` when you want to overwrite the current remote version with your local YAML.

### 5. Run or open the test

```bash
revyl test run smoke-login-ios
revyl test open smoke-login-ios
revyl test report smoke-login-ios --json
```

## Workflow 2: Scaffold First, Then Edit Locally

This path is useful when you want the CLI to create the remote test shell first.

### 1. Create the test scaffold

```bash
revyl test create smoke-login-ios --platform ios
```

Useful flags:

```bash
revyl test create smoke-login-ios --platform ios --no-open
revyl test create smoke-login-ios --platform ios --app <app-id>
revyl test create smoke-login-ios --platform ios --module login
revyl test create smoke-login-ios --platform ios --tag smoke --tag ios
```

This flow:

- creates the remote test immediately
- adds the alias to `.revyl/config.yaml` unless `--no-sync` is used
- syncs the generated YAML into `.revyl/tests/<name>.yaml`
- optionally opens the browser editor unless `--no-open` is used

### 2. Edit the local YAML

After scaffold creation, refine:

```text
.revyl/tests/smoke-login-ios.yaml
```

Then push the updated YAML back to the server:

```bash
revyl test push smoke-login-ios --force
```

Use `revyl test open smoke-login-ios` when you want to continue editing in the browser instead of locally.

## YAML Anatomy

Every test file has the same top-level structure:

```yaml
test:
  metadata:
    name: my-test
    platform: ios
  build:
    name: ios-test
  blocks:
    - type: instructions
      step_description: Do something
```

Key fields:

- `test.metadata.name`: the remote test name
- `test.metadata.platform`: `ios` or `android`
- `test.build.name`: the Revyl app name to run against
- `test.blocks`: the ordered test steps

Common block types:

- `instructions`
  Use for a single user action.
- `validation`
  Use for assertions about the expected state after an action.
- `manual`
  Use for framework-level actions such as `wait`, `go_home`, `navigate`, `set_location`, `kill_app`, or `open_app`.
- `module_import`
  Use for reusable flows such as login or onboarding.
- `if`, `while`, `extraction`, `code_execution`
  Use when you need conditional logic, variables, or dynamic behavior.

Manual block examples:

```yaml
- type: manual
  step_type: wait
  step_description: "3"

- type: manual
  step_type: navigate
  step_description: myapp://inbox
```

`open_app` is valid, but Revyl opens the app automatically at test start, so using it as the first block is often unnecessary.

## Authoring Rules for Stable Tests

Prefer these patterns:

1. One action per instruction step.
2. Keep assertions in separate `validation` blocks.
3. Validate durable user-visible outcomes instead of transient loading copy.
4. Use variables for credentials and secrets instead of hard-coding them in reusable tests.
5. Keep module imports at the top of the flow when they establish shared setup.

Good:

```yaml
- type: validation
  step_description: The inbox is visible.
- type: instructions
  step_description: Tap Compose.
```

Bad:

```yaml
- type: instructions
  step_description: Verify the inbox is visible and tap Compose.
```

## Reusing Modules

Modules let you keep common flows out of individual tests.

List modules:

```bash
revyl module list
revyl module list --search login
```

Print a ready-to-paste YAML snippet:

```bash
revyl module insert login
```

Example output:

```yaml
- type: module_import
  step_description: "login"
  module_id: "65c5ac48-b980-43c7-a78e-e58b0daf183b"
```

You can paste that into `test.blocks`, or seed a scaffolded test directly:

```bash
revyl test create login-smoke --platform ios --module login
```

Example YAML with a module import:

```yaml
test:
  metadata:
    name: login-smoke
    platform: ios
  build:
    name: ios-test
  blocks:
    - type: module_import
      step_description: login
      module_id: 65c5ac48-b980-43c7-a78e-e58b0daf183b
    - type: validation
      step_description: The inbox is visible.
```

## Troubleshooting

### A test with that name already exists

If create or push fails because the remote name already exists:

- choose a new test name, or
- inspect remote names first with `revyl test remote`, or
- use the scaffold flow when your goal is to reuse an existing remote test name and sync it locally

### `build.name` does not resolve

If push says the app could not be found:

```bash
revyl app list --platform ios
revyl app list --platform android
```

Then update `test.build.name` to match the actual Revyl app name exactly.

### Test shows as stale or `[missing-upstream]`

This usually means the local file or alias points at a remote test ID that no longer exists or is not visible in the current org.

Start with:

```bash
revyl sync --tests --prune
revyl test remote
```

Then re-push the local YAML if needed:

```bash
revyl test push <test-name> --force
```

### Target a non-default backend

Use `REVYL_BACKEND_URL` when you want test creation and push to hit a local, staging, or preview backend:

```bash
REVYL_BACKEND_URL=http://127.0.0.1:8000 revyl test push smoke-login-ios --force
REVYL_BACKEND_URL=http://127.0.0.1:8000 revyl module list
```

Prefer `127.0.0.1` over `localhost` if your machine resolves `localhost` to IPv6 and the backend only listens on IPv4.

If you also need browser-based commands such as `revyl test open` to point at a non-default frontend, set `REVYL_APP_URL` to the matching app host.

## Recommended Loop

For most CLI users, this is the simplest repeatable flow:

```bash
# 1) Author locally
revyl test validate ./my-test.yaml

# 2) Create and bootstrap local state
revyl test create my-test --from-file ./my-test.yaml

# 3) Iterate on the synced file
revyl test push my-test --force

# 4) Run and inspect failures
revyl test run my-test
revyl test report my-test --json
```
