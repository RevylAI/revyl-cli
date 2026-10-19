---
name: revyl-mcp
description: Base MCP skill for Revyl tool-call orchestration. Use when users want direct MCP execution instead of shell commands.
metadata:
  internal: true
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
5. When more than one device session may be live, including parallel agents sharing one MCP server, pass the `session_id` returned by `start_device_session` to every device tool. It never falls back to the active session.
6. Set `REVYL_PROJECT_DIR` in the MCP server's environment, or pass `project_dir` (the customer app's directory), so runs, sessions, and uploads follow the project's Git branch. Without either, they use the app's default branch. Report `branch_name`, `build_id`, and `branch_source` from results; when `branch_source` is `default_branch` and `git_branch_name` is present, say that Git branch had no usable build and the app's default branch ran.

