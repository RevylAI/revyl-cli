# Keep a shared Atlas branch current

Build the intended source, upload to an explicit Revyl branch, then explore the
exact returned build. Merging a PR does not move its existing Atlas evidence;
uploading alone does not populate Atlas.

## Set up once

1. Copy `maintain-atlas.py` into your repository's `scripts/` directory. Commit it
   on the source branch you will build, including `staging` for the weekly example.
2. Choose a workflow below and copy it into `.github/workflows/`. Set the repository
   variable `REVYL_APP_ID` and secret `REVYL_API_KEY`. Use a CLI release with branch
   support. Configure your build recipe and build secrets separately.
3. For remote builds, change `REVYL_PROFILE` and `REVYL_PLATFORM` to your configured
   recipe. It must target `REVYL_APP_ID` and build the **current checkout**. If you
   use the existing-artifact option, build that artifact from the selected
   checkout too.
4. If the app needs login, set `REVYL_LOGIN_VARIABLE` to a stored Revyl launch
   variable's name or ID. CLI authentication and app login are separate. Never put
   secret values in the workflow or an agent prompt.
5. Run manually first. Inspect the returned report, source SHA, exact build and
   branch Atlas, including login coverage. Then enable the desired trigger.

| Approach | File / setup |
| --- | --- |
| After merges to `main` | `refresh-main.yml`, triggered by pushes to `main`. |
| Weekly `staging` refresh | `refresh-staging-weekly.yml`. Keep the workflow on the repository default branch; it explicitly checks out current `staging`. Change the cron for daily refresh. |
| Existing successful CI build | In that job, set `REVYL_ARTIFACT` to its APK or iOS simulator artifact, `REVYL_DESTINATION` to the Revyl branch to upload to (such as `main`), and `SOURCE_SHA` to the commit it was built from. Map `REVYL_APP_ID` and `REVYL_API_KEY` into the job's `env` as `refresh-main.yml` does, then run `python3 scripts/maintain-atlas.py`. Keep checkout/build/upload/Explore inside the same app+destination concurrency group. |

The remote variant needs no mobile build toolchain on the CI runner. The helper
uses Python's standard library and the Revyl CLI. It records exact job/build/run
IDs in `revyl-maintenance-result.json`, waits for Atlas processing, and confirms
that Explore used the uploaded build. A partial usable map can succeed; inspect
its report before treating the setup as complete.

`--branch main` or `--branch staging` keeps the destination explicit and stable
even if the app default changes. The upload doesn't pin the branch. When the
branch follows Latest build, runs on it that don't choose a build use the
uploaded build, because it is the newest; a pinned branch keeps its pin until
you pin another build with `revyl app branch pin`. Making that branch the app's
default branch is optional.

## Optional test of the same build

After the helper succeeds, while the workflow still holds its concurrency slot:

```bash
BUILD_ID=$(jq -er '.build_id' revyl-maintenance-result.json)
revyl test run "$REVYL_TEST_ID" --build-id "$BUILD_ID" --timeout 1800 --json
```

Use an existing test for that app/platform, check the exit code, and increase the
job timeout to cover the extra test. A monitoring timeout does not cancel remote
work. `test status` selects a test's latest execution, so do not use it to monitor
an exact task from this recipe.

## Let Cursor help

Use a scheduled Cursor automation to dispatch the same CI workflow, or ask it to
set up and verify the workflow once:

> For repository ORG/REPO, source branch main, Revyl app APP_ID and destination
> main, install or invoke our shared-branch Atlas maintenance recipe. Use the
> intended checkout and existing build recipe/artifact; preserve the CI workflow's
> app+destination concurrency group. Upload to the explicit destination and
> explore the exact returned build. Reference stored credentials only. Prove one
> manual run, then report source SHA, build ID, report URL and login/map outcome.
> Do not upload your incidental feature branch into main. Routine refreshes do
> not open PRs or send messages. If the automation cannot guarantee a single
> writer, dispatch the serialized CI workflow instead of uploading independently.

Cursor's repository/branch selection and schedule provide context, not a
single-writer guarantee. No Revyl GitHub App permission to write CI is required.

## Ordering and recovery

Use the same stable concurrency group for every workflow writing an app and
Revyl branch, with `cancel-in-progress: false`. Check out the current source head
**after** acquiring that slot. If reusing an artifact built earlier, verify its
source SHA still matches the intended head and skip superseded artifacts. Latest
means upload order, not Git ancestry. Concurrency groups coordinate only within
one GitHub repository; choose one writer across repositories or CI providers.

Known unfinished operations are cancelled and polled to a terminal state on
failure. If the runner disappears, a launch receipt is missing, or cancellation
cannot be confirmed, stop subsequent refreshes and settle the recorded job/run
before starting another writer. Neither a cleanup handler nor CI concurrency
can recover a force-killed runner automatically. Do not blindly retry an
ambiguous launch. Keep the saved receipt with the job's results.

Provider details: [GitHub concurrency](https://docs.github.com/en/actions/how-tos/write-workflows/choose-when-workflows-run/control-workflow-concurrency),
[scheduled workflows](https://docs.github.com/en/actions/reference/workflows-and-actions/events-that-trigger-workflows#schedule),
[Cursor automations](https://cursor.com/docs/cloud-agent/automations).

Local contract checks, without credentials or remote execution:

```bash
python3 test_maintain_atlas.py
```
