---
name: add-revyl-to-omnara
description: Add Revyl to an Omnara agent, so it can start cloud iOS and Android devices and browsers, run saved Revyl tests, follow builds, and read Atlas through Revyl's hosted MCP server. Use when the user asks to add, connect, set up, update, or remove Revyl on Omnara, or to run Revyl tests on a schedule from Omnara.
---

# Add Revyl to an Omnara agent

You are giving one of the user's Omnara agents Revyl's tools: cloud iOS and
Android devices, web browsers, saved tests and workflows, builds, and Atlas.
The goal is an agent profile with Revyl's MCP server and skill attached that
the user has watched list their Revyl apps, and, if they want it, a schedule.

Files in this folder:

- `revyl.yaml`: the `mcp:` and `skills:` entries to merge into any agent config.
- `agent.yaml`: a starter agent for users who don't have one.
- `connect-revyl.mjs`: the Revyl sign-in for a self-hosted Omnara, used only
  when step 2 says so.

If this folder isn't local, fetch each file from
`https://raw.githubusercontent.com/RevylAI/revyl-cli/main/examples/omnara/<file>`.

Ground rules:

- Skip anything already done, and follow the user's lead on order.
- Stop only where a step says to ask the user; ask in a sentence or two, then
  end your turn and wait.
- Where a step gives a default, use it and say in a line what you picked.
- If a command fails, `npx omnara <command> --help` and
  [docs.omnara.com](https://docs.omnara.com) have the details.
- Never ask for a Revyl password or API key. Revyl's MCP server signs in with
  OAuth only, so an API key doesn't work as a bearer token here.

## 1. Connect to Omnara

Run `npx omnara whoami`. If you aren't logged in, run `npx omnara login`
without piping its output, show the user the approval link and code it prints,
and wait for them to approve. On a self-hosted Omnara, first run
`npx omnara config --api-url <origin>/api/v1 --issuer-url <origin>`.

Pick the org and project (`npx omnara whoami --json`,
`npx omnara projects list --org <org-id> --json`): use them if there's one of
each, otherwise the defaults from `npx omnara config` or the project named
`Default`, and ask only if it's still unclear. Save the choice with
`npx omnara config --org <org-id> --project <project-id>`.

## 2. Sign in to Revyl

Reuse a secret named `revyl-mcp-oauth` if one exists
(`npx omnara secrets list --owner-kind project --owner-project-id <project-id> --name revyl-mcp-oauth --json`).
Otherwise start Omnara's MCP sign-in, and tell the user to sign in to Revyl and
pick the Revyl organization the agent should use:

```sh
npx omnara secrets mcp-oauth --owner-kind project --owner-project-id <project-id> \
  --mcp-url https://backend.revyl.ai/api/v1/mcp --name revyl-mcp-oauth
```

That works on Omnara Cloud. Revyl's sign-in accepts only Omnara Cloud's
callback URL, so on a self-hosted Omnara it fails with `invalid_redirect_uri`.
There, run this instead, which signs in through a local callback on port
33418 and stores the same kind of secret:

```sh
node connect-revyl.mjs --project <project-id>
```

Either way, note the secret's `id` (`sec_…`). Omnara refreshes the token on
its own. If tool calls later fail with `mcp_connection_failed`, delete the
secret and run this step again.

## 3. Upload the Revyl skill

Reuse a skill named `revyl-workspace` if the project has one
(`npx omnara skills list --owner-kind project --owner-project-id <project-id> --json`).
Otherwise download it and upload the folder:

```sh
mkdir -p revyl-workspace
curl -fsSL -o revyl-workspace/SKILL.md \
  https://raw.githubusercontent.com/RevylAI/revyl-cli/main/plugins/revyl-mcp/skills/revyl-workspace/SKILL.md
npx omnara skills create --owner-kind project --owner-project-id <project-id> \
  --archive ./revyl-workspace --json
```

Note its `id` (`skl_…`). Uploading again later adds a revision under the same
ID, and agents pick it up on their next model call.

## 4. Add Revyl to the agent

`npx omnara profiles list --json` lists the user's agents. Use the one they
named; if there are several and they didn't say, ask which one. If there are
none, use `agent.yaml` and name the profile `revyl-agent`.

- **Existing profile:** save its config with
  `npx omnara profiles get <agent-profile-id> --json` (the YAML or JSON is in
  `current_config.source`) to `./agent.yaml`. Add the `revyl-workspace` entry
  from `revyl.yaml` under its `mcp:` key and the skill ID to its `skills:`
  list, creating either key if it's missing and keeping everything else as it
  is.
- **New profile:** copy `agent.yaml` and add both keys from `revyl.yaml`.
  Pick the model with `npx omnara grant models list --json`: a strong model
  that accepts images, because Revyl's `screenshot` returns one. That gives
  `{{MODEL_PROVIDER_CONFIG}}` and `{{MODEL_NAME}}`.

Replace `{{REVYL_OAUTH_SECRET_ID}}` and `{{REVYL_WORKSPACE_SKILL_ID}}`, then
check that `grep -n '{{' agent.yaml` prints nothing. Save it with
`npx omnara profiles update <agent-profile-id> --file ./agent.yaml --json`, or
`npx omnara profiles create --name revyl-agent --file ./agent.yaml --json`.
Note the profile's `id` and `current_config_id`.

The permissions in `revyl.yaml` let the agent read and act inside a session
it already has without asking, and make it ask before anything that uses
device time or changes the Revyl organization: starting a session, a test
run, a build, or an exploration, and creating or changing a test. Keep them
unless the user wants otherwise.

## 5. Try it

```sh
npx omnara agents launch --profile <agent-profile-id> --config <current-config-id> \
  --message "List my Revyl apps." --json
```

Give the user the link to watch it:
`https://app.omnara.com/projects/<project-id>/agents/<agent-id>` (on a
self-hosted Omnara, its own origin). The Revyl calls show up as
`mcp__revyl-workspace__…`. Then suggest a first device check they can send in
the console, such as "Start an Android session for <app>, check that it opens,
and stop it". The agent asks before starting the session; they approve it in
the console.

## 6. Reach it and schedule it (optional)

Ask in one message where the user wants to talk to the agent and whether it
should run their Revyl tests on a schedule. Wait for the reply.

- **Slack or Discord:** follow
  [`integrations.md`](https://raw.githubusercontent.com/omnara-ai/omnara/main/examples/integrations.md),
  naming the integrations `<profile-name>-slack` or `-discord` and the bot
  "Revyl".
- **Nightly smoke run:** a scheduled run has no one to approve it, so set
  `run_test` to `always_allow` under `tools:` in the agent's
  `revyl-workspace` entry, update the profile, and tell the user you did.
  Then create the trigger at 2am in the user's IANA timezone (ask if you
  can't tell it from their machine), naming the saved workflow or tests to
  run:

  ```sh
  npx omnara crons create --name <profile-name>-nightly-smoke \
    --target-type profile --target-agent-profile-id <agent-profile-id> \
    --cron '0 2 * * *' --timezone <user-timezone> \
    --message-template 'Run the saved Revyl workflow "<name>" once with run_test, follow it with get_run until it finishes, and report passed and failed tests with the failing step and report_url.' --json
  ```

  To post each report in Slack, see "Scheduled runs in a channel" in
  `integrations.md`.

## 7. Wrap up

Summarize what you created: the secret, the skill, the profile or the change to
it, and any integration or trigger. To remove Revyl later, delete its entry
from the agent's `mcp:` and the skill from `skills:`, and update the profile.
Other agents in the project may share the secret and the skill, so delete
each one with `npx omnara secrets delete <secret-id>` or
`npx omnara skills delete <skill-id>` only when no other profile's config
references its ID.
