---
name: revyl-test
description: Check the current change on a Revyl cloud device, build first - reuse the pull request's preview build or another build of this exact commit when one exists, otherwise build it after asking - then validate the changed behavior, report evidence, and stop everything it started.
disable-model-invocation: true
argument-hint: "[ios|android|web] [what to check]"
---

# Revyl test

Check the change in this workspace on a Revyl cloud device and report evidence.
Read the user's arguments: Claude Code appends them to this skill as
`ARGUMENTS:` (`/revyl-cli:revyl-test [ios|android|web] [what to check]`), and in
Codex they follow `$revyl-test` in the message. Arguments are hints, not
authorization to guess.

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

## 1. Resolve (read-only)

- The app directory that owns the nearest `.revyl/config.yaml`, its build
  profiles, and the platform. If none applies, offer `revyl-setup` and stop.
- What to check: from the arguments, otherwise from the diff
  (`git diff`, `git diff --cached`, and the commits on this branch). Name the
  screens and behaviors the change touches.
- The full `HEAD` SHA (`git rev-parse HEAD`), the branch, whether the working
  tree has uncommitted changes to the app, and whether `HEAD` is pushed
  (`git rev-parse @{u}` equals `HEAD`). When `gh` is available, read the open
  pull request for the branch with `gh pr view --json number,headRefName,headRefOid`.
- Whether pull-request builds are on: `"$LAUNCHER" github status --json` in the
  app directory, `project.status == "enabled"`. The session-start context may
  already say so.

## 2. Find a usable build (free)

A build is usable only if it was built from exactly the code under test.
Uncommitted app changes rule out every existing build: skip to step 3.

- **Pull-request preview build.** When `HEAD` is pushed, has a pull request,
  and pull-request builds are on, the Revyl GitHub App builds each push. Call
  hosted `list_builds` with the app's `app_id` and the pull request's head
  branch, and take the build whose `commit_sha` equals the full `HEAD` SHA.
  Follow one that is `queued` or `running` with `get_build` (its `id`,
  `wait_seconds` 45), calling again while it runs, for up to 20 minutes or the
  limit the user gave, and tell the user it's still building every few
  minutes. Use it once `ready_for_device_session` is true. A `failed` build
  carries `failure_reason`: report it and go to step 3 instead of retrying.
- **Any other build of this commit** (a CI upload or an earlier remote build):
  first hosted `list_builds` with the `app_id` and the branch, then without
  the branch, taking a build whose `commit_sha` equals the full `HEAD` SHA.
  Use a match whose `source` is `pull_request` or `remote_build` as it is:
  those record a commit only when they were built from it. Confirm an
  `upload` match, and search when the hosted tools aren't available, with
  `"$LAUNCHER" build list --app <app-id> --branch <branch> --limit 100 --json`,
  matching the full SHA exactly against `metadata.scm_head_sha` (CI uploads),
  `metadata.commit_sha` (cloud builds that record it),
  `metadata.source_metadata.commit_sha`, or `metadata.git.commit`. Skip any
  build whose `metadata.git.dirty` is true before reading any of these: it was
  made from uncommitted edits, and its commit fields name only the commit it
  started from. Follow `has_next` with `--page 2`, `--page 3`,
  and so on, and when the branch finds no match, repeat without `--branch`
  (every branch): a CI upload can record another branch name, such as `HEAD`
  from a detached checkout. Without the hosted tools this is also how to find
  a finished preview build; a preview still building isn't listed until it
  uploads.
- A short SHA, a branch name, or a version label is never a match.
- **A build that records no commit is unknown, not a different commit.** Cloud
  builds (`metadata.source` `revyl_build`) from this plugin's pinned CLI
  record none. One this conversation started (its `metadata.build_job_id` is
  the job you started) from the current `HEAD` while the app had no
  uncommitted changes, and from a profile with no Git `source`, was built
  from `HEAD`'s files: use it as this commit and say why. Never call any other such build a
  different commit; say its commit is unknown and let step 3 offer it.

When `HEAD` is pushed with a pull request but pull-request builds aren't on,
say that `revyl-setup` can turn them on so later pushes come with a build, and
continue without waiting for a preview that can't come.

## 3. Confirm once

Ask one question, then start nothing until the user says yes:

- With a usable build: "Test <what> on <platform> using build <version> of
  <short SHA> (<source>)? It uses your org's device time."
- Without one: "No build of <short SHA> exists. Build it on a Revyl runner (uses
  build minutes) and then test it on <platform>, or wait for the pull
  request's preview build?" Name the profile you would build.
- With uncommitted app changes: "Build this workspace, uncommitted changes
  included, on a Revyl runner (uses build minutes) and then test it on
  <platform>?" Never offer the preview build or another existing build here:
  they hold the pushed commit, not these edits.
- With only a build whose commit is unknown: "Build <version> (<branch>,
  <created>) records no commit, so I can't confirm it is <short SHA>. Test it
  anyway, or build <short SHA> first (uses build minutes)?" A check on it
  reports its commit as unconfirmed.

If the arguments already named the platform and what to check and the user
typed this command, still ask this one question; never more than one.

## 4. Run the check

**Claude Code**: hand this step to the `revyl-verifier` agent from this
plugin (`revyl-cli:revyl-verifier`), and keep this conversation free. Its prompt
must start with one authorization line naming exactly what the user approved,
then the details:

```text
Authorized: <platform> device check of <app> at <full SHA>, using <build ID and source | a new cloud build of profile <profile>>, on the organization's device time; stop everything you start.
Launcher: <absolute launcher path>
App directory: <path>
App ID: <app-id>
Check: <the behaviors to verify, one per line>
```

**Codex**: spawn one subagent with that same prompt and the instructions
below, and have it return only the evidence block. If you can't spawn one, run
the steps yourself.

The steps, wherever they run:

1. **Build when needed, with the CLI**, since it packs this workspace. Native
   or rebuild-first profiles:
   `"$LAUNCHER" build --profile <profile> --platform <platform> --detach --json`
   from the app directory, then `"$LAUNCHER" build status <job-id> --json`
   until it succeeds; its `build_version_id` is the build to start. Expo and
   React Native development clients use the hot-reload loop, which loads this
   workspace's JavaScript through Metro and starts its own session:
   `"$LAUNCHER" dev --profile <profile> --platform <platform> --detach --json --no-open`.
2. **Device.** Reuse the session `dev` returned. Otherwise call hosted
   `start_device_session` with `platform` and the `build_id` (web also needs
   the app's `app_id`), or
   `"$LAUNCHER" device start --build-version-id <build-id> --json` when the
   hosted tool isn't available. Give the user `viewer_url` at once.
3. **Validate, with the hosted tools.** When `atlas_search` is available, look
   up the changed screen to plan how to reach it. Drive with
   `device_instruction`, one plain-language step per call, and check each
   behavior with `device_validation`. A result still `running` is followed
   with `get_device_step`, never sent again; a session still starting is
   called again with the same `session_id`. Capture the screen with
   `screenshot` and look at each image before describing it. Without the
   hosted tools, use `"$LAUNCHER" device instruction "<step>" -s <session-id> --json`,
   `"$LAUNCHER" device validation "<statement>" -s <session-id> --json`, and
   `"$LAUNCHER" device screenshot -s <session-id> --out <file> --json`, then
   open each file. A false or missing verdict is a failed check.
4. **Clean up.** Stop only the session and dev context created for this check:
   hosted `stop_device_session`, `"$LAUNCHER" device stop -s <session-id> --json`,
   or `"$LAUNCHER" dev stop <context> --json`. Cleanup runs after a failure
   too. A stop is finished only when its result says `stopped: true`.
   Otherwise check `list_device_sessions` or `"$LAUNCHER" device list --json`;
   while the session is still listed, wait about ten seconds and stop it once
   more, then report it as ended or still stopping. Retry once, never more.

## 5. Report

Return one evidence block:

```text
Revyl check: <passed | failed | not run>
Commit: <full SHA>   Platform: <platform>
Build: <build ID> (<pull-request preview | CI upload | cloud build | local upload>)
Session: <session ID> (stopped | still stopping)
- <behavior>: <passed | failed> - <reason>
Screenshots: <what each shows>
```

Offer to post it on the pull request; never post it, share a session, or
publish screenshots without a yes. Report any Revyl problem that got in the way.
