---
name: revyl-cli
description: Base CLI skill for Revyl command-driven workflows. Use when users want shell-command setup, Atlas understanding, execution, test authoring, or run triage without MCP tool calls.
---

# Revyl CLI Skill

Use this as the default Revyl skill when workflows should be expressed as `revyl` commands.

## Native Agent Behavior

- Ask at most 1-3 concise clarification questions only when the target app, platform, session, URL, or sensitive action cannot be inferred from the repo or Revyl CLI.
- Prefer safe defaults and keep moving when `revyl init --detect`, `revyl dev list`, `revyl app list`, screenshots, or reports can answer the question.
- When Revyl prints a viewer, editor, report, or local app URL, open it in the native browser/tool surface when available: Codex Browser/in-app browser for local URLs, Revyl URLs, screenshots, and page checks; Claude Code `.claude/skills` slash-command discovery plus WebFetch/WebSearch or configured MCP/browser tools; Cursor `.cursor/skills` plus `.cursor/rules/revyl-skills.mdc` and available MCP/browser tools.
- If no browser tool is exposed, report the URL and verify through `revyl device screenshot`, `revyl device report`, or `revyl test report` instead of claiming browser access.
- A screenshot URL or local image path is not visual verification. Open and read the image before making claims about visible UI.
- Confirm before entering sensitive data, submitting forms, uploading files, accepting browser permissions, changing sharing/access, or deleting data.

## Route to Specific CLI Skills

- Use `revyl-cli-dev-loop` for local dev loop workflows and exploratory path capture.
- Use `revyl-cli-atlas` for media-grounded questions about an app's screens, graph relationships, journeys, or coverage, including reconciling unexpected evidence through clips, extracted frames, and originating reports.
- Use `revyl-cli-atlas-review` only when the user explicitly requests an Atlas feedback mutation.
- Use `revyl-cli-create` for authoring robust YAML tests.
- Use `revyl-cli-auth-bypass` for test-only authenticated app state setup.
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

When `revyl auth status` reports no credential:

```bash
# Headless agent with REVYL_API_KEY in the environment. Preferred: the key
# never reaches argv or the process list.
revyl auth persist-cloud-env

# A human is available: prints an approval URL to open in any browser.
revyl auth login
```

`revyl auth login --api-key=$REVYL_API_KEY` also works, but the value must use
the `=` form; a space-separated value is ignored and the CLI prompts instead.
