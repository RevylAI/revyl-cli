# Creating Tests

This guide covers the full test authoring lifecycle: YAML-first creation, reusable modules, code execution scripts, variables, control flow, workflows, and team sync patterns.

## Choose a Workflow

1. **YAML-first CLI (recommended)** -- start from a local YAML file, validate it, create the remote test, and bootstrap `.revyl/config.yaml` automatically.
2. **Scaffold first** -- create an empty or module-seeded remote test with `revyl test create`, sync the generated YAML into `.revyl/tests/`, then edit and push locally.
3. **Dev loop to regression** -- use `revyl dev test create` after an exploratory session, then refine the synced YAML and push it back as a stable regression.

## Prerequisites

- Authenticated with `revyl auth login`
- An app/build available for the target platform
- The correct app name for `test.build.name` (check with `revyl app list`)

---

## YAML-First CLI

### 1. Write a YAML file

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

### 2. Validate

```bash
revyl test validate ./smoke-login-ios.yaml
```

### 3. Create the test

```bash
revyl test create smoke-login-ios --from-file ./smoke-login-ios.yaml
```

### 4. Iterate and push

```bash
# Edit .revyl/tests/smoke-login-ios.yaml, then:
revyl test push smoke-login-ios --force
revyl test run smoke-login-ios
```

## Scaffold First

```bash
revyl test create smoke-login-ios --platform ios
revyl test create smoke-login-ios --platform ios --module login  # Seed with a module
```

Then edit `.revyl/tests/smoke-login-ios.yaml` and push.

---

## YAML Anatomy

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

Common block types:

| Type | Usage |
|------|-------|
| `instructions` | A single user action |
| `validation` | An assertion about expected state |
| `manual` | Framework-level actions: `wait`, `go_home`, `navigate`, `set_location`, `kill_app`, `open_app` |
| `module_import` | Import a reusable module |
| `if` / `while` | Conditional logic and loops |
| `extraction` | Extract data from the screen into a variable |
| `code_execution` | Run Python/JS/TS/Bash code |

---

## Reusable Modules

Modules are shared groups of test blocks that can be imported into any test.

### Create a module

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

```bash
revyl module create login-flow \
  --from-file modules/login.yaml \
  --description "Standard email/password login"
```

### Import into a test

```bash
revyl module insert login-flow    # Prints a ready-to-paste YAML snippet
```

```yaml
- type: module_import
  step_description: "login-flow"
  module_id: "65c5ac48-b980-43c7-a78e-e58b0daf183b"
```

### Manage modules

```bash
revyl module list                               # List all modules
revyl module list --search login                # Filter by name/description
revyl module get login-flow                     # View module blocks
revyl module update login-flow --from-file new.yaml
revyl module delete login-flow                  # Delete (fails if still imported)
```

---

## Code Execution Scripts

Code execution blocks let you run Python, JavaScript, TypeScript, or Bash code as part of a test.

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
revyl script delete seed-user
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

### While Loops

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

A workflow is a named collection of tests that run together.

### Create and manage

```bash
revyl workflow create smoke-tests
revyl workflow add-tests smoke-tests login-flow checkout-flow search-flow
revyl run smoke-tests -w                       # Build, upload, and run
revyl run smoke-tests -w --no-build            # Run without rebuilding
```

### Manage workflows

```bash
revyl workflow list
revyl workflow info smoke-tests
revyl workflow remove-tests smoke-tests logout
revyl workflow delete smoke-tests
revyl workflow config smoke-tests --parallelism 3 --retries 2
```

### Tags

```bash
revyl tag create smoke
revyl tag add login-flow smoke regression
revyl tag list
revyl test create my-test --platform ios --tag smoke --tag ios
```

---

## Authoring Best Practices

1. **One action per instruction step.** Don't combine "tap X and type Y" into one block.
2. **Separate validations.** Keep assertions in their own `validation` blocks.
3. **Validate durable outcomes.** Check user-visible state (e.g. "inbox is visible") not transient state (e.g. "loading spinner disappeared").
4. **Use variables for secrets.** Never hardcode credentials in reusable tests.
5. **Put modules at the top.** Shared setup flows belong at the beginning.

---

## Full Example: E2E Checkout

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

## Troubleshooting

| Problem | Fix |
|---------|-----|
| "A test with that name already exists" | Choose a new name, or use `revyl test remote` to inspect existing tests |
| `build.name` does not resolve | Run `revyl app list --platform ios` and update `test.build.name` to match |
| Test shows as stale | Run `revyl sync --tests --prune` then `revyl test push <name> --force` |

---

## What's Next

- [Running Tests](running-tests.md) -- execution flags, workflows, JSON output
- [YAML Schema](/yaml/yaml-schema) -- full block type reference
- [CI/CD Pipeline](../ci-cd.md) -- run tests in GitHub Actions
