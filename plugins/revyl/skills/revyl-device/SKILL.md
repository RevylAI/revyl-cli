---
name: revyl-device
description: Start one Revyl cloud device (or browser) on a chosen app build and hand back the viewer link, after one confirmation that it uses the organization's device time.
disable-model-invocation: true
argument-hint: "[app] [ios|android|web] [branch or build]"
---

# Revyl device

Start one cloud device session on an app build and give the user its viewer
link. Read the user's arguments: Claude Code appends them to this skill as
`ARGUMENTS:` (`/revyl-cli:revyl-device [app] [ios|android|web] [branch or build]`), and in Codex they follow
`$revyl-device` in the message. Arguments are hints, not authorization to guess:
resolve anything they leave out from the repository and ask only when it stays
ambiguous.

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

## Resolve the app and build (free)

1. **App.** Prefer the app IDs in the nearest `.revyl/config.yaml`
   (`build.profiles.<profile>.<platform>.app_id`). Otherwise call hosted
   `list_apps` (with `platform` or `search`), or run
   `"$LAUNCHER" app list --platform <platform> --json`. Never guess an ID.
2. **Build.** By default the session installs the app's default build: the
   default branch's latest or pinned build. For a branch or pull request, find
   its build with hosted `list_builds` (`app_id`, `branch`) or
   `"$LAUNCHER" build list --app <app-id> --branch <branch> --json`. Use only a
   build that is complete and installable (`ready_for_device_session` in the
   hosted result). To see which build a default start would pick, run
   `cd <app-directory> && "$LAUNCHER" build which --app <app-id> --json`:
   it follows the current directory's Git branch, so run anywhere else it
   names the wrong branch's build. Name the branch it chose in the
   confirmation.

## Confirm once

Ask one question naming the app, platform, build, and that the session uses
the organization's device time, unless the arguments already named all of
them. Start nothing without a yes.

## Start

When the hosted `start_device_session` is listed, call it with `platform` on
every call, plus `build_id` for an exact build or `app_id` alone for the app's
default build; web always needs `app_id`, also with `build_id`. It takes no branch: resolve a
branch to its build ID first, or use the CLI's `--branch`. Otherwise use the
CLI:

```sh
"$LAUNCHER" device start --platform <ios|android|web> --app-id <app-id> --json
"$LAUNCHER" device start --build-version-id <build-id> --json
```

Add `--branch <name>` to the first form for a branch. Record the returned
session ID and give the user `viewer_url` at once. When the hosted
`screenshot` tool is listed, take one so the user sees the screen in the
conversation; otherwise `"$LAUNCHER" device screenshot -s <session-id> --out <file> --json`,
and open the file before describing it.

Finish by saying the session keeps using device time until it is stopped or
idles out, and that `revyl-stop` stops it.
