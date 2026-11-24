---
name: revyl-mcp-dev-loop
description: Optional MCP dev-first loop for mobile and Web screenshot-observe-action execution. Not started by the Cursor plugin.
metadata:
  internal: true
---

# Revyl MCP Dev Loop Skill

This skill is for users who **opt into** custom MCP. The Cursor plugin does not
start an MCP server. Add a personal MCP entry with a literal command such as
`revyl` or `/usr/local/bin/revyl`, never `${...}`.

Each server profile registers its own tools. Follow the section for the profile
the server runs: `revyl mcp serve --tool-profile dev` for the focused loop, or
`--tool-profile core` or `--tool-profile full` for the wider tool set. Call only tools the
server lists.

## Web addresses

Revyl's browser runs in the cloud and refuses `localhost`, so a Web session
needs a public HTTPS address. Pick it in this order, taking the first that
works:

1. The local dev server through a temporary public HTTPS tunnel, such as
   `cloudflared tunnel --url http://localhost:<port>`. Ask the user once before
   you open it, and name a deployed address too when you found one.
2. A deployed or preview address that serves this app.
3. Neither: stop and tell the user why. Never open someone else's site or a
   placeholder.

Check the address with `revyl setup check-url <address> --json`: go on only
when `outcome` is `reachable` and the `title` it returns matches what the
code renders. If the CLI says `setup` is an unknown command, run
`revyl upgrade`; if it is still unknown, skip the Web app, because an address can't be checked safely without it, and name the CLI upgrade as the next step. If `outcome` is not `reachable`, follow its `hint` once, such as allowing the tunnel's host for this run of the dev server, and check again. Take the next address, or stop and tell the user.

## Dev profile

### Mobile apps

1. Call `start_dev_loop`. Pass `profile` and `platform` when repository
   evidence determines the named recipe; never invent an active/default value.
2. On success, share `viewer_url` as a clickable link and confirm the session
   is active. When the inline Revyl app exposes **Open live device**, the user
   may use it to hand the URL to the host browser; the link remains the portable
   fallback.
3. On a setup failure, follow Setup Recovery below and retry `start_dev_loop`
   once.
4. Work through the interaction loop below, then call `stop_dev_loop()` unless
   the user asked to keep the loop running.

### Web apps

`start_dev_loop` accepts only `ios` and `android`, and `device_session` takes no
address, so start a Web session from the shell, with an address picked and
checked as Web addresses describes.

```bash
revyl device start --platform web --app-id <web-app-id> --url <public-https-address> --json
```

For a tunnel address, add `--branch local-dev` so the temporary address never
becomes the app's default build. Pass the returned `session_id` to every
`screenshot`, `interact`, and `device_validation` call, and end the session
with `revyl device stop --session-id <session-id>` when finished.

### Interaction loop

For each iteration:
1. `screenshot()`
2. State visible UI in one short line.
3. Take one best action with `interact(task="Tap the Sign In button")`.
4. `screenshot()` to verify.
5. Repeat.

Check each outcome with `device_validation(description="...")`.

## Core and full profiles

These profiles act through the grounded device tools, which take a
natural-language `target`.

### Mobile apps

1. Call `start_dev_loop`, share `viewer_url`, and handle setup failures as the
   dev profile does.
2. Work through the interaction loop below, then call `stop_dev_loop()` unless
   the user asked to keep the loop running.

### Web apps

`start_dev_loop` accepts only `ios` and `android`. Pick and check the
address as Web addresses describes, then call `start_device_session(platform="web", app_id="<web-app-id>", url="<public-https-address>")`.
For a tunnel address, add `branch_name="local-dev"` so the temporary address
never becomes the app's default build. End the session with
`stop_device_session(session_id="<session-id>")` when finished.

### Interaction loop

For each iteration:
1. `screenshot()`
2. State visible UI in one short line.
3. Take one best action, such as `device_tap(target="Sign In button")`,
   `device_type(target="Email field", text="...")`,
   `device_swipe(target="Product list", direction="up")`, or
   `device_instruction(description="Open the checkout screen")`.
4. `screenshot()` to verify.
5. Repeat.

Check each outcome with `device_validation(description="...")`.

## Execution Guardrails

1. For a mobile project, the first tool call must be `start_dev_loop`.
2. Do not call listing tools unless the user explicitly asks.
3. Treat `next_steps` as advisory only.
4. Re-anchor with `screenshot()` before state-dependent actions.
5. Describe targets in natural language. Do not calculate or supply coordinates.
6. Use `setup_status` only when the user explicitly asks for setup diagnostics.
   Its `remediation` object is the read-only view of the same recovery plan:
   follow `check_command` before `apply_command` when both are present.
7. Never claim that a Cloud Agent opened the viewer on the user's local computer. Cloud tools run on the remote VM; the inline open control and clickable URL are client-side handoffs.
8. Up to two actions may run before verification for obvious two-step entry
   flows, such as filling two form fields.

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
