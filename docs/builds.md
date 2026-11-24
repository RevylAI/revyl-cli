# Build with Revyl

`revyl build` runs a configured recipe in the cloud:

```bash
revyl build --build-profile development --platform ios
```

The recipe lives in `.revyl/config.yaml`. If the profile or platform is
ambiguous, specify it explicitly; no profile is stored as active or default.

## Before building

Run `revyl auth login`, then `revyl init` from your project root. It detects
your project and writes `.revyl/config.yaml` with initial build commands. Edit
them if your project has a custom build setup. Detected JavaScript installs
use the lockfile exactly (`npm ci`, `--frozen-lockfile`, `--immutable`) and
activate the pnpm or Bun version pinned by `packageManager`; XcodeGen projects
run `xcodegen generate` first; Expo profiles follow a static `expo.platforms`.

Cloud builds require a Git worktree and an accessible, same-platform app UUID
in the recipe's `app_id`. Use `revyl app list --platform ios` (or `android` or `web`)
to find an app, or `revyl app create` to create one. Cloud builds consume
metered compute credits and wait in a queue until a build runner is free;
failures never fall back to local execution.

Inside a running computer, `REVYL_COMPUTER_ID` changes the default to a local
build and upload so the checkout's toolchain runs on that computer. Pass
`--remote` to keep the cloud build, or unset `REVYL_COMPUTER_ID` to restore the
ordinary cloud default.

Cloud builds upload tracked files with your edits and nonignored untracked
files. Never track secrets: `.gitignore` does not exclude already-tracked
files. Store credentials with `revyl build secret set NAME` and reference
their names in the recipe's `secrets` or with `--secret NAME`.

## Private dependencies

Upload credentials as build secrets and configure authentication before the
command that downloads private dependencies. The same setup commands run for
CLI uploads and GitHub-triggered builds.

### Private Git dependencies and submodules

For HTTPS Git downloads, add an entry to `$HOME/.netrc`. Choose the example for
your Git host. Create the secret with the masked prompt:

```bash
revyl build secret set GITHUB_TOKEN
# For GitLab instead:
revyl build secret set GITLAB_TOKEN
```

Select the name under `build.secrets` (or the recipe's `secrets`):

```yaml
secrets:
  - GITHUB_TOKEN
```

For GitLab, select `GITLAB_TOKEN` instead. Use a token with read access to each
dependency repository. A GitLab personal access token needs `read_repository`;
a deploy token needs its assigned username instead of `oauth2` below.

Add the matching setup command to the recipe before dependency installation.
These examples add a new host without erasing entries for other services. If
that host already has an entry, update it instead of appending a duplicate.

**GitHub:**

```yaml
setup_commands:
  - name: Configure GitHub credentials
    run: |
      set -eu
      set +x
      umask 077
      touch "$HOME/.netrc"
      chmod 600 "$HOME/.netrc"
      printf '\nmachine github.com\nlogin x-access-token\npassword %s\n' \
        "$GITHUB_TOKEN" >> "$HOME/.netrc"
```

**GitLab:**

```yaml
setup_commands:
  - name: Configure GitLab credentials
    run: |
      set -eu
      set +x
      umask 077
      touch "$HOME/.netrc"
      chmod 600 "$HOME/.netrc"
      printf '\nmachine gitlab.com\nlogin oauth2\npassword %s\n' \
        "$GITLAB_TOKEN" >> "$HOME/.netrc"
```

These entries authenticate subsequent HTTPS Git commands to the matching host.
SSH URLs such as `git@github.com:company/sdk.git` require SSH authentication.
Submodules are separate repositories recorded in `.gitmodules`; with a Git
checkout available on the runner, download them after configuring credentials:

```bash
git submodule update --init --recursive
```

Xcode Swift Package dependencies are a separate mechanism. To use system Git's
authentication when resolving their Git repositories, use
`xcodebuild -resolvePackageDependencies -scmProvider system` with your project's
workspace/project and scheme arguments. Ensure Xcode is actually using system
Git: an explicit `IDEPackageSupportUseBuiltinSCM=false` preference can prevent
this on Xcode 26.3. This does not configure authentication for package registries
or binary artifact downloads.

### Private npm packages

Upload and select an npm token:

```bash
revyl build secret set NPM_TOKEN
```

```yaml
secrets:
  - NPM_TOKEN
setup_commands:
  - npm ci
```

Add this literal line to the project's `.npmrc`, preserving existing settings:

```ini
//registry.npmjs.org/:_authToken=${NPM_TOKEN}
```

Commit the variable reference, never the token value. npm reads `NPM_TOKEN`
from the build environment. For another registry, configure its hostname and
any required package scope in `.npmrc`.

**Using SSH instead:** Store a dedicated SSH private key as a build secret and
configure SSH before fetching Git dependencies. Register the public key with
the Git host, use SSH repository URLs, and configure verified host keys in
`~/.ssh/known_hosts`. npm registry downloads still need registry credentials.

## Build controls

Read exactly attributed executions with `revyl build runs <BUILD_UUID>
--page 1 --limit 20 --json`. The response preserves pagination and unknown or
cancelled outcomes; follow `has_next` before drawing build-wide conclusions.
See [agent investigations](agent-investigations.md) for report drill-down and
Atlas comparison across builds.

- `--json` returns the remote-build response. While a `--json` build waits,
  the CLI prints the queued job ID and its `revyl build status <id> --follow`
  command as one line on stderr as soon as the build is queued; stdout stays a
  single JSON object.
- Cloud builds wait for completion. Use `--detach` to return a job ID,
  `revyl build status <id> --follow` to follow it, or `revyl build cancel <id>`
  to cancel it. Ctrl-C stops following without cancelling the build.
- `revyl build logs <id>` prints a build's full log from the start, where
  `build status` shows only the latest events. Add `--follow` to stream it
  until the build ends, or `--json` for one log event per line.
- When a cloud build fails or times out, `revyl build status <id>` and the
  failure output of `revyl build` also print a "Build errors" excerpt: the
  first `error:`, `** BUILD FAILED **`, Gradle `FAILURE:` and Kotlin `e:`
  lines of the log with two lines of context each, up to ten errors (from the
  latest lines when a very long log has none near its start). With
  `--json`, the same lines are in `error_excerpt`, with `...` between
  separate parts.
- `--image`, `--env`, `--timeout`, `--detach`, and `--no-cache` are cloud-only.

This default changes only `revyl build`. `revyl dev` remains local unless you
pass `--remote`; test/workflow `--build` continuations remain local, and
`revyl build upload` still uploads an existing artifact.

## Uploading existing builds

Use `revyl build upload` to upload an artifact produced by your own pipeline.
To associate it with a GitHub PR, see
[Use your own CI](https://docs.revyl.com/integrations/github#use-your-own-ci).

To remove one uploaded build version, run
`revyl build delete <build-version-id>`. It asks for confirmation; pass
`--force` to skip the prompt and `--json` for a machine-readable result. Find
build version IDs with `revyl build list --app <id>`.

To download a build version's artifact, run
`revyl build download <build-version-id>`. It saves the file under the
artifact's own name in the current directory; pass `--out <path>` for another
file path or an existing directory, and `--force` to overwrite an existing
file. The file appears only once it is complete, and when the build has a
recorded SHA-256 checksum the download is verified against it and discarded on
a mismatch. `--json` prints the saved path, size, SHA-256 and whether it was
verified. The download link is a short-lived credential, so it is printed only
with `--print-url`, which also warns on stderr. `--timeout` bounds the whole
download (default 30 minutes).

## Web builds

`revyl build --platform web` uses the same local or remote managed build path.
Configure `build.profiles.<name>.web` with explicit `build_commands` and an
`output_path` directory. Static sites need `index.html`; server-rendered apps
also supply `run.start` and package their runtime dependencies in that directory.
See [Web builds](web-builds.md) for recipes and runtime variable names.

## Build branches and defaults

`revyl build` uploads to `--branch`, otherwise your Git branch when automatic
detection is on (created if needed), otherwise the app's default branch.
Turn detection off in **App settings** > **Build branches** to use the app's
default branch for automatic uploads and runs across surfaces. Explicit
`--branch` choices still take precedence. Uploads never pin; pass `--pin` to
make the new build the branch's default build. See
[Build branches from the CLI](build-branches.md), and use the
[build-and-explore recipes](explore.md#keep-main-or-staging-current) to keep
`main` or `staging` mapped.

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
