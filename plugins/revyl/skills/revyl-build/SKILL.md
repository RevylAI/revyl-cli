---
name: revyl-build
description: Build this workspace's app on a Revyl cloud runner, or upload an artifact already built, and return the Revyl build ID, after one confirmation that it uses the organization's build minutes.
disable-model-invocation: true
argument-hint: "[ios|android|web] [profile] [artifact path]"
---

# Revyl build

Produce a Revyl build of this workspace and return its build ID.
Read the user's arguments: Claude Code appends them to this skill as
`ARGUMENTS:` (`/revyl-cli:revyl-build [ios|android|web] [profile] [artifact path]`), and in Codex they follow
`$revyl-build` in the message. Arguments are hints, not authorization to guess:
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

## Resolve (free)

Work from the app directory that owns `.revyl/config.yaml`, never the
monorepo root. Read its `build.profiles`: each profile has a recipe per
platform (`ios`, `android`, `web`) with an `app_id`. Pick the exact profile and
platform from the arguments and the repository; ask only when several fit. If
there is no configuration, offer `revyl-setup` instead of building. For
recipes, frameworks, and simulator or emulator settings, follow the
`revyl-build-config` skill.

Before a pushed commit's build, check whether one already exists: when GitHub
pull-request builds are on for the project, the pull request has a build of
its head commit (find it with hosted `list_builds` or
`"$LAUNCHER" build list --app <app-id> --branch <branch> --json`). Say so
instead of building again, unless the user still wants a new build.

## Confirm once

Ask one question naming the app, profile, platform, and that a cloud build
uses the organization's build minutes, unless the arguments already named all
of them. An upload of an existing artifact uses no build minutes; confirm the
file, app, and platform instead.

A cloud build uploads tracked files, including uncommitted edits, and
untracked files Git doesn't ignore. Look at `git status --short` first and stop
to ask if an untracked file looks like a secret or credential.

## Build or upload

- **Cloud build** (the default):

  ```sh
  "$LAUNCHER" build --profile <profile> --platform <platform> --detach --json
  "$LAUNCHER" build status <job-id> --json
  ```

  `--detach` returns the job at once; poll `build status` every 30 seconds or
  so, for at most the recipe's timeout, telling the user it's still building.
  Don't report success until the build reaches its terminal success state, and
  report a failure with the last log lines `build status` shows.
- **Local build** when the user wants this machine's toolchain: add `--local`
  in place of `--detach`. It runs the same recipe here and uploads the result.
- **Upload an existing artifact** (a simulator `.app`, `.app.zip`, or `.tar.gz`
  for iOS; an installable `.apk` for Android):

  ```sh
  "$LAUNCHER" build upload --file <path> --platform <ios|android> --app <app-id> --json
  ```

  Require `.success == true` and `.count == 1` and use `.build.build_id`.

The build lands on `--branch <name>` when given, otherwise the current Git
branch when the app detects Git branches, otherwise the app's default branch.
Never pass `--pin` unless the user asks to make it the branch's default build.

## Report

Return the build ID, version, branch, and how it was produced (cloud, local,
or upload). Offer the next step without taking it: test it with
`revyl-test` or open it with `revyl-device`.
