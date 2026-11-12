---
name: revyl-setup
description: Set up Revyl for this repository step by step - CLI sign-in, the hosted tools' sign-in, the project's .revyl/config.yaml and apps, and GitHub connection with pull-request builds - asking before every file write or account change.
disable-model-invocation: true
argument-hint: "[app directory] [github]"
---

# Revyl setup

Get this repository ready for Revyl device checks and pull-request builds.
Read the user's arguments: Claude Code appends them to this skill as
`ARGUMENTS:`, and in Codex they follow `$revyl-setup` in the message. Do the
steps in order, skip any that are already done, and stop at the first one the
user declines. Every step that writes a file, creates something in the Revyl
organization, or changes GitHub needs its own yes; this command spends no
device or build time.

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


## 1. Where things stand

Run `"$LAUNCHER" doctor --json` and, in a GitHub repository,
`"$LAUNCHER" github status --json` from the app directory. Summarize what is
done and what is left in two or three lines before changing anything.

## 2. Sign in

If the CLI isn't signed in, run `"$LAUNCHER" auth login` and hand the user the
approval link; wait for them. For the hosted tools, ask the user to sign in
once: in Claude Code, `/mcp` and **Authenticate** on
`plugin:revyl-cli:revyl-workspace`; in Codex, `codex mcp login revyl-workspace`
in a terminal. Both should choose the same Revyl organization.

## 3. Project configuration

Work in the app's own directory, not a monorepo root.

- An existing Revyl project elsewhere (another checkout or teammate): with a
  yes, `"$LAUNCHER" config pull` restores its configuration; it writes
  `.revyl/config.yaml`, replacing any local copy.
- A legacy configuration: `"$LAUNCHER" config migrate --check --json`, summarize
  what it would drop, and only with a yes run `"$LAUNCHER" config migrate --write`
  (it keeps a backup).
- No configuration: with a yes, `"$LAUNCHER" init --non-interactive` detects the
  build system and writes `.revyl/config.yaml` and `.revyl/.gitignore`. Show the
  generated build profiles. Each recipe needs an `app_id` of the same platform:
  find one with `"$LAUNCHER" app list --platform <platform> --json`, or, with a
  yes, create one with `"$LAUNCHER" app create --name <name> --platform <platform> --json`,
  then put its ID in the recipe.

Finish with `"$LAUNCHER" config validate` and fix what it reports. For recipe
details per framework, use the `revyl-build-config` skill.

## 4. GitHub and pull-request builds

With GitHub connected and pull-request builds on, each push to a pull request
gets a Revyl build, so `revyl-test` and `revyl-proof` can test it without
building. Read `github status --json`:

- `connected` false: ask "Connect the Revyl GitHub App so pull requests get
  Revyl builds?" Only after a yes, run
  `"$LAUNCHER" github connect --no-open --no-wait --json` and give the user its
  `install_url`. If they aren't a GitHub organization owner, GitHub sends an
  owner an install request instead. When they say it's done, confirm with
  `github status --json`.
- `repository.access_granted` false: the user adds this repository to the Revyl
  GitHub App installation's repository access on GitHub, then you confirm.
- `project.status` not `enabled`: `github setup` completes the project's
  `pr_review` configuration, may update `.revyl/config.yaml`, and publishes
  it, but it needs an interactive terminal, so an agent's shell can't run it.
  With a yes, either ask the user to run `"<absolute launcher path>" github setup`
  in their own terminal from the app directory, with the resolved launcher
  path written out (a plugin-only install has no `revyl` on `PATH`), or write the `pr_review` block yourself and
  run `"$LAUNCHER" config validate --json`. How that file reaches Revyl
  depends on `project.authority`; read it before offering anything:
  - `git_default_branch`: Revyl follows the file on the default branch, and
    `config push` refuses. Offer to commit the validated file; it takes effect
    once it merges to the default branch. `config push --force` publishes
    once without changing that, and the next default-branch change replaces
    it, so offer it only when the user asks to publish before merging.
  - `manual`, or a project not published yet (`project.status`
    `not_published`): with a yes, publish the validated file with
    `"$LAUNCHER" config push`.

For the details of pull-request builds, proof, and comment commands, use the
`revyl-github` skill.

## 5. Done

Run `doctor --json` and `github status --json` again and report the result.
Suggest `revyl-test` as the next step.
