---
name: revyl-cli-dev-loop
description: CLI-first Revyl dev loop for hot reload sessions, exploratory flows, and conversion of successful paths into reusable tests.
---

# Revyl CLI Dev Loop Skill

Use this skill when the user wants a local CLI-driven dev loop instead of MCP tool-by-tool orchestration.

## Primary Loop

```bash
# 1) Initialize project and hot reload settings
revyl init
revyl init --hotreload

# 2) Start local dev loop
revyl dev
```

During exploration:
1. Perform user actions in the app.
2. Capture the exact path that succeeded.
3. Describe each action with explicit target language (for example "Tap Sign In button", "Type into Email field").
3. Convert the path into a test.

## Convert Ad Hoc Flow to Test

```bash
revyl dev test create <test-name> --platform ios
revyl dev test open <test-name>
revyl test push <test-name> --force
revyl test run <test-name>
```

If a run fails:

```bash
revyl test report <test-name> --json
```

## Guardrails

1. Use one user action per instruction step.
2. Keep validations separate from actions.
3. Prefer user-visible outcomes over implementation details.
4. Stop local loop cleanly with `Ctrl+C` when done.
5. Action wording should include what to press/type using clear target descriptions.

## Agent Execution

`revyl dev` is a persistent, never-terminating process (hot-reload server + cloud
device tunnel). Running it with default Shell settings will block the agent forever.

1. **Always background immediately** -- use `block_until_ms: 0`.
2. **Poll for readiness** -- use AwaitShell with pattern `Hot reload ready`
   and a generous timeout (~120 s) to confirm startup succeeded.
3. **Detect failures early** -- if the process exits or output contains
   `Error:` before the ready line, stop and report the error to the user.
4. **Device commands in a separate terminal** -- `revyl device tap`,
   `screenshot`, `type`, and `swipe` are short-lived. Run them in a
   different Shell call, not the dev-loop terminal.
5. **Do not interact with TTY prompts** -- the dev loop prints
   `[r] rebuild native + reinstall` and `[q] quit`. These require a real
   TTY. To rebuild, kill the background process and re-launch instead.
6. **Attaching to an existing dev context** -- if a dev loop is already
   running, use `revyl dev attach <context>` instead of starting a new one.
   This is also long-running; background it the same way and poll for
   `Hot reload ready`. Use `revyl dev list` (short-lived) to discover
   active contexts first.

```
Shell(command="revyl dev start --platform ios", block_until_ms=0)
AwaitShell(pattern="Hot reload ready", block_until_ms=120000)

# Or attach to an existing context
Shell(command="revyl dev list")
Shell(command="revyl dev attach default", block_until_ms=0)
AwaitShell(pattern="Hot reload ready", block_until_ms=120000)

Shell(command="revyl device screenshot")
Shell(command="revyl device tap --target 'Login button'")
```
