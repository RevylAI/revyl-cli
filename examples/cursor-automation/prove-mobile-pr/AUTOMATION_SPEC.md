# Automation spec — Prove mobile PR (BYO-CI)

Copy-paste companion for [PROMPT.md](PROMPT.md). Create the Automation at
[cursor.com/automations](https://cursor.com/automations).

## Triggers

| Preference | Trigger | When to use |
| --- | --- | --- |
| Required | **CI completed** / **Workflow run completed** | Fires after the job that uploads the artifact to Revyl finishes. Scope it to that upload job, not every CI workflow in the repo. |

Do not use **Pull request pushed** / **Pull request updated** as the default.
Those fire before upload finishes and the first comment is "proof was not run."
PROMPT.md still brief-waits and retries as a backstop.

## Tools

Enable exactly:

| Tool | Purpose |
| --- | --- |
| **Comment on pull request** | Create or edit the `## Revyl device proof` comment. Required. |
| Shell / terminal | Install the CLI if missing, then `revyl build list`, `revyl device start --build-version-id …`, `revyl device stop`, `session publish` / `session share`. Required. |

Do **not** enable Revyl MCP. Do not call `start_dev_loop` or `list_builds`.
Do **not** rely on `gh` or raw GitHub API tokens for commenting. The native
Comment tool posts as the Automation identity.

The desktop plugin pin does not apply to Automation runners. PROMPT.md starts
with `install.sh` so `revyl` is on PATH.

## Secrets and variables

| Name | Kind | Required | Notes |
| --- | --- | --- | --- |
| `REVYL_API_KEY` | Secret | Yes | A Revyl CLI API key from Settings. Same key the CI upload job uses. |
| `REVYL_APP_ID` | Variable | Yes | Revyl app UUID that receives CI uploads. |

Build secrets (signing keys, Expo tokens, store credentials) stay in **your** CI. This Automation never builds the app and does not need a Cursor API key in GitHub.

## Identity

- Comments appear as the Cursor Automation / agent identity configured for the Automation, not as the engineer whose laptop last touched the repo.
- Device sessions and uploaded evidence are attributed to the Revyl org behind `REVYL_API_KEY`.
- Reviewers open the shareable session link without a Revyl login; do not rewrite that URL's origin.

## Repository prerequisites

1. CI uploads a simulator `.app` / `.app.zip` or installable `.apk` with
   `revyl build upload` and
   `--version "${{ github.event.pull_request.head.sha || github.sha }}"`
   so GitHub Actions metadata (`scm_head_sha`, PR number, etc.) is stamped.
2. CLI on the Automation runner PATH via `install.sh` in PROMPT.md, with
   `REVYL_API_KEY`.
3. Paid Cursor plan, and the repo connected in Cursor, so Comment on pull
   request works.
4. Optional: `.revyl/config.yaml` with `auth_bypass` / `before_session` when
   the app needs a signed-in session.

## Twin path (Revyl GitHub App)

If the team later connects the Revyl GitHub App, the same upload step works
with `use_existing_ci: true` and `proof_harness: { kind: cursor }`. That is
not the first-run path. See the Mintlify page `guides/cursor-proof` and
`integrations/github` ("Use your own CI").
