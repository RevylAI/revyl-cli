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
