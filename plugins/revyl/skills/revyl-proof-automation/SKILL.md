---
name: revyl-proof-automation
description: Set up automatic device proof for every pull request - Revyl's own proof of changes on builds the Revyl GitHub App makes, the same proof on builds CI uploads, or a GitHub Actions job that runs claude -p or codex exec to prove the CI build. Use when asked to make CI or GitHub prove each pull request on a device, or to set up proof of changes.
---

# Set up pull-request proof

Make each pull request get checked on a Revyl device automatically. This skill
writes configuration and CI files in the repository and explains the one-time
steps only the user can take. It starts no device and spends nothing; to prove
one pull request now, use `revyl-proof`.

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

## Pick a path

Run `"$LAUNCHER" github status --json` in the app directory first, then
recommend one path:

1. **Revyl builds and proves** (recommended): the Revyl GitHub App builds each
   pull request from a build profile and Revyl's proof agent checks it. Nothing
   runs in CI.
2. **CI builds, Revyl proves**: CI keeps building and uploads the artifact;
   the GitHub App matches the upload to the pull request and proves it.
3. **CI builds, an agent proves in GitHub Actions**: a workflow runs
   `claude -p` or `codex exec` against the uploaded build. Use it only when the
   team wants its own agent and prompt in CI.

Paths 1 and 2 need the Revyl GitHub App connected with access to the
repository. If `connected` or `repository.access_granted` is false, offer
`revyl-setup` for that step; never issue the install link without a yes.

## Path 1: Revyl builds and proves

The project needs a build profile that makes a standalone build (see
`revyl-build-config`), then this in `.revyl/config.yaml`:

```yaml
pr_review:
  enabled: true
  build:
    kind: revyl
    profile: preview
  proof_of_changes:
    enabled: true
    harness:
      kind: revyl
```

Validate with `"$LAUNCHER" config validate --json`, then publish once the user
agrees. `revyl github setup` completes the `pr_review` block, binds the apps
it needs, and publishes in one step, but it needs an interactive terminal:
ask the user to run it in their own terminal. Otherwise publish the file you
wrote with `"$LAUNCHER" config push`. When the default branch manages the
configuration (`project.authority` is `git_default_branch`), `config push`
refuses and the change takes effect when this file merges there.

## Path 2: CI builds, Revyl proves

Add the upload step with `revyl-build-ci-upload`, then point pull-request
review at those uploads by app instead of a profile:

```yaml
pr_review:
  enabled: true
  build:
    kind: ci_upload_to_revyl
    app_ids:
      ios: <ios-app-uuid>
      android: <android-app-uuid>
  proof_of_changes:
    enabled: true
    harness:
      kind: revyl
```

`app_ids` needs at least one platform, and `profile` isn't allowed with this
kind. Validate and publish as in path 1.

## Proof options for paths 1 and 2

- `proof_of_changes.harness.kind` chooses who proves: `revyl` (Revyl's agent),
  `capy`, `cursor` (then run `"$LAUNCHER" config authorize-cursor-proof` after
  publishing), or `devin` (alpha, enabled per organization). `model_id` is
  allowed for `capy` and `cursor` only.
- `proof_of_changes.always_verify` lists checks to make on every pull request
  in addition to what the change touches.
- `pr_review.review_triggers.paths` limits which changed files trigger builds
  and proofs. Paths are repository-root globs; a quoted `"!glob"` excludes.
  Other trigger fields depend on the pinned CLI; validate before relying on
  one.

People retrigger from the pull request with a comment:
`@revyl rerun` or `@revyl retry`, optionally followed by `ios`, `android`, or
`web` and extra guidance.

## Path 3: an agent proves in GitHub Actions

Keep the upload step from `revyl-build-ci-upload` in the build job, then add a
second job that runs after it. [references/github-actions-agent.md](references/github-actions-agent.md)
has the workflow and prompt. It needs `REVYL_API_KEY` plus the agent's own key
(`ANTHROPIC_API_KEY` or `OPENAI_API_KEY`) as repository secrets; tell the user
to add them, and never ask for their values.

## Finish

Show the diff, run `"$LAUNCHER" config validate --json`, and list what the user
still has to do outside the repository (connect GitHub, add secrets, merge).
