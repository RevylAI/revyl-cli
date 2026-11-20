# Revyl device check for Kilo Code

After Kilo pushes a branch or opens a pull request, this plugin asks it to offer
one check of that change on a Revyl cloud iOS or Android device. It never runs Revyl itself, starts nothing, and sends nothing anywhere. The model decides whether the repository is a Revyl app and asks the user; a device starts only after the user says yes, through the Revyl MCP server's own tools.

It pairs with the Revyl MCP server, which Kilo users add from the Kilo
Marketplace; setup is in [Revyl's MCP guide](https://docs.revyl.com/agents/mcp).
Without the server the offer has no tools to act on, so install and sign in to
it first.

## Install

From the Kilo Marketplace, install **Revyl Device Check** for the project or
globally. Or add it to the `plugin` array of a Kilo config file, pinned to a
commit of this repository:

```json
{
  "plugin": ["git:github.com/RevylAI/revyl-cli@<commit>#plugins/revyl-kilo"]
}
```

Kilo clones the repository at that commit and loads `server.ts`. Kilo doesn't
install dependencies for git plugins, so the plugin uses only Node built-ins.

## Behavior

The plugin registers one `tool.execute.after` hook. When Kilo's `bash` tool
exits with status 0 and its command runs `git push` or `gh pr create`, the hook
reads `HEAD` and the branch in the repository it pushed, following a leading
`cd` or `git -C`, and appends one line to
the command's output. That line names the commit and branch and tells the model
to find the app with `list_apps`, wait for the branch's build with
`list_builds` and `get_build`, and call `start_device_session` only after a
yes. It adds the line once per commit for the life of the Kilo process, and
never on a detached `HEAD`, a `--dry-run` or a failed command.

## Tests

`node --test server.test.ts` covers the hook against temporary git
repositories; `go test ./plugins/revyl-kilo` runs it with the CLI's tests.
