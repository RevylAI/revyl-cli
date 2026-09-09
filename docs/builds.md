# Build with Revyl

`revyl build` runs a configured recipe in the cloud:

```bash
revyl build --profile development --platform ios
```

The recipe lives in `.revyl/config.yaml`. If the profile or platform is
ambiguous, specify it explicitly; no profile is stored as active or default.

## Before building

Run `revyl auth login` and `revyl init`, then review the generated recipe.
Cloud builds require a Git worktree and an accessible, same-platform app UUID
in the recipe's `app_id`. Use `revyl app list --platform ios` (or `android`)
to find an app, or `revyl app create` to create one. Cloud builds consume
metered compute credits and require available capacity; failures never fall
back to local execution.

Cloud builds upload tracked files with your edits and nonignored untracked
files. Never track secrets: `.gitignore` does not exclude already-tracked
files. Store credentials with `revyl build secret set NAME` and reference
their names in the recipe's `secrets` or with `--secret NAME`.

## Build controls

- `--json` returns the remote-build response.
- Cloud builds wait for completion. Use `--detach` to return a job ID,
  `revyl build status <id> --follow` to follow it, or `revyl build cancel <id>`
  to cancel it. Ctrl-C stops following without cancelling the build.
- `--image`, `--env`, `--timeout`, `--detach`, and `--no-cache` are cloud-only.

This default changes only `revyl build`. `revyl dev` remains local unless you
pass `--remote`; test/workflow `--build` continuations remain local, and
`revyl build upload` still uploads an existing artifact.

## Migrating existing local builds

If an existing script must keep building on the invoking machine, add
`--local`; use `--local --json` to preserve the local build/upload JSON format.
Local builds require your installed toolchain, `output_path`, authentication,
and secrets in the process environment. They still upload the artifact.
Non-interactive builds require `app_id`; interactive builds can select or
create the app after building. `--remote=false` remains a compatibility alias;
do not combine `--local` with `--remote`.

See [Build Configuration](https://docs.revyl.com/remote-builds/configuration)
for recipe fields, secrets, caches, and toolchain options.
