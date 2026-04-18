<!-- mintlify
title: "Test Suite at Scale"
description: "Manage a growing test suite with modules, scripts, variables, workflows, and YAML-as-code for your team"
target: cli/journey-test-suite.mdx
-->

# Test Suite at Scale

You have a handful of tests working. Now you want to scale: share common flows, parameterize tests, organize with tags, run groups as workflows, and keep everything in sync across your team.

**This guide covers:** modules, scripts, variables, control flow, workflows, tagging, sync patterns, and YAML-as-code team workflows.

<Callout type="tip" title="Already set up?">
  This guide assumes you've completed [Your First Test](/cli/journey-first-test). You have the CLI installed, authenticated, and at least one test created.
</Callout>

---

## Reusable Modules

Modules are shared groups of test blocks that can be imported into any test. Use them for login flows, onboarding sequences, or setup/teardown routines.

### Create a module

Write a YAML file with a `blocks` array:

```yaml
# modules/login.yaml
blocks:
  - type: instructions
    step_description: Tap Sign In.
  - type: instructions
    step_description: Type "{{email}}" in the email field.
  - type: instructions
    step_description: Type "{{password}}" in the password field.
  - type: instructions
    step_description: Tap Continue.
  - type: validation
    step_description: The home screen is visible.
```

Register it:

```bash
revyl module create login-flow \
  --from-file modules/login.yaml \
  --description "Standard email/password login"
```

### Import into a test

Get a ready-to-paste YAML snippet:

```bash
revyl module insert login-flow
```

```yaml
- type: module_import
  step_description: "login-flow"
  module_id: "65c5ac48-b980-43c7-a78e-e58b0daf183b"
```

Or seed a test with a module at creation time:

```bash
revyl test create login-smoke --platform ios --module login-flow
```

### Manage modules

```bash
revyl module list                               # List all modules
revyl module list --search login                # Filter by name/description
revyl module get login-flow                     # View module blocks
revyl module update login-flow --from-file new.yaml  # Update blocks
revyl module delete login-flow                  # Delete (fails if still imported)
```

---

## Code Execution Scripts

Code execution blocks let you run Python, JavaScript, TypeScript, or Bash code as part of a test. Common uses: seed a database, generate test data, call an API, or clean up state.

### Create and register a script

```python
# scripts/seed_user.py
import requests

response = requests.post("https://api.example.com/test-users", json={
    "email": "test@example.com",
    "password": "TestPass123!",
})

print(f"Created user: {response.json()['id']}")
```

```bash
revyl script create seed-user \
  --file scripts/seed_user.py \
  --runtime python \
  --description "Creates a test user via API"
```

### Use in a test

```yaml
- type: code_execution
  step_description: seed-user
  script_name: seed-user
```

### Inline code (no saved script)

```yaml
- type: code_execution
  step_description: |
    import os
    print(f"Running on: {os.uname().sysname}")
  code_execution_runtime: python
```

### Manage scripts

```bash
revyl script list                           # List all scripts
revyl script get seed-user                  # View details and code
revyl script update seed-user --file new.py # Update the code
revyl script usage seed-user                # List tests using this script
revyl script delete seed-user               # Delete
```

---

## Variables

Variables let you pass dynamic data between steps using Mustache-style `{{variable}}` templates.

### Define variables up front

```yaml
test:
  metadata:
    name: login-flow
    platform: ios
    variables:
      email: test@example.com
      password: TestPass123!
  build:
    name: my-ios-app
  blocks:
    - type: instructions
      step_description: Type "{{email}}" in the email field.
    - type: instructions
      step_description: Type "{{password}}" in the password field.
```

### Extract values at runtime

```yaml
- type: extraction
  step_description: Extract the displayed order number.
  variable_name: order_id

- type: validation
  step_description: The confirmation page shows order "{{order_id}}".
```

### Manage variables via CLI

```bash
revyl test var list login-flow
revyl test var set login-flow email test@new.com
revyl test var get login-flow email
revyl test var delete login-flow email
```

---

## Control Flow

### If / Else

```yaml
- type: if
  step_description: Is a cookie consent banner visible?
  blocks:
    - type: instructions
      step_description: Tap Accept All.
  else_blocks:
    - type: instructions
      step_description: Continue without dismissing.
```

### While loops

```yaml
- type: while
  step_description: Is there a "Load More" button visible?
  blocks:
    - type: instructions
      step_description: Tap Load More.
    - type: manual
      step_type: wait
      step_description: "2"
```

---

## Workflows

A workflow is a named collection of tests that run together. Use workflows to group smoke tests, regression suites, or platform-specific sets.

### Create a workflow

```bash
revyl workflow create smoke-tests
revyl workflow add-tests smoke-tests login-flow checkout-flow search-flow
```

### Run a workflow

The recommended way -- build, upload, and run in one command:

```bash
revyl run smoke-tests -w
```

Or without rebuilding:

```bash
revyl run smoke-tests -w --no-build
```

### Workflow output

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

### Manage workflows

```bash
revyl workflow list                              # List all workflows
revyl workflow info smoke-tests                  # Show details
revyl workflow remove-tests smoke-tests logout   # Remove a test
revyl workflow rename smoke-tests regression     # Rename
revyl workflow delete smoke-tests                # Delete
revyl workflow config smoke-tests --parallelism 3 --retries 2  # Configure
```

### Workflow overrides

Set GPS location or app overrides for all tests in a workflow:

```bash
revyl workflow location smoke-tests set 37.7749,-122.4194
revyl workflow app smoke-tests set --ios <app-id> --android <app-id>
```

---

## Tags

Organize tests by category so you can filter and run subsets.

```bash
revyl tag create smoke
revyl tag create regression
revyl tag add login-flow smoke regression
revyl tag list                          # List all tags with test counts
revyl tag get login-flow                # Tags for a specific test
```

Create tests with tags:

```bash
revyl test create my-test --platform ios --tag smoke --tag ios
```

---

## YAML-as-Code Team Workflow

### Commit `.revyl/tests/` to git

The `.revyl/tests/` directory is your source of truth. Commit it.

```bash
git add .revyl/tests/
git commit -m "Add login-smoke test"
```

### Daily sync pattern

```bash
# Start of day — pull changes made in the browser editor
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

### PR-based test changes

Edit YAML in a feature branch, review the diff in a pull request like any code change. After merge:

```bash
revyl test push --force
```

---

## Full Example: E2E Checkout with Everything

This test combines code execution (DB seeding), module import (login), extraction (order number), conditional flow (promo code), and validation:

```yaml
test:
  metadata:
    name: full-checkout
    platform: ios
    tags:
      - e2e
      - checkout
    variables:
      email: checkout-user@example.com
      password: TestPass123!
  build:
    name: my-ios-app
  blocks:
    - type: code_execution
      step_description: seed-checkout-user
      script_name: seed-checkout-user

    - type: module_import
      step_description: login-flow
      module_id: 65c5ac48-b980-43c7-a78e-e58b0daf183b

    - type: instructions
      step_description: Tap the Shop tab.
    - type: instructions
      step_description: Tap on "Orchid Mantis".
    - type: instructions
      step_description: Tap Add to Cart.

    - type: if
      step_description: Is a promotional banner visible?
      blocks:
        - type: instructions
          step_description: Tap the X to dismiss the banner.

    - type: instructions
      step_description: Tap the Cart tab.
    - type: validation
      step_description: "Orchid Mantis" is listed in the cart.
    - type: instructions
      step_description: Tap Checkout.
    - type: instructions
      step_description: Fill in shipping details and tap Continue.
    - type: instructions
      step_description: Tap Place Order.

    - type: extraction
      step_description: Extract the order confirmation number.
      variable_name: order_number
    - type: validation
      step_description: The confirmation page shows order "{{order_number}}".

    - type: instructions
      step_description: Navigate to Order History.
    - type: validation
      step_description: Order "{{order_number}}" appears in the list.
```

---

## Authoring Best Practices

1. **One action per instruction step.** Don't combine "tap X and type Y" into one block.
2. **Separate validations.** Keep assertions in their own `validation` blocks.
3. **Validate durable outcomes.** Check user-visible state (e.g. "inbox is visible") not transient state (e.g. "loading spinner disappeared").
4. **Use variables for secrets.** Never hardcode credentials in reusable tests.
5. **Put modules at the top.** Shared setup flows belong at the beginning.

---

## What's Next

<CardGroup cols={2}>
  <Card title="CI/CD Pipeline" icon="rotate" href="/cli/journey-ci-cd">
    Run workflows automatically on every pull request
  </Card>
  <Card title="YAML Schema" icon="code" href="/yaml/yaml-schema">
    Full reference for all block types, fields, and control flow
  </Card>
  <Card title="Device Automation" icon="mobile" href="/device/index">
    Programmatic device control via CLI or Python SDK
  </Card>
  <Card title="Full Command Reference" icon="book" href="/cli/reference">
    Every CLI command, flag, and option
  </Card>
</CardGroup>
