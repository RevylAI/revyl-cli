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

Inline updates do not migrate project configuration or change agent skills.
If an error requires configuration migration, follow that error's recovery
instructions separately. See [Agent skills](#agent-skills) for how to update
skills yourself.

Piped or redirected commands, CI, and detected coding agents never receive the
interactive update prompt. npm, pip, and pipx installations show their existing
package-manager upgrade command instead of updating a potentially different
installation automatically.

`--json`, `--quiet`, MCP commands, version commands, shell completion, and upgrade
commands suppress the automatic notice. Set `REVYL_NO_UPDATE_NOTIFIER=1` to
disable it entirely.

You can also run `revyl upgrade` (or its `revyl update` alias) directly. Use
`revyl upgrade --check` to check without installing. Like inline updates,
`revyl upgrade` changes only the CLI.

## Agent skills

No CLI update installs, refreshes, or overwrites agent skills. That holds for
inline updates, `revyl upgrade`, Homebrew, npm, pip, and the install script, so
there is no automatic skill update to turn off. When `revyl upgrade` finds
installed Revyl skills, it reminds you to run `revyl skill update`. Set
`REVYL_NO_POST_UPGRADE_SKILL_INSTALL=1` to hide that reminder.

To pick up skill changes from a newer CLI, update installed skills yourself:

```bash
revyl skill update                     # Installed project skills
revyl skill update revyl-cli-dev-loop  # Only the named skills
revyl skill update --global            # Installed skills in your home directory
```

`revyl skill update` refreshes only packages that Revyl installed and that are
unchanged since installation, and it never adds skills. A Revyl skill package
that you edited (for example, by merging other skills into it) or replaced by
hand under its original name is left unchanged and reported as `preserved` with
the reason. When it preserves a package, the command exits non-zero so scripts
notice. Directories under other names are never changed; they are reported only
when they still carry Revyl's installation record. To update around packages
you maintain yourself, name only the skills you want refreshed.
`revyl skill install --force` replaces a selected package even when you edited
it, so do not use it for skills you customize.

CLI v0.1.109 and earlier refreshed existing skills after a direct or Homebrew
`revyl upgrade`. On those versions, set `REVYL_NO_POST_UPGRADE_SKILL_INSTALL=1`
to skip that refresh.
