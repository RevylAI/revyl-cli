# Unified Revyl plugin for ChatGPT and Codex

This package definition combines the existing pinned-CLI workflows with the
connected Atlas and device-session workspace. The archive contains four
skills: `revyl-cloud-app`, `revyl-codex-dev-loop`, `revyl-codex-proof-ci`, and
`revyl-workspace`. Users install one package; source work runs through the CLI
in the host's cloud coding workspace, while connected MCP tools provide
embedded views and explicitly requested service operations.
Its technical package name is `revyl-workspace`, distinct from the existing
`revyl` package, so it can become a new submission rather than an attempted
MCP upgrade of the skills-only listing. Its display name remains Revyl.

The packager reuses the launchers, runtime pin, branding, references, and two CLI
skills from `revyl-cli/plugins/revyl/`. It adds this directory's portable
manifest, cloud-app skill, and workspace skill. The existing CLI-only marketplace package stays
usable without MCP, and its public listing is not updated by building a ZIP.
Do not install both variants for the same workflow: their CLI skill names are
intentionally preserved.

## Cloud workflow and execution boundary

`revyl-cloud-app` guides source creation or editing, project setup,
`revyl build --remote`, verification and persisted tests against the returned
artifact, and `revyl explore run/status/cancel` to populate Atlas. It uses the
complete pinned CLI through the existing launcher; commands are discovered
from that binary's help and schema, not mirrored into an arbitrary-command MCP
tool. Existing dev-loop and exact-SHA CI-proof workflows remain separate.

This requires a shell-capable ChatGPT Work/Codex cloud workspace with writable
source, outbound network access, and image inspection. Installing the package
does not provision that workspace, connect a repository, or authorize paid
work. Compilation and devices run on Revyl; source generation and CLI
invocation run in the host workspace. Require explicit target and spending
scope, correlate every result to its job/build/session/run ID, and clean up
only owned resources. Full CLI availability does not grant administrative,
destructive, publishing, or secret-management permission.

CLI operations can proceed without the embedded MCP viewer when separately
authenticated. Embedding still needs a running reachable service and the
matching browser identity. A merge, archive upload, and private install do not
deploy those services or publish a directory listing. Before claiming the
complete cloud experience works, verify it in the actual host with an approved
fixture, not merely through local help or archive tests.

## Build an archive

The combined package uses a PNG rendering of the existing Revyl logo for its
listing and composer. Its source remains
`revyl-cli/cursor-plugin/assets/icon.svg`; regenerate the tracked PNG with
`make -C revyl-cli prepare-openai-plugin-icon` after a branding change. This
generation step requires librsvg's `rsvg-convert`; packaging and tests use the
prepared asset without requiring an image renderer.

Run the generator from `revyl-cli/` after validating the generated CLI assets:

```sh
make check-codex-plugin
go run ./cmd/package-openai-plugin \
  --registered-app-id "$REVYL_OPENAI_APP_ID" \
  --out ./build/revyl-private.zip
```

The registered app ID selects an existing private MCP connection and is supplied
at packaging time, not stored in the repository. That archive's `.app.json`
connects the combined package to the registered server. It does not convert a
private tunnel into a public-review endpoint.
Private archives omit public-review metadata: OpenAI's review importer requires
a declared MCP server for those cases, not an existing-app mapping. The test
plan stays in the maintained manifest and is included in public-mode archives.
When uploading a new version of a private plugin created by ChatGPT, inspect its
downloaded manifest and pass that exact technical name with `--package-name`.
OpenAI requires the name to match the existing package. This override is only
available with `--registered-app-id`; public packages retain `revyl-workspace`.

For a new public submission, assemble the archive with the actual permanent MCP
endpoint instead:

```sh
go run ./cmd/package-openai-plugin \
  --mcp-url "$REVYL_MCP_URL" \
  --out ./build/revyl-submission.zip
```

This mode emits one Streamable HTTP connection in `mcp.json`, without a private
registered-app mapping. Set `REVYL_MCP_URL` to the real deployed HTTPS endpoint;
never substitute a temporary tunnel, an example URL, or the frontend URL. The
generator validates package structure, not endpoint availability or OAuth.
Use a fresh output path; the generator does not overwrite an existing archive.

For client installation and account connection, use the authoritative
[MCP setup guide](https://docs.revyl.com/cli/mcp-setup). For OpenAI packaging and
submission, follow [Package your plugin](https://developers.openai.com/plugins/build/plugins)
and [Submit your plugin](https://developers.openai.com/plugins/deploy/submission).

## Public review gates

ZIP generation is not proof of submission readiness. Before submitting:

1. Deploy the permanent HTTPS MCP service and embedded frontend. A private
   tunnel, local process, or temporary browser authorization is not a public
   service. Include the MCP connection in the initial submission package;
   OpenAI does not support adding one to an existing skills-only listing.
2. Authenticate each connected user through the supported OAuth flow. Authorize
   every app and session against that user's organization. Do not share the
   developer's credential, device-session indexes, or mutable MCP state between
   users. A CLI session can be displayed through MCP only when both identities
   are authorized for that same session.
3. Expose focused tools with accurate annotations and bounded operations. Review
   the deployed catalogue rather than assuming a broad development profile is
   suitable for publication. Browsing must not provision devices, and retries
   must not duplicate session creation.
4. Use narrowly allowed embed origins and test the declared CSP under normal
   enforcement. Verify browser sign-in, expiry, account isolation, mobile
   layouts, WebRTC reconnects, and cleanup. Exclude development tools and
   unrelated admin or purchase flows from the submitted UI.
5. Run all five positive and three negative cases in `plugin.json` on the
   release deployment. The supplied cases are a test plan, not recorded passes.
   Provide a dedicated reviewer account with sample data, a working fixture
   repository/build, and a full-SHA CI artifact where the case requires one.
6. Add an accessible demonstration recording to the review metadata and enter
   reviewer credentials only in the portal's private fields. Confirm publisher
   verification, endpoint-domain verification, support contact access, and the
   privacy disclosures. Do not bundle credentials or private pilot evidence.

Keep the existing public listing available until the combined replacement has
passed review and its migration is communicated. Packaging does not publish,
retire, rename, or transfer either listing.

## Outcomes and validation

The intended outcome is one installation that reaches useful Atlas or device
evidence without a duplicate session or unnecessary browser handoff. Expect
successful in-host view opens to increase and time to usable evidence to fall;
no baseline is asserted. Guardrails are authorization failures, cross-account
access, incorrect success reports, duplicate provisioning, and device minutes
per verified result.

For the cloud workflow, the customer outcome is a verified, mapped app without
a local mobile toolchain; the company outcome is successful use of remote
builds, tests, and exploration. The primary metric is completion of an
authorized build-to-proof-to-map attempt, expected to increase; guardrails are
failed builds, false success reports, leaked sessions, and device/build spend
per successful attempt. No baseline or target is asserted.

Existing `cli_command_started` and `cli_command_completed` events cover CLI
invocations; persisted build, test, session, and exploration records provide
their authoritative outcomes. Correlate the IDs in the private verification
record to validate this loop. These facts support operation-level outcomes,
not a plugin-specific automated conversion funnel.

The reused CLI lifecycle events and frontend view events remain unchanged.
They do not identify a ChatGPT-specific funnel by themselves; add typed host
attribution and confirmed view/session outcomes before measuring public
adoption. Package tests are structural and do not establish those product
outcomes, account isolation, or a successful device run.
