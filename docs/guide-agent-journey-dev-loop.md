<!-- mintlify
title: "Agent Journey: Dev Loop"
description: "End-to-end flow from install to agent-driven device verification in revyl dev"
target: cli/agent-journey-dev-loop.mdx
-->

This journey shows a complete path from setup to productive agent-driven verification.

## 1. Install and authenticate

```bash
brew install RevylAI/tap/revyl    # Homebrew (macOS)
pipx install revyl                # pipx (cross-platform)
uv tool install revyl             # uv
pip install revyl                 # pip
```

Then authenticate:

```bash
revyl auth login
revyl auth status
```

## 2. Configure MCP for your coding tool

Use one of these:

1. Codex: `codex mcp add revyl -- revyl mcp serve`
2. Claude Code: `claude mcp add revyl -- revyl mcp serve`
3. Cursor: use the one-click install on [MCP Setup](/cli/mcp-setup) or add `revyl mcp serve` in Cursor MCP settings.

Then verify locally:

```bash
revyl mcp serve
```

## 3. Install the Revyl skill

```bash
revyl skill install
```

By default this installs the CLI skill family.
For MCP-driven agent flows, install MCP skills explicitly:

```bash
revyl skill install --mcp
```

If you want one tool explicitly:

```bash
revyl skill install --codex
revyl skill install --cursor
revyl skill install --claude
revyl skill install --codex --mcp
revyl skill revyl-mcp-dev-loop install --codex
```

Restart your IDE/agent after skill installation.

## 4. Start your dev loop

```bash
revyl init
revyl init --hotreload
git checkout -b feature/new-login
revyl build upload --platform ios-dev
revyl dev
```

When ready, Revyl prints a viewer URL and deep link details.

If you already have a local artifact and want to skip the build command:

```bash
revyl build upload --platform ios-dev --skip-build
revyl dev --platform ios
```

## 5. Give the agent a high-leverage prompt

Use this template:

```text
Use Revyl MCP tools only.
Goal: bypass login and reach the home screen.
For each step: screenshot, briefly describe what is visible, take one best action, then screenshot again to verify.
If anything unexpected happens, re-observe before taking another action.
At the end, summarize final screen, actions taken, and bugs found.
```

## 6. Re-anchor if the agent starts acting blind

```text
Pause actions. Re-anchor now:
1) screenshot,
2) describe current screen,
3) choose one action with reason,
4) screenshot verify.
Then continue.
```

## 7. Close the loop cleanly

1. Stop the dev session with `Ctrl+C` in the `revyl dev` terminal.
2. Ask the agent for a short execution summary.
3. Convert successful ad hoc flows to reusable tests.

Continue with [Agent Journey: Ad Hoc to Test](/cli/agent-journey-adhoc-to-test).
