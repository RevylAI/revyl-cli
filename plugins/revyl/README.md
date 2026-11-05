# Revyl plugin for Codex and Claude Code

This local plugin gives Codex and Claude Code the same pinned-launcher skills
for an explicitly authorized Revyl CLI development loop (`revyl-dev-loop`) and
CI-uploaded-build proof (`revyl-proof-ci`). One folder serves both hosts: one
set of skills, one checksum-pinned launcher, and a manifest for each host. It
has no hooks and does not start an MCP server.

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
Claude Code plugin is named `revyl-cli` because Revyl's hosted plugin in
Anthropic's directory, `revyl-cli/plugins/revyl-claude/`, is already named
`revyl`; the distinct name keeps their install ids and skill prefixes apart.

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

The pinned CLI's build listing searches only the newest 20 versions. CI proof
is limited to an exact-SHA match in that window; older builds must be reported
as an incomplete lookup, not as absent or successfully proved. Full-history
lookup requires a published CLI with paginated build listing.

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

1. Install from the local marketplace, confirm both skills load, and complete
   normal Revyl sign-in on first use without adding a CLI to the user `PATH`.
2. Change mobile app code and confirm the agent offers a device check before
   calling the work done, and starts nothing until the user agrees.
3. Run an authorized iOS fixture change, share the viewer immediately, inspect
   its screenshot, return the validation result, and stop the owned loop.
4. Run an authorized Android fixture change with the same artifact, evidence,
   and cleanup guarantees.
5. Rebuild a native change and verify the newly installed artifact, not an old
   seed build or merely a ready device session.
6. Match a CI-uploaded artifact to the exact full PR SHA, verify it without
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
reference's auth-bypass skill is not bundled and must never be installed or
invoked automatically.
