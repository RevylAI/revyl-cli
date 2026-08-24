---
name: revyl-cli-create
description: Create robust Revyl E2E tests using CLI commands from app source analysis or exploratory sessions.
---

# Revyl CLI Test Authoring Skill

## Native Agent Behavior

- Ask at most 1-3 concise clarification questions only when the target app, platform, session, URL, or sensitive action cannot be inferred from the repo or Revyl CLI.
- Prefer safe defaults and keep moving when `revyl init --detect`, `revyl app list`, `revyl test list`, screenshots, or reports can answer the question.
- When Revyl prints an editor, report, viewer, or local app URL, open it in the native browser/tool surface when available: Codex Browser/in-app browser for local URLs, Revyl editor/report URLs, screenshots, and page checks; Claude Code `.claude/skills` slash-command discovery plus WebFetch/WebSearch or configured MCP/browser tools; Cursor `.cursor/skills` plus `.cursor/rules/revyl-skills.mdc` and available MCP/browser tools.
- If no browser tool is exposed, report the URL and verify through `revyl test report`, `revyl device screenshot`, or `revyl device report` instead of claiming browser access.
- Confirm before entering sensitive data, submitting forms, uploading files, accepting browser permissions, changing sharing/access, or deleting data.

## End-to-End Authoring Loop

```bash
# 1) Confirm auth and target app/build
revyl auth status
revyl app list --platform <ios|android>

# 2) Establish project config (choose the applicable flow)
revyl config pull                          # Existing registered project
# revyl init -y                            # New local project
# revyl config migrate --check             # Legacy config: inspect first
# revyl config migrate                     # Legacy config: then convert

# 3) Create from YAML
revyl test create <test-name> --from-file ./<test-name>.yaml

# 4) Iterate on .revyl/tests/<test-name>.yaml, then push and run
revyl test push <test-name> --force
revyl test run <test-name>

# 5) Inspect results and refine
revyl test status <test-name>
revyl test report <test-name> --json
```

YAML-first creation requires the selected project to already have a valid
`.revyl/config.yaml`:

```bash
revyl test create <test-name> --from-file ./test.yaml
```

The CLI checks the YAML with backend validation, copies it into
`.revyl/tests/`, and pushes it. It never creates or migrates project
configuration.

If you prefer to scaffold first:

```bash
revyl test create <test-name> --platform ios --no-open
# edit .revyl/tests/<test-name>.yaml
revyl test push <test-name> --force
```

If you have a completed exploratory device session:

```bash
revyl test create --from-session <session-id> <test-name> --app <app-id>
revyl test run <test-name>
```

The name can be omitted; the compiler will use its suggested session title.

For full examples and troubleshooting, see `docs/tests/creating-tests.md`.

If this test comes from a running `revyl dev` session:

```bash
revyl device screenshot --out /tmp/revyl-current.png
revyl device report --session-id <session-id> --json
```

Then author YAML explicitly and create it with `revyl test create --from-file`. Put repeated setup such as login, onboarding, or seed data in a reusable module/setup block so feature tests do not duplicate it.

## Tool Map

- Tests: `revyl test create`, `push`, `run`, `report`, `status`, and `history`.
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
- Org-level secrets use `revyl global var set name=value --secret` and are referenced as `{{global.name}}`.
- Define or extract variables before use. Never hardcode secrets in reusable YAML or modules.

```bash
revyl test var set <test-name> email=test@example.com
revyl global var set login-password='secret' --secret
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
  script: "seed-user"
  variable_name: seeded_user_id
```

Legacy YAML may use `step_description` to hold an internal script UUID, but new authored YAML should use `script`.

Use inline code only for small one-offs when a saved script would be unnecessary:

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
  module: "login-flow"
```

Legacy YAML may use `module_id` to hold an internal module UUID, but new authored YAML should use `module`.

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
      script: "seed-checkout-user"
      variable_name: seeded_user_id

    - type: module_import
      module: "login-flow"

    - type: instructions
      step_description: "Complete checkout for {{product-name}} using the saved shipping address."
    - type: extraction
      step_description: "Extract the order confirmation number."
      variable_name: order_number
    - type: validation
      step_description: "The confirmation page shows order {{order_number}}."
```

## Conversion Rules

1. One action per instruction step.
2. Keep validation in separate validation steps.
3. Validate user-facing outcomes, not transient loading text.
4. Replace secrets with variables or global variables.
5. Use `module_import` blocks for reusable setup like login or onboarding.
6. Preserve the exact target language that worked in the device report when it is more specific than a generic tap.
7. Use `code_execution` for API setup, data seeding, backend checks, and deterministic helper logic.

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
