# Build with Revyl

`revyl build` runs a configured recipe in the cloud:

```bash
revyl build --profile development --platform ios
```

The recipe lives in `.revyl/config.yaml`. If the profile or platform is
ambiguous, specify it explicitly; no profile is stored as active or default.

## Before building

Run `revyl auth login` and `revyl init`, then review the generated recipe.
For Expo, `revyl init` writes a `preview` profile that runs `expo prebuild`
(when the native directory is absent) and then Xcode or Gradle, producing a
standalone iOS simulator `.app` or installable APK without Expo credentials.
Use it for `revyl build --profile preview` and pull-request builds. It also
writes a `development` profile of EAS dev-client builds, which `revyl dev`
selects for hot reload. Check the generated Xcode scheme: without an existing
`ios/` project, it is the name `expo prebuild` derives from an ASCII
`expo.name`. For other names, run `npx expo prebuild --platform ios`, then
`revyl init --detect` to read the generated scheme.

Cloud builds require a Git worktree and an accessible, same-platform app UUID
in the recipe's `app_id`. Use `revyl app list --platform ios` (or `android` or `web`)
to find an app, or `revyl app create` to create one. Cloud builds consume
metered compute credits and require available capacity; failures never fall
back to local execution.

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

- `--json` returns the remote-build response.
- Cloud builds wait for completion. Use `--detach` to return a job ID,
  `revyl build status <id> --follow` to follow it, or `revyl build cancel <id>`
  to cancel it. Ctrl-C stops following without cancelling the build.
- `--image`, `--env`, `--timeout`, `--detach`, and `--no-cache` are cloud-only.

This default changes only `revyl build`. `revyl dev` remains local unless you
pass `--remote`; test/workflow `--build` continuations remain local, and
`revyl build upload` still uploads an existing artifact.

## Uploading existing builds

Use `revyl build upload` to upload an artifact produced by your own pipeline.
To associate it with a GitHub PR, see
[Use your own CI](https://docs.revyl.ai/integrations/github#use-your-own-ci).

## Web builds

`revyl build --platform web` uses the same local or remote managed build path.
Configure `build.profiles.<name>.web` with explicit `build_commands` and an
`output_path` directory. Static sites need `index.html`; server-rendered apps
also supply `run.start` and package their runtime dependencies in that directory.
See [Web builds](web-builds.md) for recipes and runtime variable names.

## Build branches and defaults

Builds upload to the app's default branch unless you pass `--branch <name>`.
Pass it in automation that must keep uploading to one branch, such as
`--branch base`, even if the default changes. Generated version labels don't
choose the branch.
Each branch either follows **Latest build**, so runs use its newest build, or is
pinned to one build. Without `--pin`, an upload doesn't pin its branch: a branch
on Latest build runs the new build because it is the newest, and a pinned
branch keeps its pin. Pin with `revyl app branch pin` or upload with `--pin`.
For a reproducible run, pass the returned build ID.

Apps created before branches started with `base` pinned to the build they ran.
When uploads stopped pinning, every branch pinned to its newest usable build
moved to **Latest build**, so those branches now run each new upload. Pin a
branch again to keep it on one build.

Run `revyl build which --app "My App"` to inspect app-level branch and build
selection before a run. See [Inspect the selected build](build-branches.md#inspect-the-selected-build)
for `--branch`, `--json`, and how this differs from saved test/workflow choices.

See [Build branches](build-branches.md) for branch management, saved test and
workflow selections, and Atlas scope.
To keep `main` or `staging` mapped after merges or on a schedule, use the
[build-and-explore recipes](explore.md#keep-main-or-staging-current).

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
