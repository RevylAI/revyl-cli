# PR Review with Revyl

Two ways to test PRs with Revyl: **agent-driven** (AI reads the diff and explores your app) or **CLI-driven** (run existing tests on every PR).

## Agent-Driven Review

An AI agent reads the PR diff, starts a cloud device, and interactively validates the changes.

- **Workflow:** [`agent-driven.yml`](agent-driven.yml)
- **Playbook:** [`agent-playbook.md`](agent-playbook.md) -- customize this for your app

### How it works

1. PR is opened (or someone comments `/review`)
2. CI installs `revyl` and runs Claude Code
3. The agent reads the diff, then drives `revyl device` commands to explore the changed screens
4. Results are posted as a PR comment with screenshots and a session recording link

### Setup

1. Add secrets to your repo: `REVYL_API_KEY`, `ANTHROPIC_API_KEY`
2. Set `REVYL_APP_ID` in the workflow (or as a secret)
3. Copy `agent-playbook.md` into your repo as `CLAUDE.md` and customize it for your app
4. Copy `agent-driven.yml` to `.github/workflows/`

---

## CLI-Driven Review

Run pre-defined tests or workflows automatically on every PR. No AI agent needed.

- **Workflow:** [`cli-driven.yml`](cli-driven.yml)

### How it works

1. PR is opened or updated
2. CI installs `revyl` and runs `revyl workflow run <name>`
3. Exit code 0 = pass, 1 = fail
4. Results posted as a PR comment

### Setup

1. Add `REVYL_API_KEY` as a repo secret
2. Set your workflow name in `cli-driven.yml`
3. Copy to `.github/workflows/`

---

## When to use which

| | Agent-driven | CLI-driven |
|---|---|---|
| Best for | Exploratory, visual validation of UI changes | Regression testing with known test flows |
| Requires | Anthropic API key + playbook | Pre-defined tests in Revyl |
| Speed | Slower (agent explores) | Faster (runs known steps) |
| Coverage | Adapts to what changed | Fixed test suite |
