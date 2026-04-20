# Agent Skills

> [Back to README](../README.md) | [MCP Setup](mcp-setup.md) | [Commands](../COMMANDS.md)

Skills are embedded playbooks that teach your AI coding agent how to use Revyl effectively. They improve execution quality for device interaction loops, test authoring, and failure triage. Skills are optional but significantly improve the experience.

## Install

```bash
revyl skill install              # Auto-detect tool; install CLI skill family (default)
revyl skill install --mcp        # Install MCP skill family
revyl skill install --cli --mcp  # Install both skill families
```

### Tool-specific install

```bash
revyl skill install --cursor                # CLI family for Cursor
revyl skill install --codex --mcp           # MCP family for Codex
revyl skill install --claude --cli --mcp    # Both families for Claude Code
```

### Global install

By default, skills are installed at the project level. Use `--global` for user-level installation (applies to all projects):

```bash
revyl skill install --cursor --global
revyl skill install --codex --global --cli --mcp
```

### Installation locations

| Tool | Project-level | User-level (`--global`) |
|------|--------------|------------------------|
| Cursor | `.cursor/skills/<skill-name>/SKILL.md` | `~/.cursor/skills/<skill-name>/SKILL.md` |
| Claude Code | `.claude/skills/<skill-name>/SKILL.md` | `~/.claude/skills/<skill-name>/SKILL.md` |
| Codex | `.codex/skills/<skill-name>/SKILL.md` | `~/.codex/skills/<skill-name>/SKILL.md` |

### Refresh skills after CLI update

```bash
revyl skill install --codex --force    # Re-install with latest content
```

---

## CLI Skill Family

Use the CLI family when workflows should be expressed as `revyl` shell commands.

| Skill | Description |
|-------|-------------|
| `revyl-cli` | Base skill. Routes to the correct sub-skill based on the task. Teaches deterministic command sequences, secret handling via env vars, and target-style action phrasing. |
| `revyl-cli-dev-loop` | Local dev loop execution. Guides the agent through `revyl dev` startup, hot-reload device interaction, capturing successful paths, and converting them into stable regression tests. |
| `revyl-cli-create` | Test authoring. Teaches the agent to write well-structured YAML tests, validate them, create/push to the remote, and run the first execution. Enforces one-action-per-step and durable validation patterns. |
| `revyl-cli-analyze` | Failure triage. Teaches the agent to fetch test reports, classify failures (real bug, flaky test, infra issue, test improvement), and provide exact next actions with rerun commands. |

## MCP Skill Family

Use the MCP family when execution should happen through MCP tool calls (e.g. from Cursor, Claude Code, Codex).

| Skill | Description |
|-------|-------------|
| `revyl-mcp` | Base skill. Routes to the correct sub-skill. Enforces screenshot-before-action re-anchoring and one-action-per-loop-iteration patterns. |
| `revyl-mcp-dev-loop` | Live device execution via MCP tools. Teaches the screenshot-observe-act-verify loop, max 2 actions before re-anchoring, and `start_dev_loop` as the required first call. |
| `revyl-mcp-create` | Test authoring via MCP tools. Guides the agent through validate -> create -> run -> report using MCP tool calls instead of shell commands. |
| `revyl-mcp-analyze` | Failure triage via MCP tools. Same classification framework as `revyl-cli-analyze` but using MCP tool calls for data retrieval. |

## CLI vs MCP: When to Use Which

- Use **CLI skills** when the agent has shell access and you want explicit, auditable command sequences.
- Use **MCP skills** when the agent runs inside an MCP host (Cursor, Claude Code, Codex) and you want tool-call orchestration.
- Install **both families** when you want the agent to pick the best approach based on context.

---

## Manage Skills

```bash
revyl skill list                                 # List available skill names
revyl skill show --name revyl-cli-dev-loop       # Print a specific skill to stdout
revyl skill export --name revyl-mcp-dev-loop -o SKILL.md  # Export to file
revyl skill install --name revyl-cli-create --name revyl-cli-analyze --codex  # Selective install
```

---

## Workflow: Ad-Hoc -> Convert -> Regress

The most common end-to-end workflow:

```bash
# 1) Install both skill families
revyl skill install --cli --mcp --codex --force

# 2) Run exploratory flow (revyl dev + CLI/MCP dev-loop skill)
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

---

## Prompt Examples

### CLI dev-loop

```text
Use the revyl-cli-dev-loop skill.
Goal: verify I can sign in and reach Home using CLI flow.
Use only Revyl CLI commands (no MCP tool calls).

Steps:
1) start from project root
2) run revyl init --hotreload if needed
3) run revyl dev and wait for readiness
4) summarize exact actions I should perform in app
5) convert successful flow into a test:
   - revyl dev test create login-smoke --platform ios
   - revyl dev test open login-smoke
   - revyl test push login-smoke --force
   - revyl test run login-smoke
6) if run fails, fetch report with revyl test report login-smoke --json and classify failure
```

### MCP dev-loop

```text
Use the revyl-mcp-dev-loop skill.
Use Revyl MCP tools only.
Goal: bypass login and land on Home screen.

Rules:
1) first call must be start_dev_loop
2) loop: screenshot -> one-line observation -> one best action -> screenshot verify
3) max 2 actions before re-anchor
4) if state is unexpected, stop and re-anchor
5) end with summary: final screen, actions, anomalies
```

### MCP create

```text
Use the revyl-mcp-create skill.
Create a new ios test named checkout-smoke from this flow:
- open Shop
- open product Orchid Mantis
- add to cart
- open cart
- verify Orchid Mantis and price $62.00

Use MCP tools to:
1) validate YAML
2) create/update test
3) run test
4) report pass/fail with task id
```

### CLI analyze

```text
Use the revyl-cli-analyze skill.
Analyze this failed test run end-to-end:
1) run revyl test report checkout-smoke --json
2) classify failure as REAL BUG, FLAKY TEST, INFRA ISSUE, or TEST IMPROVEMENT
3) provide exact next action and rerun command
```
