![Revyl CLI](assets/hero.gif)

# Revyl CLI

AI-powered mobile testing from your terminal. Create, run, and manage end-to-end tests for iOS and Android apps without leaving your development environment.

Full web dashboard docs at [docs.revyl.ai](https://docs.revyl.ai).

## Getting Started

New to Revyl? Start here: **[Getting Started](getting-started.md)** -- install, pick your framework, and go from repo to test results in minutes.

## Quick Start

```bash
# 1. Install
curl -fsSL https://revyl.com/install.sh | sh

# 2. Check your environment
revyl doctor

# 3. Authenticate
revyl auth login

# 4. Initialize your project
cd your-app && revyl init

# 5. Install agent skills
# Available: revyl-cli-dev-loop (dev loop), revyl-cli-create (test authoring)
revyl skill install --force

# 6. Build and upload
revyl build upload

# 7. Start the dev loop
revyl dev
```

## Build Guides

Framework-specific guides to go from repo to test results in 3 commands.

- [Expo](builds/expo.md) — local build or EAS cloud, two commands to test
- [React Native (bare)](builds/react-native.md) — xcodebuild / Gradle, then upload
- [Flutter](builds/flutter.md) — flutter build, then upload
- [iOS Native (Swift)](builds/ios-native.md) — xcodebuild for simulator, or auto-detect from DerivedData
- [Android Native (Kotlin/Java)](builds/android-native.md) — Gradle assembleDebug, then upload
- [Building in CI](builds/ci-builds.md) — `--url` uploads, GitHub Action, EAS cloud patterns

## Developer Loop

- [Dev Loop](developer_loop/dev-loop.md) — `revyl dev` with hot reload, device interaction, test creation
- [Dev Setup](developer_loop/dev-setup.md) — framework detection and provider configuration for Expo, React Native, Flutter, Swift, Android, KMP, Bazel

## Tests

- [Your First Test](tests/first-test.md) — install, authenticate, create a YAML test, run it, view the report
- [Creating Tests](tests/creating-tests.md) — YAML authoring, modules, scripts, variables, control flow, workflows
- [Running Tests](tests/running-tests.md) — `revyl test run`, `revyl workflow run`, output formats, build pipelines
- [Syncing Tests](tests/syncing-tests.md) — push, pull, diff, and reconcile local YAML with the Revyl server

## Device Automation

- [Overview](device/index.md) — capabilities and architecture
- [Quickstart](device/quickstart.md) — start a device session in 60 seconds
- [Troubleshooting](device/troubleshooting.md) — common issues and fixes

## Device SDK

- [Overview](device-sdk/index.md) — Python SDK overview and quick example
- [Scripting](device-sdk/scripting.md) — automate multi-step device workflows with Python
- [Multi-Session](device-sdk/multi-session.md) — run parallel device sessions
- [Streaming](device-sdk/streaming.md) — embed live WebRTC video from cloud devices
- [SDK Reference](device-sdk/reference.md) — DeviceClient, ScriptClient, ModuleClient, BuildClient

## CI/CD

- [CI/CD Integration](ci-cd.md) — GitHub Actions, raw CLI in CI, GitLab, environment variables

## Integrations

- [MCP Setup](integrations/mcp-setup.md) — Cursor, Claude Code, Codex, VS Code, Windsurf, Claude Desktop
- [Expo Dashboard](integrations/expo-dashboard.md) — auto-import builds from EAS
- [Agent Skills](integrations/skills.md) — embedded skills for device loops and test creation
- [Agent Dev Loop](integrations/agent-dev-loop.md) — AI agent-assisted development workflow

## Reference

- [Command Reference](COMMANDS.md) — every command and flag
- [SDK Reference](device-sdk/reference.md) — Python SDK: DeviceClient, ScriptClient, ModuleClient, BuildClient
- [Project Configuration](CONFIGURATION.md) — `.revyl/config.yaml` reference
- [Authentication](guide-authentication.md) — API keys, browser login, environment variables
- [CLI Landing Page](cli-index.md) — installation, global flags, updating
