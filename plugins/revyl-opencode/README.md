# Revyl for OpenCode

Revyl gives your OpenCode agent cloud iOS simulators, Android emulators, web
browsers, and Mac computers to run, test, and verify your app, plus Atlas, a
map of your app's screens and flows. This plugin for OpenCode 2:

- adds Revyl's hosted MCP server as `revyl`. It signs in with your Revyl
  account through OAuth; no API key or local install is needed;
- adds Revyl's skills: `revyl-workspace` for the hosted tools, and the
  `revyl-cli-dev-loop`, `revyl-cli-atlas`, `revyl-cli-create`, and
  `revyl-cli-auth-bypass` skills for the Revyl CLI;
- adds a `/revyl` command that sends your request to Revyl's tools, or tells
  you how to sign in when the server needs it;
- lists the session's running Revyl devices in the terminal UI's sidebar, and
  opens each new device's live viewer in the session's browser pane in the
  OpenCode desktop app.

The agent starts a device session, build, or test only when you ask for one.

## Install

```bash
opencode plugin add opencode-revyl
```

Or add `"opencode-revyl"` to `plugins` in `opencode.json`. Then sign in:
run `/mcps` in OpenCode, select `revyl`, and sign in, or run
`opencode mcp auth revyl` in a terminal. Your browser opens Revyl's sign-in,
where you choose the organization to connect.

## Use

```text
/revyl
/revyl start an Android session of my app and check the login screen
```

`/revyl` alone lists your Revyl apps and suggests next steps. You can also ask
for Revyl in any prompt; the skills tell the agent how to use the tools.

When the agent starts a device with the hosted server, the sidebar's **Revyl
devices** section shows its platform and viewer link until the agent stops it.
The desktop app opens the viewer beside the conversation; sign in to Revyl in
that pane when it asks. **Revyl: Show device** in the command palette reopens the
session's latest device. Other clients can read the same list through the
`revyl.devices` RPC that `opencode-revyl/rpc` exports.

## Options

```json
{
  "plugins": [{ "package": "opencode-revyl", "options": { "mcp": "local" } }]
}
```

`mcp` is `"hosted"` by default. `"local"` uses the Revyl CLI's server,
`revyl mcp serve`, which signs in with `revyl auth login` or
`REVYL_API_KEY`; use it where OAuth can't run, such as OpenCode's GitHub
agent. `false` adds no server, so `/revyl` uses the CLI. A `revyl`
server you configured yourself, and skills you installed with
`revyl skill install --opencode`, take precedence over the plugin's.

Setup for other agents is in [Revyl's MCP guide](https://docs.revyl.com/agents/mcp).

## Development

The skills in `skills/` are copies; `node scripts/sync-skills.mjs` refreshes
them from their sources in this repository. `node --test src/index.test.ts`
runs the tests, including a check that the copies are current. To try a
change, add this directory's absolute path to `plugins` in an
`opencode.json`.
