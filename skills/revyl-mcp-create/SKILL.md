---
name: revyl-mcp-create
description: Create and maintain Revyl tests through MCP tools using YAML validation, create/update operations, and execution feedback loops.
---

# Revyl MCP Create Skill

Use this skill when tests should be authored and managed through MCP tools.

## Core MCP Flow

1. Build YAML from ordered instructions and validations.
2. Validate YAML:
   - `validate_yaml(content="...")`
3. Create or update:
   - `create_test(name="...", platform="ios", yaml_content="...")`
   - `update_test(test_name_or_id="...", yaml_content="...", force=true)`
4. Execute and inspect:
   - `run_test(test_name="...")`
   - `get_test_status(task_id="...")`

## Authoring Rules

1. One action per instruction step.
2. Keep validations separate from instructions.
3. Validate user-visible outcomes.
4. Use variables for sensitive or dynamic values.

## Shared Setup Reuse

If shared steps appear in 3+ tests:
1. `list_modules()`
2. `insert_module_block(module_name_or_id="...")`
3. update test YAML to import module.

