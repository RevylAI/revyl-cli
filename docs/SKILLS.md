# Agent Skills

> [Back to README](../README.md) | [MCP Setup](MCP_SETUP.md) | [Commands](COMMANDS.md)

Install agent skills to improve AI tool integration with Revyl.

## Install Skills

```bash
revyl skill install              # Auto-detect tool; install CLI skill family (default)
revyl skill install --mcp        # Install MCP skill family
revyl skill install --cli --mcp  # Install both skill families
revyl skill install --cursor     # Cursor only
revyl skill install --claude     # Claude Code only
revyl skill install --codex      # Codex only
```

## Manage Skills

```bash
revyl skill list                 # Show available skill names
revyl skill show --name revyl-cli
revyl skill show --name revyl-mcp-dev-loop
revyl skill export --name revyl-cli-create -o SKILL.md
revyl skill export --name revyl-mcp-analyze -o SKILL.md
revyl skill revyl-mcp-dev-loop install --codex
revyl skill install --name revyl-cli-create --name revyl-cli-analyze --codex
```

## Available Embedded Skills

| Skill | Description |
|-------|-------------|
| `revyl-cli` | Base CLI workflow guidance |
| `revyl-cli-create` | CLI test authoring and conversion |
| `revyl-cli-analyze` | CLI failure triage |
| `revyl-cli-dev-loop` | CLI dev loop execution |
| `revyl-mcp` | Base MCP orchestration guidance |
| `revyl-mcp-create` | MCP test authoring |
| `revyl-mcp-analyze` | MCP failure triage |
| `revyl-mcp-dev-loop` | MCP live device execution loop |

## Workflow: Ad-Hoc -> Convert -> Regress

Use this loop for the most common workflow (explore flow now, keep as regression later):

```bash
# 1) Install both skill families
revyl skill install --cli --mcp --codex --force

# 2) Run exploratory flow (usually with revyl dev + CLI/MCP dev-loop skill)
# ...perform exploratory interactions...

# 3) Convert the successful path into a test
revyl test create <test-name> --platform ios
revyl test open <test-name>
revyl test push <test-name> --force

# 4) Run regression
revyl test run <test-name>

# 5) Analyze failure details when run fails
revyl test report <test-name> --json
```

### Guidance

- Use `revyl-cli-dev-loop` or `revyl-mcp-dev-loop` to execute and observe live app behavior.
- Use `revyl-cli-create` or `revyl-mcp-create` to structure exploratory steps into stable YAML.
- Use `revyl-cli-analyze` or `revyl-mcp-analyze` to classify failures and drive precise rewrites.
