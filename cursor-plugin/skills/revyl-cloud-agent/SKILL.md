---
name: revyl-cloud-agent
description: Revyl conventions for Cursor Cloud/background agents - CLI install, sign-in, remote builds, artifact evidence, session cleanup, and PR flow.
---

# Revyl Cloud Agent Skill

Use this skill whenever you are running as a Cursor Cloud/background agent (headless Linux VM) and working with Revyl. Drive devices with the CLI. The plugin does not start MCP.

## Environment Ground Rules (non-negotiable)

- The VM is headless and non-interactive. Never start the `revyl dev` TUI.
- Before the first `revyl` command, prepend the user PATH directories the
  plugin publishes the CLI onto (`~/.revyl/bin`, then `~/.local/bin` if that
  path already holds a different CLI):

```bash
export PATH="$HOME/.revyl/bin:$HOME/.local/bin:$PATH"
```
The plugin cache is not on PATH. Do not invoke `${CURSOR_PLUGIN_ROOT}` or the
cached binary as the shell command.

- If a real `REVYL_API_KEY` is in the environment, run `revyl auth persist-cloud-env` once so the key never reaches argv. Never print the key.
- If that command reports no key, run `revyl auth login` and post the printed approval URL as a clickable markdown link plus the short code, then wait. Optional `REVYL_API_KEY` as a Runtime Secret is the unattended path, not the only path. Never request or accept the key in chat.
- **The VM has no Xcode.** Native iOS dev loops must use `revyl dev --remote`. Treat Android the same unless the VM demonstrably has the SDK.

## Session Lifecycle (devices cost money and outlive the VM)

- Cloud device sessions do not die when the VM exits. `revyl dev stop` is mandatory before completion.
- At the start of a run, use `revyl dev list` to check for a suitable existing session. Do not stack new devices on stale ones.

## Bounded Monitoring (never hang the shell)

- Use `revyl dev status` for independent status snapshots.
- After native changes, call `revyl dev rebuild`, continue independent work, then `revyl dev rebuild --wait` with a finite timeout.

## Artifacts and Evidence

- Post `viewer_url` as a clickable markdown link as soon as `revyl dev --detach --json` returns.
- Use `revyl device screenshot` and `revyl device validation` as evidence.
- Never claim the Cloud VM opened a browser or the user's local Cursor Desktop.

## Auth Bypass and Secrets

- Selected launch configurations apply automatically to fresh raw device, dev, test, and workflow runs. Do not fetch their payloads or repeat them explicitly.
- If the app shows a logged-out state mid-session but the boot token is still valid, run `revyl dev auth refresh --json`. If the token itself expired, `revyl dev stop` then `revyl dev` so a fresh mint is applied as launch environment.
- Never paste launch-configuration payloads, tokens, or API keys into code, chat, logs, screenshots, or PRs.

## Optional custom MCP

Users who add a personal MCP entry with a literal command such as `revyl` or
`/usr/local/bin/revyl` can install the MCP dev-loop skill by name. Never
`${CURSOR_PLUGIN_ROOT}` or `${env:...}`. The plugin does not start that server.
Do not call MCP tools unless that skill is installed and the tools are
registered.

## Git Hygiene

- Before committing, confirm `.revyl/.gitignore` exists (created by `revyl init`; it keeps dev-session runtime state out of git — only `config.yaml` and `tests/` belong in the repo).
- Never commit artifacts, screenshots, logs, or minted launch-var files.

## PR Flow

- If `gh` is read-only in this environment, use the ManagePullRequest tool instead.
- The PR body carries the evidence: the `viewer_url`, the key inline screenshots, and a one-line summary of what was verified on-device.
