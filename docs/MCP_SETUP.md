<!-- mintlify
title: "MCP Setup"
description: "Connect Revyl to AI coding agents via MCP"
target: cli/mcp-setup.mdx
-->

# MCP Server Setup

> [Back to README](../README.md) | [Commands](COMMANDS.md) | [Agent Skills](SKILLS.md) | [SDK](SDK.md)

Connect Revyl to your AI coding tools so your agent can provision cloud devices, run tests, and interact with mobile apps directly.

MCP gives capability. Skills give strategy. Your prompt gives intent.

## Quick Install

**Cursor** -- add to `.cursor/mcp.json` in your project root (or `~/.cursor/mcp.json` for global):

```json
{
  "mcpServers": {
    "revyl": {
      "command": "revyl",
      "args": ["mcp", "serve"]
    }
  }
}
```

Restart Cursor after saving. If you previously ran `revyl auth login`, no API key is needed.

[![Install in VS Code](https://img.shields.io/badge/VS_Code-Revyl-0098FF?style=flat&logo=visualstudiocode&logoColor=ffffff)](vscode:mcp/install?%7B%22name%22%3A%22revyl%22%2C%22type%22%3A%22stdio%22%2C%22command%22%3A%22revyl%22%2C%22args%22%3A%5B%22mcp%22%2C%22serve%22%5D%7D)  [![Install in VS Code Insiders](https://img.shields.io/badge/VS_Code_Insiders-Revyl-24bfa5?style=flat&logo=visualstudiocode&logoColor=ffffff)](vscode-insiders:mcp/install?%7B%22name%22%3A%22revyl%22%2C%22type%22%3A%22stdio%22%2C%22command%22%3A%22revyl%22%2C%22args%22%3A%5B%22mcp%22%2C%22serve%22%5D%7D)

**Claude Code**: `claude mcp add revyl -- revyl mcp serve`

**Codex**: `codex mcp add revyl -- revyl mcp serve`

> The one-click buttons install the server without an API key. Run `revyl auth login` first, or add `REVYL_API_KEY` to your MCP config afterward.

## Prerequisites

### 1. Install the CLI

```bash
brew install RevylAI/tap/revyl    # Homebrew (macOS)
pipx install revyl                # pipx (cross-platform)
uv tool install revyl             # uv
pip install revyl                 # pip
```

### 2. Authenticate

```bash
revyl auth login                  # Browser-based login
```

Or set an API key:

```bash
export REVYL_API_KEY=your-api-key
```

### 3. Verify

```bash
revyl auth status   # Should show "Authenticated"
revyl mcp serve     # Should start the MCP server (Ctrl+C to stop)
```

---

## Setup by Tool

### Cursor

Create `.cursor/mcp.json` in your project root (project-scoped) or `~/.cursor/mcp.json` (global):

```json
{
  "mcpServers": {
    "revyl": {
      "command": "revyl",
      "args": ["mcp", "serve"],
      "env": {
        "REVYL_API_KEY": "your-api-key"
      }
    }
  }
}
```

Restart Cursor after editing. If you previously ran `revyl auth login`, you can omit the `env` block.

### Claude Code

```bash
claude mcp add revyl -- revyl mcp serve
claude mcp add revyl -e REVYL_API_KEY=your-api-key -- revyl mcp serve  # Explicit key
claude mcp list  # Verify
```

### Codex (OpenAI)

CLI:

```bash
codex mcp add revyl -- revyl mcp serve
```

Config file (`~/.codex/config.toml`):

```toml
[mcp_servers.revyl]
command = "revyl"
args = ["mcp", "serve"]
env = { REVYL_API_KEY = "your-api-key" }
```

If your CLI workflow uses `--dev`, include it for MCP too:

```toml
[mcp_servers.revyl]
command = "revyl"
args = ["--dev", "mcp", "serve"]
env = { REVYL_API_KEY = "your-api-key" }
```

Keep MCP and your shell CLI pointed at the same binary and flags. A mismatch can make session lists appear inconsistent.

### Claude Desktop

Edit the config file:

- **macOS**: `~/Library/Application Support/Claude/claude_desktop_config.json`
- **Windows**: `%APPDATA%\Claude\claude_desktop_config.json`

```json
{
  "mcpServers": {
    "revyl": {
      "command": "revyl",
      "args": ["mcp", "serve"],
      "env": {
        "REVYL_API_KEY": "your-api-key"
      }
    }
  }
}
```

### VS Code (Copilot Chat)

Add to your VS Code `settings.json`:

```json
{
  "mcp": {
    "servers": {
      "revyl": {
        "command": "revyl",
        "args": ["mcp", "serve"],
        "env": {
          "REVYL_API_KEY": "your-api-key"
        }
      }
    }
  }
}
```

### Windsurf

Create or edit `~/.codeium/windsurf/mcp_config.json`:

```json
{
  "mcpServers": {
    "revyl": {
      "command": "revyl",
      "args": ["mcp", "serve"],
      "env": {
        "REVYL_API_KEY": "your-api-key"
      }
    }
  }
}
```

---

## MCP Tools Reference

The Revyl MCP server exposes tools across the following categories. Device action tools use **grounded targeting by default** -- describe the element in natural language (`target="Sign In button"`) and coordinates are resolved automatically. You can also pass raw `x, y` as an override.

### Device Session

| Tool | Description |
|------|-------------|
| `start_device_session` | Start a cloud device session (iOS or Android) |
| `stop_device_session` | Stop a device session |
| `get_session_info` | Get session details including viewer URL |
| `list_device_sessions` | List all active sessions |
| `switch_device_session` | Switch the active session by index |

<Callout type="info" title="Multi-session targeting">
  All device action, control, live step, vision, and app management tools accept an optional **`session_index`** parameter. When omitted, the active session is used. Pass `session_index` explicitly to target a specific device when running multiple sessions. See [Multi-Session Guide](/device/multi-session) for workflows and examples.
</Callout>

### Device Actions

| Tool | Description |
|------|-------------|
| `device_tap` | Tap an element by target or coordinates |
| `device_double_tap` | Double-tap an element |
| `device_long_press` | Long press with configurable duration |
| `device_type` | Type text into a field |
| `device_swipe` | Swipe in a direction from a target or point |
| `device_drag` | Drag from one point to another |
| `device_pinch` | Pinch/zoom gesture |
| `device_clear_text` | Clear text in a field |

### Device Controls

| Tool | Description |
|------|-------------|
| `device_wait` | Wait for a fixed duration |
| `device_back` | Android back button |
| `device_key` | Press a key (ENTER, BACKSPACE) |
| `device_shake` | Trigger shake gesture |
| `device_go_home` | Return to home screen |
| `device_kill_app` | Kill the current app |
| `device_open_app` | Open a system app by name |
| `device_navigate` | Open a URL or deep link |
| `device_set_location` | Set GPS coordinates |
| `device_download_file` | Download a file to device storage |

### Live Steps

| Tool | Description |
|------|-------------|
| `device_instruction` | Execute one instruction step (natural-language action) |
| `device_validation` | Execute one validation step (assertion) |
| `device_extract` | Execute one extract step (data extraction) |
| `device_code_execution` | Execute one code execution step by script ID |

### Vision

| Tool | Description |
|------|-------------|
| `screenshot` | Capture a screenshot of the current screen |

### App Management

| Tool | Description |
|------|-------------|
| `install_app` | Install an app from a URL |
| `launch_app` | Launch an installed app by bundle ID |

### Diagnostics

| Tool | Description |
|------|-------------|
| `device_doctor` | Run diagnostics on auth, session, device, and grounding health |
| `auth_status` | Check authentication status and user info |

### Test Management

| Tool | Description |
|------|-------------|
| `run_test` | Run a test by name or ID |
| `create_test` | Create a new test from YAML content |
| `update_test` | Update an existing test's YAML content |
| `delete_test` | Delete a test by name or ID |
| `list_tests` | List tests from `.revyl/config.yaml` |
| `list_remote_tests` | List all tests in the organization |
| `get_test_status` | Get the status of a running or completed test |
| `cancel_test` | Cancel a running test by task ID |
| `validate_yaml` | Validate YAML test syntax without creating |
| `get_schema` | Get the CLI command and YAML test schema |

### Workflow Management

| Tool | Description |
|------|-------------|
| `run_workflow` | Run a workflow by name or ID |
| `create_workflow` | Create a new workflow |
| `delete_workflow` | Delete a workflow by name or ID |
| `list_workflows` | List all workflows in the organization |
| `cancel_workflow` | Cancel a running workflow by task ID |
| `open_workflow_editor` | Get the URL to the workflow browser editor |
| `add_tests_to_workflow` | Add tests to a workflow |
| `remove_tests_from_workflow` | Remove tests from a workflow |
| `get_workflow_settings` | Get workflow location and app overrides |
| `set_workflow_location` | Set GPS location override for a workflow |
| `clear_workflow_location` | Remove GPS location override |
| `set_workflow_app` | Set app overrides per platform |
| `clear_workflow_app` | Remove app overrides |

### Module Management

| Tool | Description |
|------|-------------|
| `list_modules` | List reusable test modules |
| `get_module` | Get module details and blocks |
| `create_module` | Create a module from blocks |
| `delete_module` | Delete a module (fails if in use) |
| `insert_module_block` | Get a `module_import` YAML snippet for a module |

### Tag Management

| Tool | Description |
|------|-------------|
| `list_tags` | List all tags with test counts |
| `create_tag` | Create a tag (upsert) |
| `delete_tag` | Delete a tag from all tests |
| `get_test_tags` | Get tags for a specific test |
| `set_test_tags` | Replace all tags on a test |
| `add_remove_test_tags` | Add/remove tags without replacing |

### Variables and Environment

| Tool | Description |
|------|-------------|
| `list_variables` | List test variables (`{{name}}` syntax in steps) |
| `set_variable` | Add or update a test variable |
| `delete_variable` | Delete a test variable |
| `delete_all_variables` | Delete all test variables |
| `list_env_vars` | List environment variables (encrypted, injected at launch) |
| `set_env_var` | Add or update an env var |
| `delete_env_var` | Delete an env var |
| `clear_env_vars` | Delete all env vars |

### Build Management

| Tool | Description |
|------|-------------|
| `list_builds` | List available build versions |
| `upload_build` | Upload a build file to an app |
| `create_app` | Create a new app for build uploads |
| `delete_app` | Delete an app and all its build versions |

### Script Management

| Tool | Description |
|------|-------------|
| `list_scripts` | List code execution scripts |
| `get_script` | Get script details and source code |
| `create_script` | Create a new script |

### Dev Loop

| Tool | Description |
|------|-------------|
| `start_dev_loop` | Start an Expo hot-reload dev loop |
| `stop_dev_loop` | Stop the active dev loop |

---

## Multi-Session Example

Run the same login flow on iOS and Android simultaneously:

```
# Start both devices
start_device_session(platform="android")   → session_index: 0
start_device_session(platform="ios")       → session_index: 1

# Install app on each
install_app(app_url="https://example.com/app.apk", session_index=0)
install_app(app_url="https://example.com/app.ipa", session_index=1)

# Run the same flow on both
device_tap(target="Sign In", session_index=0)
device_tap(target="Sign In", session_index=1)

device_type(target="Email field", text="user@test.com", session_index=0)
device_type(target="Email field", text="user@test.com", session_index=1)

# Verify both
screenshot(session_index=0)
screenshot(session_index=1)

# Clean up
stop_device_session(all=true)
```

Indices are **stable** — stopping one session does not renumber the others. See [Multi-Session Guide](/device/multi-session) for details.

---

## Prompt Library

Use these copy/paste prompts to activate the right skill family.

### CLI dev-loop prompt (`revyl-cli-dev-loop`)

```text
Use the revyl-cli-dev-loop skill.
Goal: verify I can sign in and reach Home using CLI flow.
Use only Revyl CLI commands (no MCP tool calls).

Steps:
1) start from project root
2) run revyl init if needed
3) run revyl dev and wait for readiness
4) summarize exact actions I should perform in app
5) convert successful flow into a test:
   - revyl dev test create login-smoke --platform ios
   - revyl dev test open login-smoke
   - revyl test push login-smoke --force
   - revyl test run login-smoke
6) if run fails, fetch report with revyl test report login-smoke --json and classify failure
```

### MCP dev-loop prompt (`revyl-mcp-dev-loop`)

```text
Use the revyl-mcp-dev-loop skill.
Use Revyl MCP tools only.
Goal: bypass login and land on Home screen.

Rules:
1) first call must be start_dev_loop
2) loop: screenshot -> one-line observation -> one best action -> screenshot verify
3) max 2 actions before re-anchor
4) if state is unexpected, stop and re-anchor
5) end with summary: final screen, actions, anomalies
```

### MCP create prompt (`revyl-mcp-create`)

```text
Use the revyl-mcp-create skill.
Create a new ios test named checkout-smoke from this flow:
- open Shop
- open product Orchid Mantis
- add to cart
- open cart
- verify Orchid Mantis and price $62.00

Use MCP tools to:
1) validate YAML
2) create/update test
3) run test
4) report pass/fail with task id
```

### CLI analyze prompt (`revyl-cli-analyze`)

```text
Use the revyl-cli-analyze skill.
Analyze this failed test run end-to-end:
1) run revyl test report checkout-smoke --json
2) classify failure as REAL BUG, FLAKY TEST, INFRA ISSUE, or TEST IMPROVEMENT
3) provide exact next action and rerun command
```

---

## Verify It Works

After configuring your tool, try these prompts:

- "Start an Android device and take a screenshot"
- "List all my Revyl tests"
- "Run the login-flow test"
- "Install this app and tap the Sign In button"

If something goes wrong, ask the agent to "Run device_doctor" -- it checks auth, session, device, and grounding health.

---

## Troubleshooting

### "revyl: command not found"

The CLI is not in your PATH.

```bash
which revyl        # Check location
pip show revyl     # If installed via pip

# Or use the full path in your MCP config:
# "command": "/usr/local/bin/revyl"
```

### Authentication errors

```bash
revyl auth login     # Re-authenticate
revyl auth status    # Check current status
```

### MCP server not responding

1. Restart your IDE/tool
2. Check the server starts manually: `revyl mcp serve`
3. Enable debug logging by adding `"--debug"` to the `args` array in your MCP config
4. Run `revyl device doctor` to check connectivity

### "no active device session"

Sessions auto-terminate after 5 minutes of idle time. Call `start_device_session` to provision a new device.

### DNS failures in sandboxed agents

If direct device service DNS lookups fail (e.g. in Codex/Claude sandbox environments), the CLI/MCP automatically falls back to backend proxy routing.

If actions still fail after fallback:

1. Run `device_doctor` to verify session and device status
2. Confirm the session still appears in `list_device_sessions`
3. Start a fresh session if the current one was terminated externally

### Grounding model not finding elements

1. Take a `screenshot()` to see what's actually on screen
2. Use more specific descriptions: `"blue 'Sign In' button"` instead of `"button"`
3. Rephrase the target using exact visible text and retry
