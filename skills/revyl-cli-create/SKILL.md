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

For full examples and troubleshooting, see `docs/TEST_CREATION.md`.

If this test comes from a running `revyl dev` session:

```bash
revyl dev test create <test-name> --platform ios
revyl dev test open <test-name>
```

## Conversion Rules

1. One action per instruction step.
2. Keep validation in separate validation steps.
3. Validate user-facing outcomes, not transient loading text.
4. Replace secrets with variables.
5. Use `module_import` blocks for reusable setup like login or onboarding.

Good:

```yaml
- type: validation
  step_description: "The PLACE ORDER button shows total $77.76"
- type: instructions
  step_description: "Tap PLACE ORDER"
```

Bad:

```yaml
- type: instructions
  step_description: "Verify total is $77.76 then tap PLACE ORDER"
```

## Definition of Done

1. Test name communicates intent.
2. Test passes on correct behavior.
3. Test fails on intended regression.
4. Validations are stable across expected data variation.
