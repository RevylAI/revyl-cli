---
name: revyl-proof-automation
description: Set up a Cursor Automation that proves each pull request on a Revyl cloud device using a CI-uploaded build. Use when creating Cursor proof, BYO-CI device proof, proof of changes without the Revyl GitHub App, or wiring revyl build upload plus a Cursor Automation.
---

# Revyl Proof Automation Setup

Use this skill to **create** the Cursor Automation and CI upload for
[Cursor proof (BYO-CI)](https://docs.revyl.ai/guides/cursor-proof). Do not
use it to prove a PR that already has a matching build — that is
`revyl-proof-ci`.

This skill does not start a device and cannot create the Automation in Cursor's
UI. Use it when the user asks to prove a pull request or set up Cursor proof.
Finish the three steps here.

Do not start an empty device. Do not connect the Revyl GitHub App as the setup
path.

## 1. Confirm Revyl auth

```bash
revyl auth status
```

If unauthenticated, run `revyl auth login` and post the printed approval URL as
a clickable markdown link. Wait for approval. For Automations / Cloud Agents,
they will set `REVYL_API_KEY` themselves — never print it.

## 2. Add one upload step to the job that already builds the app

Inspect existing GitHub Actions (or equivalent). After the job that already
produces a simulator `.app` or installable `.apk`, add **upload only**. Edit
that workflow. Do not add a second unrelated workflow file.

```bash
revyl build upload \
  --file path/to/app.apk \
  --app "<your-app-id>" \
  --platform ios \
  --version "${{ github.event.pull_request.head.sha || github.sha }}" \
  --yes \
  --json
```

Use `--platform ios` or `--platform android` to match the Revyl app. Do not
guess Android. Wait until the platform is known before writing the upload
command. Use the PR head SHA. Store `REVYL_API_KEY` as a CI secret. Bake the
app id into the command — do not also create a `REVYL_APP_ID` GitHub variable.
GitHub Actions metadata (`scm_head_sha`, PR number, and related fields) is
stamped automatically.

Hard rules for CI:

- The file is a simulator `.app` / `.app.zip` or an installable `.apk` — not a
  store `.ipa` or Play AAB.
- The job **only uploads**. It does not start a device, prove, or comment.
- No Cursor API key belongs in GitHub.
- Signing keys, Expo tokens, and store credentials stay in CI — the Automation
  never needs them.

## 3. Emit the Automation checklist

The agent cannot create the Automation. Tell the user to finish at
[cursor.com/automations](https://cursor.com/automations):

1. Paste the contents of [PROMPT.md](PROMPT.md) in this skill directory
   **verbatim** as the Automation instructions. Read that file and show it in a
   copy-ready block. Do not paraphrase.
2. Enable **Comment on pull request** and **shell**. Do not enable Revyl MCP.
   Do not call `start_dev_loop` or `list_builds`.
3. Trigger must be **CI completed** / **Workflow run completed** scoped to the
   upload job. Do not recommend Pull request pushed as the default.
4. Add secret `REVYL_API_KEY` (a Revyl CLI API key from Settings) and
   variable `REVYL_APP_ID`.
5. Surface, do not pretend to fix: paid Cursor plan, and the repo connected in
   Cursor so Comment on pull request works.

The Automation runner must install the CLI itself (`install.sh` in PROMPT.md).
The desktop plugin pin does not apply there.

Do **not** rely on `gh` or the GitHub API for commenting. The native Comment
tool posts as the Automation identity.

| Name | Kind | Where |
| --- | --- | --- |
| `REVYL_API_KEY` | Secret | CI + Cursor Automation |
| `REVYL_APP_ID` | Variable | Automation only |
| Signing / Expo / store credentials | Secret | CI only |

## What the Automation will do

```text
CI builds the app
  → revyl build upload (commit metadata stamped)
  → Cursor Automation starts
  → matches Revyl build for the PR head SHA
  → revyl device start --build-version-id …
  → screenshots / validations
  → session share + publish
  → ## Revyl device proof comment on the PR
```

If no matching build exists after a brief wait, it comments that proof was not
run and stops. It never rebuilds and never claims proof without a device
session.

## Related

- Prove a PR that already has a CI-uploaded build: `revyl-proof-ci`
- Cloud agent hygiene: `revyl-cloud-agent`
- Docs: [Cursor proof (BYO-CI)](https://docs.revyl.ai/guides/cursor-proof)
