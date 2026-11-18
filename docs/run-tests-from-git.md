# Run tests from Git

After you turn on **Run tests from Git**, each run uses the test, module and script
files in `.revyl/` on its own Git branch instead of the copies saved in Revyl.
A pull request that changes a test runs the changed test, and every other
branch keeps running its own.
[Test definitions from Git](https://docs.revyl.com/tests/definitions-from-git)
explains how a run picks its files.

## Query runs by branch or commit

Use an authenticated CLI. If you have not signed in, run `revyl auth login`
first. These queries search the runs visible to that organization; a branch
name is not scoped to one project or app.

Run history records two branches for each run: the Git branch its test
definition came from, for a snapshot-backed run, and the Revyl branch of its
build. `--branch` matches a run when either branch has that name. To list
every test execution that used a branch, run:

```bash
revyl test history --branch <branch-name> --json --limit 100 --offset 0
```

The JSON response includes the test and execution status, and may include a
session ID. A snapshot-backed record also includes its Git branch and commit; a
record without a snapshot Git branch has no snapshot Git fields. The
default page contains 10 executions; `--limit` accepts 1-100 and `--offset`
accepts 0-100000 and skips earlier results. Continue while `has_more` is
`true` by increasing `--offset` by `shown_count`.

Use a lowercase hexadecimal 40- or 64-character commit SHA when you need to
decide whether one revision needs another gate run:

```bash
revyl test history --git-commit-sha <git-commit-sha> --json --limit 100 --offset 0
```

Add `<test-name>` or a test ID to narrow either query to one test:

```bash
revyl test history <test-name> --git-commit-sha <git-commit-sha> --json --limit 100 --offset 0
```

To narrow a branch query to one test, pass its name or ID:

```bash
revyl test history <test-name> --branch <branch-name> --json --limit 100 --offset 0
```

The response includes the recorded definition source and, when available, the
session. A snapshot-backed branch query has this response shape:

```json
{
  "branch": "<branch-name>",
  "items": [
    {
      "test_name": "<test-name>",
      "status": "starting",
      "session_id": "<session-id>",
      "git_branch_name": "<git-branch-name>",
      "git_commit_sha": "<commit-sha>",
      "has_uncommitted_definition_changes": false,
      "test_definition_source": "cli_upload"
    }
  ],
  "total_count": 1,
  "shown_count": 1,
  "offset": 0,
  "has_more": false,
  "test_id": "",
  "test_name": ""
}
```

For snapshot-backed executions, the JSON output includes `git_branch_name`,
`git_commit_sha`, `has_uncommitted_definition_changes`,
`test_definition_source`, and `session_id`;
[Check which commit each run used](#check-which-commit-each-run-used) explains
the commit fields. The two Git fields are absent when the run has no snapshot
Git branch. To list
the device sessions joined to those test runs, run:

```bash
revyl device history --branch <branch-name> --status all --json --limit 100 --offset 0
```

To list sessions from one exact snapshot commit, use the same session filters:

```bash
revyl device history --git-commit-sha <git-commit-sha> --status all --json --limit 100 --offset 0
```

The default page contains 20 terminal sessions; `--limit` accepts 1-100 and
`--offset` accepts nonnegative values and skips earlier results. Use
`--status all` to include every session status, and continue until
`offset + sessions.length` reaches `total`. Add `--app-id <app-uuid>` when one
app's history is needed.
The response includes `limit`, `offset`, `total`, and session rows. Test fields
appear when a session has a linked test execution; a session that matched by
its build branch can have no test or snapshot Git fields. The following is a
relevant excerpt:

```json
{
  "limit": 100,
  "offset": 0,
  "sessions": [
    {
      "id": "<session-id>",
      "test_name": "<test-name>",
      "test_execution_id": "<execution-id>",
      "branch_name": "<revyl-branch-name>",
      "git_branch_name": "<git-branch-name>",
      "git_commit_sha": "<git-commit-sha>",
      "has_uncommitted_definition_changes": false
    }
  ],
  "total": 1
}
```

`--branch` matches any run that used the branch for its tests from Git or for
its Revyl build, so a run whose tests and build came from different branches
matches either branch name.

Your repository's default Git branch and the app's `base` branch, which Revyl
creates with the app, count as one branch. A run that read its tests from the
default Git branch, such as `main`, and used a build on `base` matches both
`--branch main` and `--branch base`, and neither the dashboard nor the CLI
flags its tests and build as coming from different branches. Revyl records
whether a run's Git branch is the repository's default when the run starts,
only for a project with **Run tests from Git** on.

In the device query, a session is included when a linked snapshot's Git branch
matches or the session's build is on a Revyl branch with that name.
Filtering checks any linked test execution, while displayed test and Git
fields come from the latest linked execution. A session can therefore be
included by an older matching execution and display newer linked metadata.
`--git-commit-sha` matches the exact commit recorded on a test definition
snapshot and therefore only matches snapshot-backed runs. If you pass both
filters, the run must match both. The output keeps `git_branch_name` and
`branch_name` separate so you can see the Git branch and Revyl build branch
that were recorded. When both are known, the JSON output's
`tests_and_build_from_different_branches` says whether the run's tests and
build came from different branches, and the table output adds a line for each
run where they did, or a single line about every run when they all did. The
JSON output also includes
`git_branch_is_repository_default_branch` and `branch_is_initial_branch` when
the run recorded them; a run on the default Git branch with a build on `base`
counts as one branch only when both are `true`. These commands report recorded
runs; they do not list the `.revyl/` files that currently exist on a branch.

If the Revyl server doesn't support Git branch and commit filters yet,
`revyl device history --branch <branch-name>` with `--app` or `--app-id` falls
back to sessions whose build came from that Revyl branch of the app and prints a
warning on stderr. Every other `--branch` query, and every `--git-commit-sha`
query, fails with an error instead of returning unfiltered results. For
`revyl device history --branch <branch-name>` without an app, the error
suggests adding `--app-id`.

## Check which commit each run used

Each run that read its tests from Git or from your checkout records the commit
its test definition came from, and whether your checkout had uncommitted
changes to its test files. Use an authenticated CLI, as for the queries in
[Query runs by branch or commit](#query-runs-by-branch-or-commit).

To see them for one run, pass its execution ID, or a test's name for the
test's latest run:

```bash
revyl test report <execution-id> --json
```

The report includes these fields at its top level, with the same names and
meanings as in `revyl test history --json` items and
`revyl device history --json` sessions:

- `git_commit_sha`: the commit Revyl read the test's file at, or your
  checkout's `HEAD` commit when the CLI sent your checkout's files.
- `has_uncommitted_definition_changes`: `true` when the checkout the CLI sent
  had changes not committed to Git in `.revyl/tests/`, `.revyl/modules/` or
  `.revyl/scripts/`, such as an edited or new file, so the files the run used
  can differ from `git_commit_sha`. It's `false` when that checkout had no
  such changes, and for a run that read its files from Git. Changes elsewhere
  in the checkout, such as your app's code or `.revyl/config.yaml`, don't
  count.
- `test_definition_source`: `cli_upload` when the CLI sent your checkout's
  files, or `git_commit_read` when Revyl read them from Git.
- `git_branch_name`: the Git branch the test definition came from.

A run without a test definition snapshot, such as a run of a test's saved copy,
has none of these fields, so read a missing
`has_uncommitted_definition_changes` as unknown, not as `false`. The report
of a run that sent a checkout with uncommitted changes includes this excerpt,
with identifiers replaced by placeholders:

```json
{
  "git_branch_name": "<branch-name>",
  "git_commit_sha": "<commit-sha>",
  "has_uncommitted_definition_changes": true,
  "test_definition_source": "cli_upload"
}
```

To list a branch's runs whose files can differ from their commit, keep the
items where the field is `true`:

```bash
revyl test history --branch <branch-name> --json --limit 100 --offset 0 \
  | jq '.items[] | select(.has_uncommitted_definition_changes == true) | {test_name, session_id, git_commit_sha}'
```

The table output of `revyl test history`, `revyl test status` and
`revyl device history` adds a line under the table for each such run:

```text
Run #1's checkout had test files with uncommitted changes, so they can differ from Git commit <commit>.
```

When every run in the table has the same line, the table output prints it once,
starting with `Every run` instead, or `Every session` in
`revyl device history`.

## Check a branch's test status

To find out whether a branch's tests pass, show the newest run of each test on
that branch. Sign in with `revyl auth login`, or in CI set the `REVYL_API_KEY`
environment variable to an API key, and run:

```bash
revyl test status --branch <branch-name>
```

The first line counts the tests with a run on the branch, and how many of
their newest runs passed, failed, are still running or have no known result.
The table lists each test's newest run: failed tests first, then running,
unknown and passed, with the newest run first within each group. LAST RUN is
how long ago that run started. For example:

```text
Newest run of each test on branch "main": 12 tests, 10 passed, 2 failed, 0 running, 0 unknown

  #       TEST                            RESULT      LAST RUN      DURATION      GIT COMMIT        SESSION
──────────────────────────────────────────────────────────────────────────────────
  1       zzob-d-repo-edited              failed      6d ago        1m 32s        <commit-sha>      <session>
  2       zzob-b-repo-edited              failed      7d ago        1m 26s        <commit-sha>      <session>
  3       zzob-d-revyl-edited             passed      6d ago        1m 39s        <commit-sha>      <session>
  4       zzob-d-secret-checkout          passed      6d ago        1m 29s        <commit-sha>      <session>
  5       zzob-c-secret-note              passed      6d ago        1m 30s        <commit-sha>      <session>
  6       zzob-c-add-todo                 passed      6d ago        1m 26s        <commit-sha>      <session>
  7       zzob-a-add-todo                 passed      7d ago        1m 7s         <commit-sha>      <session>
  8       zzob-b-revyl-edited             passed      7d ago        1m 30s        <commit-sha>      <session>
  9       zzob-b-secret-checkout          passed      7d ago        1m 16s        <commit-sha>      <session>
  10      zzob-a-secret-note              passed      7d ago        1m 9s         <commit-sha>      <session>
  11      zzqa-web-pinned                 passed      7d ago        1m 11s        <commit-sha>      <session>
  12      zzqa-pr1-pinned-definition      passed      7d ago        1m 24s        <commit-sha>      <session>

Every run's tests came from Git branch "main" and its build from Revyl branch "base".
```

To show the runs whose test definitions came from one exact commit, pass a
lowercase hexadecimal 40- or 64-character SHA instead of `--branch`, or with it
to show only the runs that match both:

```bash
revyl test status --git-commit-sha <git-commit-sha>
```

A run without a test definition snapshot has no recorded commit, so it never
matches a commit. Add `<test-name>` or a test ID to either command to show
only that test's newest matching run, in the layout of
`revyl test status <test-name>` with its result and Git commit added.
`revyl test status` with no test and neither flag stops with
`provide a test name or ID, or use --branch or --git-commit-sha`.

The CLI reads every page of results, so the counts and the table cover every
test with a run on the branch. A test that never ran on the branch isn't
listed, and tests deleted in Revyl are left out.

### How test status matches and counts runs

Each test's RESULT, `run_result` in JSON, says how its newest run counts,
checked in this order:

- `running`: the run's session is queued or still in progress, or a workflow
  run has queued a test that reads its definition from Git or your checkout
  and not started its session yet.
- `failed`: the run failed, timed out or was cancelled, recorded that it
  didn't succeed, or recorded an error. Its `status` still shows `timeout`
  or `cancelled` for those.
- `passed`: the run succeeded and recorded no error.
- `unknown`: none of these, such as a run that finished without recording
  whether it succeeded.

Test status decides which branch a run belongs to differently from
`revyl test history --branch`, which matches either branch:

- A run that recorded the Git branch its test definition came from belongs to
  that Git branch, even when its build came from a Revyl branch with another
  name.
- A run that recorded no Git branch, such as a run of a test's saved copy,
  belongs to its build's Revyl branch, because Revyl can't tell which Git
  branch such a run was for. When that Revyl branch has another name than your
  Git branch, such as `base` for your default Git branch, check it with its
  own `--branch` to see those runs.
- A run that read its tests from your repository's default Git branch and used
  a build on the app's `base` branch counts under both names only when it
  recorded both facts: `git_branch_is_repository_default_branch` and
  `branch_is_initial_branch` are both `true`. A run that didn't record
  whether its Git branch was the default, including every run from before
  Revyl recorded it, counts only under its recorded Git branch name.

Revyl picks each test's newest matching run, by when the run was created,
before it looks at the run's result, so an older passing run never hides a
newer failed one. A run that never finishes, including a test queued in a
workflow run that never ends, stays `running` until a newer run of that test
replaces it, and its LAST RUN age shows how long ago it started or was queued.

### Count each test's runs over a time window

To see how reliable each test has been lately, add `--since`:

```bash
revyl test status --branch <branch-name> --since 7d
```

`--since` takes a whole number of hours or days, such as `24h` or `7d`.
Only runs created in that window count, including when Revyl picks each test's
newest run, so a test with no run in the window isn't listed. Each row adds
RUNS, the test's finished runs in the window, and PASSED, how many of them
passed and their share, rounded down so that only a test whose every finished
run passed shows 100%. A finished run is one that passed, failed or has no
known result; a run still going isn't counted. A failure quarantined in its
workflow counts as failed here. A second line under the first
totals the finished runs of every test. For example:

```text
Newest run of each test on branch "main" in the last 7 days: 5 tests, 4 passed, 1 failed, 0 running, 0 unknown
Finished runs in the last 7 days: 16, of which 10 (62%) passed

  #      TEST                        RESULT      LAST RUN      RUNS      PASSED       GIT COMMIT
────────────────────────────────────────────────────────────────────────
  1      zzob-d-repo-edited          failed      6d ago        2         0 (0%)       <commit-sha>
  2      zzob-d-revyl-edited         passed      6d ago        3         2 (66%)      <commit-sha>
  3      zzob-d-secret-checkout      passed      6d ago        3         2 (66%)      <commit-sha>
  4      zzob-c-secret-note          passed      6d ago        4         3 (75%)      <commit-sha>
  5      zzob-c-add-todo             passed      6d ago        4         3 (75%)      <commit-sha>

Every run's tests came from Git branch "main" and its build from Revyl branch "base".
```

Any other value stops with
`--since must be a whole number of hours or days above zero, such as 24h or 7d, not "<value>"`.

### Fail a CI job unless every test passed

To make the exit code the answer, for example in a merge or deploy check that
reads results it didn't start, add `--fail-unless-all-tests-passed`:

```bash
revyl test status --git-commit-sha <git-commit-sha> --fail-unless-all-tests-passed
```

Pass `--git-commit-sha` to judge one revision, such as a pull request's head
commit, or `--branch` to judge each test's newest run on the branch, whichever
commit it used. Either way the check covers every test with a matching run, not
only the tests one workflow run started.

| Exit code | Meaning |
| --- | --- |
| `0` | At least one test has a matching run, and every test's newest run passed. A failure quarantined in its workflow doesn't count. |
| `2` | A test's newest run failed outside quarantine or has no known result, or no test has a matching run. |
| `3` | No newest run failed or has no known result, but at least one is still queued or running. |
| `1` | The command itself failed, such as a mistyped flag, a sign-in problem or a server error. |

Without the flag, the command exits `0` whenever it succeeds, even when no test
matched. With it, the
last line says the result: `✓ Every test passed` for `0`, and why for `2` or
`3`, such as `Error: No test has a run on branch "<branch-name>"` when no test
matched, or:

```text
Newest run of each test on branch "main": 1 test, 0 passed, 1 failed, 0 running, 0 unknown

  #      TEST                             RESULT      LAST RUN      DURATION      GIT COMMIT        SESSION
──────────────────────────────────────────────────────────────────────────────────
  1      run-tests-from-git-checkout      failed      28h ago       56s           <commit-sha>      <session>
Error: Not every test passed: 1 failed
```

A failed newest run that was quarantined in its workflow still shows as
`failed`, and a line under the table says it doesn't stop the check:

```text
Newest run of each test on branch "<branch-name>": 2 tests, 1 passed, 1 failed, 0 running, 0 unknown

  #      TEST                                 RESULT      LAST RUN      DURATION      GIT COMMIT        SESSION
──────────────────────────────────────────────────────────────────────────────────────
  1      zzqa-status-quarantined-failure      failed      10m ago       15s           <commit-sha>      <session>
  2      zzqa-pr1-pinned-definition           passed      14d ago       1m 46s        <commit-sha>      <session>

Run #1 was quarantined in its workflow, so its failure doesn't stop --fail-unless-all-tests-passed.

✓ Every test passed, apart from 1 whose failure was quarantined in its workflow
```

A workflow run counts each test that reads its definition from Git or from your
checkout as `running` from when Revyl queues the workflow run, before the
test's device session exists. So a check right after
`revyl workflow run --no-wait` returns exits `3` until those tests finish,
instead of passing or failing on their earlier runs. Such a row shows when the
workflow run was queued, with no duration or session, and a line under the
table says the test is queued:

```text
Newest run of each test on branch "<branch-name>": 2 tests, 1 passed, 0 failed, 1 running, 0 unknown

  #      TEST                            RESULT       LAST RUN      DURATION      GIT COMMIT        SESSION
──────────────────────────────────────────────────────────────────────────────────
  1      zzqa-status-live-20261118       running      1s ago        -             <commit-sha>      -
  2      zzqa-pr1-pinned-definition      passed       14d ago       1m 46s        <commit-sha>      <session>

Run #1 is queued in its workflow and has no device session yet.
Error: Not every test has finished: 1 still queued or running
```

The command checks once and doesn't wait. To wait for the tests, run it again
while it exits `3`, and bound the wait, for example with your CI job's timeout,
because a run that never finishes stays `running`:

```bash
until revyl test status --branch <branch-name> --fail-unless-all-tests-passed; do
  status=$?
  if [ "$status" -ne 3 ]; then exit "$status"; fi
  sleep 30
done
```

While a test is queued this way, it matches `--branch` only by the Git branch
its definition comes from. If the workflow run ends before it starts the test,
for example because it was cancelled, the test shows its earlier newest run
again. A test the workflow run had already started keeps that run and its
result.

A test that runs as saved in Revyl, rather than from a file in Git or your
checkout, counts only once its device session exists, because Revyl matches
such a run to a branch by its build, which it picks when the run starts. Until
then the check sees the test's earlier newest run, or doesn't list the test. To
gate a CI job on a workflow run that the job starts itself, you can also run
`revyl workflow run` without `--no-wait`: it waits for the workflow run and
exits with a nonzero code unless every test passed.

### Read test status as JSON

Add `--json` to get the same results as one object:

```bash
revyl test status --branch <branch-name> --since 7d --json
```

For a branch where one test ran in the window, the output looks like this,
with identifiers replaced by placeholders:

```json
{
  "branch": "main",
  "created_from": "2026-11-11T01:46:20Z",
  "latest_run_result_counts": {
    "failed_count": 1,
    "passed_count": 0,
    "running_count": 0,
    "unknown_count": 0
  },
  "quarantined_failed_latest_run_count": 0,
  "run_result_counts_since_created_from": {
    "failed_count": 1,
    "passed_count": 3,
    "running_count": 0,
    "unknown_count": 0
  },
  "test_count": 1,
  "tests": [
    {
      "branch_name": "base",
      "build_version": "<build-version>",
      "duration": 56.680798,
      "git_branch_name": "main",
      "git_commit_sha": "<commit-sha>",
      "has_uncommitted_definition_changes": false,
      "report_url": "<report-url>",
      "run_result": "failed",
      "run_result_counts_since_created_from": {
        "failed_count": 1,
        "passed_count": 3,
        "running_count": 0,
        "unknown_count": 0
      },
      "session_id": "<session-id>",
      "started_at": "2026-11-16T21:15:43.458720Z",
      "status": "failed",
      "task_id": "<task-id>",
      "test_definition_source": "git_commit_read",
      "test_id": "<test-id>",
      "test_name": "run-tests-from-git-checkout",
      "was_quarantined": false
    }
  ]
}
```

- `test_count`: the number of tests with a matching run.
- `latest_run_result_counts`: how many of those tests' newest runs have each
  result.
- `quarantined_failed_latest_run_count`: how many of the failed newest runs
  were quarantined in their workflow.
- `tests`: one object per test, in the table's order, with its newest run's
  `run_result`, `was_quarantined`, `task_id`, `session_id`, `status`,
  `started_at`, `duration` in seconds, `branch_name`, `build_version`,
  `git_branch_name`, `git_commit_sha`, `has_uncommitted_definition_changes`,
  `test_definition_source` and `report_url`. `test_id`, `test_name`,
  `run_result` and `status` are always there, and so are `task_id`,
  `was_quarantined` and `report_url` once the run has started. Any other field
  the run didn't record is left out; read a missing
  `has_uncommitted_definition_changes` as unknown.
- A test that a workflow run has queued, before its device session exists, has
  `status` `queued` and, instead of `task_id`, `session_id`, `started_at`,
  `duration`, `was_quarantined` and `report_url`, it has `workflow_task_id`,
  the workflow run's ID that `revyl workflow status` takes, and `queued_at`,
  when the workflow run was queued.
- `branch` and `git_commit_sha` repeat the selectors you passed.
- With `--fail-unless-all-tests-passed`, the exit code works as without
  `--json`, and its `Error:` line goes to standard error, so standard output
  holds only the JSON.
- With `--since`, `created_from` is the start of the window, and
  `run_result_counts_since_created_from` counts every matching run in it: at the
  top level for every test, and in each test's object for that test. Its
  `running_count` counts the runs still going, which RUNS leaves out.

With a test name, `--json` prints that test's object in the layout of
`revyl test status <test-name> --json`, with the same fields added.

If the Revyl server can't show test status by branch or commit yet, the
command exits `1` with
`this Revyl server can't show test status by branch or commit yet`,
followed by the server's response. `revyl test status <test-name>` without
these flags works on every Revyl server.

## Prerequisites

- Your repository is connected to Revyl through the GitHub or GitLab
  integration, and its `.revyl/config.yaml` belongs to a Revyl project there.
  `revyl github status` shows the repository with `access granted`, or
  `revyl gitlab status` shows it with `access: true`. Runs that read files
  from Git (dashboard, API and Revyl PR review runs, sessions you start in the
  test editor, and `--from-checkout=false`) read them through this connection.
- The project builds or receives uploads for the app your tests run on: one of
  its build profiles sets that app as its `app_id`, or its pull-request review
  takes your CI's uploads for that app. When a run reads Git, a test on any
  other app runs its saved copy.
- Each test you want to run from a file has a saved test in Revyl. Step 2
  writes a file in `.revyl/tests/` for each one, whose `_meta.remote_id` is
  that test's ID. The files leave out the tests' secret variables, which stay
  in Revyl and still apply when the tests run from Git; see
  [Variables and secrets](https://docs.revyl.com/tests/definitions-from-git#variables-and-secrets).
- Every Revyl CLI that reads `.revyl/config.yaml` in the repository, in CI and
  on developers' machines, must be version 0.1.234 or later while the file sets
  `run_test_definitions_from_git`, with any value. What an older CLI does there
  depends on its version:

  - Revyl CLI 0.1.217 and older stop with
    `run_test_definitions_from_git is not a supported field` when they read the
    file.
  - Revyl CLI 0.1.218 to 0.1.233 run tests without sending the checkout's
    files, as
    [`--from-checkout=false`](https://docs.revyl.com/tests/definitions-from-git#which-files-a-run-uses)
    does. When a test's file sets `variables`, their `revyl test push` deletes
    the test's saved secrets and saves the file's variables as plain text.

  While **Run tests from Git** is on, Revyl refuses each test or workflow run
  that a CLI older than 0.1.234 starts on one of the project's apps, from any
  folder, with the error `cli_upgrade_required`. Run `revyl upgrade` to
  update a CLI. The Revyl GitHub Actions install the latest CLI unless their
  `cli-version` input pins an older one; with an older pin, the step stops
  before the run starts.

## Turn on Run tests from Git

1. Check your CLI version:

   ```bash
   revyl version
   ```

   The output includes the version:

   ```text
   revyl version 0.1.234
   ```

   If it's older than 0.1.234, run `revyl upgrade`.

2. Write your saved tests, and the modules and scripts they use, into files.
   On a new branch from your up-to-date default branch, run this once for
   each app the project builds, where `<app-name>` is the app's name or ID
   from `revyl app list`:

   ```bash
   revyl test pull --all --app <app-name> --include-modules-and-scripts
   ```

   The pull writes a file for every saved test on the app, then a file for
   each module those tests use and each script the tests and modules use, and
   lists what it wrote:

   ```text
   Discovering all organization tests...
   Pulling tests...

   ✓ <test-name>: pulled v1
   Fetching the modules and scripts the tests use...

   ✓ module <module-name>: wrote .revyl/modules/<module-name>.yaml
   ✓ script <script-name>: wrote .revyl/scripts/<script-name>.py
   ```

   Repeat `--app` to pull several apps in one command. Without `--app`,
   `--all` writes a file for every saved test in your organization, on every
   app. A test file you changed since it was last pulled or pushed is kept,
   with a warning that ends `(use --force)`, and so is a module or script
   file whose content differs from the saved copy. Step 3 shows how to compare
   a kept test file with its saved test; to take the saved copy of a kept
   module or script, run `revyl module pull <module-name> --force` or
   `revyl script pull <script-name> --force`. Without
   `--include-modules-and-scripts`, `revyl module pull` and
   `revyl script pull` write every saved module and script in your
   organization. [Sync tests](https://docs.revyl.com/tests/sync) covers
   pulling and pushing tests.

   After the pull, make sure Git can stage the files. If Revyl finds the
   `.revyl/.gitignore` file that an older Revyl CLI wrote, it adds the module
   and script rules and prints:

   ```text
   Updated .revyl/.gitignore so modules and scripts can be committed with git add -A.
   ```

   If another ignore rule hides a pulled file, the CLI warns with the rule's
   file, line and pattern and tells you which negation rules to add. Follow
   that warning, then run:

   ```bash
   git add -A
   git status --short
   ```

   Confirm that the files in `.revyl/modules/` and `.revyl/scripts/` appear in
   the staged changes before you edit them.

3. Check each test's file against the test saved in Revyl. In the same
   checkout, run:

   ```bash
   revyl test list
   ```

   `STATUS` is `synced` when a test's file and its saved test match:

   ```text
     NAME                            STATUS          LOCAL      REMOTE      LAST SYNC
   ────────────────────────────────────────────────────────────────
     <test-name>                     synced          v1         v1          2d ago
     <other-test-name>               conflict        v1         v4          2d ago
   ```

   `modified` means the file has edits the saved test doesn't, `outdated`
   means the saved test has edits the file doesn't, and `conflict` means both
   changed. Once the setting is on, a test's file in Git runs as it is, so
   edits saved in Revyl that aren't in the file stop running. For each test
   that isn't `synced`, compare the file with the saved test:

   ```bash
   revyl test diff <test-name>
   ```

   It compares them as `revyl test pull` would write the file, with the
   test's steps, variables and other settings. Lines that start with `-` come
   from the file, and lines that start with `+` from the saved test. Here the
   file changes a variable:

   ```text
   --- local
   +++ remote
   @@ -6,7 +6,7 @@
            name: <app-name>
            build_selection: inherit
        variables:
   -        item_name: sourdough bread
   +        item_name: bread
        location:
            latitude: 37.7749
            longitude: -122.4194
   ```

   When the file and the saved test match, it prints `No differences found`.

   To run the saved test instead, write it into the file with
   `revyl test pull <test-name>`. For a `modified` or `conflict` test, add
   `--force`, which replaces the file:

   ```text
   ✓ <test-name>: pulled v4
   ```

4. Add the setting to `.revyl/config.yaml`, next to `project`:

   ```yaml
   run_test_definitions_from_git: true
   ```

   Then check the file:

   ```bash
   revyl config validate
   ```

   Its first line of output is:

   ```text
   ✓ Configuration is valid locally
   ```

   Its `Authority:` line says how the project's configuration is managed
   now, `manual` or `git_default_branch`; step 7 checks it again after the
   merge. Until the setting is published, it also prints `Not published` and
   a line that starts `⚠ Run tests from Git is off for this project`. When the
   default branch manages the project's configuration, that line ends with
   `To match, merge this branch into the default branch, which turns it on for the project.`,
   which step 6 does.

5. Run a test from your checkout, where `<test-name>` is the test's name or
   ID:

   ```bash
   revyl test run <test-name>
   ```

   The CLI sends the checkout's files, uncommitted changes included, and
   prints what the run used, with your checkout's commit and branch:

   ```text
   Definition: Checkout @ <commit> · <branch> · uncommitted changes · 1 test, 1 module and 1 script from files
   ```

   If a file has a problem, the run doesn't start, and the error starts with the
   file, for example:

   ```text
   Error: .revyl/tests/<test-name>.yaml:30: Module '<module-name>' is not in the files or in the organization
   ```

   [Troubleshoot runs from Git](https://docs.revyl.com/tests/troubleshoot-run-from-git)
   lists each error and its fix.

   Until the setting is published, the run also prints a line after
   `Definition:` that starts `Run tests from Git is off for this project`,
   ending the same way as in step 4.

6. Commit `.revyl/config.yaml`, `.revyl/tests/`, `.revyl/modules/` and
   `.revyl/scripts/`, and `.revyl/.gitignore` if step 2 changed it, open a
   pull request ready for review, and merge it to the default branch. Revyl
   PR review skips draft pull requests by default, so a draft gets no review
   comment.

   CI jobs on that pull request already run its files, because the CLI reads
   the setting from the checkout. Revyl PR review keeps the project's published
   setting until the pull request merges. When the default branch manages the
   project's configuration and Revyl PR review comments on the pull request,
   the comment's section for the project says, where `<project-name>` is the
   repository's name, or the project root for a project in a subfolder:

   ```text
   Merging this pull request turns on Run tests from Git for <project-name>. Runs use the test, module and script files in Git, not the copies saved in Revyl. Edits saved in Revyl that aren't in your files don't run; to bring them into the files, run `revyl test pull --include-modules-and-scripts`.
   ```

7. After the merge, in an up-to-date checkout of the default branch, check
   that Revyl runs the new configuration:

   ```bash
   revyl config validate
   ```

   When the merge adds or changes the project's `.revyl/config.yaml` on the
   default branch, Revyl publishes the file, usually within a minute, and from
   then on the default branch manages the configuration, as
   [Default-branch authority](https://docs.revyl.com/integrations/github#default-branch-authority)
   describes. A project whose `Authority:` line showed `manual` before the
   merge now shows `git_default_branch`:

   ```text
   ✓ Configuration is valid locally
   ✓ Connected validation succeeded
     Authority:       git_default_branch
   ✓ Published: Revyl runs this exact configuration
   ```

   Until Revyl applies the merged file, `revyl config validate` prints
   `Not published`. When the default branch already managed the
   configuration before the merge, it names the commit Revyl hasn't applied:
   `⚠ Not published: Revyl hasn't applied commit <commit> from the default branch yet`
   and `Next: wait a minute, then run "revyl config validate" again`. The line
   that starts `⚠ Run tests from Git is off for this project` ends with
   `Revyl turns it on for the project when it applies commit <commit> from the default branch.`
   Wait a minute and run `revyl config validate` again.

   If it still prints `Not published` when you run it again, Revyl hasn't
   applied the merged file; [Default-branch authority](https://docs.revyl.com/integrations/github#default-branch-authority)
   lists what stops it. Publish the setting from this checkout: run the
   command on its `Next:` line, usually `revyl config push`, or turn it on in
   the dashboard under **Integrations > GitHub** (or **GitLab**):
   select the repository and project root, then turn on **Run tests from Git**
   in the **Test definitions** card of **Pull requests**, select **Save**, and
   select **Save** again in **Turn on Run tests from Git?**. Then run
   `revyl config validate` again.

   Run `revyl config push` only after the merge, in an up-to-date checkout of
   the default branch. If you run it from the branch before the merge, the
   setting turns on while the default branch still has the old files, so
   dashboard and API runs read those files until the merge.

   `revyl config push` says what changed:

   ```text
   ✓ Published the complete project configuration
   Run tests from Git is on. Runs use the test, module and script files in Git, not the copies saved in Revyl. Edits saved in Revyl that aren't in your files don't run; to bring them into the files, run `revyl test pull --include-modules-and-scripts`.
   ```

## Verify

1. Run a test without sending your checkout's files:

   ```bash
   revyl test run <test-name> --from-checkout=false
   ```

   The run reads the test's file from Git:

   ```text
   Definition: 1 test from Git branch <branch> @ <commit>
   ```

2. In the dashboard, open the run's report, select **Info**, then
   **Test definition**. It shows the repository, the file, and the Git branch
   and commit the run read the file at. `revyl test report <test-name> --json`
   shows them for the test's latest run as `git_branch_name` and
   `git_commit_sha`, with `has_uncommitted_definition_changes` set to
   `false` because the run read the file from Git.

## Several apps or repositories

Turn on **Run tests from Git** in each project whose tests should run from
files. The setting belongs to one project, the `.revyl/config.yaml` in one
folder of one repository, so a project in another repository or folder keeps
running its saved tests until you turn it on there too.

A run that reads Git looks only in the projects that have the setting on and
build or receive uploads for the test's app. Keep each test's file in the
project that builds its app, and write only that app's tests there:

```bash
revyl test pull --all --app <app-name>
```

`<app-name>` is the app's name or ID, which `revyl app list` shows; put a name
with spaces in quotes. Repeat `--app` for each app the project builds. Without `--app`, `--all` writes a file
for every saved test in your organization, on every app, and when those tests
are on more than one app it ends with:

```text
These tests are on <app-count> apps. To pull only one app's tests, run `revyl test pull --all --app <app name or ID>` (`revyl app list` shows each app's name and ID).
```

A test's modules and scripts come from the files in its own project. When tests
in two repositories use one module, each repository needs its own file for it,
which `revyl module pull <module-name>` writes; a project without the file uses
the saved module. Each repository's file is its own copy, so a change to it in
one repository doesn't reach the other. Scripts work the same way with
`revyl script pull <script-name>`.

Two projects with the setting on that build the same app can't both have a file
for one test. Runs of that test then fail with
`duplicate_test_definition_file`, and
[Troubleshoot runs from Git](https://docs.revyl.com/tests/troubleshoot-run-from-git#both-link-test-test-id-duplicate_test_definition_file)
shows the fix.

## Turn off Run tests from Git

When you turn off **Run tests from Git**, runs use the tests, modules and
scripts saved in Revyl again: the versions from before you turned it on, plus
anything pushed with `revyl test push` while it was on. Changes made in Git
while it was on stay in Git. Turning it off doesn't save them to Revyl, so
step 1 saves the ones you want to keep. The steps use Revyl CLI 0.1.234 or
later (`revyl version` shows it), signed in to your organization
(`revyl auth status` shows it).

How you publish the change depends on who manages the project's configuration.
`revyl config validate` shows it on its `Authority:` line: `git_default_branch`
when the default branch manages it, as
[Default-branch authority](https://docs.revyl.com/integrations/github#default-branch-authority)
describes, and `manual` when you publish it with `revyl config push` or the
dashboard. Turning the setting off doesn't change the authority.

1. To keep the changes made in Git, save them to Revyl. In an up-to-date
   checkout of your default branch, run:

   ```bash
   revyl test push --force --include-modules-and-scripts
   ```

   The push saves each test file over its saved test, with the `variables`,
   `env_vars`, `device` and `location` the file sets, and creates a saved
   test for a file that has none. Before it saves a test, it creates each module and
   script the tests use that has a file but no saved copy, updates each saved
   module and script whose file differs, and prints each one:

   ```text
   Runs in <repository> use the test files in Git, so this push only updates the saved fallback copies.
   Checking test status...
   ✓ Created script <script-name> from .revyl/scripts/<script-name>.py
   ✓ Updated script <script-name> from .revyl/scripts/<script-name>.py
   ✓ Created module <module-name> from .revyl/modules/<module-name>.yaml as v1
   ✓ Updated module <module-name> from .revyl/modules/<module-name>.yaml to v<version>
   Pushing tests...

   ✓ <test-name>: pushed to v<version>
   ```

   The first line shows only while the setting is on. When a saved test with no
   file in this checkout uses a module or script the push updated, the push
   names that test. It runs the new version from then on, so check that it
   still passes:

   ```text
   ⚠ Other saved tests use module <module-name> too: <other-test-name>
   ```

   When a test's steps save but its settings don't, the push prints the error
   and exits with a non-zero status, so run the same command again:

   ```text
   ✗ <test-name>: saved v<version>, but its settings failed to save: <error>
   ```

   Before you push, add `--dry-run` to list what the push would save, and run
   `revyl test list`: a test with the status `conflict` changed in Revyl since
   its file was last pulled or pushed, and `--force` replaces that change. To
   leave a test out, push the others by name instead, one at a time:
   `revyl test push <test-name> --force --include-modules-and-scripts`. The
   push doesn't delete a saved test whose file you deleted;
   `revyl test remote` lists every saved test, and
   `revyl test delete <test-name>` deletes one.

   The push also updates the `_meta` block at the top of each test file it
   saves, which records the saved test's ID and version. Commit those changes
   with the change in step 2.

2. Remove this line from `.revyl/config.yaml`:

   ```yaml
   run_test_definitions_from_git: true
   ```

   Remove the line instead of setting it to `false`, because Revyl CLI 0.1.217
   and older reject the key with any value, and so does a Revyl GitHub Action
   step whose `cli-version` pins a CLI older than 0.1.234.

3. Publish the change:

   - If the default branch manages the configuration, commit the changes on a
     branch, open a pull request and merge it. Revyl turns the setting off when
     the merge lands on the default branch. Until then, CI jobs on the pull
     request already run without sending its files, and print a line that
     starts `Run tests from Git is on for this project` and ends with
     `To match, merge this branch into the default branch, which turns it off for the project.`
     If Revyl PR review
     comments on the pull request, the comment's section for the project says,
     where `<project-name>` is the repository's name, or the project root for
     a project in a subfolder:

     ```text
     Merging this pull request turns off Run tests from Git for <project-name>. Runs use the tests, modules and scripts saved in Revyl again: the versions from before it was turned on, plus anything pushed with revyl test push while it was on. Changes made in Git while it was on stay in Git; to use them, run revyl test push --force --include-modules-and-scripts in a checkout that has them.
     ```

     The dashboard switch is locked for these projects.
   - If it's a manual project, commit the changes to your default branch,
     through a pull request if your default branch needs one. Then, in an
     up-to-date checkout of the default branch, run `revyl config validate`.
     When it prints `Not published`, run `revyl config push` there. It
     publishes the whole `.revyl/config.yaml`, so run it only where the file
     is the version you want Revyl to run, and only after the merge. It
     prints:

     ```text
     ✓ Published the complete project configuration
     Run tests from Git is off. Runs use the tests, modules and scripts saved in Revyl again: the versions from before it was turned on, plus anything pushed with `revyl test push` while it was on. Changes made in Git while it was on stay in Git; to use them, run `revyl test push --force --include-modules-and-scripts` in a checkout that has them.
     ```

     Until the setting is published, CLI runs from checkouts without the line
     read their files from Git and print a line that starts
     `Run tests from Git is on for this project`.

     You can turn it off in the dashboard instead: under
     **Integrations > GitHub** (or **GitLab**), select the
     repository and the project root, the folder that holds `.revyl/`, turn off **Run tests from Git** in the
     **Test definitions** card of **Pull requests**, select **Save**, and
     select **Save** again in **Turn off Run tests from Git?**. Still remove
     the line and commit it: until you do, CLI and CI runs from a checkout
     that has the line keep sending its files, and the next
     `revyl config push` from one turns the setting back on.

4. In an up-to-date checkout of your default branch, check that Revyl runs the
   new configuration:

   ```bash
   revyl config validate
   ```

   ```text
   ✓ Configuration is valid locally
   ✓ Connected validation succeeded
     Authority:       manual
   ✓ Published: Revyl runs this exact configuration
   ```

   When the default branch manages the configuration, the `Authority:` line
   shows `git_default_branch`. A line that starts `⚠ Run tests from Git is`
   means the checkout and the project's setting still disagree;
   [Troubleshoot runs from Git](https://docs.revyl.com/tests/troubleshoot-run-from-git) lists both
   lines and their fixes.

After you turn it off:

- `revyl test run` from a checkout without the line sends no files, and
  prints no `Definition:` line, the line that names the files a run used.
- A change to a test, module or script file reaches runs only after you push
  it with `revyl test push`, as before you turned the setting on. After a
  branch with such changes merges into the default branch, run step 1 again.
  To run a branch's files before then without saving them, pass
  `--from-checkout` to `revyl test run`, or set the Revyl GitHub Actions'
  `from-checkout` input to `'true'`.
- Tests, modules and scripts open for editing in the dashboard again.
- Earlier reports still show the files they ran under **Info**, then
  **Test definition**.
- The files in `.revyl/tests/`, `.revyl/modules/` and `.revyl/scripts/` stay
  in Git.

### Open branches after you turn it off

A branch created before you turned it off still has
`run_test_definitions_from_git: true` in `.revyl/config.yaml`. In a checkout
of each open branch, merge your default branch, where `<default-branch>` is
your default branch's name:

```bash
git fetch origin
git merge origin/<default-branch>
```

Then push the branch. If `_meta` lines in a test file conflict, keep either
side. After the branch
merges into the default branch, step 1 of
[Turn off Run tests from Git](#turn-off-run-tests-from-git) saves its test,
module and script changes, and `--force` overwrites a test whose `_meta`
is out of date.

Until an open branch has the merge:

- Revyl CLI 0.1.217 and older stop in that checkout with
  `run_test_definitions_from_git is not a supported field`. Revyl CLI 0.1.218
  to 0.1.233 run tests without sending the checkout's files, as
  [`--from-checkout=false`](https://docs.revyl.com/tests/definitions-from-git#which-files-a-run-uses)
  does. When a test's file sets `variables`, their `revyl test push` deletes
  the test's saved secrets and saves the file's variables as plain text. The
  Revyl GitHub Actions install the latest CLI, but a step whose `cli-version`
  pins one older than 0.1.234 stops with
  `The run_test_definitions_from_git key in .revyl/config.yaml needs Revyl CLI 0.1.234 or newer`.
- A current CLI keeps sending that checkout's files when it runs tests, and
  warns about it without failing. `revyl config validate` prints:

  ```text
  ⚠ Run tests from Git is off for this project, but this checkout's .revyl/config.yaml turns it on, so CLI runs from this checkout send its files. To match, remove `run_test_definitions_from_git: true` from .revyl/config.yaml, or run `revyl config push` to turn it on.
  ```

  When the default branch manages the configuration, the line ends with
  `To match, merge the default branch into this branch.` instead.
  `revyl test run` prints the same kind of line after `Definition:`.
- On a manual project, `revyl config push` from that checkout turns the
  setting back on. The Revyl GitHub Actions never run `revyl config push`.

After the merge, `revyl config validate` in that checkout prints
`✓ Published: Revyl runs this exact configuration`, and Revyl CLI 0.1.217 and
older work there again.

### Turn Run tests from Git on again

1. Bring the edits saved in Revyl since you turned it off into your files. On
   a new branch from your up-to-date default branch, run:

   ```bash
   revyl test pull --include-modules-and-scripts
   ```

   The pull updates each test file you haven't changed since it was last
   pulled or pushed, and keeps one you changed, with a warning:

   ```text
   ⚠ <test-name>: local changes would be overwritten (use --force)
   ```

   It writes no file for a test created in Revyl while the setting was off.
   `revyl test pull --all --app <app-name> --include-modules-and-scripts`
   writes a file for every saved test on that app that has none, and without
   `--app` it writes one for every saved test in your organization that has
   none. Delete the files of tests you don't want to run from Git.

   The pull then writes a file for each module and script the test files use
   that has none. It keeps every module and script file that differs from its
   saved copy, with a warning, even when only the saved copy changed:

   ```text
   ⚠ module <module-name>: .revyl/modules/<module-name>.yaml has local changes that would be overwritten (use --force)
   ```

   For each file with a warning, choose the version to keep. Commit your files
   first. To take the saved copy, run the command again with the name and
   `--force`: `revyl test pull <test-name> --force`,
   `revyl module pull <module-name> --force` or
   `revyl script pull <script-name> --force`. Then `git diff` shows what the
   saved copy changed, and `git checkout -- <file>` puts your committed
   version back.

2. Commit the files, then follow
   [Turn on Run tests from Git](#turn-on-run-tests-from-git) from step 4,
   which adds the line, checks it, runs a test and publishes the setting.
   Merging the pull request turns it on when Revyl applies the merged file;
   otherwise `revyl config push` from the default branch turns it on and
   prints `Run tests from Git is on.` with what runs use from then on.

3. In a checkout of each branch created while it was off, merge your default
   branch, the way
   [Open branches after you turn it off](#open-branches-after-you-turn-it-off)
   shows. Until you do, its CLI and CI runs don't send its files, and
   `revyl config validate` there prints a line that starts
   `⚠ Run tests from Git is on for this project`.

## CLI options

- `revyl test run` and `revyl workflow run` send the checkout's files when the
  setting is on. `--from-checkout` sends them in a repository without the
  setting, and `--from-checkout=false` sends none, so the run reads the files
  at the head of the run's branch in Git.
- `--branch` picks the Revyl branch the run takes its build from. Use it to
  choose a build, not to choose which Git branch's files run: to have Revyl
  read a feature branch's files from Git, run
  `revyl test run <test-name> --from-checkout=false` from a checkout of that
  branch, without `--branch`. Without `--branch`, a run uses the branch saved
  on the test, else your Git branch when the app follows Git branches, else the
  app's default branch, which reads your repository's default branch.
- `revyl test run --json` adds `test_definition_source` and
  `test_definition_fallback_reason`, and for a run that read Git,
  `test_definition_git_branch_name`, `test_definition_git_commit_sha` and, when
  Git doesn't have the run's branch, `test_definition_missing_git_branch_name`.
  `revyl workflow run --json` adds `test_count_by_definition_source` and
  `test_count_by_definition_fallback_reason`, and the three Git fields when
  every test that read Git read the same branch and commit.
- `revyl test pull --include-modules-and-scripts` writes a file for each
  module the pulled tests use and each script the tests and modules use, so
  `--all --app <app-name>` writes one app's tests with what they use. Without
  it, `revyl module pull` and `revyl script pull` write every saved module
  and script in the organization. A module or script file with other content
  is kept unless `--force` is set.
- `revyl test push` creates each module and script the pushed tests use that
  has a file but no saved copy. With `--include-modules-and-scripts`, it also
  updates each saved module and script whose file differs, and names the other
  saved tests that use one it updated. With the setting on, it updates only the
  saved copies that runs use when Git has no file.

## Related

- [Test definitions from Git](https://docs.revyl.com/tests/definitions-from-git):
  how a run picks its files, its fallbacks, and what it shows.
- [Troubleshoot runs from Git](https://docs.revyl.com/tests/troubleshoot-run-from-git):
  errors and their fixes.
- [Run tests in CI](https://docs.revyl.com/tests/ci): pull request jobs that run
  the pull request's files.
