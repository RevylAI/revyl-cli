# Revyl for Cursor

Run, verify, and test mobile apps on real cloud iOS and Android devices from
Cursor. The plugin will bundle Revyl's focused MCP tools, development skills,
routing rule, and a pinned Revyl runtime bootstrap.

> **Coming soon:** the official Revyl plugin is not yet available in Cursor
> Marketplace. Use the current CLI-based Cursor MCP setup in the
> [MCP setup guide](https://docs.revyl.ai/cli/mcp-setup).

## What the plugin adds

- **MCP server**: 11 focused setup, dev-loop, and device tools with streamed
  progress, screenshots, grounding, and structured outcomes.
- **Viewer handoff**: the inline device app offers **Open live device** through
  the MCP Apps host when supported, with the returned HTTPS viewer link as the
  portable fallback.
- **Skills**: `revyl-cloud-agent`, `revyl-mcp-dev-loop`, and `revyl-ci-sync`.
- **Rule**: routes mobile run, preview, and verification requests to Revyl.
- **Runtime bootstrap**: downloads and pins the published Revyl CLI runtime
  declared in `runtime-manifest.json` when the plugin starts MCP.

## Install and configure

Until the Marketplace release, follow the canonical
**[MCP setup guide](https://docs.revyl.ai/cli/mcp-setup)** to install the Revyl
CLI, connect Cursor over MCP, authenticate, and configure Cloud Agent secrets.
The same guide will become the source of truth for Marketplace installation
after launch.

## Runtime bootstrap

Each published plugin release pins one immutable Revyl CLI GitHub Release and
the SHA-256 checksum for every supported OS and architecture. On MCP startup,
the launcher selects the matching asset, reuses it only when its checksum
matches, or downloads it to a temporary file and installs it atomically after
verification. A corrupt cache entry is repaired on the next online start, and
a new runtime pin uses a separate versioned cache directory.

The first start for a new runtime requires network access to GitHub Releases.
Bootstrap failures are written only to standard error so MCP output remains
valid. Developers may select an existing executable with `REVYL_BINARY`;
normal Marketplace users do not need a separate CLI installation.

## Verify

After installing the plugin or completing the current CLI-based MCP setup,
start a new Cursor Agent chat and ask:
**"Run this app on a Revyl device."** The agent should return the live viewer
and device-backed evidence.

## Maintainer release lifecycle

This section is the source of truth for plugin version selection, release
states, Marketplace submission, and recovery. The
[MCP setup guide](https://docs.revyl.ai/cli/mcp-setup) remains the
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

The plugin version and CLI runtime version are independent semantic versions:

- **Plugin-only release:** choose a higher `PLUGIN_VERSION` and retain an
  already published `RUNTIME_VERSION`. Use this for changes limited to plugin
  skills, rules, hooks, MCP configuration, assets, or documentation.
- **Runtime-coupled release:** publish the CLI runtime first, then choose the
  new runtime and a higher plugin version. The CLI GitHub Release must contain
  `checksums.txt` and these six assets:
  `revyl-darwin-amd64`, `revyl-darwin-arm64`, `revyl-linux-amd64`,
  `revyl-linux-arm64`, `revyl-windows-amd64.exe`, and
  `revyl-windows-arm64.exe`.

The first Marketplace release follows the runtime-coupled path. Never replace
assets under an existing runtime tag; publish a new runtime version instead.

### Prepare the candidate

If the source `revyl-mcp-dev-loop` skill changed, regenerate its plugin copy
first:

```bash
make -C revyl-cli sync-cursor-plugin-skills
```

After the selected runtime release is available, generate and verify the
candidate:

```bash
make -C revyl-cli prepare-cursor-plugin-release \
  PLUGIN_VERSION=<plugin-version> \
  RUNTIME_VERSION=<runtime-version>
make -C revyl-cli prepare-cursor-plugin-release \
  PLUGIN_VERSION=<plugin-version> \
  RUNTIME_VERSION=<runtime-version> \
  CHECK=1
```

Preparation requires network access to the runtime GitHub Release. It
atomically updates release metadata in these documents; do not hand-edit their
version or checksum fields:

- `revyl-cli/cursor-plugin/.cursor-plugin/plugin.json`
- `revyl-cli/.cursor-plugin/marketplace.json`
- `revyl-cli/cursor-plugin/runtime-manifest.json`

Confirm the plugin version matches in all three documents,
`runtime-manifest.json` has `prepared: true`, and all six SHA-256 fields are
populated.

### Validate the exact candidate

Run from the monorepo root:

```bash
make dogfood-cursor-plugin-check
git diff --check
make dogfood-cursor-plugin
```

Run **Developer: Reload Window**, then validate the isolated copy rather than
the linked development install. Confirm the plugin exposes its three skills,
one routing rule, two hooks, and the focused eleven-tool MCP profile.

Use disposable Cursor profiles to cover clean installation, missing and
expired authentication, missing or ambiguous project setup, invalid
configuration, upgrade from the previous plugin version, and uninstall.
Confirm stale plugin files are absent after upgrade and uninstall does not
delete Revyl credentials. Device-backed and Cursor Cloud checks require
explicit approval for their target environments and must stop every session
they create.

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
2. confirm the published components and eleven tools match the candidate;
3. run one approved device-backed smoke and record the viewer, screenshot,
   semantic result, build state, and final cleanup;
4. upgrade from the previous published version and confirm stale files are
   absent; and
5. uninstall and confirm plugin components disappear while credentials remain.

Record the plugin version, runtime version, public repository revision, review
state, and credential-free evidence for each release.

### Recovery

Before publication, correct a bad version or runtime selection by rerunning the
generator, then repeat `CHECK=1` and every candidate gate. A corrupt cached
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
[MCP setup](https://docs.revyl.ai/cli/mcp-setup) •
[Cursor plugin docs](https://cursor.com/docs/plugins)
