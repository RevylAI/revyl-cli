# Build branches

A branch is a named line of builds for one app, such as `main`, `staging`, or
`feature-login`. Every build belongs to one branch, and the test runs, sessions,
and Atlas evidence that come from a build stay with its branch. Each app starts
with a `base` branch, which is the app's default branch until you choose another.
Branch names are unique within an app and case-sensitive. iOS and Android apps
have separate branches.

## What branches are for

- **A source-of-truth Atlas for `main`.** After each merge, have CI build `main`,
  upload it with `--branch main`, and explore that build. Atlas scoped to `main`
  then maps the app you ship, without feature work mixed in. See
  [Keep main or staging current](explore.md#keep-main-or-staging-current).
- **Feature work kept apart.** Upload in-progress builds with
  `--branch feature-login`. Runs and links that use `main` keep using its builds.
- **Tests on a chosen branch.** Run a test or workflow on a branch once, or save
  the branch on it so every run uses that branch. See [Test a branch](#test-a-branch).
- **`main` compared with a feature branch.** Switch Atlas between the two, compare
  a build from each, or run the same workflow on both. See
  [Compare two branches](#compare-two-branches).
- **A steady release candidate.** Pin `staging` to one build and upload newer
  builds with `--no-set-current`. Runs that use `staging` keep the pinned build.

## How a run picks its build

1. **A choice for this run**, such as `--branch staging`, `--build-id`, or a
   branch or build picked in a run dialog.
2. **The saved selection** of the test, workflow, or embed.
3. **Default branch**, which uses the app's default branch.

Each branch has a **default build**. A branch that follows **Latest build** uses
its newest usable upload; a **pinned** branch uses one exact build, set with
`revyl app branch pin`.

| Selection | Behavior |
| --- | --- |
| Default branch | No branch chosen: use the app's default branch and that branch's default build. |
| Automatic (`inherit`) | Use the chosen branch's default build. |
| Latest build (`latest`) | Use the chosen branch's newest usable upload, even when the branch is set to an exact build. |
| Exact build (`pinned`) | Use one specific build. Later uploads don't replace it. |

**Default branch** is evaluated when a run starts, so it follows a later change of
the app's default branch. A saved branch or exact build stays fixed. Once a run
starts, its branch and build stay fixed, including retries.

If the chosen branch doesn't exist, has no usable build, or is pinned to a build
that is no longer available, the run fails instead of using another branch or
build. Upload a build to that branch, or choose another one. Uploading to a
branch that doesn't exist creates it.

An upload from `revyl build` (local or remote), `revyl build upload`, or
`revyl init` pins its branch to the new build, even when the branch was
following Latest. Expo imports do the same. In a local `revyl dev` loop, a
fresh initial build or an initial Expo/React Native DerivedData upload pins the
branch; reused native artifacts and later rebuild uploads never change the pin.
`revyl dev --remote` pins its branch, the app's default branch unless you pass
`--branch`, to every remote build it makes, including each rebuild.

Uploads with `--no-set-current`, file uploads in the dashboard, the GitHub
upload-build action, MCP, and the terminal UI never change a pin. The older
`revyl build upload --set-current=false` flag also leaves the pin unchanged. An
API upload pins its branch only when the request sets `set_as_current: true`,
and a streamed upload never does; a remote build request pins its branch unless
it sets `set_as_current: false`. A branch with no pin uses its newest build, so
it picks up uploads that don't pin it. To keep a build while uploads continue,
pin it and upload with `--no-set-current`. Deleting a branch's pinned build puts
the branch back on Latest.

Changing the app's default branch doesn't change saved selections. Apps created
before branches start with `base` pinned to the app's current build, or on
Latest when there was none, so runs keep using the same build.

## Test a branch

Run once on a branch without changing any saved settings:

```bash
revyl test run login --branch staging
revyl workflow run smoke --branch staging
```

To make every run of a test or workflow use a branch, save it:

```bash
revyl test build set login --branch staging
revyl workflow app set smoke --ios <ios-app-id> --ios-branch staging
```

`revyl test build clear login` returns a test to Default branch.

## Compare two branches

Each branch keeps its own builds and Atlas evidence, so you can compare `main`
with a feature branch directly:

- **Atlas map.** Pass `--branch main` or `--branch feature-login` to
  `revyl atlas brief` or `revyl atlas graph`, or open each map with
  `revyl atlas open --app "My App" --branch <name>`.
- **Two builds.** Compare the screens and transitions observed in one build from
  each branch:

  ```bash
  revyl build list --app "My App" --branch main
  revyl build list --app "My App" --branch feature-login
  revyl atlas compare --app <app-id> --base-build <main-build-id> --head-build <feature-build-id>
  ```

- **Test results.** Run the same workflow on each branch and compare the
  reports. Test history can be filtered by branch.

  ```bash
  revyl workflow run smoke --branch main
  revyl workflow run smoke --branch feature-login
  ```

## Manage branches

```bash
revyl app branch list --app "My App" --json
revyl app branch create staging --app "My App"
revyl app branch default staging --app "My App"
revyl app branch pin staging <build-id> --app "My App"
revyl app branch unpin staging --app "My App"
revyl app branch rename staging release --app "My App"
```

The list shows the app's default branch first, marked `(default)` in the table
and `is_default` in JSON, then the other branches by latest activity: a new
build, a pin or unpin, or a rename moves a branch up. **DEFAULT BUILD** shows
the branch's default build:
**Latest build** with the version a run uses now, such as
`Latest build (v4)`, or the exact build's version. **BUILD ID** is the build a
run on the branch uses now, or `-` when it has no builds. Unpinning, or
deleting the pinned build, returns the branch to Latest. Saved
selections refer to branches by ID, so they survive a rename; scripts that pass
branch names need the new name. Branches can't be deleted or archived.

## Upload and list builds

```bash
revyl build --profile preview --platform ios --branch staging
revyl build upload --file ./app.apk --app "My App" --branch staging
revyl build list --app "My App"
revyl build list --app "My App" --branch staging --page 1 --page-size 20
revyl build list --app "My App" --branch HEAD
revyl build list --app "My App" --all-branches --json
```

`--branch` chooses the branch a build uploads to, and creates it if it doesn't
exist. Use long-lived branches like `main` or `staging`. Don't pass per-PR
values like `${{ github.head_ref }}`; branches can't be deleted yet. Without
`--branch`, uploads go to the app's default branch. A newly created
app's first `revyl init` upload uses its initial `base` branch; linked apps use
their default branch. `revyl init --branch <name>` overrides either choice.
A queued build or upload keeps its branch even if the app's default branch
changes before it completes. Version
labels are unique within an app, so a label used on one branch can't be uploaded
to another.

Each build also records the Git branch and commit it was built from, and the
CLI's default version label starts with the Git branch name. These show where a
build came from. Pull request preview builds from the Revyl GitHub App compile
the pull request's code and upload to the app's default branch.

Build lists show all branches of the selected app by default. Pass `--branch`
to filter by one branch; `--all-branches` explicitly selects the default All
view. When no Revyl branch has the name you pass, the list filters each fetched
page by the Git branch the builds came from. The heading says which matched:
`Builds (Revyl branch: main):` or `Builds (Git branch: main):`. `--branch HEAD`
stands for your current Git branch; outside Git or on a detached HEAD it fails,
so pass `--branch <name>` or omit `--branch` to list every branch. Uploads and
runs treat `HEAD` and `all` as ordinary branch names. Cross-branch tables show
both **GIT BRANCH** and **REVYL BRANCH**. JSON keeps each build's
branch identity and includes `branch_filter` only when a filter is applied.
Pagination still applies across the selected scope. An existing branch remains
browsable even when it has no runnable build.

Upload results include `branch_id` and `branch_name`. Keep the returned build ID
to run that exact build later; a remote build job ID is not a build ID.

`--no-set-current` adds a build without changing the branch's pin. A branch
with no pin still uses it, because it is the newest.

## Inspect the selected build

Use `revyl build which` to see the branch and runnable build selected for one
app without starting a run or changing its settings:

```bash
revyl build which --app "My App"
revyl build which --app "My App" --branch staging
revyl build which --app "My App" --json
```

It uses an explicit `--branch`, otherwise the app's default branch, then that
branch's default build: **Latest build** or an exact build. Your local Git
branch does not change this selection. Explicit branch errors and unavailable
pins fail instead of selecting another build. `HEAD` is a literal branch name
here; omit `--branch` to use the app default.

This is app-level inspection. Saved test or workflow selections can choose a
different branch or exact build. The result describes selection now; a later
upload or settings change can change what a future run selects.

JSON stdout contains `app_id`, `app_name`, `branch_id`, `branch_name`,
`branch_source` (`explicit_branch` or `default_branch`),
`build_id`, `build_version`, and the current `branch_build_selection`
(`latest` or `pinned`).
A failed selection exits nonzero without printing a success object.

## Browse builds in the terminal UI

The app build browser opens on All branches, including when you return to it.
Use its branch filter to narrow the list; rows show their known branch names.
The browsing filter does not choose the destination of an upload or the build
for a run. Those actions keep their explicit, saved, and default selections.

Pickers that choose a run, a saved selection, or an upload destination show the
app's default branch once, as `Default branch (base)` with its default build,
followed by the other branches, most recently active first. A selection that
already names the default branch keeps that branch's own row. The build choice
after a branch is **Automatic**, **Latest build**, or **Exact build**.

Branch pickers, exact-build pickers, and the app's build list show one page at a
time. Press `m` to load more choices. Press `/`, type a search, then press Enter
to search the full catalog within the selected scope. Submit an empty search to
clear it; Escape cancels editing the search. A failed later page leaves the
loaded choices available; press `m` to retry that page or `R` to refresh.

## Run options

These options apply to one run and don't change saved settings:

```bash
revyl explore run "My App" --branch staging --build-selection latest
revyl test run login --branch staging --build-selection inherit
revyl test run login --build-id <build-id>
revyl workflow run smoke --branch staging --build-selection latest
revyl workflow run smoke --ios-app <app-id> --ios-build-id <build-id>
revyl device start --app-id <app-id> --branch staging
revyl device install --app-id <app-id> --branch staging --build-selection latest
revyl device start --build-version-id <build-id>
revyl dev --profile development --platform ios --branch staging
```

Without these options, test and workflow runs use their saved selections. A
workflow's `--branch` applies to each app the workflow runs, and each of those apps
needs a branch with that name. With test or workflow `--build`, `--branch` chooses
where the new build uploads, and the run uses that exact build. Runtime app
overrides replace the workflow's saved app settings; platforms you don't override
use each test's saved selection. Don't combine `--build` with `--build-selection`.

Device start/install and dev take an exact build with `--build-version-id`;
Explore and tests use `--build-id`. An exact build can't be combined with
`inherit` or `latest`. Workflows take exact builds per platform with
`--ios-build-id` and `--android-build-id`, together with the matching app.

Without `--branch`, `--build-version-id`, `--build-selection`, or `--seed-latest`,
a local `revyl dev` loop starts with the newest build on the app's default
branch, even when that branch is pinned. An Expo or React Native loop first
looks for the newest one built from your current Git branch and prints a
warning when there is none. A dev context keeps its branch for detached startup
and every rebuild. `--seed-latest` skips the branch's pin; `--build` builds
afresh and `--no-build` never builds. Installing another build on a device
doesn't change the build the session started with in its history. `revyl device
history --app-id <id> --branch staging` filters history by branch; without
filters it shows all sessions.

## Saved selections

```bash
revyl test build show login
revyl test build set login --branch staging --build-selection latest
revyl test build set login --build-id <build-id>
revyl test build clear login
revyl workflow app set smoke --ios <app-id> --ios-branch staging --ios-build-selection inherit
revyl workflow app set smoke --ios-automatic
revyl workflow app disable smoke
revyl workflow app enable smoke
revyl workflow app clear smoke
```

`revyl test build show` prints a test's saved branch and build, such as
`staging (<branch-id>)` and **Automatic**; add `--json` for the saved IDs. Older
version-only pins show their saved version without inventing a branch; JSON
includes `pinned_version` alongside the stored selector fields.
`test create` accepts the same `--branch`, `--build-selection`,
and `--build-id` choices, including when it creates a test from YAML or a
session. Workflows save iOS and Android separately with the `--ios-*` and
`--android-*` options; `--ios-automatic` saves Default branch for iOS.
`revyl workflow app show` names each app and branch with its ID.
`revyl workflow app disable` turns a workflow's app
override off and keeps its choices; `revyl workflow app enable` turns them back
on. `revyl workflow app clear` removes the choices, like turning the override
off in the dashboard.

In YAML, a test's selection lives under `test.build` as `branch_id`,
`build_selection`, and `pinned_build_id`. An exact build needs both IDs and
`build_selection: pinned`. Omitting the selection on a new test means Default
branch. When updating a test, omitting all three keeps its saved selection, and
`build_selection: inherit` with both IDs omitted resets it to Default branch. The
older `pinned_version` key is still accepted; YAML that includes both forms must
name the same build, branch, and version.

## Atlas scope

```bash
revyl atlas graph --app "My App"
revyl atlas graph --app "My App" --branch staging
revyl atlas graph --app "My App" --all-branches
revyl atlas graph --app "My App" --branch staging --build latest
revyl atlas graph --app "My App" --build <build-id>
```

Atlas reads and `atlas open` show All branches by default. Pass `--branch` or
`--branch-id` to narrow the evidence, even for a branch with no runnable build.
`--all-branches` explicitly selects All. `--build latest` uses the selected
branch's newest usable build, or the app's default branch when none is selected,
even when that branch is pinned. An exact build implies its branch, and an
explicit branch must match it. `--build all` means all branches and can't be
combined with a branch. All retains authorization, pagination, time windows,
and recent-build limits within the selected app. A recent-build limit applies
once across the selected scope, not once per branch. Eligible legacy evidence
without a build appears only in All.

Branches filter evidence, not screens: merging or hiding screens and setting
identity roots apply to the whole app. Share links keep their scope. Merging a
pull request doesn't move builds or Atlas evidence to another branch, and
uploading a build doesn't explore it. To keep a shared `main` or `staging` Atlas
current after merges or on a schedule, use the
[maintenance recipes](explore.md#keep-main-or-staging-current).

## MCP inputs

MCP tools that choose a build use the same choices:
`inherit` (Automatic) for the branch's default build,
`latest` to skip its pin, and `pinned` with an exact build ID. Inputs for one run
override saved selections, and Default branch uses the app's default branch. A missing
branch or unavailable build fails instead of switching to another.

| Tool | Selection inputs |
| --- | --- |
| `start_device_session`, `device_session`, `install_app` | `app_id`, `branch_name`, `build_selection`, or exact `build_version_id`. |
| `start_dev_loop` | `branch_name`, `build_selection`, or exact `build_version_id`; the dev context keeps its branch for rebuilds. |
| `run_test` | `branch_id` or `branch_name`, `build_selection`, or exact `build_version_id`. Omission keeps the test's saved selection. |
| `run_workflow` | Common `branch_name` and `build_selection` (`inherit`/`latest`), or exact `ios_build_id` / `android_build_id` with the matching app. |
| `create_test`, `update_test` | `branch_id` or `branch_name`, `build_selection`, `pinned_build_id`. Omitted update selectors preserve settings; `inherit` alone resets to Default branch. |
| `set_workflow_app` | `ios_app_id` plus `ios_branch_id` or `ios_branch_name`, `ios_build_selection`, `ios_pinned_build_id`; corresponding `android_*` fields for Android. |
| `upload_build` | Destination `branch_id` or `branch_name`; omission uses the app default. Results include branch ID/name and exact `version_id`. |
| `list_builds` | `app_id` lists versions across all branches; optional `branch_id` or `branch_name` narrows them. Results are in `versions`, with each build's branch identity. Unfiltered responses omit the top-level branch. Without `app_id`, legacy app summaries remain in `builds`. |

Exact build and branch inputs must agree. A choice for one run doesn't save a new
default or pin. Read-only queries that resolve which build would run keep these
same selection rules. Saved branch IDs survive renames. See
[MCP setup](https://docs.revyl.ai/cli/mcp-setup) for installation.

## API selection

Use branch IDs for saved references and names for one-off choices. Branch and
build IDs must belong to the same app, and you must have access to it.

| Operation | API |
| --- | --- |
| List/create branches | `GET` / `POST /api/v1/apps/{app_id}/branches`. The list puts the app default first (`is_default`), then the most recently active, and gives each branch the build Latest would use (`latest_build`, `null` when there is none); it takes `search` (part of the name, any case), `name` (exact), `page` and `page_size` (up to 100). Because the order follows recent activity, a script walking every page should start again from page 1 when a page repeats a branch. Create body: `{"name":"staging"}`. |
| Read/rename a branch | `GET` / `PATCH /api/v1/apps/{app_id}/branches/{branch_id}`; rename body: `{"name":"release"}`. |
| Pin/unpin a branch's default build | Same branch `PATCH`, with `pinned_build_id` set to a build UUID or explicit `null`. |
| Change app default | `PATCH /api/v1/apps/{app_id}` with `default_branch_id`. |
| Resolve an app build | `POST /api/v1/apps/resolve` with `app_id`, optional `branch_id` or `branch_name`, and `build_selection`; `pinned` also requires `pinned_build_id`. |
| Upload destination | Supply `branch_id` or `branch_name` on the upload/build request. The accepted destination remains fixed through completion. `set_as_current: true` also pins the branch to the new build; remote build requests default to it. |

Saved test and workflow selections use `branch_id`, `build_selection`, and
`pinned_build_id`. Embed requests keep `build_id` for an exact build. Runtime
request field names vary by endpoint, so check its published schema; an exact
build ID is never a remote build job ID. Atlas API reads accept `branch_id`;
omitting both branch and build reads every branch. The dashboard and CLI resolve
their selected scope before sending branch-scoped reads.
