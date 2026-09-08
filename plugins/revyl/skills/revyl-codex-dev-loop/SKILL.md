---
name: revyl-codex-dev-loop
description: Run, preview, debug, and verify mobile app changes on Revyl cloud devices from Codex, with screenshots and semantic validation.
---

# Revyl Codex Dev Loop

Use this skill for an authorized Revyl development loop in Codex. This plugin
has no hooks and no MCP server. Installation never changes an app's source or
`.revyl/config.yaml`; later source/configuration work is allowed only when the
task explicitly requires it and the user authorizes it.

## Launcher and setup

Resolve the launcher from this skill's own absolute directory before changing
to the app directory. On POSIX use `../../scripts/launch-revyl`; on Windows use
`../../scripts/launch-revyl.cmd`. Call that absolute launcher for **every**
Revyl command; do not use `revyl` from `PATH`, `PLUGIN_ROOT`, or another
installed CLI.

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

If authentication is needed, use `"$LAUNCHER" auth login`. An existing
environment-provided secret may authenticate the launcher without being copied
or named in an argument. Never run credential-provisioning or secret-bridge
commands, print credentials, or paste them into chat. Post the normal login
approval URL as a clickable link and wait for approval; do not claim that a
browser opened.

## Authorization and selection

Before any build, remote session, cloud device, or paid operation, require
authorization naming the app, environment, and paid operation. Resolve the
exact profile and platform from the repository, task, and user context without
changing them; ask only when they remain ambiguous. Run from the app directory
that owns `.revyl/config.yaml`, not the monorepo root. Let `dev` create its new
context, capture the returned context and session IDs, and clean up only those
resources when the work ends or fails.

For native or rebuild-first apps, use the detached remote loop with explicit
selection and no browser opening:

```sh
"$LAUNCHER" dev --profile <exact-profile> --platform <ios-or-android> \
  --remote --detach --json --no-open
```

Return the handshake's `viewer_url` immediately without opening it. A viewer or
`--wait-ready` only means the session is available, not that the current build
succeeded. Wait for the build to reach its terminal success state before
claiming a build result.

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
- Do not install, enable, configure, or invoke `revyl-cli-auth-bypass`. That
  skill is not shipped with this plugin, and authentication bypass is never an
  automatic fallback.
- Use exact resolved profile and platform values, and clean up only the context
  and session created for the authorized request.

Report an unavailable authorization or ambiguous target instead of guessing.
