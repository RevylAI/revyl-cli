---
name: revyl-cli-create
description: Create robust Revyl E2E tests using CLI commands from app source analysis or exploratory sessions.
---

# Revyl CLI Test Authoring Skill

## Quick Start

```bash
# 1) Create test skeleton
revyl test create <test-name> --platform ios

# 2) Open local YAML and write stable steps
revyl test open <test-name>

# 3) Push and run
revyl test push <test-name> --force
revyl test run <test-name>

# 4) Pull report when failure happens
revyl test report <test-name> --json
```

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

