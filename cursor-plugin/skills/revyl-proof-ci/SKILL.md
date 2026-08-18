---
name: revyl-proof-ci
description: Prove a pull request using a CI-uploaded Revyl build — match commit, start a device by build id, capture evidence, and comment. Guidance only; execution stays on the Revyl CLI.
---

# Revyl Proof from CI Builds

Use this skill when proving a pull request whose artifact was **already uploaded
by CI** (`revyl build upload`), with or without the Revyl GitHub App. It layers
on top of `revyl-cli-dev-loop` / `revyl-cloud-agent` for device hygiene; load
those when you are in a Cursor Cloud or Automation environment.

This skill does **not** add a new runtime. Prove with the existing Revyl CLI
only.

## When to use

- Cursor Automation / Cloud Agent tasked with device proof after CI upload
- Manual "prove this PR" requests against a BYO-CI build
- Twin path: Revyl GitHub `use_existing_ci: true` + `proof_harness: { kind: cursor }`

To **create** the scheduled Automation (CI upload + paste-ready prompt), use
`revyl-proof-automation`. Canonical prompt and triggers also live in
`revyl-cli/examples/cursor-automation/prove-mobile-pr/` and
[Cursor proof](https://docs.revyl.ai/integrations/cursor-proof).

## Non-negotiable rules

- **Do not rebuild.** Find the Revyl build for this commit; do not run
  `revyl build` / local native builds to produce a new artifact for proof.
- **Never claim proof without a device session.** If no matching build exists
  after a brief wait/retry, say so and stop.
- Prefer `revyl device start --build-version-id <id>`.
- Stop every session before finishing (`revyl device stop`).
- Comment with Cursor's native **Comment on pull request** tool. Edit an
  existing `## Revyl device proof` comment when present; otherwise create one.
- Never paste `REVYL_API_KEY`, launch-var values, or tokens into comments.

## Loop

1. **Match the build to the PR head commit**
   - `revyl build list --app <REVYL_APP_ID> --json`
   - Match `metadata.scm_head_sha`, `metadata.git.commit` /
     `commit_short`, or a version string equal to the head SHA
   - Brief wait + retry once or twice if CI upload has not landed yet

2. **Start the device on that build**
   ```bash
   revyl device start --build-version-id <build-version-id> --json
   ```
   Run from the directory that owns `.revyl/config.yaml` so auth_bypass /
   before_session apply.

3. **Exercise the diff**
   - Read the PR diff; verify the behaviour it changes
   - `revyl device screenshot` / `revyl device validation`
   - Save a small set of key screenshots locally with descriptive names

4. **Publish evidence**
   ```bash
   revyl device stop
   revyl device report --session-id <session-id> --json
   revyl session share <session-id> --json
   revyl session publish <file> --session <session-id> --json
   ```
   Use `shareable_link` and each `public_url`. Do not paste short-lived
   `video_url` / `X-Amz-*` links. Do not commit screenshots.

5. **PR comment**
   - Heading `## Revyl device proof`
   - Bold **build id**, **app commit**, **device**, **pass/fail**
   - `## Evidence` with shareable report link + HTML `<img width="220">`
     thumbnails in a table (same shape as the Automation `PROMPT.md`)

## Related

- Set up the Cursor Automation: `revyl-proof-automation`
- Automation package: `revyl-cli/examples/cursor-automation/prove-mobile-pr/`
- CI upload example: `revyl-cli/examples/ci-github-actions/upload-for-cursor-proof.yml`
- Cloud agent hygiene: `revyl-cloud-agent` skill
