<!-- mintlify
title: "Device Automation"
description: "Start, control, and debug Revyl cloud devices with CLI, Python, or AI agents"
target: device/index.mdx
-->

This section is the canonical home for Revyl device automation docs.

If you are new, start with [Device Quickstart](/device/quickstart).

## Choose Your Path

<CardGroup cols={2}>
  <Card title="Device Quickstart" icon="rocket" href="/device/quickstart">
    Learn the core session and action loop in minutes.
  </Card>
  <Card title="Device Scripting Guide" icon="code" href="/device/scripting-guide">
    Control sessions and actions from Python scripts.
  </Card>
  <Card title="CLI Device Commands" icon="terminal" href="/device/cli-commands">
    Use `revyl device` directly from your terminal.
  </Card>
  <Card title="Troubleshooting" icon="screwdriver-wrench" href="/device/troubleshooting">
    Fix session, install, grounding, and action issues quickly.
  </Card>
</CardGroup>

## When To Use What

| Goal | Best Entry Point |
|------|------------------|
| First end-to-end run on a cloud device | [Device Quickstart](/device/quickstart) |
| Scripted device actions in Python | [Device Scripting Guide](/device/scripting-guide) |
| Full command-level control | [CLI Device Commands](/device/cli-commands) |
| Agent-driven device control from IDE | [MCP Setup](/cli/mcp-setup) |
| CI orchestration of test and workflow runs | [API Quickstart](/api-reference/quickstart) |

## Core Ergonomics Loop

Whether you use CLI, SDK, or MCP, use the same reliable loop:

1. Take a screenshot or otherwise re-observe current state.
2. Choose one best action.
3. Verify the result immediately.
4. Repeat.

This avoids blind action chains and makes failures easier to debug.
