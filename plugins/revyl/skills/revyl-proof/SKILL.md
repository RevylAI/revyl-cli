---
name: revyl-proof
description: Prove a pull request on a Revyl cloud device using the build of its exact head commit - the Revyl GitHub App's preview build or a CI upload - without rebuilding, and draft the proof for the pull request.
disable-model-invocation: true
argument-hint: "[pull request number or URL] [ios|android|web]"
---

# Revyl proof

Prove a pull request with a build of its exact head commit and draft the
evidence. Read the user's arguments: Claude Code appends them to this skill as
`ARGUMENTS:` (`/revyl-cli:revyl-proof [pr] [platform]`), and in Codex they follow
`$revyl-proof` in the message. This command never builds or uploads: proof is
about the artifact the pull request produced. To check unpushed work, use
`revyl-test` instead.

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

## Resolve (read-only)

- The pull request: from the arguments, otherwise the current branch's. With
  `gh`, read `gh pr view <pr> --json number,headRefName,headRefOid,files`. You
  need the **full** head SHA; a short SHA or branch name never matches.
- The app and platform from the changed files and the nearest
  `.revyl/config.yaml`; ask only when ambiguous.

## Find the build of the head commit (free)

1. **Preview build.** When pull-request builds are on for the project
   (`"$LAUNCHER" github status --json` reports `project.status` `enabled`),
   call hosted `list_builds` with the app's `app_id` and the head branch and
   take the build whose `commit_sha` equals the head SHA. Follow it with
   `get_build` (`wait_seconds` 45) while it is `queued` or `running`, for up to
   20 minutes or the user's limit, and say it's still building every few
   minutes. A `failed` build: report its `failure_reason` and stop.
2. **CI upload.** Otherwise, or when the hosted tools aren't listed, follow the
   `revyl-proof-ci` skill's exact-SHA lookup over `build list`.

No build of the head SHA means **proof not run**: say which lookups ran, and
that pull-request builds (`revyl-setup`) or a CI upload step would produce one.
Never fall back to another commit's build or start a build.

## Confirm once, then prove

Ask once, naming the pull request, app, platform, build, and that it uses the
organization's device time. Then follow `revyl-proof-ci` from its device step:
start a session on that exact build (hosted `start_device_session` with
`platform` and `build_id`, plus `app_id` for web, or `"$LAUNCHER" device start --build-version-id <build-id> --json`),
share `viewer_url`, exercise only the changed behavior with instructions and
validations, capture screenshots and look at them, and stop the session even
when a check fails, retrying a stop once as `revyl-proof-ci` describes. Prefer
the hosted tools for these steps. In Claude Code you may hand this to the `revyl-verifier`
agent with the authorization line `revyl-test` describes; in Codex, to one
subagent.

## Draft and publish

Return a draft headed `## Revyl device proof` with the full SHA, the build ID
and source, each validation verdict, and only evidence you collected. Posting
it, `session share`, and `session publish` are separate external actions: ask
first, and name the audience. Never include credentials, launch-variable
values, or signed URLs.

When the project's Proof of Changes is on, the Revyl GitHub App also posts its
own proof on each push. A reviewer re-runs it by commenting
`@revyl rerun [ios|android|web] [guidance]` (or `retry`) on the pull request;
suggest that comment rather than posting it.
