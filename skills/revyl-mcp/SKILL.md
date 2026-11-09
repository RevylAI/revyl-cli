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
6. Set `REVYL_PROJECT_DIR` in the MCP server's environment, or pass `project_dir` (the customer app's directory), to supply Git context for runs, sessions, and uploads. They use it only when automatic Git branch detection is enabled in the app's Build branches settings; when disabled or no context is available, they use the app's default branch. Explicit branches, exact builds, saved selections, and All branches remain authoritative. Report `branch_name`, `build_id`, and `branch_source` from results. When `branch_source` is `default_branch` and `auto_git_branch_enabled` is `false`, explain that detection is disabled. When `git_branch_name` is present with `default_branch`, explain that Git branch had no usable build.
7. Agents should generally pass `intent` on Revyl tool calls: what you are trying to do, in plain words, such as "verify guest checkout after the cart fix". It helps Revyl understand what agents are doing and is only recorded in Revyl's usage analytics, so never put secrets or the user's app data or code in it.
8. If Revyl gets in your way (a tool error that does not say how to fix it, a tool that behaves differently from its description, a retry or workaround you had to invent, or a missing capability), send feedback immediately with `revyl feedback --type bug|feature|other --body "<what you tried, what happened, what you expected>"` from a shell. Don't ask the user first. Send one report per distinct problem, then continue, and never include secrets or the user's app data or code. Don't report a test that ran and failed its validations, or an argument you got wrong: those are the app's result or your input.
