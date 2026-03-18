<!-- mintlify
title: "Revyl CLI"
description: "Install and use the Revyl CLI for local-first mobile app testing"
target: cli/index.mdx
-->

The Revyl CLI brings AI-powered mobile testing to your terminal. Create, sync, and run tests locally without leaving your development environment.

## Why Use the CLI?

- **Local-first workflow** - Edit tests in your IDE, sync with the cloud
- **CI/CD ready** - Integrate testing into your build pipelines
- **Fast iteration** - Run tests directly from your terminal
- **Version control** - Store test definitions alongside your code

## Installation

Install the CLI using your preferred method:

<CodeGroup>

```bash Homebrew (macOS)
brew install RevylAI/tap/revyl
```

```bash pipx (cross-platform)
pipx install revyl
```

```bash uv
uv tool install revyl
```

```bash pip
pip install revyl
```

</CodeGroup>

The CLI binary auto-downloads on first use when installed via pip, pipx, or uv.

<Callout type="tip" title="macOS users">
  On macOS, Homebrew is the recommended installation method. It handles updates automatically via `brew upgrade revyl`.
</Callout>

### Direct download

Download the binary for your platform from [GitHub Releases](https://github.com/RevylAI/revyl-cli/releases). Available builds:

| Platform | Architecture | Asset |
|----------|--------------|-------|
| macOS | arm64 (Apple Silicon) | `revyl-darwin-arm64` |
| macOS | amd64 (Intel) | `revyl-darwin-amd64` |
| Linux | arm64 | `revyl-linux-arm64` |
| Linux | amd64 | `revyl-linux-amd64` |
| Windows | amd64 | `revyl-windows-amd64.exe` |

After downloading, make the binary executable (macOS/Linux) and place it on your `PATH`.

### Verify Installation

```bash
revyl version
```

You should see output like:

```
revyl version 1.x.x
```

### Run Diagnostics

Check that everything is configured correctly:

```bash
revyl doctor
```

This command verifies:
- CLI version and updates
- Authentication status
- API connectivity
- Project configuration
- Build system detection

## Quick Start

Get up and running in 5 commands:

```bash
# 1. Install the CLI
brew install RevylAI/tap/revyl    # or: pipx install revyl

# 2. Authenticate with your API key
revyl auth login

# 3. Initialize your project
cd your-app
revyl init

# 4. Pull existing tests from Revyl
revyl test pull

# 5. Run a test
revyl test run login-flow
```

<Callout type="tip" title="First Time?">
  If you don't have tests yet, create one in the [Revyl dashboard](https://app.revyl.ai) first, then use `revyl test pull` to download it locally.
</Callout>

## Updating the CLI

Use the upgrade command that matches how you installed:

<CodeGroup>

```bash Homebrew
brew upgrade revyl
```

```bash pipx
pipx upgrade revyl
```

```bash pip
pip install --upgrade revyl
```

```bash Direct download
revyl upgrade
```

</CodeGroup>

If you installed via direct download, `revyl upgrade` performs a self-update (downloads the latest binary, verifies checksums, and replaces the executable). To check for updates without installing, use:

```bash
revyl upgrade --check
```

## Global Flags

These flags work with any command:

| Flag | Description |
|------|-------------|
| `--debug` | Enable debug logging |
| `--dev` | Use local development servers |
| `--json` | Output results as JSON |
| `--quiet, -q` | Suppress non-essential output |

## Next Steps

For focused device session and action workflows, see [Device Automation](/device/index).

<CardGroup cols={2}>
  <Card title="Authentication" icon="key" href="/cli/authentication">
    Set up your API key
  </Card>
  <Card title="Project Setup" icon="folder" href="/cli/project-setup">
    Initialize your project
  </Card>
  <Card title="Managing Tests" icon="list" href="/cli/tests">
    Sync and manage tests
  </Card>
  <Card title="Running Tests" icon="play" href="/cli/running-tests">
    Execute tests locally
  </Card>
  <Card title="Dev Loop" icon="bolt" href="/cli/dev-loop-guide">
    Iterate quickly with revyl dev
  </Card>
  <Card title="Device Automation" icon="mobile" href="/device/index">
    Start, control, and debug cloud devices
  </Card>
  <Card title="iOS Publishing" icon="paper-plane" href="/cli/publishing-ios">
    Upload and distribute to TestFlight
  </Card>
</CardGroup>
