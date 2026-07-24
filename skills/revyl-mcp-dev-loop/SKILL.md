---
name: revyl-mcp-dev-loop
description: MCP dev-first mobile loop for reliable screenshot-observe-action execution and grounded interactions.
---

# Revyl MCP Dev Loop Skill

Use this skill for the full flow:
1. Start dev loop equivalent.
2. Execute screenshot-observe-action cycles.

## Default Operating Mode

Always prefer dev-loop flow before plain device-only flows:
1. Call `start_dev_loop`.
2. On success, share `viewer_url` as a clickable link and confirm the session is active. When the inline Revyl app exposes **Open live device**, the user may use it to hand the URL to the host browser; the link remains the portable fallback.
3. On a setup failure, follow the single structured `remediation` action and
   retry `start_dev_loop` once.
4. Call `screenshot()` and begin interaction.

Fallback to plain device session only when dev loop is unavailable.

## Execution Guardrails

1. First tool call must be `start_dev_loop`.
2. Do not call listing tools unless the user explicitly asks.
3. Treat `next_steps` as advisory only.
4. Re-anchor with `screenshot()` before state-dependent actions.
5. Express device actions through the current natural-language schema, for example `interact(task="Tap the Sign In button")`. Do not calculate or supply coordinates.
6. Use `setup_status` only when the user explicitly asks for setup diagnostics.
7. Never claim that a Cloud Agent opened the viewer on the user's local computer. Cloud tools run on the remote VM; the inline open control and clickable URL are client-side handoffs.

## Setup Recovery

Handle setup outcomes as bounded recovery steps:

- `auth_required` / `auth_expired` / `auth_invalid`: run `remediation.command` once, then retry `start_dev_loop`.
- `cloud_secret_required`: tell the user to add `remediation.env_name` as a Runtime Secret and start a new Cloud session. Do not retry when `restart_required` is true.
- `project_not_initialized`: run the exact command from `remediation.command` once with the working directory set to `remediation.working_directory`, then retry `start_dev_loop`. The command ends with `init --non-interactive`; its executable may be the plugin-pinned runtime rather than `revyl` on `PATH`. Do not rewrite it, add `--force`, or initialize silently.
- `project_ambiguous`: inspect `remediation.candidate_roots`, select the intended project from repository context, and retry with that exact root as `project_dir`. If the intended project is unclear, ask the user to choose. Do not initialize another project or retry without an explicit `project_dir`.
- `project_invalid`: report `remediation.config_path` and wait for it to be repaired before retrying.

After one remediation and one retry, stop and report any remaining failure. Do
not enter a setup loop.

## Interaction Loop

For each iteration:
1. `screenshot()`
2. State visible UI in one short line.
3. Take one best action with `interact(task="...")`.
4. `screenshot()` to verify.
5. Repeat.

Short deterministic burst allowance:
- Up to two actions before verification for obvious two-step entry flows.
