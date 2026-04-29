---
name: revyl-cli-create
description: Create robust Revyl E2E tests using CLI commands from app source analysis or exploratory sessions.
---

# Revyl CLI Test Authoring Skill

## Quick Start

```bash
# 1) Author and validate YAML locally
revyl test validate ./<test-name>.yaml

# 2) Create from YAML (bootstraps .revyl/tests/ and config)
revyl test create <test-name> --from-file ./<test-name>.yaml

# 3) Iterate on .revyl/tests/<test-name>.yaml, then push and run
revyl test push <test-name> --force
revyl test run <test-name>

# 4) Pull report when failure happens
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

## Conversion Rules

1. Write instruction steps at the level of a meaningful user intent, not every tiny tap or keystroke.
2. Prefer one free-form instruction followed by one validation for the important outcome it should produce.
3. Add validations at stable checkpoints and final outcomes. Do not validate after every small interaction.
4. Keep validations in separate `validation` blocks from the instruction that caused the state change.
5. Validate durable user-facing behavior, not transient loading text, animations, timing artifacts, or implementation details.
6. Replace secrets with variables.
7. Use `module_import` blocks for reusable setup like login or onboarding.

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
