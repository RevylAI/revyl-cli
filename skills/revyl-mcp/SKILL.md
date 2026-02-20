---
name: revyl-mcp
description: Base MCP skill for Revyl tool-call orchestration. Use when users want direct MCP execution instead of shell commands.
---

# Revyl MCP Skill

Use this skill when execution should happen through Revyl MCP tools.

## Route to Specific MCP Skills

- Use `revyl-mcp-dev-loop` for live app interaction loops.
- Use `revyl-mcp-create` for test authoring and update flows.
- Use `revyl-mcp-analyze` for execution triage.

## Operating Rules

1. Use MCP tools only.
2. Re-anchor frequently with `screenshot()` before stateful actions.
3. Prefer one action per loop iteration unless the sequence is trivial and deterministic.
4. If the user asks for shell-command guidance, switch to the `revyl-cli` skill family.

