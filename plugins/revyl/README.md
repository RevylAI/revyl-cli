# Revyl Codex Plugin

This local Codex plugin provides pinned-launcher skills for an explicitly
authorized Revyl CLI development loop and CI-uploaded-build proof. It has no
hooks and does not start an MCP server.

The canonical user setup, including adding this exported `revyl-cli/` folder as
a local Codex marketplace and installing the `revyl` plugin, is
[Revyl's IDE and MCP setup guide](https://docs.revyl.ai/cli/mcp-setup). This
repository has not been published as a public Codex marketplace listing.

## Maintainers

The plugin's launcher implementation and copied references are generated from
shared sources. Its maintained `runtime-version` pins a Codex-specific published
CLI release; it does not reuse the current Cursor plugin pin. After changing
those sources or the Codex pin, run `make -C revyl-cli sync-codex-plugin`; use
the corresponding check target before release. Sync resolves checksums against
the published release assets and needs network access; the structural tests are
hermetic once generated files are present, including the CI drift check. CI
enforces the Codex package version independently from Cursor and the CLI.
The generated scripts support POSIX and Windows launchers, but native Windows
runtime behavior has not yet been verified.

Generated and structural checks validate the plugin package. They do not run a
build, provision a device, or prove a live app; those are separate gates that
require an explicitly authorized app, environment, and target.

The pinned CLI's build listing searches only the newest 20 versions. CI proof
in this prototype is limited to an exact-SHA match in that window; older builds
must be reported as an incomplete lookup, not as absent or successfully proved.
Full-history lookup requires a published CLI with paginated build listing.

The primary adoption metric is the share of first-time Codex verification
attempts that reach a confirmed device-backed result, expected to increase as
setup friction falls. Track authentication failures, false success, leaked
sessions, and device minutes per result as guardrails; no baseline is asserted.
The plugin adds no installation telemetry. Existing `cli_command_started`,
`cli_command_completed`, and `cli_command_failed` events carry Codex attribution
and record the commands from development start through validation and cleanup.
Correlate these with server-confirmed outcomes during the pilot; a completed
command alone does not establish that an assertion passed.

## Manual review

Source and installation validation has no model-authenticated session or live
device coverage. Before release, verify these cases manually:

1. Install in a clean Codex profile, confirm both skills load, and complete
   normal Revyl sign-in on first use without adding a CLI to the user `PATH`.
2. Run an authorized iOS fixture change, share the viewer immediately, inspect
   its screenshot, return the validation result, and stop the owned loop.
3. Run an authorized Android fixture change with the same artifact, evidence,
   and cleanup guarantees.
4. Rebuild a native change and verify the newly installed artifact, not an old
   seed build or merely a ready device session.
5. Match a CI-uploaded artifact to the exact full PR SHA, verify it without
   rebuilding, and return evidence or an authorized PR comment.

Also verify these refusals:

1. Missing authorization or invalid credentials results in clarification or
   sign-in, not a build, device, or disclosure of protected data.
2. An ambiguous app or mismatched CI build does not start proof or trigger a
   fallback build; resolve full SHAs through read-only PR context when possible.
3. A false or missing semantic verdict is reported as failed verification, even
   when the device operation completed successfully, and cleanup still runs.

Also check interruption, upgrade/uninstall, hooks disabled, denied network
permissions, no image-viewing capability, and evidence publication without
approval. The reference's auth-bypass skill is not bundled and must never be
installed or invoked automatically.
