---
name: revyl-status
description: Report Revyl readiness for this workspace without spending anything - pinned CLI, sign-in, project configuration, GitHub and pull-request builds, hosted tool connection, and running sessions or dev loops.
disable-model-invocation: true
---

# Revyl status

Report where this workspace stands with Revyl. This command only reads: it
never signs in, writes files, connects GitHub, starts or stops anything, or
uses device or build time.

## Launcher and links

Run every Revyl command through this plugin's pinned launcher, never `revyl`
from `PATH` or another installed CLI. Resolve it from this skill's own
directory:

- **Claude Code** substitutes that directory here: `${CLAUDE_SKILL_DIR}`.
- **Codex** lists the path of this `SKILL.md`; use its absolute parent
  directory. If the Claude Code value above isn't an absolute path, use this
  rule instead.

The launcher is `../../scripts/launch-revyl` from that directory on POSIX and
`../../scripts/launch-revyl.cmd` on Windows. Shell variables don't persist
between tool calls in either host, so write the resolved absolute path into
each command; below, `"$LAUNCHER"` stands for it. Run each command from the app
directory (the one holding `.revyl/config.yaml`), never from the plugin's own
folder: the CLI reads the current directory's Git branch, commit, and config,
so a command run inside the plugin resolves the wrong build or project. The
launcher detects the host itself; never set `REVYL_PLUGIN_HOST`. If a command
needs sign-in, run `"$LAUNCHER" auth login` and hand the user its approval
link. Never print or ask for credentials.

Never open a browser or claim one opened. Give each sign-in, viewer, or report
URL to the user as soon as you have it:

- **Claude Code**: the full `https://` URL on its own line.
- **Codex**: a Markdown link, such as `[Open the Revyl device](URL)`.

## Hosted tools first, CLI for the workspace

This plugin bundles Revyl's hosted `revyl-workspace` MCP tools. Use them first
for looking up builds and for device work: `list_apps`, `list_builds`,
`get_build`, `start_device_session`, `device_instruction`,
`device_validation`, `get_device_step`, `screenshot`, `list_device_sessions`,
and `stop_device_session`. They return the screenshot as an image you can see,
wait for a starting device, and confirm a stop. Keep the CLI for what needs this
workspace (building, uploading, the dev loop, and project config) and for any
hosted tool that is missing, needs sign-in, or fails.

Claude Code can list a plugin's MCP tools by name only, as deferred tools. Load
the ones you need with one ToolSearch call before the first of them, such as
`select:mcp__plugin_revyl-cli_revyl-workspace__list_builds,mcp__plugin_revyl-cli_revyl-workspace__start_device_session`;
a deferred tool is available once loaded. If the hosted tools need sign-in,
mention it once (Claude Code: `/mcp` and **Authenticate** on
`plugin:revyl-cli:revyl-workspace`; Codex: `codex mcp login revyl-workspace`)
and carry on with the CLI. Never use `start_device_session` for a session the
CLI started. If the hosted tools can't see a session the CLI returned, the two
sign-ins point at different accounts or organizations: say so instead of
starting another device.

## Checks

Run these from the directory the user is working in, each in its own call:

```sh
"$LAUNCHER" --version
"$LAUNCHER" doctor --json
"$LAUNCHER" github status --json
"$LAUNCHER" dev list
"$LAUNCHER" device list --json
```

Skip `github status` outside a Git repository with a GitHub `origin`, and
skip the last three when `doctor` reports the CLI isn't signed in. When the
hosted `list_device_sessions` tool is listed, call it too; otherwise note that
the hosted tools aren't signed in or aren't available here.

## Report

Answer in a short list:

- **CLI**: the pinned version, signed in or not.
- **Project**: the `.revyl/config.yaml` path relative to the repository, or
  that none applies; any configuration problem `doctor` names.
- **GitHub**: App connected, this repository granted, and the project's
  pull-request automation status (`enabled`, `not_published`, `disabled`,
  `not_configured`). When `enabled`, pushed pull requests get Revyl builds to
  test. Otherwise say what's missing and that `revyl-setup` can fix it.
- **Hosted tools**: available, needs sign-in, or not listed.
- **Running**: dev contexts in this worktree and device sessions, with their
  IDs and viewer links. Say which, if any, this conversation started.

Never include API keys, tokens, or signed URLs. Suggest one next step only
when something is missing: sign in, `revyl-setup`, or `revyl-stop` for
sessions this conversation left running.
