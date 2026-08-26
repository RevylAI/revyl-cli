---
name: revyl-cli-optimize-tests
description: Optimize existing Revyl YAML tests by converting granular, button-press-level steps into intent-driven natural-language instructions, reducing step count and run time without losing coverage. Supports a deep mode that inspects the last execution's report and screenshots to verify instruction wording against what actually happened on screen.
---

# Revyl CLI Test Optimization Skill

## Native Agent Behavior

- Ask at most 1-3 concise clarification questions only when the target test, file, or sensitive action cannot be inferred from the repo or Revyl CLI.
- Prefer safe defaults and keep moving when `revyl test list`, `revyl test pull`, `revyl test report`, or the local `.revyl/tests/*.yaml` can answer the question.
- When Revyl prints an editor, report, or viewer URL, open it in the native browser/tool surface when available: Codex Browser/in-app browser for local URLs, Revyl editor/report URLs, screenshots, and page checks; Claude Code `.claude/skills` slash-command discovery plus WebFetch/WebSearch or configured MCP/browser tools; Cursor `.cursor/skills` plus `.cursor/rules/revyl-skills.mdc` and available MCP/browser tools.
- If no browser tool is exposed, report the URL and verify through `revyl test report` instead of claiming browser access.
- Confirm before pushing a rewritten test over an existing remote version without a `--force` review, or before deleting/restoring versions.
- If the arguments contain `deep` or `--deep`, run in Deep Mode (see below) instead of Standard Mode. Otherwise default to Standard.

## Why This Matters

Revyl executes `instructions` steps with an LLM driving perception and action against the live screen. Each step is its own reasoning-and-action loop, so:

- **More steps = slower runs.** A test with 12 granular taps takes roughly 12 perception/action cycles; the same flow expressed as 2-3 intent-driven steps takes 2-3.
- **Granular steps are more brittle.** "Tap the button at the top-right" breaks the moment that button moves, gets relabeled, or a new step is inserted upstream. "Complete checkout using the saved shipping address" survives UI rearrangement because the LLM re-derives the concrete actions from the current screen each run.
- **QA habits carry over from brittle frameworks.** Testers used to Selenium/Appium-style scripts tend to write one instruction per tap or field. That habit fights the model instead of using it — Revyl already has an LLM in the loop, so give it a goal, not a script.

The fix is almost always mechanical: find runs of consecutive `instructions` blocks and collapse them into one instruction that states the user's goal.

## Modes

- **Standard** (default) — static analysis of the local YAML only: the Optimization Loop and Merge Rule below.
- **Deep** (`deep <test-name>` or `<test-name> --deep`) — everything in Standard, plus pulling the test's last execution report and cross-checking each `instructions` step against what the agent actually did on screen before rewriting. See "Deep Mode" below.

Reach for Deep when the YAML already looks reasonably merged (the "already intent-level" case) but the instruction wording might still be fighting the model — vague targets, steps that needed multiple corrective actions, or an instruction that assumes something not actually on screen.

## Optimization Loop

```bash
# 1) Get the current YAML locally
revyl test list
revyl test pull <test-name>          # or: revyl test remote --tag <tag>

# 2) Inspect the blocks and count consecutive `instructions` runs
cat .revyl/tests/<test-name>.yaml

# 3) Rewrite consecutive instruction blocks per the Merge Rule below
# edit .revyl/tests/<test-name>.yaml

# 4) Validate before pushing
revyl test validate .revyl/tests/<test-name>.yaml
revyl test diff <test-name>

# 5) Push and rerun to confirm equivalent behavior
revyl test push <test-name> --force
revyl test run <test-name>
revyl test report <test-name> --json

# 6) Roll back if the optimized version regresses real coverage
revyl test versions <test-name>
revyl test restore <test-name> --version <n>
```

Compare `revyl test report` step counts and duration before and after. A successful optimization pass reduces total blocks and wall-clock run time while producing the same pass/fail result on the same build.

## Deep Mode

Deep mode adds a report-inspection pass before applying the Merge Rule, so rewrites are grounded in what actually happened on the device rather than guesses from the YAML alone.

### 1. Pull the last execution

```bash
revyl test history <test-name> --json        # confirm a prior execution exists
revyl test run <test-name>                   # only if none exists yet
revyl test report <test-name> --json > /tmp/report.json     # by name = latest execution
```

If the report fetched by test name comes back with an empty `steps` array (can happen depending on report state), re-fetch by the specific task/execution id printed by `test run` or `test history` instead:

```bash
revyl test report <task-id> --json > /tmp/report.json
```

### 2. Inspect each `instruction` step's actual evidence

For every step where `step_type == "instruction"` in the report JSON, pull:

- `step_description` — what the test asked for.
- `effective_status` / `effective_status_reason` — whether the agent considered the goal satisfied, and why.
- `actions[]` — every physical action taken to satisfy that one instruction, each with `action_type`, `agent_description`, `reasoning`, `screenshot_before_url`, `screenshot_after_url`.

`len(actions)` per step is a signal:

- **One action, clean success** — an atomic, well-scoped step (or a merge candidate if adjacent instructions read as one user goal).
- **Multiple actions inside one step** — the agent already did multi-step reasoning to satisfy a single instruction; that instruction is already appropriately goal-level, not something to split further.
- **Action `reasoning` shows hesitation, a wrong-element correction, or re-grounding between actions** — the instruction's wording was ambiguous against the actual screen and needs sharper, more specific intent language (not necessarily more or fewer steps).

For `validation` steps, `validation_reasoning` and `video_analysis.reasoning` / `.thinking` show what the checkpoint actually saw. Use this to confirm the validation still makes sense as a boundary — it almost always should stay exactly where it is (see Merge Rule).

### 3. Pull screenshots for a visual check

Download the most informative frames — favor `screenshot_before_url` of a step's first action and `screenshot_after_url` of its last, plus any step whose `reasoning` hinted at confusion:

```bash
curl -sL "<screenshot_url>" -o /tmp/step-<order>-before.png
curl -sL "<screenshot_url>" -o /tmp/step-<order>-after.png
```

View them (the Read tool renders images) and compare against the step's `step_description`:

- Does the screen actually show what the instruction assumes — the named button, field, or state? Flag any instruction that names something absent or superseded by a different control.
- Does the before/after pair across a run of adjacent instructions read as one continuous user action, confirming they're safe to merge under the standard Merge Rule?
- Is there a modal, permission prompt, or intermediate state visible that the instruction text doesn't account for — evidence the instruction needs a conditional clause (see `app-cold-start`'s "dismiss any startup interstitial" phrasing as a model for this).

### 4. Rewrite grounded in the evidence, goal-state first

Before drafting any merged wording, find the *terminal state* the run is driving toward — usually the `validation` block immediately after the merge candidates, or the final screenshot if there's no validation yet. That terminal state is the actual goal; the individual actions in the trace are just one run's path to it, not the spec.

Draft the instruction backward from that goal, not forward from the trace:

- Classify each obstacle the trace shows by **category**, not by the specific instance — "a startup interstitial", "a permission alert" — rather than transcribing what this one run happened to see ("the modal titled 'Welcome to the new X App!'", "the alert asking whether X can send notifications"). The next run may see a different modal, a reordered alert, or none at all — the category survives; the transcribed copy breaks the moment it changes.
- Only fall back to a literal name, title, or exact copy when it's load-bearing for disambiguation — e.g., two similar-looking buttons on the same screen, or a control whose accessible name doesn't match its visible label. If the trace shows the model finds the right element from the category description alone, drop the specifics even if a `testID` or exact string was available.
- Phrase it as "<generic action> until <goal state>" rather than "<action A>, then if X do Y, then if Z do W" — let the terminal validation (or its plain-language equivalent) be the stop condition instead of enumerating every branch this one trace happened to take.

Apply the same Merge Rule and "Describe by Intent, Not Position" guidance as Standard mode below, but justify each rewrite with what the report actually showed rather than assumptions from the YAML alone. When the agent's own successful `agent_description`/`reasoning` describes the action more clearly than the original `step_description` did, prefer the *category* it implies over quoting it verbatim.

### 5. Standard guardrails still apply

Never edit a module's own contents while optimizing the test that imports it (report screenshot/report findings about a module as suggestions only, per the Merge Rule's module-handling section). Keep every validation untouched. Re-run and confirm equivalent pass/fail after pushing. Roll back on regression.

## Merge Rule

Scan `test.blocks` (and each `if`/`while`/`module` body) for **runs of two or more consecutive `type: instructions` blocks with nothing else between them.** Collapse each run into a single `instructions` block whose `step_description` states the overall user intent in one sentence.

Do **not** merge across:

- An intervening `validation` or `extraction` block — these are checkpoints and must stay in place and in order.
- An `if` / `while` boundary — conditional branches represent distinct decision points, not one linear intent.
- A `module_import` boundary — modules are reusable units; don't fold their surrounding steps into the module call or vice versa.

**Never edit a module's own contents as part of test optimization.** If a test contains a `module_import` block, treat the module as opaque and out of scope for rewriting:

1. Pull the module for inspection only: `revyl module get <module-name-or-id>` (or `--json` for the raw blocks).
2. Analyze its `instructions` for the same kind of consecutive-step bloat covered by this Merge Rule.
3. Report findings as suggestions (e.g., "module `login-flow` has 5 consecutive instruction steps that could collapse to 1") — do not run `revyl module update` or otherwise rewrite the module's blocks.
4. Only modify a module if the user explicitly asks you to optimize *that module*, as a distinct, separately-confirmed action from optimizing the test that imports it.
- A `code_execution` block — it's not a user action and shouldn't be summarized away or absorbed into an instruction.
- Two genuinely different user goals that happen to be adjacent (e.g., "complete checkout" and "then update the saved shipping address in account settings" are two intents even with no validation between them).

When merging, keep any exact target language that was necessary to disambiguate a specific control (e.g., a button label that's one of several similar ones on screen). Drop purely mechanical narration ("tap", "then tap", "scroll down and tap") in favor of the goal it serves.

## Describe by Intent, Not Position

Check each instruction to see if it describes what to do as opposed to where it is. Naming an exact position (a button's location on screen, its order in a list) breaks the moment the layout shifts. Prefer stating the intent and let Revyl find the element when possible. If a merged step_description carries over positional phrasing from the original granular steps (e.g., "tap the third item in the list", "tap the button in the top-right corner"), rewrite it to name the target by what it is or does instead of where it sits — fall back to position only when nothing else disambiguates the control.

## Before / After

Before — QA-style granular steps, six round trips through the model for one goal:

```yaml
- type: instructions
  step_description: "Tap the Cart icon."
- type: instructions
  step_description: "Tap the Checkout button."
- type: instructions
  step_description: "Tap the shipping address field."
- type: instructions
  step_description: "Select the saved address."
- type: instructions
  step_description: "Tap Continue to payment."
- type: instructions
  step_description: "Tap Place Order."
- type: validation
  step_description: "The order confirmation screen is visible."
```

After — one intent-driven step, same coverage, one round trip:

```yaml
- type: instructions
  step_description: "Complete checkout using the saved shipping address."
- type: validation
  step_description: "The order confirmation screen is visible."
```

Before — granular steps that straddle a real checkpoint (do not fully collapse):

```yaml
- type: instructions
  step_description: "Tap Sign In."
- type: instructions
  step_description: "Enter the email {{email}}."
- type: instructions
  step_description: "Enter the password {{global.login-password}}."
- type: instructions
  step_description: "Tap the Sign In submit button."
- type: validation
  step_description: "The home screen is visible."
- type: instructions
  step_description: "Tap the Settings tab."
- type: instructions
  step_description: "Tap Notifications."
- type: instructions
  step_description: "Toggle push notifications on."
```

After — two intents separated by the real checkpoint, not one giant step:

```yaml
- type: instructions
  step_description: "Sign in with {{email}} and {{global.login-password}}."
- type: validation
  step_description: "The home screen is visible."
- type: instructions
  step_description: "Enable push notifications in Settings."
```

## Guardrails

- Preserve every `validation` and `extraction` block's content, order, and variable names exactly. Optimization touches `instructions` grouping, not assertions.
- Don't over-merge into a single mega-step covering the entire test — an instruction should still map to one coherent, verifiable user goal. If two goals need independent evidence in the report when something fails, keep them as separate instructions.
- If a granular step exists because the flow genuinely needs a mid-flow anchor (e.g., a value must be extracted between two actions, or a conditional depends on intermediate state), keep the split — the Merge Rule's exceptions already cover this.
- Re-run after every optimization pass. A step count reduction that changes the pass/fail outcome is a regression, not an optimization — investigate before pushing further.
- Never fold secrets or literal credentials into a merged `step_description`; keep `{{variable}}` and `{{global.name}}` references intact.
- Never write to a module (`revyl module update`, `revyl module delete`, etc.) while optimizing the test that imports it. Modules are analyze-and-suggest only unless the user explicitly asks to optimize that specific module.
- Don't introduce or preserve positional language ("top-right", "third row", "the button below the header") in a merged `step_description`. State the goal and let Revyl locate the element; only name a position when no other description disambiguates the control.

## Definition of Done

1. No unexplained run of 2+ consecutive `instructions` blocks remains — each surviving run of steps has a merge exception (validation/extraction between them, branch boundary, module boundary, or genuinely separate intents).
2. Total block count for the test decreased, or the test was already intent-level.
3. `revyl test validate` passes on the rewritten YAML.
4. `revyl test run` after push produces the same pass/fail result as before optimization on an unchanged build.
5. Validations, extractions, variables, and secrets are unchanged in content and order.
6. Report step count and duration are lower than the pre-optimization baseline.
7. Any `module_import` blocks in the test were left untouched; if their modules had optimization opportunities, those were reported as suggestions, not applied.
8. No merged `step_description` relies on screen position or list order to identify a target unless nothing else disambiguates it.

Deep Mode adds:

9. The last execution's report (or a fresh run, if none existed) was pulled and every `instructions` step's `actions`/`reasoning` were checked, not just the static YAML.
10. At least one rewrite decision — a merge, a wording change, or a deliberate "leave as-is" — is traceable to specific report evidence (an `effective_status_reason`, an action `reasoning`, or a screenshot), not just a visual scan of the YAML.
11. Any instruction found to name UI that the screenshots show doesn't exist, or that omits an interstitial the screenshots show does appear, was corrected or flagged.
12. The terminal `validation` (or goal screenshot) was identified before drafting, and merged wording states obstacles by category and ends on that goal state ("dismiss any startup interstitials ... until you see the home screen") rather than transcribing this run's specific modal titles, alert copy, or branch-by-branch conditionals — unless a literal string was load-bearing for disambiguation.
