---
name: revyl-dev-loop
description: Run, preview, debug, and verify mobile app changes on a real Revyl cloud device with the bundled, pinned Revyl CLI. Use after changing iOS, Android, Expo, React Native, Flutter, or other mobile app code and before reporting that work done, to offer an on-device check instead of leaving it to the pull request's proof of changes; also use when asked to run, preview, or screenshot the app on a device.
---

# Revyl Dev Loop

Use this skill for an authorized CLI development loop in a shell-capable host.
It behaves the same in Claude Code and Codex; where the hosts differ, each rule
below names the host it applies to. Installation never changes an app's source
or `.revyl/config.yaml`; later source or configuration work is allowed only
when the task explicitly requires it and the user authorizes it.

For a request to browse Atlas or view an existing device, prefer the connected
Revyl workspace tools when available; do not start a development loop just to
open a view. Source builds and rebuilds stay on this pinned CLI workflow. If
the host has no shell, explain that limitation instead of running shell commands
through another tool or starting a replacement device.

## Hosted Revyl workspace tools

Revyl's hosted workspace tools (the `revyl-workspace` MCP server, which the
Codex and Claude Code plugin bundles) find builds, show and act on device
sessions, take screenshots, and read Atlas. They are optional: every step in
this skill also works through the CLI. They sign in separately from the CLI,
once per host, with Revyl's OAuth page:

- **Claude Code**: run `/mcp`, select `plugin:revyl-cli:revyl-workspace`, and
  choose **Authenticate**.
- **Codex**: run `codex mcp login revyl-workspace` in a terminal.

Only suggest signing in when the user wants something those tools do. Check
which tools are actually available before naming one; a tool the hosted server
hasn't deployed yet, such as a build lookup or a device step, is simply absent,
and the CLI command for the same step still works. The CLI builds, uploads,
and starts development loops. The hosted tools may act on a session the CLI
started, by its returned `session_id`. If they cannot see that session, the
two sign-ins point at different accounts or organizations: say so, and never
start another device to work around it.

## Offer a device check before calling mobile work done

When you have changed mobile app code in this task (screens, components,
navigation, styles, native modules, or app configuration) and are about to
report the work as done, first offer to verify it on a Revyl cloud device with
this loop. Do not defer that check to a pull request's proof of changes. Ask
once, naming the app directory, the platform, and that the check uses the
organization's device time, for example: "Want me to check this on a Revyl iOS
cloud device before I wrap up? It uses your org's device time."

Start nothing that uses device or build time until the user says yes. If they
decline, finish without the check and do not ask again for the same change.
Skip the offer when the change cannot affect the running app, such as docs,
tests, or CI configuration only.

## Launcher and setup

Resolve the launcher from this skill's own absolute directory before changing
to the app directory. Find that directory the way your host provides it:

- **Claude Code** substitutes it into this file when the skill loads:
  `${CLAUDE_SKILL_DIR}`.
- **Codex** lists each skill with the path of its `SKILL.md`; use that file's
  absolute parent directory. If the Claude Code value above is not an absolute
  path, your host did not substitute it, so use this rule instead.

On POSIX the launcher is `../../scripts/launch-revyl` from that directory; on
Windows it is `../../scripts/launch-revyl.cmd`. Call that absolute launcher for
**every** Revyl command; do not use `revyl` from `PATH`, `PLUGIN_ROOT`, or
another installed CLI. Shell variables do not persist between tool calls in
either host, so write the resolved absolute launcher path into each command.
Run each command from the app directory (the one holding `.revyl/config.yaml`),
never from the plugin's own folder: the CLI reads the current directory's Git
branch, commit, and config, so a command run inside the plugin resolves the
wrong build or project.

```sh
SKILL_DIR="<absolute directory containing this SKILL.md>"
LAUNCHER="$(cd "$SKILL_DIR/../../scripts" && pwd)/launch-revyl"
"$LAUNCHER" --version
"$LAUNCHER" auth status
```

```bat
REM Windows cmd.exe
set "SKILL_DIR=<absolute directory containing this SKILL.md>"
set "LAUNCHER=%SKILL_DIR%\..\..\scripts\launch-revyl.cmd"
"%LAUNCHER%" --version
"%LAUNCHER%" auth status
```

The launcher detects whether Claude Code or Codex started it; never set
`REVYL_PLUGIN_HOST` yourself.

If authentication is needed, use `"$LAUNCHER" auth login`. An existing
environment-provided secret may authenticate the launcher without being copied
or named in an argument. Never run credential-provisioning or secret-bridge
commands, print credentials, or paste them into chat. Give the user the normal
login approval URL as a link (see [Links](#links)) and wait for approval.

## Links

Never open a browser and never claim one opened; the user opens every login,
viewer, and report URL. Share each one as soon as you have it:

- **Claude Code**: put the full `https://` URL on its own line, not only inside
  Markdown link text, so terminals and the IDE panels keep it clickable.
- **Codex**: give it as a Markdown link, such as `[Open the Revyl device](URL)`;
  the Codex app opens it when the user clicks.

## Authorization and selection

Before any build, remote session, cloud device, or paid operation, require
authorization naming the app, environment, and paid operation; a yes to the
offer above that names them counts. Resolve the exact profile and platform from
the repository, task, and user context without changing them; ask only when
they remain ambiguous. Run from the app directory that owns
`.revyl/config.yaml`, not the monorepo root. Let `dev` create its new context,
capture the returned context and session IDs, and clean up only those resources
when the work ends or fails.

If the launcher reports that the local configuration uses a legacy format,
run `"$LAUNCHER" config migrate --check --json` and summarize the fields it
would drop. Migrating rewrites `.revyl/config.yaml` (keeping a backup), so ask
before running `"$LAUNCHER" config migrate --write`, and never hand-edit the
file to get past the error.

Start the loop detached, with explicit selection and no browser opening. Pick
the mode from the app's stack, as the reference below describes:

- **Native or rebuild-first** apps (Swift, Kotlin, Flutter, KMP, Bazel, or any
  profile without a dev server) use the remote loop, which builds on a Revyl
  runner:

  ```sh
  "$LAUNCHER" dev --profile <exact-profile> --platform <ios-or-android> \
    --remote --detach --json --no-open
  ```

- **Expo and React Native** profiles whose development client loads JavaScript
  from Metro use the hot-reload loop. Omit `--remote`: the remote loop skips
  Metro and the relay, so such a client cannot load the app.

  ```sh
  "$LAUNCHER" dev --profile <exact-profile> --platform <ios-or-android> \
    --detach --json --no-open
  ```

Give the handshake's `viewer_url` to the user immediately without opening it.
A viewer or `--wait-ready` only means the session is available, not that the
current build succeeded. Wait for the build to reach its terminal success state
before claiming a build result.

When the hosted Revyl workspace tools are connected (an `open_revyl_workspace`
tool, from this plugin, Revyl's Claude plugin, or the Revyl app in Codex), they
can display the same returned session with `view="device"` and
`session_id=<returned-session-id>`, and `screenshot` returns its current
screen as an image in the conversation. Never call `start_device_session` for a
session the CLI already created, and never start a second device for it.
Browser and CLI authentication are separate; inaccessible sessions require the
user to connect the matching account, not a new session or credential copied
into chat.

Use short-lived commands in separate calls. Target the returned context with
`--context` for status and rebuild, and pass it positionally to stop:

```sh
"$LAUNCHER" dev status --context <returned-context>
"$LAUNCHER" dev rebuild --context <returned-context> --wait --timeout 600 --json
"$LAUNCHER" dev stop <returned-context> --json
```

Target **every** device command with `-s <returned-session-id>`. Open each
captured screenshot before describing it. A false or missing semantic verdict
is a failed validation, but cleanup still runs for the returned context/session.
Bound status polling to the configured build timeout. If that limit expires,
report a timeout and stop the owned loop instead of polling indefinitely.

## Detailed CLI mechanics

Resolve this skill's `../../references/revyl-cli-dev-loop.md` to an absolute
path and read it for framework behavior, evidence, and troubleshooting. This
skill overrides that reference whenever they conflict:

- Do not use its browser-opening, URL-opening, foreground, persistent-shell,
  `PATH`, or installed-CLI examples. Use `--detach --json --no-open` and the
  absolute launcher above.
- Do not start a local app source/configuration change, a device, build, or
  paid action without the named authorization above.
- Do not invoke `revyl-cli-auth-bypass` yourself. It is an explicit-only
  command the user starts, and authentication bypass is never an automatic
  fallback.
- Use exact resolved profile and platform values, and clean up only the context
  and session created for the authorized request.
- Skip the reference's "Revyl got in your way" feedback bullet. The pinned
  runtime has no `feedback` command, so describe the Revyl problem to the user
  in your report instead.

Report an unavailable authorization or ambiguous target instead of guessing.
