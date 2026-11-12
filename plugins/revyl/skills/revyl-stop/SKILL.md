---
name: revyl-stop
description: Stop the Revyl device sessions, dev loops, and builds this conversation started, and nothing else.
disable-model-invocation: true
---

# Revyl stop

Stop what this conversation started on Revyl. Stopping saves device time and
needs no confirmation; touching anything else does.

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

## What this conversation owns

Collect, from earlier in this conversation, every dev context, device session
ID, and remote build job ID that a Revyl command or hosted tool returned for a
start this conversation made. Only those are yours to stop. When there are
none, list what is running (`"$LAUNCHER" dev list` and
`"$LAUNCHER" device list --json`), show it, and ask which to stop; stop
nothing without an answer.

## Stop

Use the same surface that started each resource, one call each:

```sh
"$LAUNCHER" dev stop <context> --json
"$LAUNCHER" device stop -s <session-id> --json
```

For a session the hosted tools started, call `stop_device_session` with its
`session_id`. Claude Code can list the hosted tools by name only, as deferred
tools: load the ones you need first with one ToolSearch call, such as
`select:mcp__plugin_revyl-cli_revyl-workspace__stop_device_session,mcp__plugin_revyl-cli_revyl-workspace__list_device_sessions`. Never use `--all` or any other global cleanup.

A remote build this conversation queued is different: it may still be wanted.
Ask before cancelling it with `"$LAUNCHER" build cancel <job-id>`.

## Settle

A stop is finished only when its result says `stopped: true` (hosted
`stop_device_session` and `"$LAUNCHER" device stop --json` alike). Otherwise
check `list_device_sessions` or `"$LAUNCHER" device list --json`; while the
session is still listed, wait about ten seconds and stop it once more. Retry
once, never more.

## Report

List each resource with its result. A stop request is not a released device:
say whether each one ended or is still stopping, and name anything that
failed to stop so the user can stop it from the viewer.
