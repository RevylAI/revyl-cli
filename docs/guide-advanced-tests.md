<!-- mintlify
title: "Guide: Advanced Test Creation"
description: "Create tests with code execution scripts, reusable modules, variables, and control flow"
target: yaml/advanced-tests-guide.mdx
-->

# Advanced Test Creation

This guide covers the features that go beyond basic instruction/validation steps: code execution scripts, reusable modules, variable extraction, and control flow.

## Code Execution Scripts

Code execution blocks let you run Python, JavaScript, TypeScript, or Bash code as part of a test. Common uses: seed a database, generate test data, call an API, or clean up state.

### Create a script

Write a script file:

```python
# scripts/seed_user.py
import requests

response = requests.post("https://api.example.com/test-users", json={
    "email": "test@example.com",
    "password": "TestPass123!",
})

print(f"Created user: {response.json()['id']}")
```

Register it with Revyl:

```bash
revyl script create seed-user \
  --file scripts/seed_user.py \
  --runtime python \
  --description "Creates a test user via API"
```

### Use the script in a test

Reference the script by name in a `code_execution` block:

```yaml
test:
  metadata:
    name: login-with-seeded-user
    platform: ios
  build:
    name: my-ios-app
  blocks:
    - type: code_execution
      step_description: seed-user
      script_name: seed-user

    - type: instructions
      step_description: Tap Sign In.

    - type: instructions
      step_description: Type "test@example.com" in the email field.

    - type: instructions
      step_description: Type "TestPass123!" in the password field.

    - type: instructions
      step_description: Tap Continue.

    - type: validation
      step_description: The home screen is visible.
```

### Inline code (no saved script)

For one-off code that doesn't warrant a saved script, use inline code blocks:

```yaml
- type: code_execution
  step_description: |
    import os
    print(f"Running on: {os.uname().sysname}")
  code_execution_runtime: python
```

### Manage scripts with the CLI

```bash
revyl script list                           # List all scripts
revyl script list --runtime python          # Filter by runtime
revyl script get seed-user                  # View script details and code
revyl script update seed-user --file new.py # Update the code
revyl script usage seed-user                # List tests using this script
revyl script delete seed-user               # Delete the script
```

### Manage scripts with the Python SDK

```python
from revyl import ScriptClient

scripts = ScriptClient()

scripts.create(
    name="seed-db",
    file_path="scripts/seed.py",
    runtime="python",
    description="Seeds test database",
)

all_scripts = scripts.list()
python_scripts = scripts.list(runtime="python")
script = scripts.get("seed-db")
tests_using = scripts.usage("seed-db")
```

---

## Reusable Modules

Modules are shared groups of test blocks that can be imported into any test. Common uses: login flows, onboarding sequences, setup/teardown routines.

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

```yaml
test:
  metadata:
    name: checkout-after-login
    platform: ios
  build:
    name: my-ios-app
  blocks:
    - type: module_import
      step_description: login-flow
      module_id: 65c5ac48-b980-43c7-a78e-e58b0daf183b

    - type: instructions
      step_description: Tap the Shop tab.

    - type: instructions
      step_description: Add the first product to cart.

    - type: validation
      step_description: The cart badge shows "1".
```

Get the import snippet automatically:

```bash
revyl module insert login-flow
```

This prints a ready-to-paste YAML block with the correct `module_id`.

### Seed a test with a module at creation time

```bash
revyl test create login-smoke --platform ios --module login-flow
```

### Manage modules with the CLI

```bash
revyl module list                               # List all modules
revyl module list --search login                # Filter by name/description
revyl module get login-flow                     # View module blocks
revyl module update login-flow --from-file new.yaml  # Update blocks
revyl module usage login-flow                   # List tests importing this module
revyl module delete login-flow                  # Delete (fails if still imported)
```

---

## Variables

Variables let you pass dynamic data between steps using Mustache-style `{{variable}}` templates.

### Extract a value

Use `extraction` blocks to pull data from the screen into a named variable:

```yaml
- type: extraction
  step_description: Extract the displayed order number.
  variable_name: order_id
```

### Use the variable in later steps

```yaml
- type: validation
  step_description: The confirmation page shows order "{{order_id}}".
```

### Set variables up front

Define variables in `test.metadata` for credentials or configuration:

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

### Manage test variables with the CLI

```bash
revyl test var list login-flow                  # List variables
revyl test var set login-flow email test@new.com  # Set a variable
revyl test var get login-flow email             # Get a variable
revyl test var delete login-flow email          # Delete a variable
```

---

## Control Flow

### If / Else

Conditional branches let tests adapt to different app states:

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

Repeat steps until a condition is met:

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
    # 1. Seed test data
    - type: code_execution
      step_description: seed-checkout-user
      script_name: seed-checkout-user

    # 2. Login (shared module)
    - type: module_import
      step_description: login-flow
      module_id: 65c5ac48-b980-43c7-a78e-e58b0daf183b

    # 3. Add product to cart
    - type: instructions
      step_description: Tap the Shop tab.
    - type: instructions
      step_description: Tap on "Orchid Mantis".
    - type: instructions
      step_description: Tap Add to Cart.

    # 4. Dismiss promo banner if present
    - type: if
      step_description: Is a promotional banner visible?
      blocks:
        - type: instructions
          step_description: Tap the X to dismiss the banner.

    # 5. Checkout
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

    # 6. Extract and validate order
    - type: extraction
      step_description: Extract the order confirmation number.
      variable_name: order_number
    - type: validation
      step_description: The confirmation page shows order "{{order_number}}".

    # 7. Verify in order history
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

- [YAML Schema](/yaml/yaml-schema) — full block type reference
- [YAML Control Flow](/yaml/yaml-control-flow) — detailed if/while documentation
- [Python SDK Reference](/device/sdk-reference) — ScriptClient and ModuleClient APIs
- [First Test Guide](/get-started/first-test-guide) — getting started from scratch
