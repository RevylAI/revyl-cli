# Syncing Tests

Keep local YAML test files and the Revyl server in sync using a git-like workflow. Edit tests locally, push changes upstream, pull updates from teammates, and reconcile the full project in one command.

## How It Works

Tests live as YAML files in `.revyl/tests/`. Each file contains a `_meta` block that tracks the relationship with the remote copy on the Revyl server:

```yaml
_meta:
    remote_id: a1b2c3d4-5678-90ab-cdef-1234567890ab
    remote_version: 3
    local_version: 3
    last_synced_at: "2026-03-16T10:11:53-07:00"
    checksum: sha256:e3b0c44298fc1c149afbf4c8996fb924...
test:
    metadata:
        name: login-flow
        platform: ios
    build:
        name: ios-test
    blocks:
        - type: instructions
          step_description: Tap Sign In.
```

| Field | Purpose |
|-------|---------|
| `remote_id` | UUID linking this file to the server-side test |
| `remote_version` | Last known server version at time of sync |
| `local_version` | Local version counter |
| `last_synced_at` | Timestamp of the last push or pull |
| `checksum` | SHA-256 of the blocks content; used to detect local edits |

When you push, the CLI sends the local blocks to the server and increments the version. When you pull, the CLI downloads the remote blocks and updates the `_meta` accordingly. Conflicts are detected by comparing version numbers and checksums.

---

## Sync Statuses

Every test has a sync status derived from comparing local `_meta` against the remote API:

| Status | Meaning |
|--------|---------|
| `synced` | Local and remote are identical |
| `modified` | Local has unpushed changes (checksum differs from last sync) |
| `outdated` | Remote has a newer version than the last pull |
| `conflict` | Both local and remote have diverged since the last sync |
| `local-only` | Test exists only in `.revyl/tests/`, not on the server |
| `remote-only` | Test exists on the server but has no local YAML file |
| `orphaned` | The `_meta.remote_id` points to a test that no longer exists, is inaccessible, or has an invalid ID |

---

## `revyl sync` -- Full Reconciliation

`revyl sync` is the top-level command that reconciles the entire project in one pass. It covers three domains: **tests**, **app links**, and **hot reload mappings**.

```bash
revyl sync
```

The sync process for tests:

1. **Name-match linking** -- local files that match a remote test by name (but lack a `remote_id`) are offered for linking.
2. **Import** -- remote-only tests with no local counterpart are imported after confirmation (interactive) or skipped (non-interactive / `--skip-import`).
3. **Status reconciliation** -- each linked test is checked and the appropriate action is applied (pull, push, detach, prune).

### Flags

| Flag | Description |
|------|-------------|
| `--tests` | Only sync tests (skip app links and hot reload checks) |
| `--apps` | Only sync build platform app_id links |
| `--dry-run` | Preview planned actions without writing any files |
| `--prune` | Auto-prune stale/deleted mappings and orphaned links |
| `--skip-import` | Only sync tests already in `.revyl/tests/`; skip importing remote-only tests |
| `--workflow "Name"` | Only sync tests belonging to a specific workflow (by name or ID) |
| `--bootstrap` | Rebuild config mappings from local YAML `_meta.remote_id` values (useful after cloning a repo) |
| `--non-interactive` | Disable prompts; apply deterministic defaults |
| `--interactive` | Force interactive prompts (requires TTY stdin) |
| `--skip-hotreload-check` | Skip validating hot reload platform key mappings |
| `--json` | Output results as machine-readable JSON |

### Examples

```bash
revyl sync                          # Full reconciliation (tests + app links + hot reload)
revyl sync --tests                  # Sync tests only
revyl sync --tests --prune          # Sync tests and clean up stale mappings
revyl sync --workflow "Smoke"       # Only sync tests in the Smoke workflow
revyl sync --skip-import            # Only sync existing local tests
revyl sync --dry-run --json         # Preview as JSON (great for CI)
revyl sync --non-interactive        # No prompts; safe for scripts
revyl sync --bootstrap              # Rebuild mappings after cloning a repo
```

---

## `revyl test list` -- View Sync Status

Show all local tests with their sync status, local version, and remote version:

```bash
revyl test list
```

Example output:

```
NAME              STATUS     LOCAL  REMOTE  LAST SYNCED
login-flow        synced     3      3       2 hours ago
checkout-flow     modified   4      3       1 day ago
browse-products   outdated   2      5       3 days ago
signup-flow       local-only 1      -       never
```

### Flags

| Flag | Description |
|------|-------------|
| `--json` | Output results as JSON |

---

## `revyl test push` -- Push Local Changes

Upload local test changes to the Revyl server.

```bash
revyl test push                   # Push all modified tests
revyl test push login-flow        # Push a specific test
revyl test push --force           # Force overwrite remote version
revyl test push --dry-run         # Preview what would be pushed
```

When pushing, the CLI:

1. Validates the local YAML.
2. Resolves `build.name` to an `app_id` and any module/script names to UUIDs.
3. Sends the blocks to the server with an `expected_version` for optimistic concurrency.
4. If the remote version has advanced (HTTP 409 conflict), the push is rejected. Use `--force` to overwrite, or pull first and re-push.
5. On success, updates `_meta` with the new `remote_version`, `checksum`, and `last_synced_at`.

What gets synced: blocks, tags, custom variables, environment variables, and device targets.

### Flags

| Flag | Description |
|------|-------------|
| `--force` | Force overwrite the remote version (bypass conflict detection) |
| `--dry-run` | Show what would be pushed without pushing |

---

## `revyl test pull` -- Pull Remote Changes

Download test changes from the Revyl server to local YAML.

```bash
revyl test pull                   # Pull all tests in local config
revyl test pull login-flow        # Pull a specific test
revyl test pull --all             # Pull ALL org tests, including remote-only
revyl test pull --force           # Force overwrite local changes
revyl test pull --dry-run         # Preview what would be pulled
```

When pulling, the CLI:

1. Fetches the remote test and converts tasks to blocks.
2. Strips server-generated block IDs.
3. Resolves UUIDs back to human-readable names (modules, scripts).
4. Pulls associated tags, custom variables, environment variables, and device targets.
5. Writes the updated YAML with refreshed `_meta`.

Use `--all` to discover and import tests created by teammates in the web UI that don't exist locally yet.

### Flags

| Flag | Description |
|------|-------------|
| `--all` | Pull all tests from the organization, including those not in local config |
| `--force` | Force overwrite local changes |
| `--dry-run` | Show what would be pulled without pulling |

---

## `revyl test diff` -- Compare Local vs Remote

Show a unified diff between the local and remote versions of a test:

```bash
revyl test diff login-flow
```

Example output:

```diff
--- local: login-flow
+++ remote: login-flow
@@ -3,6 +3,7 @@
   - type: instructions
     step_description: Tap Sign In.
   - type: instructions
-    step_description: Type test@example.com in the email field.
+    step_description: Type "{{email}}" in the email field.
+  - type: validation
+    step_description: The dashboard is visible.
```

---

## `revyl test remote` -- List All Organization Tests

List every test in your Revyl organization, regardless of local project state:

```bash
revyl test remote                      # List all tests
revyl test remote --platform ios       # Filter by platform
revyl test remote --tag regression     # Filter by tag
revyl test remote --limit 20           # Limit results
revyl test remote --json               # JSON output
```

### Flags

| Flag | Description |
|------|-------------|
| `--platform <ios\|android>` | Filter by platform |
| `--tag <name>` | Filter by tag name |
| `--limit <n>` | Maximum number of tests to return (default: 50) |
| `--json` | Output results as JSON |

---

## Common Workflows

### First-time setup (clone and sync)

After cloning a repository that already has `.revyl/tests/` files:

```bash
revyl auth login
revyl sync --bootstrap              # Rebuild config mappings from _meta.remote_id values
revyl test list                     # Verify everything is linked
```

### Import all org tests locally

Pull every test from your org so the full suite is available locally:

```bash
revyl test pull --all
```

Or use the interactive flow:

```bash
revyl sync                          # Will prompt to import each remote-only test
```

### Edit-push-run loop

```bash
# 1. Edit a test locally
vim .revyl/tests/login-flow.yaml

# 2. Check what changed
revyl test diff login-flow

# 3. Push the update
revyl test push login-flow

# 4. Run it
revyl test run login-flow
```

### Pull a teammate's changes

```bash
revyl test list                     # See which tests are outdated
revyl test pull checkout-flow       # Pull one test
# or
revyl test pull                     # Pull all outdated tests
```

### Resolve a version conflict

When both you and a teammate have changed the same test:

```bash
revyl test list                     # Shows "conflict" status
revyl test diff login-flow          # See the remote changes
# Option A: Pull remote, then re-apply your edits
revyl test pull login-flow --force
# Option B: Force-push your version
revyl test push login-flow --force
```

### CI dry-run validation

Check sync status in CI without modifying anything:

```bash
revyl sync --dry-run --json --non-interactive
```

Parse the JSON output to gate deployments on sync health.

### Prune stale mappings

Clean up orphaned links and removed tests:

```bash
revyl sync --tests --prune
```

---

## Troubleshooting

| Problem | Fix |
|---------|-----|
| "version conflict" on push | Someone updated the test remotely. Run `revyl test pull` first, or use `--force` to overwrite. |
| Test shows as `orphaned` | The remote test was deleted or you lost access. Run `revyl sync --prune` to detach the link. |
| "test not found" on pull | The test name doesn't match any remote test. Run `revyl test remote` to see available tests. |
| Stale `_meta` after cloning | Run `revyl sync --bootstrap` to rebuild config mappings from local `_meta.remote_id` values. |
| Push rejected with invalid YAML | Run `revyl test validate .revyl/tests/<name>.yaml` to check syntax before pushing. |
| Sync imports too many tests | Use `--skip-import` to only sync tests already in `.revyl/tests/`, or `--workflow "Name"` to scope to one workflow. |

---

## What's Next

- [Creating Tests](creating-tests.md) -- YAML authoring, modules, scripts, variables, control flow
- [Running Tests](running-tests.md) -- execution flags, workflows, JSON output
- [Command Reference](../COMMANDS.md) -- every command and flag
- [CI/CD Integration](../ci-cd.md) -- run tests in GitHub Actions
