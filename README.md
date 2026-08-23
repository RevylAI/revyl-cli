<p align="center">
  <img src="docs/assets/hero.gif" alt="Revyl" width="600" />
</p>

<h1 align="center">Revyl</h1>

<p align="center">
  <em>Proactive Reliability for Mobile Apps</em>
</p>

<p align="center">
  <a href="https://github.com/RevylAI/revyl-cli/releases"><img src="https://img.shields.io/badge/version-0.1.93-9D61FF" alt="Version" /></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-blue.svg" alt="License: MIT" /></a>
  <a href="https://github.com/RevylAI/homebrew-tap"><img src="https://img.shields.io/badge/brew-RevylAI/tap/revyl-orange" alt="Homebrew" /></a>
  <a href="https://pypi.org/project/revyl/"><img src="https://img.shields.io/pypi/v/revyl" alt="PyPI" /></a>
</p>

---

Revyl is an AI-powered testing platform for mobile apps. Define tests in natural language, run them on cloud devices, and catch bugs before your users do. It works with iOS and Android, supports Expo / React Native / Flutter / native builds, and integrates with your CI pipeline and AI coding tools.

## Install

### sh

```bash
curl -fsSL https://revyl.com/install.sh | sh
```

### Windows PowerShell

```powershell
irm https://raw.githubusercontent.com/RevylAI/revyl-cli/main/scripts/install.ps1 | iex
```

### Homebrew (macOS)

```bash
brew install RevylAI/tap/revyl
```

### pipx (cross-platform)

```bash
pipx install revyl
```

### uv

```bash
uv tool install revyl
```

The PyPI package is CLI-only and contains the native binary for your platform,
so `uv`, `pipx`, and `pip` do not download executable code at first run.

## Authenticate

Create a free account at [app.revyl.com](https://app.revyl.com), then log in via the CLI:

```bash
revyl auth login                        # Approve in a browser, then credentials are stored locally
```

The CLI prints an approval URL and a short code and waits. Open the URL wherever
you are signed in — it does not have to be this machine, so the same command
works over SSH, in a container, and in a cloud agent.

For an unattended machine, use an API key from your dashboard instead:

```bash
revyl auth login --api-key              # Prompts for the key and stores it
export REVYL_API_KEY=your-api-key       # Or pass it through the environment
```

## Quick Start

```bash
cd your-app
revyl doctor                            # Check CLI, auth, connectivity
revyl auth login                        # Approve in a browser (if not already authed)
revyl init                              # Guided wizard: build system, apps
revyl skill install --force             # Install recommended agent skills
revyl build                             # Build and upload a dev binary
revyl dev                               # Launch TUI: live device + hot reload
```


When you're ready to run outside the dev loop:

```bash
revyl test run login-flow --build       # Build, upload, and run in one step
revyl workflow create smoke-tests --tests login-flow,checkout
revyl workflow run smoke-tests          # Run the full workflow
revyl explore run --platform ios        # Explore the app and build its Atlas map
```

YAML-first creation can bootstrap local state without a pre-existing `.revyl/config.yaml`:

```bash
revyl test create login-flow --from-file ./login-flow.yaml
revyl test create --from-session <session-id> login-flow --app <app-id>
```

See the [Revyl Docs](https://docs.revyl.com/) for the full authoring workflow, YAML examples, module imports, and troubleshooting.

> `revyl dev` starts your local dev server, tunnels it to a cloud device, and installs the latest build automatically. Use `--platform android` or `--platform ios` to pick a platform (defaults to iOS).


## Agent Skills

Interactive `revyl init` asks which AI coding tool you use and installs the
recommended Revyl skills for that tool automatically. Use the bundled install
for the recommended workflow bundle, or install a single skill when the agent
should focus on one intent:

```bash
revyl skill list
revyl skill install --force                            # Install recommended skills
revyl skill install --name revyl-cli-dev-loop --force  # Dev loop + device exploration
revyl skill install --name revyl-cli-atlas --force     # Visual Atlas understanding
revyl skill install --name revyl-cli-atlas-review --force # Requested Atlas feedback changes
revyl skill install --name revyl-cli-create --force    # Stable YAML test authoring
revyl skill install --name revyl-cli-auth-bypass --force # Auth bypass setup
revyl skill install --cursor --force                   # Force Cursor if auto-detect is ambiguous
revyl skill install --codex --force                    # Force Codex if auto-detect is ambiguous
revyl skill install --claude --force                   # Force Claude Code if auto-detect is ambiguous
revyl skill install --global --force                   # Install for all projects
revyl skill show --name revyl-cli-dev-loop
revyl skill export --name revyl-cli-create -o SKILL.md
```

Use `revyl-cli-dev-loop` when you want the agent to start or attach to a generic
Revyl dev loop, interact with the device, and verify with screenshots or
reports. Use `revyl-cli-atlas` when the agent should explore an app bottom-up as
a graph, opening relevant screenshots at each node and tracing misunderstood
connections backward through edge clips and their originating reports. Use
`revyl-cli-atlas-review` when the user explicitly asks the agent to create or
manage grounded Atlas annotations. Use
`revyl-cli-create` when you want the agent to author or refine a
stable Revyl YAML test, validate it, push it, run it, and iterate from reports.
Use `revyl-cli-auth-bypass` when the agent should set up test-only auth bypass
after inspecting the app. Implement the handler in the detected stack; do not
install a separate platform skill.

Example prompts:

```text
Use the revyl-cli-dev-loop skill. Detect the app stack, start or attach to the Revyl dev loop, keep it running after Dev loop ready, and verify with revyl device screenshot before changing strategy.
```

```text
Use the revyl-cli-atlas skill. Start from the app's graph anchors, visually inspect each relevant screen, traverse observed edges in both directions, and investigate misunderstood evidence through its clips and originating reports before answering.
```

```text
Use the revyl-cli-create skill. Create a checkout smoke test from this flow, validate it, push it, and run it once.
```

```text
Use the revyl-cli-auth-bypass skill. Set up test-only auth bypass for this app and verify valid and rejected links on a Revyl device.
```

## Documentation

For full documentation of the CLI see the [Revyl Docs](https://docs.revyl.com/).

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
