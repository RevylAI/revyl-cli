# Revyl for Cursor

Source for the Revyl Cursor Marketplace plugin: pinned CLI install hooks,
skills, and a routing rule.

For user-facing plugin information (what it adds, install and configure,
verify), see the [Revyl Documentation](https://docs.revyl.com/).
Operator procedure in the monorepo: `docs/runbooks/cursor-plugin-release.md`.
This file is the maintainer-facing source of truth for the release lifecycle
below; do not duplicate its instructions into published docs.

## Maintainer release lifecycle

This section is the source of truth for plugin version selection, release
states, Marketplace submission, and recovery. The
[plugin guide](https://docs.revyl.ai/integrations/cursor/plugin) remains the
user-facing installation source.

### Release states

- **Development:** `runtime-manifest.json` may have `prepared: false` and empty
  checksums. Development commits are not publishable.
- **Candidate:** all three release documents agree, the runtime pin
  is immutable and downloadable, and the exact plugin artifact has passed the
  release gates below.
- **Submitted:** the prepared public repository revision has been submitted
  for initial review or requested for re-indexing after an update.
- **Published:** Cursor has approved the submitted revision. Complete the
  post-publish checks before marking the release complete.
- **Superseded:** a newer corrective or feature release is published. Do not
  assume this removes older installations.

### Choose the release type

The plugin version and CLI runtime version are independent semantic versions.
Install hooks download the pin in `runtime-manifest.json`, not
`revyl-cli/VERSION` on the branch. Operator command sequences, including when
`VERSION` is unpublished and the pin must stay on an existing GitHub Release,
live in `docs/runbooks/cursor-plugin-release.md`.

- **Plugin-only release:** choose a higher plugin version and retain an
  already published runtime. Use this for changes limited to plugin
  skills, rules, hooks, assets, or documentation.
- **Runtime-coupled release:** publish the CLI runtime first, then pin that
  runtime. The CLI GitHub Release must contain `checksums.txt` and these six
  assets: `revyl-darwin-amd64`, `revyl-darwin-arm64`, `revyl-linux-amd64`,
  `revyl-linux-arm64`, `revyl-windows-amd64.exe`, and
  `revyl-windows-arm64.exe`.
- **API-coupled release:** production backend and frontend must serve any new
  endpoints the CLI will call before the CLI GitHub Release exists. Then pin as
  a runtime-coupled release.

Never replace assets under an existing runtime tag; publish a new runtime
version instead.

### Prepare the candidate

From `revyl-cli/`:

```bash
./scripts/bump patch --plugin
```

That is the same as `make cursor-plugin-bump-patch`. GNU Make cannot take `--plugin`.
The command patch-bumps the plugin version (`0.1.1` → `0.1.2`), pins
`RUNTIME_VERSION` from `revyl-cli/VERSION` unless you override it, syncs copied
plugin skills, then verifies in the same invocation. It does not touch CLI
`VERSION`. If `VERSION` has no published GitHub Release yet, pass
`RUNTIME_VERSION=<published>` so the pin stays on an existing tag. Plugin semver
and CLI runtime stay independent.

```bash
./scripts/bump minor --plugin
./scripts/bump major --plugin
```

`cursor-plugin-bump-patch` / `cursor-plugin-bump-minor` / `cursor-plugin-bump-major`
remain aliases. Overrides still work: `PLUGIN_VERSION=… RUNTIME_VERSION=… make cursor-plugin-release`.
`cursor-plugin-release` is the implementation target. `CHECK=1` is
verify-only and must not write.

Preparation requires network access to the runtime GitHub Release. It
atomically updates release metadata in these documents; do not hand-edit their
version or checksum fields:

- `revyl-cli/cursor-plugin/.cursor-plugin/plugin.json`
- `revyl-cli/.cursor-plugin/marketplace.json`
- `revyl-cli/cursor-plugin/runtime-manifest.json`

Confirm the plugin version matches in all three documents,
`runtime-manifest.json` has `prepared: true`, and all six SHA-256 fields are
populated.

Marketplace re-index is a later, approval-gated step. It is not part of
prepare.

Plugin-owned changes (hooks, skills, rules, assets, generator manifests) must
increase `plugin.json` version. CI fails the PR otherwise. Run
`./scripts/bump patch --plugin`, or add the `no-plugin-release` label when the
PR must not cut a pin.

### Validate the exact candidate

Run from the monorepo root:

```bash
make dogfood-cursor-plugin-check
git diff --check
make dogfood-cursor-plugin
```

Run **Developer: Reload Window**, then validate the isolated copy rather than
the linked development install. Confirm the plugin exposes its skills, routing
rule, and two hooks, and that the first `revyl` command can download the pin.

Use disposable Cursor profiles to cover clean installation, missing and
expired authentication — including that `revyl auth login` prints an
approval URL and short code, and never the device code behind them — missing
or ambiguous project setup, invalid configuration, upgrade from the previous
plugin version, and uninstall. Confirm stale plugin files are absent after
upgrade and uninstall does not delete Revyl credentials. Device-backed and
Cursor Cloud checks require explicit approval for their target environments
and must stop every session they create.

### Submit or update the listing

Confirm the prepared candidate is present in the public
[`RevylAI/revyl-cli`](https://github.com/RevylAI/revyl-cli) repository before
requesting review.

- For the first release, submit the public repository at
  [Cursor Marketplace Publish](https://cursor.com/marketplace/publish).
- For a later release, push the prepared update and request that Cursor
  re-index the existing listing.

Repository pushes do not publish automatically. Cursor manually reviews every
initial submission and update before publication. Submission, re-indexing,
delisting, and security escalation are external side effects and require
explicit approval for the public target.

### Verify after publication

In clean disposable profiles:

1. install at user and workspace scope;
2. confirm the published components match the candidate and that hooks can
   install the pinned CLI;
3. run one approved device-backed smoke and record the viewer, screenshot,
   semantic result, build state, and final cleanup;
4. upgrade from the previous published version and confirm stale files are
   absent; and
5. uninstall and confirm plugin components disappear while credentials remain.

Record the plugin version, runtime version, public repository revision, review
state, and credential-free evidence for each release.

### Recovery

Before publication, correct a bad version or runtime selection by rerunning
`./scripts/bump patch --plugin` (or `make cursor-plugin-release` with explicit
versions), then repeat every candidate gate. A corrupt cached
runtime repairs itself on the next online start after checksum verification.

After publication, treat recovery as a new corrective release:

1. stop promotion and identify the last known-good source;
2. apply or revert the source change;
3. choose a higher plugin version and, when needed, publish a new immutable
   runtime version;
4. regenerate and validate the candidate; and
5. request another reviewed re-index.

Cursor does not document a publisher rollback API, expedited review, or
recovery SLA. Delisting blocks new installations but may not remove existing
ones. For a security incident, use Cursor's official
[Marketplace security](https://cursor.com/help/security-and-privacy/marketplace-security)
and [publisher terms](https://cursor.com/marketplace-publisher-terms)
procedures instead of relying on a corrective release alone.

[Revyl docs](https://docs.revyl.ai) •
[Plugin setup](https://docs.revyl.ai/integrations/cursor/plugin) •
[MCP setup](https://docs.revyl.ai/cli/mcp-setup) •
[Cursor plugin docs](https://cursor.com/docs/plugins)
