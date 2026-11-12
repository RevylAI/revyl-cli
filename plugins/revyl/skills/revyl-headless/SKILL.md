---
name: revyl-headless
description: Revyl conventions for unattended agent runs - Claude Code on the web, claude -p, codex exec, and CI jobs - covering CLI sign-in with REVYL_API_KEY, finding or making builds without Xcode, bounded waits, device evidence, and always stopping sessions. Use whenever this agent works with Revyl without an interactive user or a display.
---

# Revyl in headless runs

Use this skill when nobody is watching the session live: Claude Code on the
web, `claude -p`, `codex exec`, a scheduled job, or a CI step. The same CLI
works there, but nobody can answer a question, click a link, or see a
browser, and devices keep running after the run ends.

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

## Authorization comes from the task

A headless run can't ask before spending. Start devices or builds only when
the task's own instructions authorize it (for example "prove this pull request
on an iOS device"). Otherwise do the free parts (resolve, find builds,
validate config) and report what you would run and what it would cost.

## Sign-in

- The CLI reads `REVYL_API_KEY` from the environment. Check with
  `"$LAUNCHER" auth status`. Never print the key, pass it as an argument, or
  write it to a file.
- Without a key, `"$LAUNCHER" auth login` prints an approval link that nobody
  will open: report that the run needs `REVYL_API_KEY` as a secret and stop.
- The hosted `revyl-workspace` tools need an interactive OAuth sign-in. Don't
  rely on them here; use the CLI for every step.

## Builds

- Look for an existing build of the exact commit first, as the
  `revyl-find-build` skill describes: a pull-request preview or a CI upload
  costs nothing.
- Linux runners have no Xcode, so iOS builds go to Revyl's runners:
  `"$LAUNCHER" build --profile <profile> --platform ios --detach --json`, then
  `"$LAUNCHER" build status <job-id> --json` until it finishes. Treat Android
  the same unless this machine has the Android SDK.
- For a development loop of a native or standalone build, use
  `"$LAUNCHER" dev --remote --detach --json`. Expo and React Native
  development clients load their JavaScript through Metro, which `--remote`
  doesn't start: use `"$LAUNCHER" dev --detach --json` for them. Never start
  the interactive `dev` interface.

## Bounded waits

Every wait has a limit. Use `dev status` for snapshots,
`dev rebuild --wait --timeout <seconds>` after native changes, and
`build status` polls with a deadline. When a deadline passes,
report where things stand instead of waiting longer.

## Sessions outlive the run

- Before starting a device, `"$LAUNCHER" dev list` and
  `"$LAUNCHER" device list --json` show sessions this run may reuse. Don't
  stack a new device on a stale one.
- Record each `session_id` you start. Before the run ends, on success or
  failure, stop exactly those: `"$LAUNCHER" dev stop` or
  `"$LAUNCHER" device stop --session-id <id> --json`. Never `--all`.
- A stop is finished only when its JSON says `stopped: true`. Otherwise, while
  `"$LAUNCHER" device list --json` still lists the session, wait about ten
  seconds and stop it once more, then record whether it ended.

## Evidence

- Report the `viewer_url` as soon as `--json` output returns it.
- `"$LAUNCHER" device screenshot --session-id <id> --out <file> --json` and
  `"$LAUNCHER" device report --session-id <id> --json` are the evidence. Put
  their results, not claims, in the summary or pull-request comment.
- Never claim a browser opened or that anyone saw the device.

## Launch variables and secrets

Selected launch configurations apply to new sessions on their own; don't fetch
or repeat their values. If the app shows a signed-out state mid-session,
`"$LAUNCHER" dev auth refresh --json` re-sends the sign-in; if the token itself
expired, stop the session and start a new one. Never put tokens, launch
variable values, or API keys in code, logs, screenshots, or pull requests.

## Repository hygiene

Commit `.revyl/config.yaml` and `.revyl/tests/` only. Confirm
`.revyl/.gitignore` exists before committing, and never commit screenshots,
reports, logs, or launch-variable files the run produced.
