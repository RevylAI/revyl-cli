---
name: revyl-ci-sync
description: Keep a repo's Revyl remote build mirrored with its CI pipeline - update both in the same PR and debug drift between them.
---

# Revyl CI Sync Skill

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

Use this skill when changing CI/build configuration in a repo that also has a Revyl remote build configured (`.revyl/config.yaml` with `build.profiles` or a remote dev loop in use).

## The Principle

Your Revyl remote build should mirror your CI pipeline. Both build the same app from the same source; when they diverge, "works on Revyl, fails in CI" (or the reverse) wastes a debugging session that a one-line sync would have prevented.

## The Rule

When a PR changes how CI builds the app — toolchain versions, build flags, env vars, dependency install steps, code signing, prebuild/codegen steps — mirror that change in the Revyl build configuration **in the same PR**. Not a follow-up.

## The Sync-Table Pattern

Keep a small table in the repo (near the CI config or in the repo's agent docs) mapping each CI build step to its Revyl counterpart, for example:

| CI step | Revyl counterpart |
|---|---|
| pinned Node setup | the selected `build.profiles.<profile>.ios` recipe env / toolchain |
| `pod install --repo-update` | same step in the Revyl build command |
| `EXPO_PUBLIC_*` env vars | `.revyl/config.yaml` build env / launch vars |

The table's contents are repo-specific and live in the customer repo — this skill only prescribes the pattern: every row that changes on one side changes on both sides in the same PR.

## Debugging Drift

When a build behaves differently on Revyl vs CI:

1. Diff the two build commands and their environments first (the sync table makes this a 30-second check).
2. Compare toolchain versions actually used (logs on both sides), not the ones assumed.
3. Fix by re-syncing the configs, then update the sync table so the drift class cannot silently recur.
