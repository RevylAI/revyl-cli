---
name: revyl-cli-import-tests
description: Import an existing test suite into Revyl. Converts Playwright, Cypress, Selenium, WebdriverIO, Maestro, Appium, XCUITest, Espresso, Detox, Gherkin, manual test-case exports (CSV, TestRail, Zephyr, Xray) or plain-language test docs into goal-level Revyl YAML tests with shared login modules and parameterised accounts, then validates and pushes them, optionally runs them and iterates on failures, and ends with a review list of what a person must decide.
metadata:
  internal: true
---

# Revyl CLI Test Import Skill

Use this skill when the user points at tests they already have and wants Revyl
tests from them. It is a workflow on top of `revyl-cli-create`, which stays the
authority for YAML syntax, block types, variables, modules and scripts. Read it
before writing YAML: if it is not installed, run
`revyl skill show --name revyl-cli-create`. For the full schema, run
`revyl schema --format llm`.

Revyl tests are plain-language goals executed by an agent against the live
screen. Selectors, waits, page objects and retries do not carry over and do not
need to. What carries over is each case's intent, the user-visible strings it
asserts, the data it needs, and the state it starts from.

## Native Agent Behavior

- Ask at most 1-3 concise questions, and only for what the suite and the Revyl CLI cannot answer: the target Revyl app, the platform when the source is web but the target is mobile, and how accounts are supplied. When the user asked you to proceed without questions, make the safest assumption and record it in REVIEW.
- Prefer safe defaults and keep moving when `revyl app list`, `revyl test remote` (tests in the org; `revyl test list` shows only this project's local and synced tests), `revyl module list` or the source files answer the question.
- Creating modules, global variables, scripts and tests changes the user's Revyl organization; running tests uses devices. Do those only when the user asked for them, and say which org (`revyl auth status`) before the first write.
- Never put credentials, tokens, real customer emails or phone numbers in YAML, module files, plans or reports, even when the source suite hardcodes them. Never type a secret value into a command yourself, even in an authorized run: `revyl global var set` takes the value only as an argument, so the user creates each secret global themselves.
- When Revyl prints a report or editor URL, open it with the browser tool the client exposes; otherwise report the URL and read `revyl test report <name> --json` instead.

## Do Not Convert When Running Unchanged Is Enough

Revyl can run some suites as they are on its devices. Offer this before
converting when the user's goal is "run my existing tests on Revyl devices"
rather than "have Revyl tests":

- Maestro: the Revyl Maestro runner executes a supported native subset of Maestro flows against a Revyl device session.
- Appium: the Revyl Appium driver connects a supported native WebDriver subset to a Revyl device session.

Both are source-installed and attach to an existing running session; they do
not produce Revyl tests, reports per step, or self-healing goal steps. See
https://docs.revyl.com/infrastructure/maestro and
https://docs.revyl.com/infrastructure/appium. Convert when the user wants
Revyl-native tests (goal-level steps, Revyl reports, workflows, CI via
`revyl test run`), or when the suite uses commands outside those subsets.

## Phase 1: Detect the Source Format

Identify every format in the suite and read only the matching reference:

| Signals | Format | Reference |
| --- | --- | --- |
| `*.spec.ts`/`*.test.ts` importing `@playwright/test`; `cypress/`, `cy.` calls; `driver.find_element`, `By.`; `wdio.conf`, `$()`/`browser.` | Web E2E code | [references/web-e2e.md](references/web-e2e.md) |
| YAML with `appId:` and `- tapOn`/`- assertVisible`; `XCUIApplication`, `XCTestCase`; `onView(withId`, Espresso, UiAutomator; `device.`/`element(by.id` (Detox); Appium clients and capabilities | Mobile automation code | [references/mobile-automation.md](references/mobile-automation.md) |
| `*.feature` with `Scenario`; CSV/XLSX/XML/JSON exports with Steps and Expected Result columns (TestRail, Zephyr, Xray, qTest); Markdown or docs of numbered steps | Manual or BDD cases | [references/manual-cases.md](references/manual-cases.md) |
| Existing Revyl YAML (`test:` with `blocks:`) or an earlier hand conversion supplied beside the source | Evidence, not source | None. The automated or manual suite stays the source of truth. Use the earlier conversion only as evidence of target-platform copy and navigation, which may be stale; its extra assertions are not source assertions, and its credentials or conditional login are REVIEW items |

If several formats are present, convert each with its reference but plan them
as one suite so shared setup becomes one module, not one per format.

## Phase 2: Inventory and Plan Before Writing

Read the whole suite first. Write `.revyl/import/PLAN.md` with:

1. **Target.** Revyl app name (`build.name`) and platform per test. A web suite
   can target the same web app (`platform: web`) or a mobile app that implements
   the same flows; that choice is the user's, so ask if it is not stated.
   Confirm the app exists in `revyl app list`; `revyl test validate` does not
   check `build.name`. If it is missing, ask, or keep the user's name and add REVIEW.
2. **Inventory table.** One row per source case: source path and case title,
   proposed Revyl test name, decision (`convert`, `partial` with the dropped
   steps named, `merge into <test>`, `module`, `skip`, `needs person`), and a
   one-line reason.
3. **Shared setup.** Steps that appear at the start of 2+ cases (login,
   onboarding, consent, deep navigation to a start screen) become one module
   each. Setup used by a single case stays inline. Inventory what the org
   already has with `revyl module list`, `revyl global var list`,
   `revyl script list` and `revyl test remote`, and reuse an existing module
   only after reading its blocks with `revyl module get <name>`.
4. **Data and accounts.** Every credential, account, seeded record and
   generated value the cases use, and how each will be supplied (see "Accounts,
   Secrets and Test Data").
5. **Preconditions and side effects.** Fixtures, API seeding, database resets,
   feature flags, teardown that writes data, cross-app chains.
6. **Not convertible.** Cases or steps with no user-visible equivalent on the
   target (network mocks, cookie or storage edits, pixel diffs, performance
   timings, API-only tests, multi-tab and multi-user choreography).
7. **Missing inputs.** Helpers, fixtures, page objects or step definitions the
   suite calls but you were not given. Ask for them when they decide behavior
   (how login works); otherwise infer from the call's name and add REVIEW.
8. **Credentials found in the source.** Note where they are, without copying
   the values, and add a REVIEW item to move them to a secret store and rotate
   them.

Show the plan before creating anything in the org when the suite has more than a
handful of cases or any `needs person` row. Skip as `skip` only with a reason;
never drop a case silently.

## Phase 3: Map Steps to Goal-Level Blocks

Apply these rules to every format; the reference adds format-specific mappings.

1. **One block per user goal, not per action.** Collapse runs of clicks, fills
   and scrolls that serve one goal into one `instructions` block, following the
   Merge Rule in `revyl-cli-optimize-tests` (`revyl skill show --name
   revyl-cli-optimize-tests`). A source step title or Gherkin step usually is one
   goal. Do not merge across an assertion, a branch, a module boundary or a
   data extraction. `revyl-cli-create`'s "one action per instruction step" means
   one user action toward one goal, not one tap.
2. **Assertions become `validation` blocks** that quote the exact user-visible
   strings the source asserts, in quotes. Merge assertions about the same screen
   into one validation; split only when a failure would need a different
   diagnosis. Count assertions keep the count in words ("exactly one").
3. **Name targets by visible label or purpose, never by selector or position.**
   Convert `data-testid`, accessibility IDs and XPath to the label a user sees.
   When only a selector is known, read the app source, page objects or
   screenshots for the label; if it is still unknown, describe the purpose and
   add a REVIEW item.
4. **Drop mechanics.** Explicit waits, sleeps, retries, polling, scroll-to-find,
   keyboard dismissal and app launches at the start are handled by the runner.
   Keep a `manual` `wait` only for a known slow transition, and only with integer
   seconds. A wait for an element is also an implicit assertion: when that
   element marks a checkpoint ("order placed"), keep it as a `validation`.
   Never write an `instructions` block that only looks or checks; that is a
   `validation`.
5. **Keep the source's order and anchors.** Put the source path and case ID in
   `metadata.description` and the original step titles as YAML comments above
   their blocks, so a reviewer can diff intent before the first push.
6. **Conditionals.** Source branches that handle optional UI (cookie banners,
   "what's new" sheets, permission prompts) become one `instructions` clause
   ("dismiss any startup prompt until the home screen is visible") or an `if`
   whose condition describes what a fresh install actually shows. Do not add an
   `else` that only validates the condition was false. Login is never
   conditional: every run starts logged out, so sign in unconditionally. Never
   wrap login in an `if` that checks for a logged-out screen, and drop any
   "already logged in" `else` branch; a condition that misjudges the first
   screen silently skips login and fails every later step.
7. **Never strengthen or weaken an assertion silently,** including where it is
   checked: assert on the same screen, list or tab the source asserts on, and
   name it in the validation. Commented-out or skipped assertions stay out and
   go to REVIEW. Platform copy that differs from
   the source (web vs iOS wording) uses the target's copy only when you have
   evidence of it; otherwise keep the source string and add REVIEW.

## Accounts, Secrets and Test Data

- **Shared secrets** (a staging password, an API token) become org secret global
  variables referenced as `{{global.<name>}}`. Globals must exist before a test
  or module that references them is created. The user creates each one, in
  their own terminal (`revyl global var set "<name>=<value>" --secret`) or the
  web UI. The CLI has no stdin or environment-variable option for the value
  yet, so an agent running it would put the secret in its command line; do not
  run it with a real value yourself, even when you are authorized to write to
  the org.
- **Per-run accounts and data** (an email claimed per run, an order ID) and
  other non-secret values become test variables declared under
  `test.variables` with a safe default or `""`, referenced as `{{name}}`.
  Supply real values per run with `revyl test run <test> --var name=value` from
  CI; `--var` overrides the declared default. `--var` values appear in step
  text, reports and the command line, so use them for test accounts and test
  data only, never for real customer data or secrets. Global variables cannot be
  overridden per run, so never use `{{global.x}}` for per-run values. A module
  can use `{{name}}` too; every test that imports it declares `name` under
  `test.variables`.
- **Data that must be created first** (seed an account, put an order into a
  state) becomes a leading `code_execution` block running a saved script that
  calls the user's test-data API and prints only the value, stored with
  `variable_name`. The script runs in Revyl's cloud sandbox, so the API must be
  reachable from the internet. Create it with `revyl script create`; see
  `revyl-cli-create` and https://docs.revyl.com for the script contract.
- **There is no per-run pre-launch hook for test runs.** Each run installs the
  app fresh and starts logged out, so data setup happens in CI before
  `revyl test run` or in a leading `code_execution` block.
- **Teardown that changes data** (reset account status, delete an order) is not
  a Revyl step by default; leave it to CI after the run and list it in REVIEW.
- **Checks outside the device** (an email inbox, an SMS, a scheduled job, a
  backend record) stay out of the test unless a `code_execution` script can
  check them through an API. Mark the case `partial` or `skip` and keep the
  requirement in REVIEW.
- **State the source's setup guarantees** (an account claimed in status
  "shipped", a seeded order, a fixed price list), including copy that follows
  from that state ("should arrive in" versus "is here!"): keep the assertion
  exact. If a run shows different state, that is a Data failure to fix in
  setup, never a reason to soften the validation.
- **Text derived from time** (dates, "arrives in N days", countdowns, "today"):
  validate the stable part ("a delivery estimate is shown"), extract the
  variable part with an `extraction` block if later steps need it, and list the
  original exact assertion in REVIEW.
- **Login.** Choose one path per test and say which and why in REVIEW:
  - *Launch configuration*, preferred when login is only a precondition and the
    app has a test sign-in hook. The app signs in at boot and the test has no
    login steps. Key-value launch variables reach iOS apps as `-KEY value`
    (single dash). When the app reads other tokens (`--email x --password y`),
    use a secret **iOS argument set** in Library → Launch Configurations,
    attached to the test; its name never reaches the device. Find the exact
    tokens the app reads in its code or in an existing passing run's launch
    settings; never guess them.
  - *Per-run account*: a leading `code_execution` step mints or claims a user
    into a variable. Launch configuration is fixed at boot, so that account then
    signs in through the UI module or a deep link the app supports.
  - *UI login module*, when the source case exercises login itself or the app
    has no hook. Point the user to `revyl-cli-auth-bypass` to add a hook.

  Whatever the path, login is never conditional, and credentials live only in
  secret globals or secret launch configuration, never in YAML.

## YAML Mistakes the Validator Does Not Catch

`revyl test validate` runs the backend validator, but these pass it and still
break the test. Check every file for them:

- The root is `test:` with `metadata`, `build` and `blocks`. A file using
  `steps:` instead of `blocks:` validates with zero blocks.
- `build.name` is required. A file without it can pass `revyl test validate`
  and then fail on create or push.
- `if` needs `then:` (and `while` needs `body:`). `steps:` under `if` is only a
  warning and the branch does nothing.
- `manual` `wait` takes a bare integer (`"5"`). `"5 seconds"` validates but waits
  a default instead.
- Do not start with `open_app`; the app is launched for you. If used, its value
  is a bundle ID or empty, never prose. Web tests reject device steps.
- Variable names use letters, digits, `-` and `_`, and must not start or end
  with `-` or `_`. An undeclared `{{name}}` validates; declare per-run variables
  under `test.variables` so ad-hoc runs do not see a literal placeholder.
- An undefined `{{global.x}}` validates. Confirm every referenced global exists
  with `revyl global var list` before creating the test.
- Module files contain only a top-level `blocks:` list, so `revyl test validate`
  does not accept them; `revyl module create` checks them.
- `revyl schema`'s note that `build:` is not a YAML field refers to build
  platforms in `.revyl/config.yaml`. In test YAML, `test.build.name` is the
  Revyl app name and is required.

## Phase 4: Write, Validate and Push

```bash
revyl auth status                         # confirm the target org
revyl app list --platform <ios|android|web>
revyl module list                         # reuse before creating
```

`revyl test validate` works without a project config, but `revyl test push`
and `revyl test list` need a current `.revyl/config.yaml` inside a Git
worktree. Use the existing one (a legacy config asks for `revyl config migrate`).
When the user handed you test files rather than an app repository, work in a
scratch directory: `git init`, then `revyl init -y`, which writes only the
local config and makes no org write. Otherwise set one up as `revyl-cli-create`
describes, or list it in REVIEW when you may not write to the org.

Write:

- `.revyl/import/PLAN.md` (Phase 2) and `.revyl/import/REPORT.md` (Phase 6).
- `.revyl/tests/<test-name>.yaml` per test. Use a lowercase, hyphenated slug as
  `metadata.name` and as the file name, prefix included. Start each file with
  `# Source: <path>[:<case id>]` and a `# REVIEW:` comment listing that test's
  open items. `revyl test push` rewrites pushed files with sync metadata and
  drops comments, so `metadata.description` and `.revyl/import/REPORT.md` are
  the durable record of source and REVIEW items.
- `.revyl/import/modules/<module-name>.yaml` per module (blocks only).
- `.revyl/import/scripts/<script-name>.<ext>` per data script, if any.

Keep test names stable, slug-style and prefixed when the user asks for a
prefix. `revyl test push` saves the synced test under a slug of its name; when
the name is not already a slug, edit that synced file from then on and delete
the authored one. Push updates only files that carry `_meta.remote_id`; a file
without it is created, and creation fails with "already exists" when the org
has a test of that name. To update that existing test with your file, find its
id in `revyl test remote --json` (raise `--limit` until the listing covers
every test), add it to the file as `_meta.remote_id`, and run
`revyl test push <slug> --force`. Do not use `test pull`, `test sync` or
`test create --force` for this; they replace the local file with the remote
test or still try to create.

`revyl test validate` resolves module and script names in the org, so a test
that imports a module you have not created yet fails with `MODULE_NOT_FOUND`.
Work in this order:

```bash
# 1) The user creates each secret global variable (see above); confirm the
#    names exist with: revyl global var list
# 2) Modules and scripts, once; later changes use module update --from-file.
revyl module create <module-name> --from-file .revyl/import/modules/<module-name>.yaml \
  --description "Imported from <source>"
# 3) Tests
revyl test validate .revyl/tests/*.yaml               # fix every error and warning
revyl test push --dry-run                             # review what will be created
revyl test push                                       # creates new, updates changed
```

When you may not write to the org yet, validate each test that imports a
module through a temporary copy with the module's blocks inlined in place of
the `module_import`, delete the copy, and say that the real file still needs
the module. If push or create fails after validate passed, read the error for
the mistakes above, fix the file, and push again.

## Phase 5: Run and Iterate (Optional)

Run only when the user asks. Runs use devices and credits, so bound them:
at most 3 runs per test (the first run plus 2 fixes) unless the user raises it.
Iterate on one data row of a parameterised test; run the other rows once after
it passes, and only when the user asked for them.

```bash
revyl test run <test-name> --fail-fast --no-open --json [--var name=value]
revyl test report <test-name> --json                  # latest execution
```

`--fail-fast` stops at the first failed step, which keeps a failed login from
running every later step against the wrong screen. When the failing verdict
comes from the asynchronous video check, one more step may run before the stop.
For each failure, read the failed step's `effective_status_reason`, its
actions' `reasoning` and their before and after screenshots (open the images).
Validation steps carry the verdict text in `validation_reasoning` and only a
`screenshot_before_url`. Judge every step only by `effective_status` (and
`validation_result` for validations): a failed validation can still report
`status: success` and count toward `passed_steps`. Then classify it. Trust the
screenshots over the app's source code, because the build under test may not
match the checkout:

| Class | Typical evidence | Action |
| --- | --- | --- |
| Test design | Step names UI that is not on screen, two goals in one step, a condition that never matches, a validation that asserts transient or wrong copy | Fix the YAML, validate, push, rerun |
| Data | Account in the wrong state, missing seed, empty list, expired promotion | Fix the variable or setup; otherwise REVIEW, do not rewrite the assertion. Within the run budget, recommend one diagnostic run without `--fail-fast` to show which later checks depend on the bad data and which are healthy |
| Auth | Stuck on sign-in, OTP or captcha, bypass link rejected | Fix the module or launch config; codes and captchas go to REVIEW |
| App | The app does something the source suite says is wrong | Stop iterating on that test; report it as a likely app bug with the screenshot |
| Environment | Build missing or wrong, device or infra error, timeout before the first step | Report it; rerun once at most |

Only test-design and setup fixes are yours to make. Never make a test pass by
deleting or loosening a validation the source asserted; that hides the
regression the test exists to catch. For deeper triage use `revyl-cli-analyze`.
Test runs end their own device sessions; confirm each ended (`session_status`
in the report, and `revyl device list`) and stop any session you started
yourself.

## Phase 6: Report and REVIEW List

Finish with a summary the user can act on:

1. A table: source case → Revyl test name, block count, validate/push status,
   and last run result with its failure class when run.
2. Modules, variables and scripts created or required, by name only.
3. **REVIEW**, grouped and deduplicated across the suite. Always include, when
   present: accounts and secrets the user must create or supply; preconditions
   and teardown moved to CI; time-varying assertions that were softened; labels
   guessed from selectors; web-only or platform-specific steps; omitted
   commented-out assertions; flows that send real messages, payments or codes;
   cases marked `skip`; likely app bugs from runs.

## MCP-Only Clients

The CLI flow is primary because only it has `revyl test validate` and
`--fail-fast`. If the client exposes the Revyl MCP server but no shell, keep
Phases 1-3 and 6 unchanged and use:

- `get_schema` instead of `revyl schema`; `revyl-mcp-create` for MCP authoring rules.
- `manage_modules` `create` for modules, `manage_variables` for test variables;
  the user still creates secret globals.
- `manage_tests` `create` (or `update` with `force`) per test; it validates on
  create, so fix each returned error and resubmit. It accepts only `ios` and
  `android`, so web tests need `revyl test push`; without a shell, stop and
  report that the web tests were not created. Never resubmit them as mobile.
- `manage_tests` `run` and `status`, then read the report. Without
  `--fail-fast`, ask the user to enable fail-fast on the imported tests
  (`revyl test config set <test> fail-fast true`) before running.

## Definition of Done

1. Every source case appears in the plan with a decision; none dropped silently.
2. No credential or personal data from the source is in any written file.
3. `revyl test validate` passes for every test file, and no file has a mistake
   from "YAML Mistakes the Validator Does Not Catch".
4. UI setup shared by 2+ tests exists once as a module, and each of those tests
   imports it. Setup that needs no blocks (a fresh install) has no module.
5. When pushing was requested, the tests exist in the intended org under the agreed names.
6. If runs were requested: each failure is classified, test-design failures were
   fixed within the run budget, and the rest are in REVIEW.
7. The final message lists every REVIEW item a person must decide.
