---
name: revyl-cli-optimize-tests
description: Optimize existing Revyl YAML tests by converting granular, button-press-level steps into intent-driven natural-language instructions, reducing step count and run time without losing coverage.
---

# Revyl CLI Test Optimization Skill

## Native Agent Behavior

- Ask at most 1-3 concise clarification questions only when the target test, file, or sensitive action cannot be inferred from the repo or Revyl CLI.
- Prefer safe defaults and keep moving when `revyl test list`, `revyl test pull`, `revyl test report`, or the local `.revyl/tests/*.yaml` can answer the question.
- When Revyl prints an editor, report, or viewer URL, open it in the native browser/tool surface when available: Codex Browser/in-app browser for local URLs, Revyl editor/report URLs, screenshots, and page checks; Claude Code `.claude/skills` slash-command discovery plus WebFetch/WebSearch or configured MCP/browser tools; Cursor `.cursor/skills` plus `.cursor/rules/revyl-skills.mdc` and available MCP/browser tools.
- If no browser tool is exposed, report the URL and verify through `revyl test report` instead of claiming browser access.
- Confirm before pushing a rewritten test over an existing remote version without a `--force` review, or before deleting/restoring versions.

## Why This Matters

Revyl executes `instructions` steps with an LLM driving perception and action against the live screen. Each step is its own reasoning-and-action loop, so:

- **More steps = slower runs.** A test with 12 granular taps takes roughly 12 perception/action cycles; the same flow expressed as 2-3 intent-driven steps takes 2-3.
- **Granular steps are more brittle.** "Tap the button at the top-right" breaks the moment that button moves, gets relabeled, or a new step is inserted upstream. "Complete checkout using the saved shipping address" survives UI rearrangement because the LLM re-derives the concrete actions from the current screen each run.
- **QA habits carry over from brittle frameworks.** Testers used to Selenium/Appium-style scripts tend to write one instruction per tap or field. That habit fights the model instead of using it — Revyl already has an LLM in the loop, so give it a goal, not a script.

The fix is almost always mechanical: find runs of consecutive `instructions` blocks and collapse them into one instruction that states the user's goal.

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
