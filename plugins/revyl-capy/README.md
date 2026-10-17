# Revyl plugin for Capy

Run and verify mobile app changes on Revyl cloud iOS and Android devices from
[Capy](https://capy.ai) cloud threads. The plugin follows the
[Agent Plugins](https://agent-plugins.org/specification) format that Capy
installs from GitHub, and contains one STDIO MCP server and one skill. It has
no hooks.

## Install

Add the plugin to Capy from GitHub as described in the Capy section of
[Revyl's IDE and MCP setup guide](https://docs.revyl.ai/cli/mcp-setup#capy-plugin).
Capy downloads the whole repository behind an install link and refuses one that
is too large, so install from the public `RevylAI/revyl-cli` repository rather
than from a larger repository that contains a copy of this folder.

## Credentials

The `revyl` server's recipe declares one secret, `REVYL_API_KEY`, with
`connection: member`. Revyl API keys are personal: a key acts as the person
who created it, with their role, in their Revyl organization. A per-member
connection keeps every Capy member acting as themselves in Revyl, rather than
as whoever installed the plugin. Capy passes the key to the server as an
environment variable. The plugin files hold no credentials.

A thread gets the tools only when its owner has added a key, so threads started
by a Capy service user or an automation without one have no Revyl tools.

## How the server starts

Capy runs STDIO servers on each thread's cloud machine, so the plugin works in
cloud threads only. Capy starts the server with:

```text
sh ${PLUGIN_ROOT}/scripts/launch-revyl mcp serve --profile dev
```

`scripts/launch-runtime` reads the CLI release and per-platform SHA-256 values
pinned in `runtime-manifest.json`. It reuses a verified cached CLI under the
plugin's data directory (`$PLUGIN_DATA/runtime`), adopts an installed CLI that
is byte-identical to the pin, or downloads the pinned asset from the Revyl CLI's
GitHub release and verifies it before running it. A corrupt cached file is never
run. `REVYL_BINARY` overrides the pinned CLI.

The `dev` profile exposes the CLI's focused dev-loop and device tools:
`start_dev_loop`, `get_dev_status`, `rebuild`, `wait_for_rebuild`,
`stop_dev_loop`, `device_session`, `screenshot`, `interact`,
`device_validation`, `device_navigate`, and `setup_status`. The
`revyl-capy-dev-loop` skill tells the agent how to use them.

## Viewing the device

The agent shares each session's `viewer_url` as a link, which opens Revyl's
live device viewer in the browser. Capy does not render MCP Apps views, so the
inline screenshot and workspace views that ChatGPT and other MCP Apps hosts
show do not appear in Capy threads.

## Network endpoints

| Endpoint                                                 | Purpose                                    |
| -------------------------------------------------------- | ------------------------------------------ |
| `https://github.com/RevylAI/revyl-cli/releases/download` | Pinned CLI download on first start         |
| `https://backend.revyl.ai`                               | Revyl API calls made with the member's key |
| `https://app.revyl.ai`                                   | The live device viewer the agent links to  |

Device sessions and remote builds use your organization's Revyl device and
build time. Revyl handles apps, sessions, and screenshots under its privacy
policy: <https://revyl.com/privacy>. Questions go to support@revyl.ai.

## Maintenance

`runtime-manifest.json`, `scripts/launch-runtime`, and `assets/icon.svg` are
copies of the Codex plugin's pinned runtime and the shared Revyl icon.
`make -C revyl-cli sync-codex-plugin` refreshes them with the Codex pin, and
`make -C revyl-cli sync-capy-plugin` refreshes only them; the package tests
fail while the copies differ. Increase the `plugin.json` version with every
released change.

## License

MIT
