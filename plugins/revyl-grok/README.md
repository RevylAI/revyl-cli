# Revyl plugin for Grok Build

Opens your mobile app's Revyl Atlas map and live cloud iOS, Android, web and
macOS sessions inside Grok through Revyl's hosted MCP server.

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
organization's device time, so the skill starts one, or a test run, only when
you ask, and it creates or changes a saved test only when you ask.

## Tools

| Tool                    | What it does                                                     |
| ----------------------- | ---------------------------------------------------------------- |
| `list_apps`             | Lists the organization's apps with their IDs and latest builds   |
| `open_revyl_workspace`  | Shows the Atlas map or a device session                          |
| `list_device_sessions`  | Lists queued, starting, and running device sessions              |
| `start_device_session`  | Starts one cloud iOS, Android, Web, or macOS session             |
| `screenshot`            | Captures the current screen of a running session                 |
| `stop_device_session`   | Stops one device session                                         |
| `device_instruction`    | Carries out one plain-language step on a running session         |
| `device_validation`     | Checks one plain-language statement against the screen           |
| `get_device_step`       | Waits for a step that was still running                          |
| `computer_action`       | Clicks, types, drags, scrolls, or presses keys on a Mac computer |
| `get_session_recording` | Reports a session's recording, its steps, and where to watch it  |
| `start_exploration`     | Sends explorer agents through an app to fill its Atlas           |
| `get_exploration`       | Reports where an exploration stands                              |
| `cancel_exploration`    | Cancels an exploration and stops its devices                     |
| `atlas_search`          | Finds an app's Atlas screens by name or description              |
| `atlas_graph`           | Summarizes an app's Atlas screens and flows                      |
| `atlas_diff`            | Compares an app's Atlas between two builds                       |
| `share_atlas`           | Creates a read-only link to an app's Atlas, when asked           |
| `list_builds`           | Lists an app's builds, optionally one branch's                   |
| `get_build`             | Reports one build's status and whether a session can use it      |
| `start_build`           | Builds an app from its linked GitHub repository's branch         |
| `cancel_build`          | Cancels one queued or running build                              |
| `list_tests`            | Lists saved tests or workflows with each one's last result       |
| `get_test`              | Reads a test's steps or a workflow's tests, with recent runs     |
| `run_test`              | Runs a saved test or workflow on the chosen build and device     |
| `get_run`               | Reports a run's result, failing step, screenshot, and report     |
| `cancel_run`            | Cancels a test or workflow run                                   |
| `create_test`           | Saves a new test from steps, test YAML, or a device session      |
| `update_test`           | Replaces a test's steps as a new version                         |
| `validate_test`         | Checks test YAML and saves nothing                               |
| `connect_gitlab`        | Reports whether GitLab is connected and where to connect it      |

For other editors and the Revyl CLI, see
[Revyl's IDE and MCP setup guide](https://docs.revyl.com/agents/mcp).

## Maintainers

Changes merged to staging receive an automatic patch-version bump before
publication. For a minor or major release, increase
`.grok-plugin/plugin.json` in the same change; the workflow preserves that
version without adding another patch. The PR label `skip version` (also
`skip-version` or `no-plugin-release`), or `[skip version]` in its title,
skips the automatic bump but does not prevent publication. README and
test-only changes do not trigger a bump. xAI's catalog pins a commit, so a new
version reaches Grok only after that pin is updated.

## License

MIT
