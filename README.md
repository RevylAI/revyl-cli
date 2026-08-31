<p align="center">
  <img src="docs/assets/hero.gif" alt="Revyl" width="600" />
</p>

<h1 align="center">Revyl</h1>

<p align="center">
  <em>Proactive Reliability for Mobile Apps</em>
</p>

<p align="center">
  <a href="https://github.com/RevylAI/revyl-cli/releases"><img src="https://img.shields.io/badge/version-0.1.104-9D61FF" alt="Version" /></a>
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
revyl init                              # Detect and write the local project config
revyl skill install --force             # Install recommended agent skills
revyl build --profile development --platform ios  # Build and upload one recipe
revyl dev --profile development --platform ios    # Launch TUI: live-device development loop
```

`revyl init` always writes a stable `project.id` in `.revyl/config.yaml`. When
detection succeeds, it also writes `build.framework` and named profiles for the
detected iOS and/or Android platforms before any optional interactive onboarding.
Supplying a project ID writes it locally but does not validate, attach, or publish
it; use `-y` to stop after local config creation. Authenticated interactive setup
can continue into the same GitHub PR-automation flow as `revyl github setup`, but
only after the project config has been written. When the Revyl GitHub App can
access the repository, committing that valid config to the default branch
reconciles a matching server project or creates a new Git-managed project when
its `project.id`, project root, and config path are unclaimed and its references
are valid. Pull requests validate new configs as unpublished bootstrap candidates;
creation occurs only from the actual merged default-branch commit. `revyl config
push` remains the explicit manual publication and recovery path. For an existing
legacy file, preview or perform the local conversion explicitly:

```bash
revyl config migrate --check            # Summarize the migration; do not write
revyl config migrate                    # Confirm, back up, and replace atomically
revyl config migrate --write            # Back up and replace without confirmation
```

A successfully prepared legacy migration uses one concise human-readable
summary of whether fields will be dropped or defaulted; a write creates an
exact-byte backup. JSON `--check` output contains the complete migration
proposal and migration ledger. Migration
externalizes legacy top-level test aliases to conflict-checked
`.revyl/tests/<alias>.yaml` files, preserves an existing matching mapping
byte-for-byte, and stops on destination conflicts because those are unsafe to
write. The retired top-level workflow alias cache is reported and removed.

When authenticated, migration may make read-only verified lookups to reuse or
interactively select a project from the verified repository, resolve enabled
legacy PR-build app names to exact platform app IDs, and resolve legacy PR
workflow names to exact organization workflow IDs.
Missing, duplicate, or inaccessible workflow names are omitted and reported,
including the implicit `smoke` workflow from the legacy `smoke_every_pr`
preset. An enabled PR build is never silently omitted: app or framework meaning
that still cannot be resolved is reported as lossy and omitted from the best-effort
proposal. Inspect JSON `--check` before writing; afterward, compare the reported
backup or ask a coding agent to reconcile omissions. Migration
never creates or attaches a server project, pulls or publishes configuration,
or otherwise mutates server state. Edit the project YAML directly when you
need to change project settings, then run `revyl config validate`.

You can also create and edit manual-authority projects from the GitHub integration in
the Revyl web app. After creating a project there, run `revyl config pull` from that
project root (or a directory beneath it). If no local config exists, the CLI verifies
the connected GitHub repository and writes the nearest matching project's configuration to
its exact `.revyl/config.yaml` path; it never imports legacy or invalid remote state.
When an existing config differs, pull creates an exact-byte backup, atomically
replaces the file, and reports the backup path without prompting. If the local
project was deleted and exactly one active replacement owns that same root, pull
performs the same backup and replacement without trusting the stale ID.

Project roots are immutable. Correct a mistaken root by deleting the manual project
in the web app, creating its replacement at the intended root, then running
`revyl -C <replacement-root> config pull`. A Git-managed project must first return to manual
authority by removing its designated config file from the default branch; that file
removal alone preserves the project and its automation. Project deletion remains a
web operation—there is no CLI project-delete command—and preserves the repository
connection, GitHub installation, report destination, historical results, and work
that already started.

Build profiles are customer-named recipes, not active modes. A profile can
contain an iOS recipe, an Android recipe, or both. Select the profile and
platform per invocation; add `--remote` to execute the same inherited commands,
environment, secret references, and output contract on a Revyl cloud runner,
with that recipe's remote image and caches:

```bash
revyl build --profile development --platform ios
revyl build --profile development --platform ios --remote
```

When omitted values have exactly one eligible choice, the CLI resolves them.
Otherwise it prompts interactively or fails non-interactively with the valid
choices. No profile is stored as active or default.

When you're ready to run outside the dev loop:

```bash
revyl test run login-flow --build       # Build, upload, and run in one step
revyl test run login-flow --no-open     # Suppress default report opening for a blocking terminal run
revyl workflow create smoke-tests --tests login-flow,checkout
revyl workflow run smoke-tests          # Run the full workflow
revyl explore run --platform ios        # Explore the app and build its Atlas map
```

Blocking human-terminal test and workflow runs open their completed report by
default. Use `--no-open` to suppress it. No-wait, JSON, GitHub Actions, CI,
SSH, and other headless executions never open a browser.

YAML-first creation requires the selected project to already have a
`.revyl/config.yaml`. Pull an existing registered project, initialize a new
local project, or migrate a legacy file before creating the test:

```bash
revyl config pull                       # Existing project already registered with Revyl
# or: revyl init -y                     # New local project
# legacy only: revyl config migrate --check && revyl config migrate

revyl test create login-flow --from-file ./login-flow.yaml
revyl test create --from-session <session-id> login-flow --app <app-id>
```

`test create --from-file` validates and copies the YAML into the selected
project; it never creates or migrates project configuration.

See the [Revyl Docs](https://docs.revyl.com/) for the full authoring workflow, YAML examples, module imports, and troubleshooting.

> `revyl dev` starts a live-device development loop and installs a build from one
> named profile/platform recipe. Pass `--profile` and
> `--platform` explicitly, or let Revyl resolve a unique development-like or
> sole eligible choice. Ambiguous interactive runs prompt; non-interactive runs
> fail with the valid choices. No profile or platform is stored as active or
> default.

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
