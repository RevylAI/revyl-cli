---
name: revyl-mcp-create
description: Create and maintain Revyl tests through MCP tools using create/update operations and execution feedback loops.
metadata:
  internal: true
---

# Revyl MCP Create Skill

Use this skill when tests should be authored and managed through MCP tools.

## Core MCP Flow

1. Build YAML from ordered instructions and validations.
2. Create or update:
   - `create_test(name="...", platform="ios", yaml_content="...")`
   - `update_test(test_name_or_id="...", yaml_content="...", force=true)`
3. Execute and inspect:
   - `run_test(test_name="...")`
   - `get_test_status(task_id="...")`

## Authoring Rules

1. One action per instruction step.
2. Keep validations separate from instructions.
3. Validate user-visible outcomes.
4. Use variables for sensitive or dynamic values.

## Canonical YAML

Use the backend YAML contract returned by `get_schema()`.

```yaml
- type: instructions
  step_description: "Tap Log in."

- type: validation
  step_description: "The dashboard is visible."

- type: code_execution
  script: "seed-user"
  variable_name: seeded_user_id

- type: module_import
  module: "login-flow"

- type: if
  condition: "Is a cookie banner visible?"
  then:
    - type: instructions
      step_description: "Tap Accept."
  else:
    - type: instructions
      step_description: "Continue."

- type: while
  condition: "Is there a Load More button visible?"
  body:
    - type: instructions
      step_description: "Tap Load More."
```

For new authored YAML, use `script` for code execution and `module` for module imports. Legacy UUID forms may still parse for compatibility, but do not generate them.

## Shared Setup Reuse

If shared steps appear in 3+ tests:
1. `list_modules()`
2. `insert_module_block(module_name_or_id="...")`
3. update test YAML to import module.
