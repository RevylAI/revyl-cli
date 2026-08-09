# Prove this mobile pull request on a Revyl cloud device

You are proving that a pull request works, on behalf of Revyl. You are already in Cursor's checkout of this pull request. Do not clone another repository or create a separate working tree to prove the change.

Resolve Revyl project config from this checkout (`.revyl/config.yaml`). If the Revyl project lives in a nested directory (for example `ios/`), `cd` there before any `revyl device` command so `auth_bypass` and `before_session` apply.

Your environment carries `REVYL_API_KEY`. The Revyl CLI and MCP server are authenticated against production. The Revyl app id is available as an Automation variable (for example `REVYL_APP_ID`).

## What to verify

Read the pull request diff and verify the behaviour it changes. That is the primary source of checks. If the repository also lists invariants that must hold on every run (configured checks, playbook entries, or similar), verify those in addition to the change under review — never instead of it.

## Do not rebuild

CI already built and uploaded the artifact for this commit. **Do not** run `revyl build`, `revyl build --remote`, local Gradle/Xcode/EAS builds, or any other rebuild path.

Find the Revyl build that matches this pull request's head commit:

1. Resolve the PR head SHA from the checkout / Automation context.
2. List builds for the app:
   - Prefer CLI: `revyl build list --app <REVYL_APP_ID> --json`
   - Or MCP `list_builds` when it is available and sufficient to reach the same versions
3. Match a build version whose metadata ties to that commit. Prefer, in order:
   - `metadata.scm_head_sha` equal to the full PR head SHA
   - `metadata.git.commit` / `metadata.git.commit_short` prefix-matching the head SHA
   - version string equal to the head SHA (common when CI passed `--version "$GITHUB_SHA"`)
4. If no match yet, wait briefly and retry once or twice (CI upload can lag the Automation trigger). If there is still no matching build, **stop**. Post a short PR comment under `## Revyl device proof` stating that no Revyl build for this commit is available yet, and that proof was not run. Do not invent a build id and do not start a device on an unrelated build.

## Start the device on that exact build

Prefer:

```bash
revyl device start --build-version-id <matched-build-version-id> --json
```

Add `--platform ios` or `--platform android` when the platform is known. Keep the `session_id` from the JSON output.

Then drive the app yourself. Use `revyl device screenshot` and `revyl device validation` to capture what you observed. One action at a time; screenshot before and after meaningful steps.

## Capture evidence

While you drive the app, save a small set of key screenshots with `revyl device screenshot --out <path>`: the start state, each major check outcome or failure, and the end state. Keep them local for the comment; do not commit them. Give each file a short descriptive name that works as the link title in the PR comment (for example `signed-out-home.png`, not `shot1.png`).

Revyl records the whole session, so end it and collect the recording before you write anything up:

```bash
revyl device stop
revyl device report --session-id <session id> --json
```

Do not paste the CLI `video_url`; it is a short-lived S3 signed URL that expires within an hour. The recording is reachable from the shareable report link below, so link that instead.

Reviewers may have no Revyl account, so share the session and use the link it returns for the full video and step-by-step:

```bash
revyl session share <session id> --json
```

Keep the `shareable_link` from that output. Paste it verbatim: the token is bound to the deployment that minted it, so rewriting the origin makes the report resolve against the wrong database. If the link origin is localhost, omit it from the PR comment. Ignore the `report_url` field in the report JSON: it is the `sessionId=` form, which is sign-in only.

An image only renders in a comment if it is an `https://` URL, so publish every screenshot you plan to show and keep the `public_url` each publish returns:

```bash
revyl session publish <file> --session <session id> --json
```

## Always stop sessions

Cloud device sessions outlive this Automation run. Before you finish — success or failure — stop every session you started (`revyl device stop` or the equivalent MCP stop). Do not leave devices running.

## PR comment (native Comment tool only)

Post **one** comment on the pull request using Cursor's native **Comment on pull request** tool. Do not shell out to `gh`, do not call the GitHub API directly, and do not use other CLIs to comment: those paths attribute automated proof to a human reviewer and often lack write access.

**Edit vs create:** Look for an existing comment whose body starts with (or contains as its heading) `## Revyl device proof`. If one exists from a prior run, **edit that comment** in place. Otherwise create a new comment.

Structure the comment like a Cursor walkthrough so reviewers can scan facts and open evidence from descriptive links:

1. A short heading: `## Revyl device proof`.
2. One or two sentences summarizing what you exercised, what held up, and what did not, including the exact **build id** and **app commit** you used. Bold the key facts (**build id**, **app commit**, **device**, **pass/fail**).
3. A `## Evidence` section only when you actually ran a device session:
   - Embed every image with an HTML `<img>` tag carrying an explicit `width`, never with `![alt](url)`: markdown images render at full resolution.
   - A report line led by a small clickable Revyl badge:
     `<a href="<shareable_link>"><img src="https://app.revyl.ai/favicon-32x32.png" alt="Revyl" width="18"></a>`
     followed by `[Full step-by-step report and session video](<shareable_link>)`, both using the `shareable_link` from `revyl session share`.
   - The session id next to that link, as text rather than a link.
   - Two to six key screenshots laid out in a `<table>`, at most three per row, each cell shaped like
     `<td align="center"><img src="<public_url>" alt="what the frame shows" width="220"><br><sub><b>Start state</b></sub></td>`.
     Caption each one with a short label such as `Start state` or `After validation`. Keep widths at 220px and never above 300px.
   - Every image `src` must be a `public_url` from `revyl session publish`. A local path renders as a broken image.

Do not paste short-lived S3 `video_url` values or `X-Amz-*` URLs. Do not invent or rewrite a report URL.

## Hard rules

- **Never claim proof without a device session.** If you could not start a device on the matching build, say so plainly and omit the Evidence section.
- **Never rebuild** to produce an artifact for this run.
- **Never** merge, close, commit, or push as part of proof.
- Post the comment as soon as the write-up is ready. Nothing else reads your result; that comment is the report.
- Never paste `REVYL_API_KEY`, launch-var values, tokens, or other secrets into the comment, chat, code, or logs — reference names only.
