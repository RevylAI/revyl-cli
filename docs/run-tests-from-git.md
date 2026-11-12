# Run tests from Git

After you turn on **Run tests from Git**, each run uses the test, module and script
files in `.revyl/` on its own Git branch instead of the copies saved in Revyl.
A pull request that changes a test runs the changed test, and every other
branch keeps running its own.
[Test definitions from Git](https://docs.revyl.com/tests/definitions-from-git)
explains how a run picks its files.

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
   the dashboard under **Settings → Integrations → GitHub** (or **GitLab**):
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
   and commit the run read the file at. `revyl test report` doesn't show
   them; the `Definition:` line in step 1 is the CLI's view.

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
     **Settings → Integrations → GitHub** (or **GitLab**), select the
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
- `--branch` picks the run's branch for both its build and its files. Without
  it, a run uses the branch saved on the test, else your Git branch when the
  app follows Git branches, else the app's default branch, which reads your
  repository's default branch.
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
