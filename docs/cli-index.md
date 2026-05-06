The Revyl CLI brings AI-powered mobile testing to your terminal. Create, sync, and run tests locally without leaving your development environment.

## Why Use the CLI?

- **Local-first workflow** - Edit tests in your IDE, sync with the cloud
- **CI/CD ready** - Integrate testing into your build pipelines
- **Fast iteration** - Run tests directly from your terminal
- **Version control** - Store test definitions alongside your code

## Installation

Install the CLI using your preferred method:

<CodeGroup>

```bash Shell (macOS/Linux)
curl -fsSL https://revyl.com/install.sh | sh
```

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

The shell installer downloads the native binary directly. Package-manager installs via pip, pipx, or uv auto-download the CLI binary on first use.

<Callout type="tip" title="macOS users">
  Homebrew remains a good macOS package-manager option when you want updates through `brew upgrade revyl`.
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

Get up and running in 6 commands:

```bash
# 1. Install the CLI
curl -fsSL https://revyl.com/install.sh | sh

# 2. Check your environment
revyl doctor

# 3. Authenticate
revyl auth login

# 4. Initialize your project
cd your-app
revyl init

# 5. Build and upload a dev binary
revyl build upload

# 6. Start the dev loop
revyl dev
```

From the dev TUI, create and run tests against your live device:

```bash
revyl dev test create login-flow
revyl dev test run login-flow
```

<Callout type="tip" title="First Time?">
  `revyl doctor` checks CLI version, authentication, API connectivity, project config, and build system detection. Run it whenever something feels off.
</Callout>

## Updating the CLI

Use the upgrade command that matches how you installed:

<CodeGroup>

```bash Shell installer
curl -fsSL https://revyl.com/install.sh | sh
```

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
  <Card title="Managing Tests" icon="list" href="/cli/creating-tests">
    Sync and manage tests
  </Card>
  <Card title="Running Tests" icon="play" href="/cli/running-tests">
    Execute tests locally
  </Card>
  <Card title="Dev Loop" icon="bolt" href="/cli/dev-loop">
    Iterate quickly with revyl dev
  </Card>
  <Card title="Device Automation" icon="mobile" href="/device/index">
    Start, control, and debug cloud devices
  </Card>
</CardGroup>
