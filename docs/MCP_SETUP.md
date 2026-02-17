# MCP Server Setup

Connect Revyl to your AI coding tools (Cursor, Claude Code, Codex, VS Code, Claude Desktop) so your agent can provision cloud devices, run tests, and interact with mobile apps directly.

> **Public docs**: [docs.revyl.ai/cli/mcp-setup](https://docs.revyl.ai/cli/mcp-setup)

## Quick Install

[![Add Revyl MCP to Cursor](https://cursor.com/deeplink/mcp-install-dark.png)](cursor://anysphere.cursor-deeplink/mcp/install?name=revyl&config=eyJjb21tYW5kIjoicmV2eWwiLCJhcmdzIjpbIm1jcCIsInNlcnZlIl19)

[![Install in VS Code](https://img.shields.io/badge/VS_Code-Revyl-0098FF?style=flat&logo=visualstudiocode&logoColor=ffffff)](vscode:mcp/install?%7B%22name%22%3A%22revyl%22%2C%22type%22%3A%22stdio%22%2C%22command%22%3A%22revyl%22%2C%22args%22%3A%5B%22mcp%22%2C%22serve%22%5D%7D)  [![Install in VS Code Insiders](https://img.shields.io/badge/VS_Code_Insiders-Revyl-24bfa5?style=flat&logo=visualstudiocode&logoColor=ffffff)](vscode-insiders:mcp/install?%7B%22name%22%3A%22revyl%22%2C%22type%22%3A%22stdio%22%2C%22command%22%3A%22revyl%22%2C%22args%22%3A%5B%22mcp%22%2C%22serve%22%5D%7D)

**Claude Code**: `claude mcp add revyl -- revyl mcp serve`

**Codex**: `codex mcp add revyl -- revyl mcp serve`

> **Note**: The one-click buttons install the server without an API key. Run `revyl auth login` first, or add `REVYL_API_KEY` to your MCP config afterward. See the manual setup sections below.

## Prerequisites

### 1. Install the Revyl CLI

**npm (recommended)**:

```bash
npm install -g @revyl/cli
```

**pip**:

```bash
pip install revyl
```

**Binary download**: [GitHub Releases](https://github.com/revyl/cli/releases)

### 2. Authenticate

Either log in interactively (stores credentials locally):

```bash
revyl auth login
```

Or get your API key from the [Revyl dashboard](https://app.revyl.ai) and pass it via environment variable:

```bash
export REVYL_API_KEY=your-api-key
```

### 3. Verify

```bash
revyl auth status   # Should show "Authenticated"
revyl mcp serve     # Should start the MCP server (Ctrl+C to stop)
```

---

## Cursor

### Project-scoped (recommended)

Create `.cursor/mcp.json` in your project root:

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

### Global

Create or edit `~/.cursor/mcp.json`:

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

> **Note**: Restart Cursor after editing MCP config. If you previously ran `revyl auth login`, you can omit the `env` block.

---

## Claude Code

One command:

```bash
claude mcp add revyl -- revyl mcp serve
```

With an explicit API key:

```bash
claude mcp add revyl -e REVYL_API_KEY=your-api-key -- revyl mcp serve
```

Verify it was added:

```bash
claude mcp list
```

---

## Codex (OpenAI)

### CLI

```bash
codex mcp add revyl -- revyl mcp serve
```

### Config file

Add to `~/.codex/config.toml`:

```toml
[mcp_servers.revyl]
command = "revyl"
args = ["mcp", "serve"]
env = { REVYL_API_KEY = "your-api-key" }
```

---

## Claude Desktop

Edit the Claude Desktop config file:

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

Restart Claude Desktop after saving.

---

## VS Code (Copilot Chat)

Add to your VS Code `settings.json` (Cmd+Shift+P > "Preferences: Open User Settings (JSON)"):

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

---

## Windsurf

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

## Installing the Agent Skill (optional, better UX)

The Revyl agent skill teaches your AI assistant optimal tool usage patterns, workflows, and troubleshooting. It's optional but significantly improves the experience.

The skill file is at: [`skills/revyl-device/SKILL.md`](../skills/revyl-device/SKILL.md)

### Cursor

Copy the skill content into a Cursor rule:

1. Open `.cursor/rules/` in your project
2. Create a file like `revyl-device.mdc`
3. Paste the contents of `SKILL.md`

### Claude Code / Claude Desktop

Add as project knowledge:

1. Open your Claude project settings
2. Add the `SKILL.md` content as a knowledge document

### Codex

```bash
# If using the codex skills system
cp skills/revyl-device/SKILL.md ~/.codex/skills/revyl-device/SKILL.md
```

---

## Verify It Works

After configuring your tool, try these prompts:

- "Start an Android device and take a screenshot"
- "List all my Revyl tests"
- "Run the login-flow test"
- "Install this app and tap the Sign In button"

If something goes wrong, ask the agent to "Run device_doctor" -- it checks auth, session, worker, and grounding health.

---

## Troubleshooting

### "revyl: command not found"

The CLI is not in your PATH. Fix:

```bash
# Check where it's installed
which revyl        # npm
pip show revyl     # pip

# Or use the full path in your MCP config
"command": "/usr/local/bin/revyl"
```

### Authentication errors

```bash
# Re-authenticate
revyl auth login

# Or check your API key
revyl auth status
```

### MCP server not responding

1. Restart your IDE/tool
2. Check the server starts manually: `revyl mcp serve`
3. Enable debug logging: set `REVYL_DEBUG=true` in your MCP config's `env` block
4. Run `revyl device doctor` from the CLI to check connectivity

### "no active device session"

Sessions auto-terminate after 5 minutes of idle time. Call `start_device_session()` to provision a new device.

### Grounding model not finding elements

1. Take a `screenshot()` to see what's actually on screen
2. Use more specific descriptions: "blue 'Sign In' button" instead of "button"
3. Use `find_element()` first to check coordinates before acting
