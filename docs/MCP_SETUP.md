# MCP Server Setup

Connect Revyl to your AI coding tools (Cursor, Claude Code, Codex, VS Code, Claude Desktop) so your agent can provision cloud devices, run tests, and interact with mobile apps directly.

> **Public docs**: [docs.revyl.ai/cli/mcp-setup](https://docs.revyl.ai/cli/mcp-setup)

## Quick Install

**[Add to Cursor](cursor://anysphere.cursor-deeplink/mcp/install?name=revyl&config=eyJjb21tYW5kIjoicmV2eWwiLCJhcmdzIjpbIm1jcCIsInNlcnZlIl19)**

[![Add Revyl MCP to Cursor](https://cursor.com/deeplink/mcp-install-dark.png)](cursor://anysphere.cursor-deeplink/mcp/install?name=revyl&config=eyJjb21tYW5kIjoicmV2eWwiLCJhcmdzIjpbIm1jcCIsInNlcnZlIl19)

If the button does not open Cursor, go to **Settings > MCP > Add server** and add server `revyl` with args `mcp`, `serve`.

[![Install in VS Code](https://img.shields.io/badge/VS_Code-Revyl-0098FF?style=flat&logo=visualstudiocode&logoColor=ffffff)](vscode:mcp/install?%7B%22name%22%3A%22revyl%22%2C%22type%22%3A%22stdio%22%2C%22command%22%3A%22revyl%22%2C%22args%22%3A%5B%22mcp%22%2C%22serve%22%5D%7D)  [![Install in VS Code Insiders](https://img.shields.io/badge/VS_Code_Insiders-Revyl-24bfa5?style=flat&logo=visualstudiocode&logoColor=ffffff)](vscode-insiders:mcp/install?%7B%22name%22%3A%22revyl%22%2C%22type%22%3A%22stdio%22%2C%22command%22%3A%22revyl%22%2C%22args%22%3A%5B%22mcp%22%2C%22serve%22%5D%7D)

**Claude Code**: `claude mcp add revyl -- revyl mcp serve`

**Codex**: `codex mcp add revyl -- revyl mcp serve`

> **Note**: The one-click buttons install the server without an API key. Run `revyl auth login` first, or add `REVYL_API_KEY` to your MCP config afterward. See the manual setup sections below.

## 2-Minute Golden Setup (Codex)

Use this flow if you want the fastest path to a strong local experience:

```bash
# 1) Authenticate CLI
revyl auth login

# 2) Add Revyl MCP server to Codex
codex mcp add revyl -- revyl mcp serve

# 3) Install Revyl MCP skill family for Codex behavior guidance
revyl skill install --codex --mcp
```

### One-command quick setup

```bash
revyl auth login && codex mcp add revyl -- revyl mcp serve && revyl skill install --codex --mcp
```

### Verify in under 30 seconds

```bash
revyl auth status
codex mcp list
```

Then ask your agent:
- "List all my Revyl tests."
- "Start an Android device and take a screenshot."

## Mental Model: CLI <> MCP <> Skill

- `CLI` (`revyl`): the executable that actually performs operations.
- `MCP` (`revyl mcp serve`): exposes CLI operations as callable tools for AI hosts.
- `Skill` (`SKILL.md`): playbook that improves how the agent uses available tools.

Rule of thumb:
- MCP gives capability.
- Skill gives strategy.
- Your prompt gives intent.

In practice, setup order should be:
1. Install/auth the CLI.
2. Register MCP in your host.
3. Install the skill.
4. Run task prompts.

If something fails, triage in this order:
1. Auth and CLI health (`revyl auth status`).
2. MCP registration/connectivity (`codex mcp list`, `revyl mcp serve`).
3. Skill presence/scope (`.codex/skills/...` vs `~/.codex/skills/...`).

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

### Local CLI development

Build and install a local `revyl` binary, then register it with Codex:

```bash
go build -o /usr/local/bin/revyl ./cmd/revyl
codex mcp add revyl -- revyl mcp serve
```

If you prefer a repo-local binary path:

```bash
go build -o ./bin/revyl ./cmd/revyl
mkdir -p ~/.local/bin
ln -sfn "$(pwd)/bin/revyl" ~/.local/bin/revyl
export PATH="$HOME/.local/bin:$PATH"
```

If you prefer to use a local alias name such as `revyl-zakir`, use that alias in the `codex mcp` commands and config instead of `revyl`.

### Reinstall during local development

After rebuilding the local binary, refresh the MCP entry:

```bash
codex mcp remove revyl
codex mcp add revyl -- revyl mcp serve
```

If your CLI supports server listing, confirm it was updated and restart Codex if needed before continuing.

### CLI

```bash
codex mcp add revyl -- revyl mcp serve
```

If `revyl` is a shell alias, it may not be loaded by Codex process execution.
Prefer ensuring the command is on `PATH` (or use the absolute path fallback below).

### Config file

Add to `~/.codex/config.toml`:

```toml
[mcp_servers.revyl]
command = "revyl"
args = ["mcp", "serve"]
env = { REVYL_API_KEY = "your-api-key" }
```

If your normal CLI workflow uses local/dev servers, include `--dev` for MCP too:

```toml
[mcp_servers.revyl]
command = "revyl"
args = ["--dev", "mcp", "serve"]
env = { REVYL_API_KEY = "your-api-key" }
```

### Runtime parity (important)

Keep MCP and your shell CLI pointed at the same binary and flags. A mismatch
(for example MCP using `revyl mcp serve` while your shell uses an alias like
`revyl-zakir --dev`) can make session lists appear inconsistent.

Quick checks:

```bash
codex mcp list
zsh -lic 'type revyl-zakir'
```

Verify that:
- MCP `Command` points to the same binary your shell uses.
- MCP args include `--dev` if your shell alias runs in dev mode.
- Both environments use the same `REVYL_BACKEND_URL` overrides (if set).

If `revyl` is not on `PATH` (or you use a local alias name), use an absolute path:

```toml
[mcp_servers.revyl]
command = "/absolute/path/to/revyl"
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

## Installing Agent Skills (Recommended)

Revyl ships embedded skills so your assistant can run reliable device interaction loops, convert exploratory sessions into stable tests, and triage failures. This is optional but significantly improves the experience.

Skills are **embedded in the CLI binary** and can be installed with a single command.

### Default install (CLI family)

```bash
revyl skill install
```

This auto-detects tools and installs the CLI skill family:
- `revyl-cli`
- `revyl-cli-create`
- `revyl-cli-analyze`
- `revyl-cli-dev-loop`

Install MCP skill family instead:

```bash
revyl skill install --mcp
```

Install both CLI and MCP families:

```bash
revyl skill install --cli --mcp
```

MCP family skills:
- `revyl-mcp`
- `revyl-mcp-create`
- `revyl-mcp-analyze`
- `revyl-mcp-dev-loop`

### Tool-specific installation

```bash
revyl skill install --cursor                # CLI family for Cursor
revyl skill install --codex --mcp           # MCP family for Codex
revyl skill install --claude --cli --mcp    # Both families for Claude Code
```

Use project-level install when you want repo-specific behavior. Use global install when you want the same defaults across all repos.

### Global installation (user-level, applies to all projects)

```bash
revyl skill install --cursor --global
revyl skill install --claude --global --mcp
revyl skill install --codex --global --cli --mcp
```

If you update the CLI and want to refresh installed skills, run the same install command again for your target tool.

### Manual installation

If you prefer to install skills manually:

```bash
# List available skills
revyl skill list

# Export a specific skill file
revyl skill export --name revyl-cli-dev-loop -o SKILL.md
revyl skill export --name revyl-mcp-dev-loop -o SKILL.md
revyl skill export --name revyl-cli-analyze -o SKILL.md

# Or pipe a specific skill directly
revyl skill show --name revyl-cli > SKILL.md
revyl skill show --name revyl-mcp > SKILL.md
```

If you previously installed legacy folders (`revyl-device`, `revyl-dev-loop`, `revyl-adhoc-to-test`, `revyl-device-dev-loop`, `revyl-create`, `revyl-analyze`), run install again to auto-prune them:

```bash
revyl skill install --codex --force
```

Then place it in the appropriate directory for your tool:

| Tool | Project-level | User-level (global) |
| --- | --- | --- |
| Cursor | `.cursor/skills/<skill-name>/SKILL.md` | `~/.cursor/skills/<skill-name>/SKILL.md` |
| Claude Code | `.claude/skills/<skill-name>/SKILL.md` | `~/.claude/skills/<skill-name>/SKILL.md` |
| Codex | `.codex/skills/<skill-name>/SKILL.md` | `~/.codex/skills/<skill-name>/SKILL.md` |

After installing, the skill is automatically discovered by your AI agent on startup. Restart your IDE if it was already running.

---

## Maximize UX in Daily Use

- Install both MCP and skills: MCP exposes tools, skills improve execution quality.
- Ask intent-first prompts ("Run login smoke test and summarize failure cause") rather than low-level click scripts.
- For device interaction, use the loop: `screenshot()` -> action -> `screenshot()`.
- Prefer grounded targets before raw coordinates.
- Grounded targets are resolved worker-side in device coordinate space first; older workers fall back to backend grounding automatically.
- Share `viewer_url` early when collaborating with teammates.
- Always stop active device sessions when done to avoid idle billing.

---

## Verify It Works

After configuring your tool, try these prompts:

- "Start an Android device and take a screenshot"
- "List all my Revyl tests"
- "Run the login-flow test"
- "Install this app and tap the Sign In button"

If something goes wrong, ask the agent to "Run device_doctor" -- it checks auth, session, worker, and grounding health.

---

## Example Prompt Library

Use these copy/paste prompts to activate the right skill family.

### CLI dev-loop prompt (`revyl-cli-dev-loop`)

```text
Use the revyl-cli-dev-loop skill.
Goal: verify I can sign in and reach Home using CLI flow.
Use only Revyl CLI commands (no MCP tool calls).

Steps:
1) start from project root
2) run revyl init --hotreload if needed
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

### Worker DNS failures in sandboxed agents

If direct worker DNS lookups fail (for example `cog-*.revyl.ai` not resolving in Codex/Claude sandbox environments), the CLI/MCP device tools automatically fall back to backend worker proxy routing.

If actions still fail after fallback:
1. Run `device_doctor()` to verify session + worker status.
2. Confirm the session still appears in `list_device_sessions()`.
3. Start a fresh session if the current one was terminated externally.

### Grounding model not finding elements

1. Take a `screenshot()` to see what's actually on screen
2. Use more specific descriptions: "blue 'Sign In' button" instead of "button"
3. Use `find_element()` first to check coordinates before acting
