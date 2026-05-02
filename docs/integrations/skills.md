# Agent Skills

> [Back to README](../README.md) | [MCP Setup](mcp-setup.md) | [Commands](../COMMANDS.md)

Skills are embedded playbooks that teach your AI coding agent how to use Revyl effectively. The first-class public skills are focused on the customer workflows agents run most often: dev loops, test creation, and test-only auth bypass. Optional by-name skills cover narrower implementation jobs.

## Install

Interactive `revyl init` asks which AI coding tool you use and installs the
public skills for Cursor, Codex, or Claude Code automatically. Use these
commands when you want to install, refresh, or export skills manually:

```bash
revyl skill list
revyl skill install --force
revyl skill install --global --force
```

### Install by intent

Use the bundled install when you want the recommended skills:

```bash
revyl skill install --force
```

Install a single skill when the agent should focus on one workflow:

| Intent | Skill | Command |
|--------|-------|---------|
| Run a Revyl dev loop, interact with the device, and verify app behavior | `revyl-cli-dev-loop` | `revyl skill install --name revyl-cli-dev-loop --force` |
| Author or refine stable Revyl YAML tests, then validate, push, run, and inspect reports | `revyl-cli-create` | `revyl skill install --name revyl-cli-create --force` |
| Set up test-only auth bypass across a mobile app stack | `revyl-cli-auth-bypass` | `revyl skill install --name revyl-cli-auth-bypass --force` |

Add `--global` for user-level install, or add `--cursor`, `--codex`, or `--claude` when tool detection is ambiguous.

### Tool-specific install

```bash
revyl skill install --cursor --force
revyl skill install --codex --force
revyl skill install --claude --force
```

### Global install

By default, skills are installed at the project level. Use `--global` for user-level installation (applies to all projects):

```bash
revyl skill install --global --force
revyl skill install --global --cursor --force
```

### Installation locations

| Tool | Project-level | User-level (`--global`) |
|------|--------------|------------------------|
| Cursor | `.cursor/skills/<skill-name>/SKILL.md` | `~/.cursor/skills/<skill-name>/SKILL.md` |
| Claude Code | `.claude/skills/<skill-name>/SKILL.md` | `~/.claude/skills/<skill-name>/SKILL.md` |
| Codex | `.codex/skills/<skill-name>/SKILL.md` | `~/.codex/skills/<skill-name>/SKILL.md` |

### Refresh skills after CLI update

```bash
revyl skill install --force
```

---

## First-Class Skills

Use these names directly in prompts when you want the agent to follow the right workflow.

| Skill | Description |
|-------|-------------|
| `revyl-cli-dev-loop` | Use when the agent should run a generic Revyl CLI dev loop: initialize or attach, start the right hot-reload or rebuild loop for the app stack, keep the session running, interact with the device, and verify with screenshots or reports. |
| `revyl-cli-create` | Use when the agent should author or refine a stable Revyl YAML test from evidence, keep steps intent-level, use sparse user-visible validations, then validate YAML, push, run, and iterate from reports. |
| `revyl-cli-auth-bypass` | Use when the agent should set up test-only auth bypass across a mobile app stack, choose the platform recipe, add launch-var gates, and verify valid and rejected links on a Revyl device. |

Optional skills:

| Skill | Description |
|-------|-------------|
| `revyl-cli-auth-bypass-expo` | Expo and Expo Router leaf recipe used by `revyl-cli-auth-bypass` when the app stack is Expo. |
| `revyl-cli-auth-bypass-react-native` | React Native bare leaf recipe used after `revyl-cli-auth-bypass` detects a non-Expo React Native app. |
| `revyl-cli-auth-bypass-ios` | Native iOS leaf recipe used after `revyl-cli-auth-bypass` detects a Swift/iOS app. |
| `revyl-cli-auth-bypass-android` | Native Android leaf recipe used after `revyl-cli-auth-bypass` detects a Kotlin/Java Android app. |
| `revyl-cli-auth-bypass-flutter` | Flutter leaf recipe used after `revyl-cli-auth-bypass` detects a Flutter app. |

Compatibility skills from older releases remain available by exact name. The default install includes the first-class auth-bypass entrypoint plus platform leaves so agents can delegate after stack detection.

## Manage Skills

```bash
revyl skill list
revyl skill show --name revyl-cli-dev-loop
revyl skill export --name revyl-cli-create -o SKILL.md
revyl skill install --name revyl-cli-dev-loop --force
revyl skill install --name revyl-cli-create --force
revyl skill install --name revyl-cli-auth-bypass --force
revyl skill install --name revyl-cli-auth-bypass-expo --force
revyl skill install --name revyl-cli-auth-bypass-react-native --force
revyl skill install --name revyl-cli-auth-bypass-ios --force
revyl skill install --name revyl-cli-auth-bypass-android --force
revyl skill install --name revyl-cli-auth-bypass-flutter --force
revyl skill install --name revyl-cli-create --cursor --force
```

---


## Prompt Examples

### CLI dev-loop

```text
Use the revyl-cli-dev-loop skill. Detect the app stack, start or attach to the Revyl dev loop, keep it running after Dev loop ready, and verify with revyl device screenshot before changing strategy.
```

### CLI create

```text
Use the revyl-cli-create skill. Create a checkout smoke test from this flow, validate it, push it, and run it once.
```

### Auth bypass

```text
Use the revyl-cli-auth-bypass skill. Set up test-only auth bypass for this app, choose the platform recipe after inspecting the repo, and verify valid and rejected links on a Revyl device.
```

### Platform auth bypass leaf

```text
Use the platform-specific revyl-cli-auth-bypass-* leaf only after the auth-bypass entrypoint has selected the app stack. Implement the app-side hook and verify accepted/rejected states on a Revyl device.
```
