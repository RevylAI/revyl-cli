# Getting Started

## Install

```bash
curl -fsSL revyl.com/install.sh | sh
```

Or use a package manager:

<CodeGroup>

```bash Homebrew (macOS)
brew install RevylAI/tap/revyl
```

```bash pipx (cross-platform)
pipx install revyl
```

```bash pip
pip install revyl
```

</CodeGroup>

## Set Up

```bash
revyl doctor                     # Check CLI version, auth, connectivity
revyl auth login                 # Authenticate with your API key
cd your-app && revyl init        # Detect framework, create .revyl/config.yaml
revyl skill install --force      # Install agent skills: revyl-cli-dev-loop, revyl-cli-create
```

## Connect Your AI Coding Agent

Revyl skills are the recommended way to teach your AI coding agent how to use
the CLI well. During interactive `revyl init`, Revyl asks which AI coding tool
you use and installs the public skills for that tool automatically:

- **Cursor** installs to `.cursor/skills`
- **Codex** installs to `.codex/skills`
- **Claude Code** installs to `.claude/skills`
- **Skip for now** leaves setup for later

If you skipped that prompt or want to refresh skills after a CLI update, install
both first-class skills:

```bash
revyl skill install --force
```

Install one skill when you want the agent focused on a specific job:

| Agent intent | Install | Prompt with |
|---|---|---|
| Run a generic Revyl dev loop, interact with the device, and verify app behavior | `revyl skill install --name revyl-cli-dev-loop --force` | `Use the revyl-cli-dev-loop skill.` |
| Author or refine stable Revyl YAML tests, then validate, push, run, and inspect reports | `revyl skill install --name revyl-cli-create --force` | `Use the revyl-cli-create skill.` |

Useful install variants:

```bash
revyl skill list                             # List first-class skills
revyl skill install --global --force        # Install skills for all projects
revyl skill install --cursor --force        # Force Cursor if auto-detect is ambiguous
revyl skill install --codex --force         # Force Codex if auto-detect is ambiguous
revyl skill install --claude --force        # Force Claude Code if auto-detect is ambiguous
revyl skill show --name revyl-cli-dev-loop  # Print a named skill to stdout
revyl skill export --name revyl-cli-create -o FILE
```

## Pick Your Path

| I want to... | Start here |
|---|---|
| Test an **Expo** app | [Expo Build Guide](builds/expo.md) |
| Test a **React Native** (bare) app | [React Native Build Guide](builds/react-native.md) |
| Test a **Flutter** app | [Flutter Build Guide](builds/flutter.md) |
| Test a native **iOS (Swift)** app | [iOS Build Guide](builds/ios-native.md) |
| Test a native **Android (Kotlin/Java)** app | [Android Build Guide](builds/android-native.md) |
| **Control a cloud device** (no app build) | [Device Quickstart](device/quickstart.md) |
| Set up **CI/CD** testing | [CI/CD Integration](ci-cd.md) |
| Install **AI agent skills** | [Agent Skills](integrations/skills.md) |
| Connect my **AI coding agent over MCP** | [MCP Setup](integrations/mcp-setup.md) |

Each build guide walks you through the exact 2-3 commands to go from your repo to a passing test.

## What Happens Next

After following your framework's build guide, your typical workflow is:

1. **Dev loop** -- `revyl dev` connects a cloud device to your local code with hot reload. See [Dev Loop](developer_loop/dev-loop.md).
2. **Create tests** -- write YAML test definitions and run them. See [Your First Test](tests/first-test.md).
3. **CI/CD** -- run tests on every PR. See [CI/CD Integration](ci-cd.md).

## Key Concepts

- **App** -- a named container for your uploaded builds (e.g. "My App iOS"). Tests run against an app.
- **Build** -- a simulator `.app` (iOS) or `.apk` (Android) uploaded to Revyl. Each upload is tagged with your git branch.
- **Test** -- a YAML file defining steps (tap, type, validate) that run on a cloud device against your build.
- **Workflow** -- a named collection of tests that run together (e.g. "smoke-tests").
- **Device session** -- a cloud-hosted iOS or Android device you can control via CLI, SDK, or MCP.
