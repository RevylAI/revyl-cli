---
name: revyl-cloud-agent
description: Revyl conventions for Cursor Cloud/background agents - headless VM rules, remote builds, artifact evidence, session cleanup, and PR flow.
---

# Revyl Cloud Agent Skill

Use this skill whenever you are running as a Cursor Cloud/background agent (headless Linux VM) and working with Revyl. It layers cloud-agent-specific rules on top of `revyl-mcp-dev-loop`; load both.

## Environment Ground Rules (non-negotiable)

- The VM is headless and non-interactive. Prefer the Revyl MCP tools; never
  start the `revyl dev` TUI.
- Browser login is impossible. Call `start_dev_loop` first; if it returns
  `cloud_secret_required`, tell the user to add `remediation.env_name` as a
  Runtime Secret and start a new Cloud session. Never request or accept the key
  in chat, and do not retry when `restart_required` is true.
- If `start_dev_loop` returns `project_not_initialized`, run its exact
  remediation command once in the returned working directory, then retry once.
- **The VM has no Xcode.** Native iOS dev loops must call
  `start_dev_loop(remote=true, seed_latest=true)`. Treat Android the same
  unless the VM demonstrably has the SDK.

## Session Lifecycle (devices cost money and outlive the VM)

- Cloud device sessions do not die when the VM exits. `stop_dev_loop` (or
  `device_session(action="stop")`) is mandatory before completion.
- At the start of a run, use `device_session(action="list")` to check for a
  suitable existing session. Do not stack new devices on stale ones.

## Session Heartbeat (idle auto-stop is real)

- Reading files and waiting do not count as device activity. During long
  non-device work, call `get_dev_status` or `screenshot` periodically.
- If a session drops, inspect `get_dev_status` once, then start a fresh session
  and re-drive in one pass. One passed validation plus one screenshot is
  sufficient evidence.

## Bounded Monitoring (never hang the shell)

- Use `get_dev_status` for independent status snapshots.
- After native changes, call `rebuild`, continue independent work, then call
  `wait_for_rebuild` with the returned handle and a finite timeout.

## Artifacts and Evidence

- Post `viewer_url` as a clickable link as soon as `start_dev_loop` returns.
- The inline Revyl app may offer **Open live device**, which asks the Cursor host to open that URL after a user click. Keep the visible link as the guaranteed fallback when app UI or host navigation is unavailable.
- Use MCP `screenshot` and `device_validation` results as inline evidence.
- Never claim the Cloud VM opened a browser or the user's local Cursor Desktop. Automatic Cloud-to-client navigation requires a Cursor host capability; shell browser commands target only the VM.

## Auth Bypass and Secrets

- If the app shows a logged-out state mid-session, re-mint the launch vars with the repo's own mint script (values flow through env only), then `revyl dev auth refresh --json`.
- Never paste launch-var values, tokens, or API keys into code, chat, logs, screenshots, or PRs — reference variable names only. Avoid typing real credentials on-screen; use test variables.

## Git Hygiene

- Before committing, confirm `.revyl/.gitignore` exists (created by `revyl init`; it keeps dev-session runtime state out of git — only `config.yaml` and `tests/` belong in the repo).
- Never commit artifacts, screenshots, logs, or minted launch-var files.

## PR Flow

- If `gh` is read-only in this environment, use the ManagePullRequest tool instead.
- The PR body carries the evidence: the `viewer_url`, the key inline screenshots, and a one-line summary of what was verified on-device.
