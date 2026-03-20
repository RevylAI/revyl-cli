<p align="center">
  <img src="docs/assets/hero.gif" alt="Revyl" width="600" />
</p>

<h1 align="center">Revyl</h1>

<p align="center">
  <em>Proactive Reliability for Mobile Apps</em>
</p>

<p align="center">
  <a href="https://github.com/RevylAI/revyl-cli/releases"><img src="https://img.shields.io/badge/version-0.1.13-9D61FF" alt="Version" /></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-blue.svg" alt="License: MIT" /></a>
  <a href="https://github.com/RevylAI/homebrew-tap"><img src="https://img.shields.io/badge/brew-RevylAI/tap/revyl-orange" alt="Homebrew" /></a>
  <a href="https://pypi.org/project/revyl/"><img src="https://img.shields.io/pypi/v/revyl" alt="PyPI" /></a>
</p>

---

Revyl is an AI-powered testing platform for mobile apps. Define tests in natural language, run them on cloud devices, and catch bugs before your users do. It works with iOS and Android, supports Expo / React Native / Flutter / native builds, and integrates with your CI pipeline and AI coding tools.

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/RevylAI/revyl-cli/main/scripts/install.sh | sh  # Shell (macOS / Linux)
brew install RevylAI/tap/revyl          # Homebrew (macOS)
pipx install revyl                      # pipx (cross-platform)
uv tool install revyl                   # uv
pip install revyl                       # pip
```

## Authenticate

```bash
revyl auth login                        # Browser-based login (stores credentials locally)
```

Or set an API key directly:

```bash
export REVYL_API_KEY=your-api-key
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

YAML-first creation can bootstrap local state without a pre-existing `.revyl/config.yaml`:

```bash
revyl test create login-flow --from-file ./login-flow.yaml
```

See [Creating Tests](docs/TEST_CREATION.md) for the full authoring workflow, YAML examples, module imports, and troubleshooting.

> `revyl dev` starts your local dev server, tunnels it to a cloud device, and installs the latest build automatically. Use `--platform android` or `--platform ios` to pick a platform (defaults to iOS).

## MCP Server

Connect Revyl to your AI coding tools -- your agent gets cloud devices, test execution, and device interaction out of the box.

| Tool | Setup |
|------|-------|
| **Cursor** | Add to `.cursor/mcp.json`: `{"mcpServers":{"revyl":{"command":"revyl","args":["mcp","serve"]}}}` ([full guide](docs/MCP_SETUP.md#cursor)) |
| **VS Code** | [![Install in VS Code](https://img.shields.io/badge/VS_Code-Revyl-0098FF?style=flat&logo=visualstudiocode&logoColor=ffffff)](vscode:mcp/install?%7B%22name%22%3A%22revyl%22%2C%22type%22%3A%22stdio%22%2C%22command%22%3A%22revyl%22%2C%22args%22%3A%5B%22mcp%22%2C%22serve%22%5D%7D) |
| **Claude Code** | `claude mcp add revyl -- revyl mcp serve` |
| **Codex** | `codex mcp add revyl -- revyl mcp serve` |

> [Full setup guide](docs/MCP_SETUP.md) -- includes Windsurf, Claude Desktop, and agent skills

## Device SDK

```bash
pip install revyl[sdk]                  # Python SDK (includes CLI)
```

```python
from revyl import DeviceClient

with DeviceClient.start(platform="ios") as device:
    device.tap(target="Login button")
    device.type_text(target="Email", text="user@test.com")
    device.instruction("Open Settings and tap Wi-Fi")
    device.validation("Verify Wi-Fi settings are visible")
    device.screenshot(out="screen.png")
```

`pip install revyl[sdk]` gives you both the CLI and the Python SDK. If you installed the CLI via Homebrew, the SDK detects it on PATH and skips the binary download.

See [Device SDK Reference](docs/SDK.md) for the full API.

## Agent Skills

Install agent skills to improve how your AI coding tool uses Revyl:

```bash
revyl skill install              # Auto-detect tool; install CLI skill family
revyl skill install --mcp        # Install MCP skill family
revyl skill install --cli --mcp  # Install both
```

See [Agent Skills](docs/SKILLS.md) for the full list and prompt examples.

## What You Can Do

| Feature | Command | Docs |
|---------|---------|------|
| Run tests | `revyl test run <name>` | [Commands](docs/COMMANDS.md#running-tests) |
| Run workflows | `revyl workflow run <name>` | [Commands](docs/COMMANDS.md#workflow-management) |
| Cloud devices | `revyl device start` | [Commands](docs/COMMANDS.md#device-management) |
| Dev loop (Expo) | `revyl dev` | [Commands](docs/COMMANDS.md#dev-loop-expo) |
| Build and upload | `revyl build upload` | [Commands](docs/COMMANDS.md#build-management) |
| Publish to TestFlight | `revyl publish testflight` | [Commands](docs/COMMANDS.md#ios-publishing-testflight) |
| CI/CD | GitHub Actions | [CI/CD](docs/CI_CD.md) |
| Device SDK | `pip install revyl[sdk]` | [Device SDK](docs/SDK.md) |
| Agent skills | `revyl skill install` | [Skills](docs/SKILLS.md) |

## Documentation

- **[Command Reference](docs/COMMANDS.md)** -- full list of every command and flag
- **[Creating Tests](docs/TEST_CREATION.md)** -- YAML-first workflows, modules, and troubleshooting
- **[Configuration](docs/CONFIGURATION.md)** -- `.revyl/config.yaml` reference
- **[MCP Setup](docs/MCP_SETUP.md)** -- AI agent integration for all tools
- **[Agent Skills](docs/SKILLS.md)** -- embedded skills for device loops, test creation, failure analysis
- **[Device SDK](docs/SDK.md)** -- Programmatic device control
- **[CI/CD](docs/CI_CD.md)** -- GitHub Actions integration
- **[Development](docs/DEVELOPMENT.md)** -- internal dev workflow, hot reload, `--dev` mode
- **[Releasing](docs/RELEASING.md)** -- version bumping, release pipeline
- **[Public Docs](https://docs.revyl.ai)** -- full documentation site

## Troubleshooting

<details>
<summary>Xcode / Command Line Tools errors during <code>brew upgrade revyl</code></summary>

```bash
softwareupdate --all --install --force
sudo xcode-select -s /Library/Developer/CommandLineTools
brew upgrade revyl
```

If `softwareupdate` does not install Command Line Tools, reinstall them:

```bash
sudo rm -rf /Library/Developer/CommandLineTools
sudo xcode-select --install
```

If you use full Xcode builds, install the latest Xcode version from the App Store and then run:

```bash
sudo xcode-select -s /Applications/Xcode.app/Contents/Developer
```

</details>

<details>
<summary>Homebrew directory ownership errors</summary>

```bash
sudo chown -R "$(whoami)" /opt/homebrew /Users/"$(whoami)"/Library/Caches/Homebrew /Users/"$(whoami)"/Library/Logs/Homebrew
chmod -R u+w /opt/homebrew /Users/"$(whoami)"/Library/Caches/Homebrew /Users/"$(whoami)"/Library/Logs/Homebrew
```

</details>

## License

MIT
