# Revyl for Omnara agents

Give any [Omnara](https://omnara.com) agent Revyl's cloud iOS and Android
devices, web browsers, saved tests and workflows, builds, and Atlas, through
Revyl's hosted MCP server at `https://backend.revyl.ai/api/v1/mcp`.

## Add it

Open Claude Code, Codex, Cursor, or any other coding agent and say:

```text
Read https://raw.githubusercontent.com/RevylAI/revyl-cli/main/examples/omnara/SKILL.md and follow it to add Revyl to my Omnara agent.
```

It signs you in to Revyl, uploads the `revyl-workspace` skill, adds Revyl to
the agent you pick (or creates one), runs it once, and can set up a nightly
smoke run of your saved Revyl tests posted to Slack.

## What's here

| File | What it is |
| --- | --- |
| [`revyl.yaml`](revyl.yaml) | The `mcp:` and `skills:` entries to merge into any agent config |
| [`agent.yaml`](agent.yaml) | A starter agent for teams without one |
| [`SKILL.md`](SKILL.md) | The setup steps a coding agent follows, with the exact `npx omnara` commands |
| [`connect-revyl.mjs`](connect-revyl.mjs) | Revyl sign-in for a self-hosted Omnara, whose callback URL Revyl's sign-in doesn't accept |

## How it works

Omnara calls Revyl's MCP server directly, so the agent needs no machine. The
server signs in with OAuth: you choose the Revyl organization once, Omnara
stores the tokens as an `oauth_token_set` secret and refreshes them, and the
config only references the secret's ID. Revyl API keys don't work as a bearer
token on this server.

The skill is the host-neutral copy of Revyl's `revyl-workspace` skill from
[`plugins/revyl-mcp/skills/revyl-workspace`](../../plugins/revyl-mcp/skills/revyl-workspace/SKILL.md).
The permissions in `revyl.yaml` let the agent read and work inside a session
it already has, and make it ask before it starts a device session, a test
run, a build, or an exploration, or creates or changes a test. Tools Revyl
adds later ask too until you allow them.
