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
| Connect my **AI coding agent** | [MCP Setup](integrations/mcp-setup.md) |

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
