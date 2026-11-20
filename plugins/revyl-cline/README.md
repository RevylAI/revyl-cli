# Revyl device check for Cline

After Cline pushes a branch or opens a pull request, this plugin asks it to
offer one check of that change on a Revyl cloud iOS or Android device. It never runs Revyl itself, starts nothing, and sends nothing anywhere. The model decides whether the repository is a Revyl app and asks the user; a device starts only after the user says yes, through the Revyl MCP server's own tools.

It pairs with the Revyl MCP server, which Cline users add from Cline's MCP
Marketplace; setup is in [Revyl's MCP guide](https://docs.revyl.com/agents/mcp).
Without the server the offer has no tools to act on, so install and sign in to
it first.

## Install

```bash
cline plugin install https://github.com/RevylAI/revyl-cli/blob/<commit>/plugins/revyl-cline/revyl-device-check.ts
```

The Cline CLI installs it under `~/.cline/plugins/`, where the CLI, Kanban and
the Cline VS Code extension all load it for new tasks. The extension's own
Marketplace lists only skills and MCP servers, so the plugin is installed with
the CLI. It is one file that imports only Node built-ins and a type from
`@cline/sdk`, which a single-file plugin requires.

## Behavior

The plugin registers one `afterTool` hook. When Cline's `run_commands` tool
succeeds and one of its commands runs `git push` or `gh pr create`, the hook
reads `HEAD` and the branch in the repository it pushed, following a leading
`cd` or `git -C` from the task's directory, and returns one line as
`appendContext`, which Cline shows the model after the tool result. The line is
the same one the Kilo plugin adds: it names the commit and branch and tells the
model to find the app with `list_apps`, wait for the branch's build with
`list_builds` and `get_build`, and call `start_device_session` only after a
yes. It adds the line once per commit in a task, and never on a detached
`HEAD`, a `--dry-run` or a command that failed.

## Tests

`node --test revyl-device-check.test.ts` covers the hook against temporary git
repositories and checks that its line matches the Kilo plugin's word for word;
`go test ./plugins/revyl-cline` runs it with the CLI's tests.
