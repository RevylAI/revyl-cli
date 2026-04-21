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

Create a free account at [app.revyl.ai](https://app.revyl.ai), then log in via the CLI:

```bash
revyl auth login                        # Browser-based login (stores credentials locally)
```

Or set an API key directly (generate one from your dashboard):

```bash
export REVYL_API_KEY=your-api-key
```

## Quick Start

```bash
cd your-app
revyl doctor                            # Check CLI, auth, connectivity
revyl auth login                        # Browser-based login (if not already authed)
revyl init                              # Guided wizard: build system, apps
revyl build upload                      # Build and upload a dev binary
revyl dev                               # Launch TUI: live device + hot reload
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


## Agent Skills

Install agent skills to improve how your AI coding tool uses Revyl:

```bash
revyl skill install              # Auto-detect tool; install CLI skill family
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
| CI/CD | GitHub Actions | [CI/CD](docs/CI_CD.md) |
| Device SDK | `pip install revyl[sdk]` | [Device SDK](docs/SDK.md) |
| Agent skills | `revyl skill install` | [Skills](docs/SKILLS.md) |

## Documentation

- **[Command Reference](docs/COMMANDS.md)** -- full list of every command and flag
- **[Creating Tests](docs/TEST_CREATION.md)** -- YAML-first workflows, modules, and troubleshooting
- **[Configuration](docs/CONFIGURATION.md)** -- `.revyl/config.yaml` reference
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
