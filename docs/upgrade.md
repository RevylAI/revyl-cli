# Updating the Revyl CLI

After a command succeeds or fails, Revyl checks for a newer release and shows an
update notice when one is available. The check reads the latest-release redirect
on github.com, not the rate-limited GitHub API. Results, including failed checks,
are cached for 24 hours, and the check never fails the command. Non-interactive
runs never wait for it; an interactive terminal waits at most two seconds for a
pending check before showing the notice.

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

Piped or redirected commands, `--json` and `--quiet` runs, CI, and detected
coding agents never receive the interactive update prompt. Instead, they get one
stderr line at most once every 24 hours per machine:

```text
⚠ Revyl CLI 0.1.131 is available (current 0.1.96). Upgrade with: brew upgrade revyl
```

The notice names the upgrade command for the installation: `brew upgrade revyl`
for Homebrew, `pip install --upgrade revyl`, `pipx upgrade revyl`, or
`uv tool upgrade revyl` for Python packages, `npm update -g @revyl/cli` for npm,
and `revyl upgrade` for the shell installer, direct downloads, and any location
it cannot classify. npm, pip, pipx, and uv installations are never updated
automatically, because that could update a different installation.

The notice never writes to stdout, so `--json` output is unchanged. If a script
parses stdout and stderr together, set `REVYL_NO_UPDATE_NOTIFIER=1`.

Before the Revyl API stops accepting an older CLI version, its responses
announce the minimum version it will require. A CLI below that minimum prints a
stronger warning once every 24 hours per machine, in every output mode:

```text
⚠ Revyl CLI 0.1.133 will soon stop working: the Revyl API will require 0.1.140 or later. Upgrade now with: brew upgrade revyl
```

MCP commands, version commands, shell completion, and upgrade commands never
show a notice. Set `REVYL_NO_UPDATE_NOTIFIER=1` to disable update checks and
notices entirely.

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
