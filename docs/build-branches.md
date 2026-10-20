# Build branches from the CLI

Every build belongs to one branch of its app, and each app has a default branch:
`base` at first, then the branch of the app's first upload. A run on a branch
uses its default build: its Latest build, or a build you pin. Without
`--branch`, uploads go to your Git branch, created if needed, and runs try it
first when it has a usable build, then the app's default branch. Choices you
make, for one run or saved, never fall back. See
[Build branches](https://docs.revyl.com/builds/branches) for the full model.

## Manage branches

```bash
revyl app branch list --app "My App"
revyl app branch list --app "My App" --page 2 --page-size 100 --json
revyl app branch create staging --app "My App"
revyl app branch rename staging release --app "My App"
revyl app branch default staging --app "My App"
revyl app branch pin staging <build-id> --app "My App"
revyl app branch unpin staging --app "My App"
```

- `list` shows the default branch first, marked `(default)`, then the rest by
  latest activity, 50 per page (`--page-size` up to 100). Its columns are
  **BRANCH**, **BRANCH ID**, **DEFAULT BUILD**, and **BUILD ID**.
- **DEFAULT BUILD** reads `Latest build (v4)`, `Latest build (no builds)`,
  `Pinned build (v1.2)`, or `Pinned build (v1.2, unavailable)`. **BUILD ID** is
  the build a run on the branch uses now, or `-` when there is none.
- `create` returns the existing branch when the name is taken.
- `pin` takes the ID of a build on that branch, not a version label. List IDs
  with `revyl build list --app "My App" --branch staging`.
- The other commands print the branch as `Branch: <name> (<id>)`, or JSON with
  `--json`.

## Upload and list builds

```bash
revyl build --profile preview --platform ios --branch staging
revyl build upload --file ./app.apk --app "My App" --branch staging --pin
revyl build list --app "My App"
revyl build list --app "My App" --branch staging --page 2 --page-size 50
revyl build list --app "My App" --branch HEAD
```

- `--branch` chooses the upload's branch and creates it if it doesn't exist.
  Without it, the upload goes to your Git branch, created if needed, or the
  app's default branch when none is detected. When `--repo` and `--commit` name
  a different repository or commit than the checkout, the checkout doesn't
  choose the branch. Pass `--branch` when the artifact wasn't built from this
  checkout.
- `revyl init` chooses its uploads' branch the same way, so the first build it
  uploads to an app it created sets that app's default branch.
- `--pin` pins the branch to the new build. If the version label already
  exists, the upload reuses that build and doesn't pin; the warning prints the
  `revyl app branch pin` command to run.
- Version labels are unique within an app among builds that aren't deleted. A
  label used on another branch fails with
  `This version already belongs to another branch; use a unique version`.
- The output prints `App:`, `Build Version:`, `Build ID:`, and `Branch:` with
  what a run on the branch uses now: `staging (Latest build: runs use this upload)`,
  `staging (Latest build: runs use v4)`,
  `staging (pinned to v1.2; runs that don't choose a build keep using v1.2)`, or
  `staging (pinned to this build)`. Keep the build ID to run that exact build
  later; a remote build job ID isn't a build ID.

`build list` shows every branch of the app. `--branch <name>` narrows it to one
branch. When no Revyl branch has that name, it lists builds built from a Git
branch of that name instead. `--branch HEAD` means your checked-out Git branch
and fails outside Git or on a detached checkout, as in pull request CI.
The heading says which filter matched: `Builds (Revyl branch: main):` or
`Builds (Git branch: main):`. Tables show **GIT BRANCH**, the branch the build
was built from, and across branches **REVYL BRANCH**, the dashboard's
**Branch** column.

## Check which build a run uses

```bash
revyl build which --app "My App"
revyl build which --app "My App" --branch staging
revyl build which --app "My App" --json
```

`build which` uses `--branch`, otherwise your Git branch when it has a usable
build, otherwise the app's default branch, then that branch's default build. It
prints the app, branch, reason, build, and default build without starting a
run, and prints the fallback notice on stderr when your Git branch couldn't be
used. It reads the app's selection, not a test's or workflow's saved choice.
`HEAD` here is a literal branch name. A failed selection exits nonzero without a
result.

## Run options

These apply to one run and don't change saved settings:

```bash
revyl test run login --branch staging
revyl test run login --branch staging --build-selection latest
revyl test run login --build-id <build-id>
revyl test run login --all-branches
revyl workflow run smoke --branch staging
revyl workflow run smoke --ios-app <app-id> --ios-build-id <build-id>
revyl explore run "My App" --branch staging
revyl device start --app-id <app-id> --branch staging
revyl device install --app-id <app-id> --all-branches
```

| Option | Uses |
| --- | --- |
| `--branch <name>` | That branch. Fails if it doesn't exist or has no usable build. |
| `--build-selection inherit` | Automatic, the default: the branch's default build. |
| `--build-selection latest` | Latest build: the branch's newest usable build, even when it's pinned. |
| `--all-branches` | All branches · Latest build: the newest usable build on any of the app's branches. It can't be combined with `--branch`, an exact build, or another `--build-selection`. |
| `--build-id <id>` | One exact build, on `test run`, `explore run`, `test create`, and `test build set`. |
| `--build-version-id <id>` | One exact build, on `device start`, `device install`, and `revyl dev`. |
| `--ios-build-id`, `--android-build-id` | One exact build per platform on `workflow run`, with `--ios-app` or `--android-app`. `--ios-build` and `--android-build` take a version label instead. |

- A workflow's `--branch` and `--build-selection` apply to every app it runs.
  Each app needs that branch and, unless you give it an exact build, a usable
  build there; otherwise the workflow doesn't start and the error names the
  apps. Without them, each app falls back from your Git branch on its own.
- With test or workflow `--build`, the new build uploads to `--branch`, or your
  Git branch, and the run uses that exact build. Don't combine `--build` with
  `--build-selection`.
- `--ios-app` and `--android-app` replace the workflow's saved app for one run;
  platforms you don't override use each test's saved choice.
- Output names the branch of each build, such as
  `Build: v1.4 from branch feature-login`.
- `revyl device history --app-id <id> --branch staging` lists sessions that
  started with a build from that branch.
- `revyl dev` keeps one branch for startup and every rebuild, and doesn't take
  `--all-branches`. See
  [Dev Loop](https://docs.revyl.com/cli/command-reference#dev-loop) for how it
  picks the branch and build.

## Saved test and workflow choices

```bash
revyl test build show login
revyl test build set login --branch staging --build-selection latest
revyl test build set login --build-id <build-id>
revyl test build set login --all-branches
revyl test build set login --build-selection inherit
revyl test build clear login
revyl test create login --from-file ./login.yaml --branch staging
revyl workflow app set smoke --ios <app-id> --ios-branch staging --ios-build-selection inherit
revyl workflow app set smoke --ios-automatic
revyl workflow app set smoke --ios-all-branches
revyl workflow app show smoke
revyl workflow app disable smoke
revyl workflow app enable smoke
revyl workflow app clear smoke
```

- `test build show` prints the saved **Branch** (Default branch, All branches,
  or `staging (<branch-id>)`) and **Build** (Automatic, Latest build, or
  `Exact build v1.4 (<build-id>)`); `--json` gives the saved IDs.
- `test build set` saves a branch and build. `--build-selection` alone keeps
  the saved branch, so on a test saved on All branches it saves Default branch.
  To move a test saved on a named branch back to Default branch, choose Default
  branch in its dashboard settings, or push YAML with
  `build_selection: inherit` and no `branch_id`.
- `test build clear` resets a test to All branches · Latest build.
- `test create` takes the same `--branch`, `--build-selection`, `--build-id`,
  and `--all-branches`, also with `--from-file` or `--from-session`. Without
  them or a choice in the YAML, a new test starts on Default branch.
- Workflows save iOS and Android separately with the `--ios-*` and
  `--android-*` flags. `--ios-automatic` saves **Default branch** with
  **Automatic** for iOS, and `--ios-all-branches` saves All branches · Latest
  build.
- `workflow app show` names each app and branch with its ID. `disable` turns
  the app override off and keeps its choices, `enable` turns it back on, and
  `clear` removes the choices, like turning the override off in the dashboard.

## Terminal UI

- The app build browser opens on All branches, including when you return to
  it. Its branch filter narrows the list and never chooses an upload's
  destination or a run's build.
- Run, session, and upload pickers start with your Git branch when one is
  detected: `Git branch (feature-login) · falls back to base` for runs and
  sessions, or `Git branch (feature-login)` for uploads, which create it.
  Pickers that save a choice, and pickers with no Git branch, start with
  `Default branch (base)` and its default build. The other branches follow,
  most recently active first, with the app's default marked `(default)`.
- Pickers for runs, sessions, and saved choices list
  **All branches · Latest build** after the first row. Upload pickers and the
  build browser don't.
- After a branch, run, session, and saved pickers offer **Automatic**,
  **Latest build**, or **Exact build**. A workflow run takes a branch name and
  offers **Automatic** or **Latest build**; with no name, it offers
  **Keep saved settings** or **All branches · Latest build**.
- Lists load one page at a time: `m` loads more, `/` searches, `R` retries a
  failed page, and `n` names a new branch in an upload picker.

## JSON fields

- **Uploads and builds:** `branch_id` and `branch_name`, with the build ID at
  `.build.build_id` for `build upload --file` and `.version_id` for
  `build upload --url`.
- **Runs** (`test run`, `workflow run`, `workflow status`, `workflow report`,
  `explore run`, `device start`, `device install`): `branch_name` for each
  build, `branch_source` for where that branch came from, and
  `git_branch_name`:
  - `branch_source` is `git_branch` (your Git branch), `explicit_branch`
    (`--branch`, or the branch saved on the test or workflow), or
    `default_branch` (the app's default branch). It's left out when an exact
    build or All branches · Latest build chose the build.
  - `git_branch_name` is the Git branch the run sent, present only when the
    run consulted it: with `git_branch`, and with `default_branch` when your
    Git branch couldn't be used (it doesn't exist, has no usable build, or
    isn't a valid Revyl branch name). It's left out for `explicit_branch`, an
    exact build, All branches · Latest build, and a run with no Git branch.
    For example, a fallback reports
    `{"branch_name": "base", "branch_source": "default_branch", "git_branch_name": "feature-login"}`.
- **`build which`:** `app_id`, `app_name`, `branch_id`, `branch_name`,
  `branch_source` (`explicit_branch`, `git_branch`, or `default_branch`, which a
  fallback also reports), `git_branch_name` (as for runs: present with
  `git_branch`, and with `default_branch` after a fallback), `build_id`,
  `build_version`, and `branch_build_selection` (`latest` or `pinned`: the
  branch's default build).
- **`build list`:** `versions`, `count`, `total`, `page`, `page_size`, and
  `has_next`; with `--branch`, also `branch_filter` and `branch_filter_kind`
  (`revyl_branch` or `git_branch`).
- **`app branch list`:** `branches`, each with `is_default`, `pinned_build_id`,
  `pinned_build_version`, `pinned_build_available`, and `latest_build` (absent
  when the branch has no builds), plus `total`, `page`, `page_size`, and
  `total_pages`.

## Deprecated flags

`--set-current` on `revyl build` and `revyl build upload` pins like `--pin`.
`--set-current=false` and `--no-set-current` do nothing. Each prints a warning
on stderr, `--json` output is unchanged, and none appears in `--help`.
