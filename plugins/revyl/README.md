# Revyl plugin for Codex and Claude Code

This local plugin gives Codex and Claude Code the same pinned-launcher skills
for an explicitly authorized Revyl CLI development loop (`revyl-dev-loop`),
CI-uploaded-build proof (`revyl-proof-ci`), Revyl builds and CI, seven explicit
commands, a device verifier subagent for Claude Code, Revyl's hosted workspace
MCP server, a read-only session-start check, and an opt-in offer to check each
commit. One folder serves both hosts: one
set of skills, one checksum-pinned launcher, one `.mcp.json`, one
`hooks/hooks.json`, and a manifest for each host.

After an agent changes mobile app code, `revyl-dev-loop` has it offer to check
the change on a Revyl cloud device before calling the work done, instead of
leaving problems to the pull request's proof of changes. Nothing that uses
device or build time starts without the user's go-ahead.

| Host        | Manifest                     | Plugin name | Marketplace                         |
| ----------- | ---------------------------- | ----------- | ----------------------------------- |
| Codex       | `.codex-plugin/plugin.json`  | `revyl`     | `.agents/plugins/marketplace.json`  |
| Claude Code | `.claude-plugin/plugin.json` | `revyl-cli` | `.claude-plugin/marketplace.json`   |

Both marketplaces are named `revyl` and live at the root of the exported
`revyl-cli/` folder, which is the public `RevylAI/revyl-cli` repository. The
Claude Code plugin is named `revyl-cli` because Revyl's hosted-MCP Claude
plugin, `revyl-cli/plugins/revyl-claude/`, is already named `revyl`; the
distinct name keeps their install ids and skill prefixes apart.

Install steps for both hosts are in
[Revyl's IDE and MCP setup guide](https://docs.revyl.com/agents/mcp). Neither
host lists this plugin in a public directory.

The combined ChatGPT/Codex distribution reuses these skills and runtime assets
through `revyl-cli/plugins/revyl-openai/README.md`. Its generator adds the
workspace skill and one explicitly selected MCP connection without making the
CLI-only marketplace installation depend on MCP.

## Host behavior

The skills are host-neutral. Where Codex and Claude Code differ, each skill
states the rule for both hosts in the same file:

- **Skill directory**: Claude Code substitutes `${CLAUDE_SKILL_DIR}` into the
  skill when it loads; Codex lists the `SKILL.md` path. Both resolve the same
  `../../scripts/launch-revyl` from there.
- **Links**: neither host opens a browser. Claude Code prints full URLs on
  their own lines; Codex gives Markdown links.

## Commands

Commands are skills that only the user can start: each sets
`disable-model-invocation: true` for Claude Code and ships
`agents/openai.yaml` with `allow_implicit_invocation: false` for Codex. Claude
Code prefixes them with the plugin name; Codex runs them as `$name`.

| Claude Code                | Codex           | Spends                     | Does                                                                                     |
| -------------------------- | --------------- | -------------------------- | ---------------------------------------------------------------------------------------- |
| `/revyl-cli:revyl-test`    | `$revyl-test`   | device time, after one yes | Build-first check of the current change: an exact-SHA preview or CI build, else a build |
| `/revyl-cli:revyl-device`  | `$revyl-device` | device time, after one yes | Start one session on an app build and return the viewer link                              |
| `/revyl-cli:revyl-build`   | `$revyl-build`  | build minutes, after one yes | Cloud build, local build, or artifact upload; returns the build ID                     |
| `/revyl-cli:revyl-proof`   | `$revyl-proof`  | device time, after one yes | Prove a pull request on the build of its exact head commit, without rebuilding           |
| `/revyl-cli:revyl-status`  | `$revyl-status` | nothing                    | CLI, sign-in, project, GitHub, hosted tools, and running sessions                         |
| `/revyl-cli:revyl-stop`    | `$revyl-stop`   | nothing                    | Stop only what this conversation started                                                  |
| `/revyl-cli:revyl-setup`   | `$revyl-setup`  | nothing                    | Sign-in, project configuration, apps, GitHub connection and pull-request builds          |

`revyl-test` looks for a build of the exact commit before spending anything:
the Revyl GitHub App's pull-request preview build (through the hosted
`list_builds` and `get_build` tools), then a CI upload (`build list`). Only
when none exists does it ask to build. In Claude Code it then hands the device
work to `agents/revyl-verifier.md`, a plugin subagent with shell, read, and
hosted Revyl tools that refuses any task whose prompt doesn't start with an
`Authorized:` line. Codex has no plugin agents, so the skill asks Codex to
spawn one subagent with the same prompt.

## Build, CI, and GitHub skills

These skills are model-invocable: the agent uses them when a task calls for
them. None of them builds, uploads, or starts a device, and `revyl-github`
connects GitHub or publishes configuration only after the user's yes.

| Skill                    | Use                                                                                                  |
| ------------------------ | ---------------------------------------------------------------------------------------------------- |
| `revyl-build-config`     | `.revyl/config.yaml` build profiles, with recipes for native iOS and Android, Expo, React Native, Flutter, and web |
| `revyl-build-ci-upload`  | An upload step for the CI job that already builds the app, tied to the exact commit                  |
| `revyl-find-build`       | The build of a commit, branch, or pull request, and the build a run would use                        |
| `revyl-proof-automation` | Pull-request proof by the Revyl GitHub App, on its own builds or CI uploads, or by an agent in GitHub Actions |
| `revyl-ci-sync`          | Keep the Revyl build recipe in step with the CI build                                                |
| `revyl-headless`         | Rules for unattended runs: Claude Code on the web, `claude -p`, `codex exec`, and CI                 |
| `revyl-github`           | GitHub App status and connection, `pr_review` builds, triggers, and proof, preview builds, `@revyl` comments |

`revyl-ci-sync` and the explicit-only `revyl-cli-auth-bypass` are generated:
`make -C revyl-cli sync-codex-plugin` copies them from
`cursor-plugin/skills/revyl-ci-sync/` and `skills/revyl-cli-auth-bypass/` and
routes their shell commands through the bundled launcher. Edit the sources,
not the copies.

## Offer to check each commit

`hooks/revyl-offer-proof` runs after each shell command and is off by default.
Claude Code users turn it on with the plugin's `offer_proof_after_commit`
option (`/config` or `claude plugin configure revyl-cli@revyl`); Codex users set
`REVYL_PLUGIN_OFFER_PROOF=1`. When on, a `git commit`, `git push`, or
`gh pr create` whose commit touches a directory with a `.revyl/config.yaml` adds
one line of context asking the agent to offer `revyl-test` or `revyl-proof`,
once per commit. It never runs the CLI or starts anything.

## Hosted MCP server and session-start check

Both hosts load the root `.mcp.json`, which names only Revyl's hosted
`revyl-workspace` server at `https://backend.revyl.ai/api/v1/mcp`. Its tools
find builds, show and act on device sessions, take screenshots, and read Atlas.
New hosted tools appear when the backend deploys them, without a plugin
release, so the skills check which tools exist and fall back to the CLI. The
server signs in with OAuth, separately from the CLI: in Claude Code through
`/mcp` (it is listed as `plugin:revyl-cli:revyl-workspace`), in Codex with
`codex mcp login revyl-workspace`. Headless runs such as `claude -p` and
`codex exec` cannot complete OAuth and keep using the CLI. Claude Code matches
a connector on the same URL by endpoint, so the hosted Claude plugin and this
one do not duplicate tools.

Both hosts also load `hooks/hooks.json`. Its `SessionStart` hook runs
`hooks/revyl-session-check`, a POSIX script that prints up to three lines of
context and always exits 0:

- whether the pinned CLI is ready, signed in, and finds a
  `.revyl/config.yaml`, from `doctor --json`. On the first session, before
  the CLI is cached, it installs the pinned, checksum-verified CLI in the
  background through `version` and waits up to four seconds; if the install
  finishes, it reports the usual state, and otherwise it says the CLI is still
  installing and the next session reports Revyl's state;
- for a signed-in project in a GitHub repository, whether the Revyl GitHub App
  is connected, the repository is granted, and pull-request builds are on, from
  `github status --json` cached for a day per directory. When they aren't,
  it suggests offering to set them up at most once a week per directory, and
  never issues an install link or changes configuration;
- whether a newer plugin release is published, read from the public
  repository's manifest at most once a day with a two-second timeout.

Its CLI calls run with `REVYL_TELEMETRY_DISABLED=1`, so starting a session
doesn't count as CLI usage, and every call except that first-session install
runs with `REVYL_RUNTIME_NO_DOWNLOAD=1`. The first session skips the
plugin-update check to stay within the hook's time limit. Caches live in `CLAUDE_PLUGIN_DATA` when the
host sets it, otherwise under `~/.cache/revyl/plugin-hooks`. Codex runs a
plugin hook only after the user trusts it in `/hooks`, and again after any
change to the script. The OpenAI directory package doesn't include the MCP
configuration or hooks.

`scripts/launch-revyl` sets `REVYL_PLUGIN_HOST` from the host's environment:
`codex` when `CODEX_THREAD_ID`, `CODEX_SHELL`, or `CODEX_CI` is set,
otherwise `claude` when `CLAUDECODE` is set, otherwise `codex`. That order
matches the CLI's own agent detection, so the plugin cache and the `agent`
property on `cli_command_*` events name the same host. The shared runtime
rejects any other host value. Each host keeps its own runtime cache under
`~/.cache/revyl/<host>-plugin/`, and neither stamps `REVYL_CLIENT_SOURCE`,
which the CLI reserves for Cursor's device-login page.

## Maintainers

The plugin's launcher implementation, copied references, and Claude Code
manifest are generated. The Claude Code manifest is derived from the Codex
manifest, so the two always share version, description, and skills; CI fails
when they drift. Its maintained `runtime-version` pins a published CLI release
independently from the Cursor plugin. After changing the Codex manifest, the
shared Cursor launcher sources, or the pin, run
`make -C revyl-cli sync-codex-plugin`; use the corresponding check target
before release. Sync resolves checksums against the published release assets
and needs network access; the structural tests are hermetic once generated
files are present, including the CI drift check. The release workflow manages
this package's version independently from Cursor and the CLI: changes merged
to staging receive an automatic patch bump before publication. To choose a
minor or major release instead, increase `.codex-plugin/plugin.json` and run
the sync target in the same change; an explicit increase is preserved without
an extra patch. Add the PR label `skip version` (also `skip-version` or
`no-plugin-release`), or `[skip version]` in its title, to skip the automatic
bump without preventing publication. README and test-only changes do not
trigger a bump. The generated scripts support
POSIX and Windows launchers, but native Windows runtime behavior has not yet
been verified.

Generated and structural checks validate the plugin package. They do not run a
build, provision a device, or prove a live app; those are separate gates that
require an explicitly authorized app, environment, and target.

The primary adoption metric is the share of mobile code changes that reach a
confirmed device-backed check before the pull request, expected to increase as
agents offer the dev loop in both hosts. Track authentication failures, false
success, leaked sessions, and device minutes per result as guardrails; no
baseline is asserted. The plugin adds no installation telemetry. Existing
`cli_command_started`, `cli_command_completed`, and `cli_command_failed`
events carry the host in their `agent` property (`codex` or `claude_code`)
and record the commands from development start through validation and cleanup.
Correlate these with server-confirmed outcomes; a completed command alone does
not establish that an assertion passed.

## Manual review

Source and installation validation has no model-authenticated session or live
device coverage. Before release, verify these cases manually in both a clean
Codex profile and a clean Claude Code configuration:

1. Install from the local marketplace, confirm both skills, the
   `revyl-workspace` MCP server, and the `SessionStart` hook load, and
   complete normal Revyl sign-in on first use without adding a CLI to the user
   `PATH`. Sign the hosted server in once per host and confirm a hosted tool
   can see a session the CLI started.
2. Start a fresh session in a signed-in project and confirm the readiness
   lines, including GitHub state, appear as context. In Codex, trust the hook
   in `/hooks` first.
3. Change mobile app code and confirm the agent offers a device check before
   calling the work done, and starts nothing until the user agrees.
4. Run an authorized iOS fixture change, share the viewer immediately, inspect
   its screenshot, return the validation result, and stop the owned loop.
5. Run an authorized Android fixture change with the same artifact, evidence,
   and cleanup guarantees.
6. Rebuild a native change and verify the newly installed artifact, not an old
   seed build or merely a ready device session.
7. Match a CI-uploaded artifact to the exact full PR SHA, verify it without
   rebuilding, and return evidence or an authorized PR comment.

Also verify these refusals:

1. Missing authorization or invalid credentials results in clarification or
   sign-in, not a build, device, or disclosure of protected data.
2. An ambiguous app or mismatched CI build does not start proof or trigger a
   fallback build; resolve full SHAs through read-only PR context when possible.
3. A false or missing semantic verdict is reported as failed verification, even
   when the device operation completed successfully, and cleanup still runs.

Also check interruption, upgrade/uninstall, denied network permissions, no
image-viewing capability, and evidence publication without approval. The
bundled `revyl-cli-auth-bypass` skill is explicit-only and must never be
invoked automatically.
