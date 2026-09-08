---
name: revyl-codex-proof-ci
description: Verify a mobile pull request using an exact-SHA match among its app's recent CI-uploaded Revyl builds, collect device evidence, and prepare a proof summary without rebuilding.
---

# Revyl Codex Proof from CI Builds

Use this skill only for an authorized proof of a pull request whose artifact
was already uploaded by CI. It does not build, upload, or configure an app.

## Launcher and authorization

Resolve the launcher from this skill's own absolute directory before changing
to the app directory. On POSIX use `../../scripts/launch-revyl`; on Windows use
`../../scripts/launch-revyl.cmd`. Invoke that absolute launcher for **every**
Revyl command; never use `revyl` from `PATH`, `PLUGIN_ROOT`, or another
installed CLI.

```sh
SKILL_DIR="<absolute directory containing this SKILL.md>"
LAUNCHER="$(cd "$SKILL_DIR/../../scripts" && pwd)/launch-revyl"
"$LAUNCHER" --version
"$LAUNCHER" auth status
```

```bat
REM Windows cmd.exe
set "SKILL_DIR=<absolute directory containing this SKILL.md>"
set "LAUNCHER=%SKILL_DIR%\..\..\scripts\launch-revyl.cmd"
"%LAUNCHER%" --version
"%LAUNCHER%" auth status
```

Use normal `"$LAUNCHER" auth login` only when authentication is needed. An
existing environment secret may be used without placing its value in a command,
chat, or artifact. Do not run credential-provisioning or secret-bridge
commands. Post the normal login approval URL as a clickable link and wait for
approval; do not claim that a browser opened.

Before starting a device or another paid action, require authorization naming
the app, environment, and paid operation. Resolve the pull request's **full**
head SHA and platform from read-only PR, repository, and user context; ask only
when ambiguous. Do not treat a short SHA, branch name, or version label as a
match. Run from the app directory that owns `.revyl/config.yaml` so the
project's existing launch configuration applies.

## Proof loop

1. Find a CI-uploaded build whose `metadata.scm_head_sha` exactly equals the
   full PR SHA. For example:

   ```sh
   "$LAUNCHER" build list --app <app-id> --json
   ```

   This pinned CLI lists only the newest 20 versions and does not expose
   pagination. A match in this window is usable; no match does not establish
   that the artifact is absent. While the named CI upload is pending, make at
   most three retries, ten seconds apart. If no exact match arrives, stop and
   report **proof not run: recent-build lookup incomplete**. Explain the
   20-version limit, without claiming a full-history search. Do not invent
   pagination flags, bypass CLI authentication, rebuild, re-upload, or use an
   unrelated artifact as a fallback.
2. With explicit authorization, start a device on the matching build and save
   its exact session ID:

   ```sh
   "$LAUNCHER" device start --build-version-id <matching-build-id> --json
   ```

   Return the viewer URL immediately as a clickable link when one is present.
   Exercise only the changed behavior. Target every device command with
   `-s <session-id>`, save concise screenshots, and open each screenshot before
   describing it. A false or missing semantic verdict is a failed proof, but
   still stop that exact session with `"$LAUNCHER" device stop -s <session-id>`.
   Never use global cleanup.
3. Obtain a report with `"$LAUNCHER" device report -s <session-id> --json` and
   report success only when a matching device session produced the evidence. A
   missing device session is never a passed proof.

## Reporting and publication

When an authorized PR-comment surface is available, use that surface to add or
update the proof. Otherwise return a ready-to-post draft headed
`## Revyl device proof` with the full SHA, matching build ID, device result,
and only the evidence actually collected.

Session sharing and screenshot/report publication are separate external
actions. Run `session share` or `session publish` only after the user explicitly
approves publication and its audience; otherwise keep the evidence local and
return the draft without public links. Never include credentials, launch-var
values, or signed URLs.
