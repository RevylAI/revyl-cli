---
name: revyl-github
description: Work with Revyl's GitHub integration - check whether the Revyl GitHub App is connected and can see this repository, connect it after the user agrees, configure pull-request builds and proof of changes in .revyl/config.yaml with their triggers, publish that configuration, wait for a pull request's preview build and test it, and retrigger with @revyl comments. Use for any question or task about Revyl and GitHub pull requests.
---

# Revyl and GitHub

The Revyl GitHub App builds each pull request from a build profile (or picks
up the build CI uploads), proves the change on a device, and reports back on
the pull request. This skill reads its state, sets it up with the user's
agreement, and uses the builds it produces.

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

## Status (free, read-only)

Run every command in this skill from the directory that owns
`.revyl/config.yaml`. For a nested project in a monorepo you may instead pass
`-C <dir>`, and then pass the same `-C <dir>` to every later command,
including the ones that validate and publish the config, so they all read that
project:

```sh
"$LAUNCHER" github status --json
```

| Field                         | Meaning                                                                                  |
| ----------------------------- | ---------------------------------------------------------------------------------------- |
| `connected`                   | The organization has the Revyl GitHub App installed.                                     |
| `repository.access_granted`   | That installation can see this checkout's `origin` repository.                          |
| `project.status`              | `enabled`, `not_published`, `disabled`, `not_configured`, or `invalid` for this project. |
| `project.authority`           | `manual` (published with the CLI) or `git_default_branch` (follows the default branch). |
| `project.next_action`         | For `not_published`, the exact commands that publish it.                                 |
| `project_error`               | Why `project` is missing, when it isn't a missing grant.                                 |

Report the state in one or two sentences and the single next step it implies.

## Connect (only after a yes)

When `connected` is false, ask: "Connect the Revyl GitHub App so pull requests
get Revyl builds and device proof?" Only after a yes, run
`"$LAUNCHER" github connect --no-open --no-wait --json` and give the user its
`install_url` (the link is bound to the active Revyl organization). If they
don't own the GitHub organization, GitHub sends an owner an install request
instead. When they say it's done, check `github status --json` again. Never
issue the link without a yes, and never more than once per request.

When `repository.access_granted` is false, connecting again doesn't help: the
user adds this repository to the Revyl GitHub App installation's repository
access on GitHub. Say exactly that, then check again when they're done.

## Configure pull-request builds and proof

Pull-request automation lives under `pr_review` in `.revyl/config.yaml`. Read
the existing block before changing it. Revyl building from a profile:

```yaml
pr_review:
  enabled: true
  review_triggers:
    paths:
      - "apps/mobile/**"
      - "!apps/mobile/**/*.md"
  build:
    kind: revyl
    profile: preview
  proof_of_changes:
    enabled: true
    harness:
      kind: revyl
```

- `build.kind: revyl` needs `profile` to name a declared build profile with a
  phone or web recipe; `revyl-build-config` sets one up. To use CI's builds
  instead, `kind: ci_upload_to_revyl` with `app_ids` per platform (see
  `revyl-build-ci-upload`); `profile` isn't allowed with it.
- `review_triggers.paths` are repository-root globs; a quoted `"!glob"`
  excludes. A pull request triggers when a changed file matches an inclusion
  and no exclusion. Without `paths`, a nested project triggers on its own
  directory and a root project on every path. Other trigger fields depend on
  the pinned CLI; check `config validate` before relying on one.
- `proof_of_changes.harness.kind` is `revyl`, `capy`, `cursor`, or `devin`;
  `revyl-proof-automation` covers the options.
- `strict_ci_check.build: true` makes a failed Revyl build fail the pull
  request's check.

Validate after every edit:

```sh
"$LAUNCHER" config validate --json
```

## Publish (only after a yes)

Ask before publishing; it changes what every pull request in the repository
gets. Read `project.authority` from `github status --json` before offering a
way to publish, and offer only the way it allows.

- `revyl github setup` does the whole step: it connects GitHub if needed,
  completes the project's `pr_review` block, binds the apps that build mode
  needs, updates `.revyl/config.yaml` when necessary, and publishes. It needs
  an interactive terminal, so an agent's shell can't run it: with a yes, ask
  the user to run `"<absolute launcher path>" github setup` in their own
  terminal from the app directory, with the resolved launcher path written
  out, since a plugin-only install has no `revyl` on `PATH`.
- With `project.authority` `manual`, or a project not published yet
  (`project.status` `not_published`, whose `project.next_action` names the
  same step), a finished and validated file and a yes let you run
  `"$LAUNCHER" config push` to publish it.
- With `git_default_branch`, Revyl follows the file on the default branch,
  and `config push` refuses: commit the validated file on the pull request
  instead and say it takes effect when it merges there.
  `config push --force` publishes once without changing that, and the next
  default-branch change replaces it, so offer it only when the user asks to
  publish before merging.

Once published from the CLI or by the user's terminal, `github status --json`
should show `project.status` `enabled`. A Git-managed change shows up there
only after it merges to the default branch; until then, report it as
committed and waiting for that merge, not as failed.

## Use a pull request's preview build

With `project.status` `enabled`, a push to a pull request gets a build of its
head commit when the push matches the project's triggers: a change outside
`review_triggers.paths` (or the project's own directory, without `paths`)
gets none. To wait for it and test it:

1. Get the full head SHA and branch (`gh pr view <n> --json headRefName,headRefOid`,
   or `git rev-parse HEAD` once pushed), and check the pull request's changed
   files (`gh pr diff <n> --name-only`) against those triggers. When none
   match, say no preview build will come and offer `revyl-test` to build it
   instead of waiting.
2. Hosted `list_builds` with the app's `app_id` and the head branch; take the
   build whose `commit_sha` equals the full SHA. While it is `queued` or
   `running`, call `get_build` with its `id` and `wait_seconds` 45 again, up to
   20 minutes or the limit the user gave, telling the user it's still
   building every few minutes. It's ready when `ready_for_device_session` is
   true; a `failed` build carries `failure_reason`. When `list_builds` has no
   build of the SHA, check again every minute or so within the same limit.
3. Without the hosted tools, poll
   `"$LAUNCHER" build list --app <app-id> --branch <branch> --limit 100 --json`
   every minute or so, within the same limit, for a version whose
   `metadata.scm_head_sha` equals the SHA; a build still running isn't listed
   until it finishes.
4. Hand the build to `revyl-test` (or `revyl-proof` for the whole pull
   request). Testing uses device time, so those commands ask first.

When the limit passes with no build, stop waiting: say what was searched,
that the push may not have triggered a build, and offer `revyl-test` to build
the commit or `"$LAUNCHER" github status --json` to check the project.

## Retrigger from the pull request

People retrigger Revyl from a pull request comment: `@revyl rerun` or
`@revyl retry`, optionally followed by `ios`, `android`, or `web` and extra
guidance for the proof agent. Posting a comment is a visible action on
GitHub: suggest the comment text, and post it with `gh pr comment` only when
the user asks you to.

## Keep CI and Revyl in step

When the app also builds in CI, a change to how CI builds it belongs in the
same pull request as the matching Revyl recipe change; `revyl-ci-sync` has
the pattern.
