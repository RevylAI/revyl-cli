---
name: revyl-proof-ci
description: Verify a mobile pull request using an exact-SHA match among its app's CI-uploaded Revyl builds, collect device evidence, and prepare a proof summary without rebuilding.
---

# Revyl Proof from CI Builds

Use this skill only for an authorized proof of a pull request whose artifact
was already uploaded by CI. It does not build, upload, or configure an app.
This proof workflow requires shell access and uses the pinned CLI below. It
behaves the same in Claude Code and Codex; where the hosts differ, each rule
names the host it applies to. Do not silently switch to a different MCP build
lookup: its search and pagination contract may differ from the exact-SHA checks
in this skill.

## Launcher and authorization

Resolve the launcher from this skill's own absolute directory before changing
to the app directory. Find that directory the way your host provides it:

- **Claude Code** substitutes it into this file when the skill loads:
  `${CLAUDE_SKILL_DIR}`.
- **Codex** lists each skill with the path of its `SKILL.md`; use that file's
  absolute parent directory. If the Claude Code value above is not an absolute
  path, your host did not substitute it, so use this rule instead.

On POSIX the launcher is `../../scripts/launch-revyl` from that directory; on
Windows it is `../../scripts/launch-revyl.cmd`. Invoke that absolute launcher
for **every** Revyl command; never use `revyl` from `PATH`, `PLUGIN_ROOT`, or
another installed CLI. Shell variables do not persist between tool calls in
either host, so write the resolved absolute launcher path into each command.
Run each command from the app directory (the one holding `.revyl/config.yaml`),
never from the plugin's own folder: the CLI reads the current directory's Git
branch, commit, and config, so a command run inside the plugin resolves the
wrong build or project.

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
commands. Never open a browser or claim that one opened; give the user each
login or viewer URL as a link and wait for approval:

- **Claude Code**: put the full `https://` URL on its own line, not only inside
  Markdown link text, so terminals and the IDE panels keep it clickable.
- **Codex**: give it as a Markdown link; the Codex app opens it when the user
  clicks.

Before starting a device or another paid action, require authorization naming
the app, environment, and paid operation. Resolve the pull request's **full**
head SHA and platform from read-only PR, repository, and user context; ask only
when ambiguous. Do not treat a short SHA, branch name, or version label as a
match. Run from the app directory that owns `.revyl/config.yaml` so the
project's existing launch configuration applies.

If the launcher reports that the local configuration uses a legacy format,
run `"$LAUNCHER" config migrate --check --json` and summarize the fields it
would drop. Migrating rewrites `.revyl/config.yaml` (keeping a backup), so ask
before running `"$LAUNCHER" config migrate --write`, and never hand-edit the
file to get past the error.

## Proof loop

1. Find a CI-uploaded build whose `metadata.scm_head_sha` exactly equals the
   full PR SHA. Narrow the list to the pull request's head Git branch and read
   it a page at a time, newest first:

   ```sh
   "$LAUNCHER" build list --app <app-id> --branch <pr-head-branch> --limit 100 --page 1 --json
   ```

   Each page returns `versions` and `has_next`. Read the next page with
   `--page 2` and so on while `has_next` is true and no exact match has
   appeared, for at most five pages. Whenever that search ends without an
   exact match, including after five pages, repeat it without `--branch`,
   which lists every branch, with the same five-page limit: a CI upload can
   record a different branch name than the pull request's, for example `HEAD`
   from a detached checkout. Only if the fifth page of that all-branch search
   still has more, report **proof not run: build lookup incomplete** rather
   than claiming the build is absent. While the named CI upload
   is pending, make at most three retries, ten seconds apart. If no exact
   match arrives, stop and report **proof not run: no CI build of this
   commit**. Do not bypass CLI authentication, rebuild, re-upload, or use an
   unrelated artifact as a fallback.
2. With explicit authorization, start a device on the matching build and save
   its exact session ID:

   ```sh
   "$LAUNCHER" device start --build-version-id <matching-build-id> --json
   ```

   Give the user the viewer URL immediately as a link when one is present. When
   the hosted Revyl workspace tools are connected (an `open_revyl_workspace`
   tool), open the same session with `view="device"` and its returned
   `session_id`. Never start a second device through `start_device_session`.
   If browser access fails, retain the CLI's existing session and report the
   account-linking problem instead of replacing it.
   Exercise only the changed behavior. Target every device command with
   `-s <session-id>`, save concise screenshots, and open each screenshot before
   describing it. A false or missing semantic verdict is a failed proof, but
   still stop that exact session with `"$LAUNCHER" device stop -s <session-id> --json`.
   Never use global cleanup. A stop is finished only when its JSON says
   `stopped: true`; otherwise, while `"$LAUNCHER" device list --json` still
   lists the session, wait about ten seconds and stop it once more.
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
