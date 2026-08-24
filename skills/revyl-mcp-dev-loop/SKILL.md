---
name: revyl-mcp-dev-loop
description: Optional MCP dev-first mobile loop for screenshot-observe-action execution. Not started by the Cursor plugin.
---

# Revyl MCP Dev Loop Skill

This skill is for users who **opt into** custom MCP. The Cursor plugin does not
start an MCP server. Add a personal MCP entry with a literal command such as
`revyl` or `/usr/local/bin/revyl`, never `${...}`.

Use this skill for the full flow:
1. Start dev loop equivalent.
2. Execute screenshot-observe-action cycles.

## Default Operating Mode

Always prefer dev-loop flow before plain device-only flows:
1. Call `start_dev_loop`. Pass `profile` and `platform` when repository
   evidence determines the named recipe; never invent an active/default value.
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
   Its `remediation` object is the read-only view of the same recovery plan:
   follow `check_command` before `apply_command` when both are present.
7. Never claim that a Cloud Agent opened the viewer on the user's local computer. Cloud tools run on the remote VM; the inline open control and clickable URL are client-side handoffs.

## Setup Recovery

Handle setup outcomes as bounded recovery steps:

- `auth_required` / `auth_expired` / `auth_invalid`: run `remediation.command` once, then retry `start_dev_loop`. If the command cannot complete, post `outcome.authorization_url` as a clickable markdown link and stop until the user approves; that URL is a live approval request Revyl already registered, and it works from any browser.
- `cloud_secret_required`: run `remediation.command` once to bridge the hosted agent's injected `remediation.env_name`, then retry `start_dev_loop`. If that command reports no key in the environment, post `outcome.authorization_url` as a clickable link and tell the user that adding `remediation.env_name` as a Runtime Secret plus a new Cloud session is the durable fix.
- `project_not_initialized`: use `remediation.command` to pull an existing UI-created or server-managed project, or `remediation.alternative_command` to initialize a genuinely new project. Use repository and user context to choose; ask when unclear. Run only the selected exact command with `remediation.working_directory`, then retry `start_dev_loop` once. The executable may be the plugin-pinned runtime rather than `revyl` on `PATH`; do not rewrite either command or add `--force`.
- `project_legacy_config`: run the exact JSON `remediation.check_command` first and review its canonical proposal and complete omission ledger. Then run the exact `remediation.apply_command` once to create the backup and apply that proposal. If the ledger is lossy, reconcile the migrated file against the reported backup before retrying `start_dev_loop`; otherwise retry directly. Do not rewrite either command, skip the check, or hide reported omissions or ambiguities.
- `project_outside_git`: select a project directory inside an active Git worktree from repository context and retry `start_dev_loop` with that exact path as `project_dir`. Treat `remediation.env_name` as the durable runtime configuration hint, not as permission to invent a path. If no intended Git worktree is evident, ask the user to choose one.
- `project_ambiguous`: inspect `remediation.candidate_roots`, select the intended project from repository context, and retry with that exact root as `project_dir`. If the intended project is unclear, ask the user to choose. Do not initialize another project or retry without an explicit `project_dir`.
- `project_invalid`: report `remediation.config_path` and wait for it to be repaired before retrying.

After one remediation and one retry, stop and report any remaining failure. Do
not enter a setup loop.

If `start_dev_loop` fails with a profile/platform selection error instead of a
setup remediation, inspect its valid choices and retry once with exact
`profile` and `platform` values. Do not guess. If a deprecated `platform_key`
input returns `dev_loop_platform_key_removed`, replace it with `profile` and
`platform`.

## Interaction Loop

For each iteration:
1. `screenshot()`
2. State visible UI in one short line.
3. Take one best action with `interact(task="...")`.
4. `screenshot()` to verify.
5. Repeat.

Short deterministic burst allowance:
- Up to two actions before verification for obvious two-step entry flows.
