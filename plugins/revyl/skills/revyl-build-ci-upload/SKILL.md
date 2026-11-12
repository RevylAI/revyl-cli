---
name: revyl-build-ci-upload
description: Add a Revyl upload step to the CI job that already builds the app (GitHub Actions, GitLab CI, Buildkite, or another runner) so each commit and pull request has a Revyl build tied to its exact commit, without building twice. Also covers uploading EAS or other hosted artifacts by URL and registering web preview deployments. Use when wiring CI builds into Revyl.
---

# Upload CI builds to Revyl

Make the pipeline that already builds the app hand its artifact to Revyl, so
`revyl-test`, `revyl-proof`, and pull-request proof find a build of the exact
commit instead of building again. This skill edits CI configuration in the
repository. It never runs the pipeline, and it uses no build minutes.

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

## Choose the path first

If the team would rather Revyl build each pull request, the Revyl GitHub App
does that without any CI change; offer `revyl-setup` instead. Use this skill
when CI keeps building, for example because signing, private registries, or a
long native build already live there.

## 1. Find the job and the artifact

Read the CI configuration and find the job that already produces a usable
artifact. Edit that job; don't add a second build.

- iOS: a **simulator** `.app`, or a `.zip`, `.tar.gz`, or `.tgz` containing
  one. A device `.ipa` won't install on Revyl's simulators.
- Android: an installable `.apk`, not an `.aab`.
- Web: a preview deployment URL, registered instead of uploaded.

If no job makes one, say so and offer `revyl-build-config` to set up a Revyl
build instead of inventing a CI build.

## 2. Find the app

`"$LAUNCHER" app list --platform <ios|android|web> --json` gives the app's
UUID; `build.profiles.<profile>.<platform>.app_id` in `.revyl/config.yaml`
usually already names it. Write the UUID into the CI step. Don't add a CI
variable for it.

## 3. Add the upload step

Each provider has a ready step in a reference; copy the one that matches and
adjust the artifact path and app ID:

| CI                      | Reference                                                 |
| ----------------------- | --------------------------------------------------------- |
| GitHub Actions          | [references/github-actions.md](references/github-actions.md) |
| GitLab CI               | [references/gitlab-ci.md](references/gitlab-ci.md)           |
| Buildkite or any other  | [references/other-ci.md](references/other-ci.md)             |

The runner installs its own CLI; this plugin's launcher only runs on the
user's machine. The step needs one secret, `REVYL_API_KEY`, which the user
creates in Revyl and adds to the CI provider's secrets. Never write a key into
the repository or ask the user to paste one into the conversation.

Rules every step follows:

- **Exact commit.** GitHub Actions, GitLab CI, and Buildkite are detected: the
  upload records the pull request's head commit and number. GitLab's merged
  results and merge train pipelines build a merge commit instead; the GitLab
  reference covers that. On another runner,
  pass `--repo <owner/name> --commit <full-sha>` (and `--pr <number>` to name
  the review). The artifact must be built from that same commit.
- **Upload only.** The step doesn't start devices, run tests, or comment.
- **Branch.** On GitHub Actions and Buildkite, an upload without `--branch`
  lands on the pull request's branch when the app detects Git branches. Every
  other runner checks out a detached commit that names no branch, so the
  upload would land on the app's default branch: pass
  `--branch <source-branch>` there. Never add `--pin` unless the user wants
  every run on that branch to use this build.
- **Result.** `--json` output: for `--file`, require `.success == true` and
  `.count == 1` and read `.build.build_id`; for `--url`, read `.version_id`.

## 4. Hosted artifacts and web previews

- An artifact already hosted (EAS, S3, Artifactory, a CI artifact link)
  uploads by URL: `build upload --url <url> --platform <ios|android> --app <id>`,
  with `--header "Authorization: Bearer ..."` from a CI secret when the URL
  needs one. EAS iOS simulator `.tar.gz` builds are accepted as they are.
- A web preview deployment registers by URL:
  `build upload --platform web --url <preview-url> --app <web-app-id>`. URL
  registration doesn't read the Git branch; pass `--branch` to choose one.

## 5. Check it

Validate the CI file with the provider's own linter if the repository has one,
and show the diff. After the user's next push, `revyl-find-build` finds the
uploaded build by its commit. To have Revyl prove each pull request with these
uploads, offer `revyl-proof-automation`.
