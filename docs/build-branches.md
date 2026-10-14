# Build branches

A branch is a named line of builds for one app, such as `main`, `staging`, or
`feature-login`. Every build belongs to one branch, and the test runs, sessions,
and Atlas evidence that come from a build stay with its branch. Each app starts
with a `base` branch, which is the app's default branch until you choose another.
Branch names are unique within an app and case-sensitive, up to 255 characters,
with no whitespace at either end and no control characters. iOS and Android apps
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
- **A steady release candidate.** Pin `staging` to one build with
  `revyl app branch pin`. Uploads move a pin only with `--pin`, so runs that use
  `staging` keep that build while newer builds arrive.

## How a run picks its build

1. **A choice for this run**, such as `--branch staging`, `--build-id`, or a
   branch or build picked in a run dialog.
2. **The saved selection** of the test, workflow, or embed.
3. **Default branch**, which uses the app's default branch.

Each branch has a **default build**, the build a run on the branch uses when it
doesn't choose one: **Latest build**, or a **pinned** build you choose with
`revyl app branch pin`, `--pin` on an upload, or a build's pin action in the
dashboard, such as **Pin “staging” to this build**. A usable build isn't
deleted, is the artifact type the app runs (a simulator `.app` for iOS, an
`.apk` for Android), and isn't one the Revyl GitHub App built to review a pull
request. **Latest build** is the most recently uploaded usable build.

| Selection | Behavior |
| --- | --- |
| Default branch | No branch chosen: use the app's default branch and that branch's default build. |
| Automatic (`inherit`) | Use the chosen branch's default build. |
| Latest build (`latest`) | Use the chosen branch's newest usable build, even when the branch is pinned. |
| All branches · Latest build (`latest_all_branches`) | Use the newest usable build across every branch of the app. It names no branch, ignores your Git branch, and can't be combined with a branch or an exact build. |
| Exact build (`pinned`) | Use one specific build. Later uploads don't replace it, and the branch's pin doesn't change. |

**Default branch** is evaluated when a run starts, so it follows a later change of
the app's default branch. A saved branch or exact build stays fixed. Once a run
starts, its branch and build stay fixed, including retries.

If the chosen branch doesn't exist, has no usable build, or is pinned to an
unavailable build, the run fails instead of using another branch or
build, and the error names the branch and the app, such as
`Branch "staging" on "My App" has no usable build`. Upload a build to that branch, or
choose another one. Uploading to a branch that doesn't exist creates it.

Uploads never pin a branch unless you ask. Builds from `revyl build` (local or
remote), `revyl build upload`, `revyl init`, and `revyl dev` (local or
`--remote`, including every rebuild), Expo imports, file uploads in the
dashboard, the GitHub upload-build action, MCP, the terminal UI, and API
uploads land on their branch. A branch that follows Latest build then uses the
new build, because it is the newest; a pinned branch keeps its pin.

To pin while uploading, pass `--pin` to `revyl build` or `revyl build upload`.
An API upload or remote build request pins its branch only when it sets
`pin: true`. You can also pin from the dashboard or with
`revyl app branch pin`. Deleting a branch's pinned build puts the branch back
on Latest.

`--set-current` and `--no-set-current` are deprecated. `--set-current` still
pins, like `--pin`; `--set-current=false` and `--no-set-current` do nothing.
Each prints a warning on stderr, and `--json` output is unchanged.

Changing the app's default branch doesn't change saved selections. Apps created
before branches started with `base` pinned to the build they ran. When uploads
stopped pinning, every branch pinned to its newest usable build moved to
**Latest build**, so those branches now run each new upload. Pin a branch again
to keep it on one build.

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
`Latest build (v4)` for a branch on Latest, with the version a run uses now, or
`Pinned build (v1.2)` for a pinned branch. **BUILD ID** is the build a
run on the branch uses now, or `-` when it has no builds. `pin` takes a build
ID, not a version label; `revyl build list --app "My App" --branch staging`
lists each build's ID. Unpinning, or
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
values like `${{ github.head_ref }}`; branches can't be deleted. Without
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
browsable even when it has no usable build.

Upload results include `branch_id` and `branch_name`. Keep the returned build ID
to run that exact build later; a remote build job ID is not a build ID.

Without `--pin`, an upload doesn't pin its branch. The upload result says which
build a run on the branch uses now:
`staging (Latest build: runs use this upload)`,
`staging (pinned to v1.2; runs that don't choose a build keep using v1.2)` when
the branch is pinned, or `staging (pinned to this build)` after `--pin`.

## Inspect the selected build

Use `revyl build which` to see the branch and build a run would use for one app,
without starting a run or changing its settings:

```bash
revyl build which --app "My App"
revyl build which --app "My App" --branch staging
revyl build which --app "My App" --json
```

It uses an explicit `--branch`, otherwise the app's default branch, then that
branch's default build: **Latest build** or its pinned build. Your local Git
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
after a branch is **Automatic**, **Latest build**, or **Exact build**. Pickers
for runs, sessions, and saved test or workflow choices also list
**All branches · Latest build** after the default branch; the workflow run
picker offers it when you leave the branch name blank. Upload destinations and
the build browser don't offer it.

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
revyl test run login --all-branches
revyl workflow run smoke --branch staging --build-selection latest
revyl workflow run smoke --all-branches
revyl workflow run smoke --ios-app <app-id> --ios-build-id <build-id>
revyl device start --app-id <app-id> --branch staging
revyl device start --app-id <app-id> --all-branches
revyl device install --app-id <app-id> --branch staging --build-selection latest
revyl device start --build-version-id <build-id>
revyl dev --profile development --platform ios --branch staging
```

Without these options, test and workflow runs use their saved selections. A
workflow's `--branch` applies to each app the workflow runs. Each of those apps
needs a branch with that name and, unless you give it an exact build, a usable
build there; otherwise the workflow doesn't start and the error names
the branch and apps. With test or workflow `--build`, `--branch` chooses
where the new build uploads, and the run uses that exact build. Runtime app
overrides replace the workflow's saved app settings; platforms you don't override
use each test's saved selection. Don't combine `--build` with `--build-selection`.

Device start/install and dev take an exact build with `--build-version-id`;
Explore and tests use `--build-id`. An exact build can't be combined with
`inherit` or `latest`. Workflows take exact builds per platform with
`--ios-build-id` and `--android-build-id`, together with the matching app.

`--all-branches` on `test run`, `workflow run`, `device start`,
`device install`, and `explore run` uses the newest usable build across all of
the app's branches: **All branches · Latest build**. It always uses Latest, so
it can't be combined with `--branch`, an exact build (`--build-id`,
`--build-version-id`, `--build`, or a workflow's per-platform builds), or a
`--build-selection` other than `latest`. Your Git branch doesn't change it. A
dev loop follows one branch, which its rebuilds upload to, so `revyl dev`
doesn't offer it. Run output names the branch each build came from, such as
`Build: v1.4 from branch feature-login`; JSON output includes `branch_name`.

Without `--branch`, `--build-version-id`, `--build-selection`, or `--seed-latest`,
a local `revyl dev` loop starts with the newest build on the app's default
branch, even when the branch is pinned. An Expo or React Native loop first
looks for the newest one built from your current Git branch and prints a
warning when there is none. A dev context keeps its branch for detached startup
and every rebuild. `--seed-latest` uses the branch's newest build, even when the
branch is pinned; `--build` builds afresh and `--no-build` never builds.
Installing another build on a device
doesn't change the build the session started with in its history. `revyl device
history --app-id <id> --branch staging` shows only sessions that started with a
build from that branch, and fails when the app has no branch with that name;
without `--branch` it shows every branch.

## Saved selections

New dashboard tests and newly chosen workflow app overrides start on
**All branches · Latest build**. Existing saved choices stay unchanged. API,
CLI, and MCP requests that omit a selection keep **Default branch** for
compatibility with existing scripts; choose All branches explicitly there.
`revyl test build clear` also keeps its existing Default branch behavior.

`revyl test pull` leaves All branches out of YAML. Pushing that YAML back to
its existing test preserves the saved selection. To create a new All branches
test from YAML, set `build_selection: latest_all_branches` explicitly.

```bash
revyl test build show login
revyl test build set login --branch staging --build-selection latest
revyl test build set login --build-id <build-id>
revyl test build set login --all-branches
revyl test build clear login
revyl workflow app set smoke --ios <app-id> --ios-branch staging --ios-build-selection inherit
revyl workflow app set smoke --ios-automatic
revyl workflow app set smoke --ios-all-branches
revyl workflow app disable smoke
revyl workflow app enable smoke
revyl workflow app clear smoke
```

`revyl test build show` prints a test's saved branch and build, such as
`staging (<branch-id>)` and **Automatic**; add `--json` for the saved IDs. Tests
saved with only a version label show that version without inventing a branch;
JSON includes `pinned_version` alongside the stored selector fields.
`test create` accepts the same `--branch`, `--build-selection`, `--build-id`,
and `--all-branches` choices, including when it creates a test from YAML or a
session. Workflows save iOS and Android separately with the `--ios-*` and
`--android-*` options; `--ios-automatic` saves Default branch for iOS, and
`--ios-all-branches` saves All branches · Latest build.
`revyl workflow app show` names each app and branch with its ID.
`revyl workflow app disable` turns a workflow's app
override off and keeps its choices; `revyl workflow app enable` turns them back
on. `revyl workflow app clear` removes the choices, like turning the override
off in the dashboard.

In YAML, a test's selection lives under `test.build` as `branch_id`,
`build_selection`, and `pinned_build_id`. An exact build needs both IDs and
`build_selection: pinned`. `build_selection: latest_all_branches` takes no
`branch_id`. Omitting the selection on a new test means Default
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
`--branch-id` to narrow the evidence, even for a branch with no usable build.
`--all-branches` explicitly selects All. `--build latest` uses the selected
branch's newest usable build, or the app's default branch when none is selected,
even when the branch is pinned. An exact build implies its branch, and an
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
`latest` for its newest build even when the branch is pinned,
`latest_all_branches` for the newest build across all of the app's branches
(with no branch), and `pinned` with an exact build ID.
`start_dev_loop` doesn't accept `latest_all_branches`. Inputs for one run
override saved selections, and Default branch uses the app's default branch. A missing
branch or unavailable build fails instead of switching to another. `run_test`
and `run_workflow` results name each build and the branch it came from.

| Tool | Selection inputs |
| --- | --- |
| `start_device_session`, `device_session`, `install_app` | `app_id`, `branch_name`, `build_selection`, or exact `build_version_id`. |
| `start_dev_loop` | `branch_name`, `build_selection`, or exact `build_version_id`; the dev context keeps its branch for rebuilds. |
| `run_test` | `branch_id` or `branch_name`, `build_selection`, or exact `build_version_id`. Omission keeps the test's saved selection. |
| `run_workflow` | Common `branch_name` and `build_selection` (`inherit`/`latest`/`latest_all_branches`), or exact `ios_build_id` / `android_build_id` with the matching app. |
| `create_test`, `update_test` | `branch_id` or `branch_name`, `build_selection`, `pinned_build_id`. Omitted update selectors preserve settings; `inherit` alone resets to Default branch. |
| `set_workflow_app` | `ios_app_id` plus `ios_branch_id` or `ios_branch_name`, `ios_build_selection`, `ios_pinned_build_id`; corresponding `android_*` fields for Android. |
| `upload_build` | Destination `branch_id` or `branch_name`; omission uses the app default. The upload never pins its branch. Results include branch ID/name and exact `version_id`. |
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
| Pin/unpin a branch | Same branch `PATCH`, with `pinned_build_id` set to a build UUID or explicit `null`. |
| Change app default | `PATCH /api/v1/apps/{app_id}` with `default_branch_id`. |
| Resolve an app build | `POST /api/v1/apps/resolve` with `app_id`, optional `branch_id` or `branch_name`, and `build_selection`; `pinned` also requires `pinned_build_id`, and `latest_all_branches` takes no branch. The response names the app (`app_name`) and branch (`branch_name`); with no build, `resolution_status` says whether the branch has none or none that's usable. |
| Upload destination | Supply `branch_id` or `branch_name` on the upload/build request. The accepted destination remains fixed through completion. `pin: true` also pins the branch to the new build; without it, the branch's pin is unchanged. The older `set_as_current` field is ignored. |

Saved test and workflow selections use `branch_id`, `build_selection`, and
`pinned_build_id`. Embed requests keep `build_id` for an exact build. Runtime
request field names vary by endpoint, so check its published schema; an exact
build ID is never a remote build job ID. Atlas API reads accept `branch_id`;
omitting both branch and build reads every branch. The dashboard and CLI resolve
their selected scope before sending branch-scoped reads.
