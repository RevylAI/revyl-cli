<p align="center">
  <img src="docs/assets/hero.gif" alt="Revyl" width="600" />
</p>

<h1 align="center">Revyl</h1>

<p align="center">
  <em>Proactive Reliability for Mobile Apps</em>
</p>

<p align="center">
  <a href="https://github.com/RevylAI/revyl-cli/releases"><img src="https://img.shields.io/badge/version-0.1.5-9D61FF" alt="Version" /></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-blue.svg" alt="License: MIT" /></a>
  <a href="https://github.com/RevylAI/homebrew-tap"><img src="https://img.shields.io/badge/brew-RevylAI/tap/revyl-orange" alt="Homebrew" /></a>
  <a href="https://www.npmjs.com/package/@revyl/cli"><img src="https://img.shields.io/npm/v/@revyl/cli" alt="npm" /></a>
  <a href="https://pypi.org/project/revyl/"><img src="https://img.shields.io/pypi/v/revyl" alt="PyPI" /></a>
</p>

---

Proactive Reliability

## Install

```bash
brew install RevylAI/tap/revyl          # Homebrew (recommended)
npm install -g @revyl/cli               # npm
pip install revyl                       # pip
```

## Quick Start

```bash
cd your-app
revyl init                              # Guided wizard: auth, build system, apps
revyl dev                               # Launch TUI: live device + hot reload
```

From the dev TUI you can interact with a cloud device in real time, then convert what works into tests:

```bash
revyl dev test create login-flow        # Create a test from the live session
revyl dev test run login-flow           # Run it against the hot-reload build
revyl dev test open login-flow          # Open in the browser editor
```

When you're ready to run outside the dev loop:

```bash
revyl test run login-flow --build       # Build, upload, and run in one step
revyl workflow create smoke-tests --tests login-flow,checkout
revyl workflow run smoke-tests          # Run the full workflow
```

> `revyl dev` starts your local dev server, tunnels it to a cloud device, and installs the latest build automatically. Use `--platform android` or `--platform ios` to pick a platform (defaults to iOS).

## MCP Server

Connect Revyl to your AI coding tools -- your agent gets cloud devices, test execution, and device interaction out of the box.

| Tool | Setup |
|------|-------|
| **Cursor** | [![Add to Cursor](https://cursor.com/deeplink/mcp-install-dark.png)](cursor://anysphere.cursor-deeplink/mcp/install?name=revyl&config=eyJjb21tYW5kIjoicmV2eWwiLCJhcmdzIjpbIm1jcCIsInNlcnZlIl19) |
| **VS Code** | [![Install in VS Code](https://img.shields.io/badge/VS_Code-Revyl-0098FF?style=flat&logo=visualstudiocode&logoColor=ffffff)](vscode:mcp/install?%7B%22name%22%3A%22revyl%22%2C%22type%22%3A%22stdio%22%2C%22command%22%3A%22revyl%22%2C%22args%22%3A%5B%22mcp%22%2C%22serve%22%5D%7D) |
| **Claude Code** | `claude mcp add revyl -- revyl mcp serve` |
| **Codex** | `codex mcp add revyl -- revyl mcp serve` |

> [Full setup guide](docs/MCP_SETUP.md) -- includes Windsurf, Claude Desktop, and agent skills

Install agent skills (improves AI tool integration):

```bash
revyl skill install              # Auto-detect tool; install CLI skill family (default)
revyl skill install --mcp        # Install MCP skill family
revyl skill install --cli --mcp  # Install both skill families
revyl skill install --cursor     # Cursor only
revyl skill install --claude     # Claude Code only
revyl skill install --codex      # Codex only
revyl skill list                 # Show available skill names
revyl skill show --name revyl-cli
revyl skill show --name revyl-mcp-dev-loop
revyl skill export --name revyl-cli-create -o SKILL.md
revyl skill export --name revyl-mcp-analyze -o SKILL.md
revyl skill revyl-mcp-dev-loop install --codex
revyl skill install --name revyl-cli-create --name revyl-cli-analyze --codex
```

## What You Can Do

| Feature | Command | Docs |
|---------|---------|------|
| **Run tests** | `revyl test run <name>` | [Commands](docs/COMMANDS.md#running-tests) |
| **Run workflows** | `revyl workflow run <name>` | [Commands](docs/COMMANDS.md#workflow-management) |
| **Cloud devices** | `revyl device start --platform ios` | [Commands](docs/COMMANDS.md#device-management) |
| **Dev loop (Expo)** | `revyl dev` | [Commands](docs/COMMANDS.md#dev-loop-expo) |
| **Build & upload** | `revyl build upload` | [Commands](docs/COMMANDS.md#build-management) |
| **Publish to TestFlight** | `revyl publish testflight` | [Commands](docs/COMMANDS.md#ios-publishing-testflight) |
| **CI/CD** | GitHub Actions | [CI/CD](docs/CI_CD.md) |
| **Python / TypeScript SDK** | `pip install revyl` | [SDK](docs/SDK.md) |
| **Agent skills** | `revyl skill install` | [Skills](docs/SKILLS.md) |

## Documentation

- **[Command Reference](docs/COMMANDS.md)** -- full list of every command and flag
- **[Configuration](docs/CONFIGURATION.md)** -- `.revyl/config.yaml` reference
- **[MCP Setup](docs/MCP_SETUP.md)** -- AI agent integration for all tools
- **[Agent Skills](docs/SKILLS.md)** -- embedded skills for device loops, test creation, failure analysis
- **[SDK](docs/SDK.md)** -- Python and TypeScript programmatic usage
- **[CI/CD](docs/CI_CD.md)** -- GitHub Actions integration
- **[Development](docs/DEVELOPMENT.md)** -- internal dev workflow, hot reload, `--dev` mode
- **[Releasing](docs/RELEASING.md)** -- version bumping, release pipeline
- **[Public Docs](https://docs.revyl.ai)** -- full documentation site

## License

MIT
