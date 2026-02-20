---
name: revyl-mcp-dev-loop
description: MCP dev-first mobile loop for reliable screenshot-observe-action execution, grounded interactions, and conversion of successful exploratory paths into tests.
---

# Revyl MCP Dev Loop Skill

Use this skill for the full flow:
1. Start dev loop equivalent.
2. Execute screenshot-observe-action cycles.
3. Convert successful paths into reusable tests.

## Default Operating Mode

Always prefer dev-loop flow before plain device-only flows:
1. Call `start_dev_loop`.
2. Confirm session is active.
3. Call `screenshot()` and begin interaction.

Fallback to plain device session only when dev loop is unavailable.

## Execution Guardrails

1. First tool call must be `start_dev_loop`.
2. Do not call listing tools unless the user explicitly asks.
3. Treat `next_steps` as advisory only.
4. Re-anchor with `screenshot()` before state-dependent actions.
5. For taps/types/swipes, use `target` with natural language descriptions (for example `target="Sign In button"`); use raw `x,y` only as fallback.

## Interaction Loop

For each iteration:
1. `screenshot()`
2. State visible UI in one short line.
3. Take one best action and describe what to press/type using `target`.
4. `screenshot()` to verify.
5. Repeat.

Short deterministic burst allowance:
- Up to two actions before verification for obvious two-step entry flows.

## Ad Hoc to Test Conversion (MCP)

When a flow succeeds:
1. capture test name, preconditions, instructions, validations, variables.
2. run:
   - `validate_yaml(content="...")`
   - `create_test(name="...", platform="ios", yaml_content="...")`
   - `run_test(test_name="...")`
   - `get_test_status(task_id="...")`
