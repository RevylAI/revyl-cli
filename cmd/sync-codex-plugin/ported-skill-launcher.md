## Run Revyl through this plugin

This skill is shared with the standalone Revyl CLI. In this plugin, run each
Revyl command it shows through the plugin's pinned launcher, never `revyl`
from `PATH` or another installed CLI. Resolve the launcher from this skill's
own directory:

- **Claude Code** substitutes that directory here: `${CLAUDE_SKILL_DIR}`.
- **Codex** lists the path of this `SKILL.md`; use its absolute parent
  directory. If the Claude Code value above isn't an absolute path, use this
  rule instead.

The launcher is `../../scripts/launch-revyl` from that directory on POSIX and
`../../scripts/launch-revyl.cmd` on Windows. Write its resolved absolute path
into each command; the commands below show it as `"$LAUNCHER"`, and a `revyl`
command named in prose means the same command through the launcher. Run each
command from the app directory (the one holding `.revyl/config.yaml`), never
from the plugin's own folder: the CLI reads the current directory's Git branch,
commit, and config, so a command run inside the plugin resolves the wrong build
or project.
