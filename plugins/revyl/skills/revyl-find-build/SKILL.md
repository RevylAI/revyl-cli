---
name: revyl-find-build
description: Find which Revyl build to use for a commit, branch, pull request, or app - the exact-commit build from a pull-request preview or a CI upload, the build a run would pick by default, or a build's status and failure reason. Read-only and free. Use before testing or proving a change, or when asked whether a build exists, which one Revyl will use, or why a build failed.
---

# Find a Revyl build

Answer which build exists for some code, or which one Revyl would use, without
building, uploading, pinning, or starting anything.

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

## Hosted tools or CLI

The hosted `revyl-workspace` tools `list_builds` and `get_build` answer most of
this faster, and `get_build` follows a build that is still running, so use
them first. Claude Code can list them by name only, as deferred tools: load
both with one ToolSearch call
(`select:mcp__plugin_revyl-cli_revyl-workspace__list_builds,mcp__plugin_revyl-cli_revyl-workspace__get_build`)
before the first. When they are missing, need sign-in, or fail, every step has
a CLI command below.

## Resolve

- The app: the `app_id` of the profile and platform in the nearest
  `.revyl/config.yaml`, or `"$LAUNCHER" app list --platform <platform> --json`.
- The code: a full commit SHA (`git rev-parse <ref>`), the branch, and for a
  pull request its head (`gh pr view <n> --json headRefName,headRefOid` when
  `gh` is available).

## Exact commit

A build matches only when its commit equals the full 40-character SHA. A short
SHA, branch name, or version label is never a match.

- Hosted: `list_builds` with `app_id` and `branch`, then the build whose
  `commit_sha` equals the SHA. Trust that match when its `source` is
  `pull_request` or `remote_build`: those record a commit only when they were
  built from it. An `upload` can carry the commit its uncommitted edits started
  from, so confirm one with the CLI below before using it. `get_build` with its `id` (and `wait_seconds` to
  wait for one that is `queued` or `running`) reports
  `ready_for_device_session` and, for a failed build, `failure_reason`.
- CLI:

  ```sh
  "$LAUNCHER" build list --app <app-id> --branch <branch> --limit 100 --json
  ```

  A build's commit is in `metadata.scm_head_sha` (CI uploads and GitHub App
  builds), `metadata.commit_sha` (cloud builds that record it),
  `metadata.source_metadata.commit_sha`, or `metadata.git.commit` (CLI builds
  and uploads). Skip any build whose `metadata.git.dirty` is true before
  matching: it was made from uncommitted edits, and every commit field it
  carries names only the commit it started from. Follow `has_next` with
  `--page 2`, `--page 3`, and so on before concluding there is none; repeat
  without `--branch` (every branch) when the branch search finds no match, since
  a build can record a different branch name, such as `HEAD` from a detached
  checkout.

A build that records none of these has an unknown commit, never a different
one. Cloud builds (`metadata.source` `revyl_build`) from this plugin's pinned
CLI record none. One this conversation started (its `metadata.build_job_id` is
the job you started) from the current `HEAD` while the app had no
uncommitted changes, and from a profile with no Git `source`, was built from
`HEAD`'s files, so report it as this commit and say why. Report any other such build as
"commit unknown", with its branch and creation time, and let the user decide
whether to use it.

A pull-request preview build that is still building appears through the hosted
tools only; the CLI lists it once it finishes.

## What a run would use

```sh
"$LAUNCHER" build which --app <app-id> --branch <branch> --json
```

reports the branch a run would choose, why, and that branch's default build
(its latest, or a pinned one). Without `--branch` it follows the app's Git
branch detection. A test or workflow can carry its own saved build, which this
doesn't show. `"$LAUNCHER" app branch list --app <app-id> --json` lists each
branch with its default build.

## A build in progress or failed

- A cloud build started with `build --detach`: `"$LAUNCHER" build status <job-id> --json`.
- Runs that used a build: `"$LAUNCHER" build runs <build-id> --page 1 --limit 20 --json`,
  following `has_next`.

## Report

Give the build ID, version, branch, full commit, where it came from (preview,
CI upload, cloud or local build), and whether it's ready. When nothing
matches, say exactly what was searched and offer the next step without taking
it: wait for the preview, `revyl-build` to build it, or
`revyl-build-ci-upload` so CI provides one. Never pin or unpin a branch here;
`app branch pin` changes what every run on that branch uses, so it needs the
user's explicit request.
