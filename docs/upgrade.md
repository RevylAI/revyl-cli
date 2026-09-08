# Updating the Revyl CLI

After a command succeeds or fails, Revyl checks for a newer release and shows an
update notice when one is available. Release checks are cached for 24 hours and
do not prevent the command from completing if the check is unavailable.

In an interactive terminal, direct-download and Homebrew installations offer:

```text
Update now? [Y/n]
```

Press Enter or type `y` to update in place, or type `n` to keep working. Direct
downloads are checksum-verified. Homebrew runs `brew update` followed by
`brew upgrade revyl`. Revyl checks the installed version before confirming the
inline update succeeded. The original command's exit status is preserved even
if the update fails, and the original command is never automatically rerun.

Inline updates do not migrate project configuration or refresh agent skills.
If an error requires configuration migration, follow that error's recovery
instructions separately. To refresh existing agent skills, run
`revyl skill install --force`.

Piped or redirected commands, CI, and detected coding agents never receive the
interactive update prompt. npm, pip, and pipx installations show their existing
package-manager upgrade command instead of updating a potentially different
installation automatically.

`--json`, `--quiet`, MCP commands, version commands, shell completion, and upgrade
commands suppress the automatic notice. Set `REVYL_NO_UPDATE_NOTIFIER=1` to
disable it entirely.

You can also run `revyl upgrade` (or its `revyl update` alias) directly. Use
`revyl upgrade --check` to check without installing. A direct Homebrew or
binary upgrade through this command also refreshes existing agent skills.
