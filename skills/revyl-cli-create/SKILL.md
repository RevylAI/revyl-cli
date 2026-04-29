---
name: revyl-cli-create
description: Create robust Revyl E2E tests using CLI commands from app source analysis or exploratory sessions.
---

# Revyl CLI Test Authoring Skill

## End-to-End Authoring Loop

```bash
# 1) Confirm auth and target app/build
revyl auth status
revyl app list --platform <ios|android>

# 2) Author YAML locally, then validate it
revyl test validate ./<test-name>.yaml

# 3) Create from YAML (bootstraps .revyl/tests/ and config)
revyl test create <test-name> --from-file ./<test-name>.yaml

# 4) Iterate on .revyl/tests/<test-name>.yaml, then push and run
revyl test push <test-name> --force
revyl test run <test-name>

# 5) Inspect results and refine
revyl test status <test-name>
revyl test report <test-name> --json
```

YAML-first bootstrap works without an existing `.revyl/config.yaml`:

```bash
revyl test create <test-name> --from-file ./test.yaml
```

The CLI validates the YAML, copies it into `.revyl/tests/`, pushes it, and writes `.revyl/config.yaml` after the remote test is created.

If you prefer to scaffold first:

```bash
revyl test create <test-name> --platform ios --no-open
# edit .revyl/tests/<test-name>.yaml
revyl test push <test-name> --force
```

For full examples and troubleshooting, see `docs/tests/creating-tests.md`.

If this test comes from a running `revyl dev` session:

```bash
revyl dev test create <test-name> --platform ios
revyl dev test open <test-name>
```

## Tool Map

- Tests: `revyl test validate`, `create`, `push`, `run`, `report`, `status`, and `history`.
- Modules: `revyl module create/list/get/update/usage/insert` for reusable block groups.
- Scripts: `revyl script create/list/get/update/usage/insert` for `code_execution` blocks.
- Variables: `test.variables`, `revyl test var`, `revyl global var`, `extraction.variable_name`, and `code_execution.variable_name`.
- Launch env: `revyl global launch-var` plus repeated `--launch-var` only when app startup needs environment config.
- Grouping: `revyl workflow` and `revyl tag` after individual tests are stable.

## YAML Building Blocks

Start from the smallest complete test:

```yaml
test:
  metadata:
    name: smoke-login-ios
    platform: ios
    tags:
      - smoke
  build:
    name: ios-test
  variables:
    email: "{{global.login-email}}"
  blocks:
    - type: instructions
      step_description: "Sign in with {{email}}."
    - type: validation
      step_description: "The home screen is visible."
```

Use these block types:

- `instructions`: one meaningful user intent.
- `validation`: a durable assertion about user-visible state.
- `manual`: framework actions such as `wait`, `go_home`, `navigate`, `set_location`, `kill_app`, and `open_app`.
- `extraction`: read screen data into `variable_name`.
- `code_execution`: run a saved script or lightweight inline code.
- `module_import`: import a reusable module by name or ID.
- `if` / `while`: conditional branches and loops with nested blocks.

## Variables and Secrets

- Local YAML variables go under `test.variables` and are referenced as `{{variable-name}}` or `{{variable_name}}`.
- Extracted values and code execution output become variables when the block has `variable_name`.
- Test-scoped variables can be managed after creation with `revyl test var set/list/get/delete`.
- Org-level secrets use `revyl global var set name=value` and are referenced as `{{global.name}}`.
- Define or extract variables before use. Never hardcode secrets in reusable YAML or modules.

```bash
revyl test var set <test-name> email=test@example.com
revyl global var set login-password='secret'
revyl global launch-var create API_URL=https://staging.example.com
```

## Code Execution

Prefer saved scripts for setup, API seeding, backend assertions, or reusable logic:

```bash
revyl script create seed-user --file scripts/seed_user.py --runtime python
revyl script insert seed-user
revyl script usage seed-user
```

Use the snippet from `revyl script insert`, or write the block directly:

```yaml
- type: code_execution
  step_description: "seed-user"
  script: "seed-user"
  variable_name: seeded_user_id
```

Use inline code only for small one-offs:

```yaml
- type: code_execution
  step_description: |
    print("ready")
  code_execution_runtime: python
```

## Reusable Modules

Use modules for stable shared setup like login, onboarding, account creation, or checkout prep.

```yaml
# modules/login.yaml
blocks:
  - type: instructions
    step_description: "Sign in with {{email}} and {{global.login-password}}."
  - type: validation
    step_description: "The home screen is visible."
```

```bash
revyl module create login-flow --from-file modules/login.yaml --description "Standard login"
revyl module insert login-flow
revyl module usage login-flow
```

Import with the snippet from `revyl module insert`:

```yaml
- type: module_import
  step_description: "login-flow"
  module_id: "65c5ac48-b980-43c7-a78e-e58b0daf183b"
```

## Full Flow Example

```yaml
test:
  metadata:
    name: checkout-e2e-ios
    platform: ios
    tags:
      - checkout
      - e2e
  build:
    name: ios-test
  variables:
    email: checkout-user@example.com
    product-name: Orchid Mantis
  blocks:
    - type: code_execution
      step_description: "seed-checkout-user"
      script: "seed-checkout-user"
      variable_name: seeded_user_id

    - type: module_import
      step_description: "login-flow"
      module_id: "65c5ac48-b980-43c7-a78e-e58b0daf183b"

    - type: instructions
      step_description: "Complete checkout for {{product-name}} using the saved shipping address."
    - type: extraction
      step_description: "Extract the order confirmation number."
      variable_name: order_number
    - type: validation
      step_description: "The confirmation page shows order {{order_number}}."
```

## Conversion Rules

1. Write instruction steps at the level of a meaningful user intent, not every tiny tap or keystroke.
2. Prefer one free-form instruction followed by one validation for the important outcome it should produce.
3. Add validations at stable checkpoints and final outcomes. Do not validate after every small interaction.
4. Keep validations in separate `validation` blocks from the instruction that caused the state change.
5. Validate durable user-facing behavior, not transient loading text, animations, timing artifacts, or implementation details.
6. Replace secrets with variables.
7. Use `module_import` blocks for reusable setup like login or onboarding.
8. Use `code_execution` for API setup, data seeding, backend checks, and deterministic helper logic.

Good:

```yaml
- type: instructions
  step_description: "Complete checkout for Orchid Mantis using the saved shipping address."
- type: validation
  step_description: "The confirmation screen shows an order number."
```

Bad:

```yaml
- type: instructions
  step_description: "Tap Cart, verify the total, tap Checkout, verify the shipping form, enter the address, tap Continue, verify payment, then place the order."
- type: validation
  step_description: "The Cart tab is visible."
- type: validation
  step_description: "The Checkout button is visible."
- type: validation
  step_description: "The payment form is visible."
```

## Definition of Done

1. Test name communicates intent.
2. Test passes on correct behavior.
3. Test fails on intended regression.
4. Validations are stable across expected data variation.
5. Variables, scripts, modules, launch vars, tags, and workflows are created only when the test actually needs them.
