---
name: revyl-cli
description: Base CLI skill for Revyl command-driven workflows. Use when users want shell-command setup, execution, test authoring, or run triage without MCP tool calls.
---

# Revyl CLI Skill

Use this as the default Revyl skill when workflows should be expressed as `revyl` commands.

## Route to Specific CLI Skills

- Use `revyl-cli-dev-loop` for local dev loop workflows and exploratory path capture.
- Use `revyl-cli-create` for authoring robust YAML tests.
- Use `revyl-cli-analyze` for failed run triage.

## Operating Rules

1. Prefer explicit command sequences.
2. Keep secrets in env vars or test variables.
3. Keep steps deterministic and avoid hidden assumptions.

## Baseline Checks

```bash
export PATH="$HOME/.revyl/bin:$HOME/.local/bin:$PATH"
revyl auth status
revyl version
revyl test list
```

For headless agents, set `REVYL_API_KEY` and run:

```bash
revyl auth login --api-key "$REVYL_API_KEY"
```
