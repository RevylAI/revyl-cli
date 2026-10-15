# Revyl plugin for Grok Build

Opens your mobile app's Revyl Atlas map and live cloud iOS and Android device
sessions inside Grok through Revyl's hosted MCP server.

The plugin contains one MCP server configuration and one skill. It has no
hooks, runs no local commands, and downloads nothing.

## Network endpoints and credentials

| Endpoint                              | Purpose                                                                   |
| ------------------------------------- | ------------------------------------------------------------------------- |
| `https://backend.revyl.ai/api/v1/mcp` | Revyl's hosted MCP server, configured in `.mcp.json` as `revyl-workspace` |
| `https://auth.revyl.ai/oauth/2.1`     | OAuth sign-in for that server; Grok opens it on first connection          |
| `https://app.revyl.ai`                | The Atlas and device views embedded in the tool results                   |

On first use, Grok asks you to sign in to Revyl and choose the organization to
connect. The plugin carries no API key. Device sessions use your
organization's device time, so the skill starts one only when you ask.

## Tools

| Tool                   | What it does                                                   |
| ---------------------- | -------------------------------------------------------------- |
| `list_apps`            | Lists the organization's apps with their IDs and latest builds |
| `open_revyl_workspace` | Shows the Atlas map or a device session                        |
| `list_device_sessions` | Lists queued, starting, and running device sessions            |
| `start_device_session` | Starts one cloud iOS or Android device session                 |
| `screenshot`           | Captures the current screen of a running session               |
| `stop_device_session`  | Stops one device session                                       |

For other editors and the Revyl CLI, see
[Revyl's IDE and MCP setup guide](https://docs.revyl.ai/cli/mcp-setup).

## License

MIT
