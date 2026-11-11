# Send feedback

Send a bug report, friction report, feature request, or other feedback to the
Revyl team from an authenticated CLI session. Every report is read.

```bash
revyl feedback --type bug "Rebuild hangs after restarting Metro"
revyl feedback --type bug --ref <command-id> "revyl dev rebuild hung after Metro restarted; expected it to reattach"
revyl feedback --type friction "Goal: rerun one failed test on a fresh build. Ran test list, build list, then test run with a guessed --build flag. Wished for: revyl test rerun <id> --latest-build"
revyl feedback --type feature --body-file request.md --json
revyl feedback --type other --body-file - --json < feedback.txt
```

## When agents should send feedback

Coding agents should send a report themselves, without asking the user first.

Use `--type bug` when Revyl broke:

- a `revyl` command or Revyl MCP tool fails and its error does not say how to fix it
- Revyl docs, a skill, or help text turned out to be wrong

Use `--type friction` when Revyl worked but made the user's request harder than
it should have been:

- no direct command or tool did what the user asked
- output was confusing or incomplete, so the agent needed extra calls
- the agent had to guess flags, reread help, retry, or invent a workaround
- a step was slow enough that repeating the request would be painful
- it was unclear which command to use

Use `--type feature` for a capability that does not exist, and `--type other`
for anything else.

Send one report per distinct problem as soon as it happens, then continue the
task. A bug report describes what you tried, the exact command or tool, what
happened, and what you expected. A friction report gives the user's goal in
general terms, what you ran, where it got hard, and the command or tool you
wish existed. Do not report a plain mistake in your own arguments or a test that
ran and failed its checks.

### Setup and onboarding

When installing Revyl, signing in, running `revyl init`, or the first build or
device session hits a problem, a step is blocked, or the user stops or changes
course partway, report it as `bug` (Revyl broke) or `friction` (it worked but
was hard). Name the setup step reached, what blocked it, and where the user
stopped. Report a setup that only succeeded after a workaround as friction.
Never include the user's app data.

## Hints for coding agents

Under a detected coding agent, the CLI prints at most two kinds of one-line
hint on stderr. People running the CLI themselves never see either, and
`--json` stdout is unchanged.

- **Friction:** on the first command of an agent conversation, a line invites a
  `revyl feedback --type friction` report. A new conversation is one whose
  agent conversation ID (such as Codex's thread ID) differs from the last one
  shown the hint, or the first command after no agent command ran on the
  machine for 60 minutes. Sign-in, upgrade, version, help, completion,
  feedback, and `mcp serve` commands neither show the line nor count as that
  first command.
- **Failure:** when a command fails, a line suggests `revyl feedback --type
  bug`, with `--ref` set to that invocation's command ID when telemetry is
  enabled. It appears at most once every 30 minutes on a machine and is skipped
  for cancellations, sign-in failures, plan limits, and failed test verdicts.

The CLI recognizes Claude Code, Codex, Cursor, Gemini CLI, OpenCode, VS Code
Copilot, and Cline. Any other agent can set `REVYL_AGENT=<agent-name>` in the
environment it runs `revyl` from; see
[Agent attribution](sandboxed-environments.md#agent-attribution).

## Options

Choose `--type` `bug`, `friction`, `feature`, or `other`. Give the message
exactly once: as an argument, with `--body`, or with `--body-file` (`-` reads
stdin). The command never prompts. `--ref` names the command ID, run, or
session the problem came from (letters, digits, and `. _ : / -`, up to 128
characters) and is appended to the message as a `Ref:` line. The message,
including that line, must be nonempty and no longer than 4,000 characters; a
friction message has 11 fewer characters available. Do not include secrets, API
keys, or customer content.

When a Revyl API that predates the friction type rejects a friction report, the
CLI resends it once as `other` with the message prefixed `[friction] `.

The request includes the CLI version, operating system, architecture, and CLI
source; recognized agent context is sent through the existing agent header.
Feedback submissions are not otherwise retried, preventing duplicate reports.

On success, human mode prints `Feedback sent to the Revyl team.` to stderr.
With `--json`, stdout contains the API acknowledgement:

```json
{
  "sent": true,
  "attachments_uploaded": true
}
```

The CLI submits text only; `attachments_uploaded` is part of the shared support
response and does not indicate that the CLI uploaded a file.
