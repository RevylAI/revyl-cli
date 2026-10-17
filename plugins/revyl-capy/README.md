# Revyl plugin for Capy

Run and verify mobile app changes on Revyl cloud iOS and Android devices from
[Capy](https://capy.ai) cloud threads. The plugin follows the
[Agent Plugins](https://agent-plugins.org/specification) format that Capy
installs from GitHub, and contains two MCP servers and one skill. It has no
hooks.

## Install

Add the plugin to Capy from GitHub as described in the Capy section of
[Revyl's IDE and MCP setup guide](https://docs.revyl.ai/cli/mcp-setup#capy-plugin).
Capy downloads the whole repository behind an install link and refuses one that
is too large, so install from the public `RevylAI/revyl-cli` repository rather
than from a larger repository that contains a copy of this folder.

## Signing in

Both servers act as one member in their Revyl organization, and both use
per-member connections, so every Capy member acts as themselves in Revyl. The
plugin files hold no credentials.

- **`revyl-workspace`** is Revyl's hosted MCP server. Each member clicks
  **Connect** on the plugin's page and signs in to Revyl in the browser, the
  same OAuth sign-in the ChatGPT app uses. Capy keeps and refreshes the
  sign-in, so it lasts across threads. It lists apps and starts, lists,
  screenshots, and stops device sessions of existing builds.
- **`revyl`** runs the Revyl CLI on each thread's cloud machine and has the
  dev loop: remote builds, rebuilds, taps and typing, and semantic validation.
  Capy can't give a STDIO server an OAuth sign-in, so this server signs in on
  its own. A member who adds the optional `REVYL_API_KEY` secret (a personal
  key from **Settings → API Keys** in Revyl) gets dev loops with no prompt.
  Without it, the agent runs the pinned CLI's `auth login` the first time a
  thread needs the dev loop and posts an approval link, and the member
  approves it in the browser. That approval lasts for the thread's machine only. It creates a
  CLI key in the member's Revyl account, which the agent revokes with
  `auth logout` when the member is done.

## How the dev-loop server starts

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
`revyl-capy-dev-loop` skill tells the agent how to use both servers.

## Viewing the device

The agent shares each session's `viewer_url` as a link, which opens Revyl's
live device viewer in the browser. Capy does not render MCP Apps views, so the
inline screenshot and workspace views that ChatGPT and other MCP Apps hosts
show do not appear in Capy threads.

## Network endpoints

| Endpoint                                                 | Purpose                                                         |
| -------------------------------------------------------- | --------------------------------------------------------------- |
| `https://backend.revyl.ai/api/v1/mcp`                    | The hosted `revyl-workspace` server                             |
| `https://auth.revyl.ai/oauth/2.1`                        | OAuth sign-in for that server; Capy opens it on **Connect**     |
| `https://github.com/RevylAI/revyl-cli/releases/download` | Pinned CLI download when a thread first starts the dev loop     |
| `https://backend.revyl.ai`                               | Revyl API calls from the dev-loop server                        |
| `https://app.revyl.ai`                                   | The live device viewer and the dev loop's sign-in approval page |

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
