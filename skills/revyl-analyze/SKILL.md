# Revyl Failure Analysis Skill

Analyze test and workflow failures from Revyl report data to classify them as real bugs, flaky tests, or infrastructure issues. This skill teaches you how to triage failures without re-running anything — just by reading the AI agent's reasoning from the report JSON.

## When to Use

- After a test or workflow run **fails** and you need to understand why
- When triaging multiple failures across a workflow suite
- When deciding whether to file a bug report or fix a flaky test
- When a user asks you to "analyze", "triage", or "investigate" test results

## Step 1 — Fetch Report Data

```bash
# Single test (human-readable)
revyl test report <test-name>

# Single test (structured JSON for analysis)
revyl test report <test-name> --json

# Workflow suite (summary table of all tests)
revyl workflow report <workflow-name>

# Workflow suite (structured JSON)
revyl workflow report <workflow-name> --json
```

Use `--json` to get structured data you can parse. The human-readable format gives a quick pass/fail overview.

### Report JSON Structure

Top-level fields:
```json
{
  "test_name": "orchid-mantis-cart",
  "success": false,
  "total_steps": 5,
  "passed_steps": 5,
  "total_validations": 3,
  "validations_passed": 2,
  "platform": "iOS",
  "device_model": "iPhone 16",
  "duration": "3m 9s",
  "steps": [ ... ]
}
```

Each step in the `steps` array:

**Validation steps:**
```json
{
  "description": "The cart contains Orchid Mantis at $62.00",
  "order": 4,
  "status": "success" | "failure",
  "type": "validation",
  "type_data": {
    "validation_result": true | false,
    "validation_reasoning": "The screen shows the cart with..."
  },
  "actions": [{
    "action_type": "validation",
    "reasoning": "...(what the AI saw on screen)...",
    "is_terminal": true
  }]
}
```

**Instruction steps:**
```json
{
  "description": "Tap the \"ADD TO CART\" button",
  "order": 3,
  "status": "success" | "failure",
  "type": "instruction",
  "actions": [{
    "action_type": "tap",
    "agent_description": "Tap the ADD TO CART button",
    "reasoning": "...(why this action was chosen)...",
    "reflection_decision": "done" | "continue",
    "reflection_reasoning": "...(BEFORE vs AFTER comparison)...",
    "reflection_suggestion": "...(next action hint, when continue)...",
    "type_data": {
      "coordinates_x": 200,
      "coordinates_y": 500,
      "target": "the ADD TO CART button"
    }
  }]
}
```

## Step 2 — Identify Failures

Scan the report for failures:

1. **Check top-level**: `success: false` means at least one failure
2. **Check step counts**: Compare `passed_steps` vs `total_steps` and `validations_passed` vs `total_validations`
3. **Find failed steps**: Look for steps where `status: "failure"` or `type_data.validation_result: false`
4. **Special case — 0/0 steps**: If `total_steps: 0` or `passed_steps: 0` with `total_steps: 0`, the test never ran (infra issue)

## Step 3 — Examine Each Failure

For each failed step, read these fields in order:

### 3a. What was expected?
Read `description` — this is what the test expected to happen or verify.

### 3b. What did the AI actually see?
Read `type_data.validation_reasoning` (for validation steps) or `actions[].reasoning` (for instruction steps). This is the AI agent's description of what was actually on screen. This is your primary evidence.

### 3c. What happened in surrounding steps?
Read `reflection_reasoning` on the instruction steps before and after the failure. This tells you what changed between actions — the AI compares BEFORE and AFTER screenshots and describes the difference.

### 3d. Did the test complete?
Check if all instruction steps succeeded. If instructions passed but a validation failed, the app responded to interactions but the final state wasn't what was expected. If instructions also failed, there may be a navigation or element-finding issue.

## Step 4 — Classify the Failure

Assign each failure one of these categories:

### REAL BUG
The app's behavior contradicts what should happen. The test correctly identified a defect.

**Signals:**
- The AI saw something clearly wrong (e.g., wrong product, wrong price, missing element)
- Instructions executed successfully but the result was incorrect
- The `validation_reasoning` describes app state that contradicts the `description` in a way that isn't about test wording

**Example:** Test adds "Orchid Mantis ($62.00)" to cart, taps ADD TO CART successfully, but cart shows "Gold Tortoise ($18.00)". The AI correctly saw the wrong product — this is a real functional defect.

### FLAKY TEST
The app behaved correctly but the validation description was too specific, ambiguous, or brittle.

**Signals:**
- The `validation_reasoning` shows the app is actually working correctly
- The failure is due to the test listing exact items that don't cover all valid results
- The AI interpreted the description too literally (e.g., "including X, Y, Z" read as "only X, Y, Z")
- The underlying feature works but the assertion doesn't account for valid variations

**Example:** Test validates "Only beetle products are displayed including Hercules Beetle, Goliath Beetle, and Stag Beetle". The filter works correctly — it shows only beetles — but "Gold Tortoise" (which IS a beetle) also appears. The AI fails the check because Gold Tortoise wasn't in the named list. The filter is correct; the test description is too narrow.

### INFRA ISSUE
The test couldn't run at all due to infrastructure problems.

**Signals:**
- `total_steps: 0` or `passed_steps: 0` with no step data
- The test crashed before executing any steps
- Device provisioning failed, app didn't launch, worker error
- No `validation_reasoning` to analyze — there's simply no data

**Example:** Test shows 0/0 steps ran. No report data available. Likely a device setup or app installation failure.

### TEST IMPROVEMENT
The test ran and technically passed or failed, but its design makes it unreliable or unable to catch the intended bug.

**Signals:**
- A test passed when it should have failed (false positive)
- An instruction step combines verification + action, weakening the check
- The validation description is vague enough that correct and incorrect states both pass
- The test order or flow doesn't isolate the behavior being tested

**Example:** Test validates a total price then taps PLACE ORDER in the same instruction step. The AI sees the correct total in the order summary but doesn't strictly verify the button text — it just taps the button. The bug (wrong total on button) is missed because the check wasn't isolated.

## Step 5 — Suggest a Fix

### For FLAKY TEST
Suggest a broader validation description and provide the exact rewrite:

```yaml
# Before (too specific):
- type: validation
  step_description: Only beetle products are displayed including Hercules Beetle, Goliath Beetle, and Stag Beetle

# After (broader):
- type: validation
  step_description: Only beetle-type products are displayed, no non-beetle products are showing
```

If the user wants to apply the fix, use `revyl test push <test-name> --force` after updating the YAML.

### For REAL BUG
Describe the defect clearly:
- **What:** What the user did (action)
- **Expected:** What should have happened
- **Actual:** What actually happened (from `validation_reasoning`)
- **Severity:** How impactful is this? (cart/checkout bugs are high severity)

### For INFRA ISSUE
Suggest re-running the test: `revyl test run <test-name>`. If it fails again with 0/0, the issue is persistent and needs investigation.

### For TEST IMPROVEMENT
Suggest splitting combined steps, making validations more specific about the exact element to check, or restructuring the test flow to isolate the behavior.

## Recipes

### Single Test Analysis

```bash
# 1. Get the report
revyl test report <test-name> --json

# 2. Read the JSON, find failed validations
# 3. For each: read validation_reasoning, classify, suggest fix
```

### Workflow-Wide Triage

```bash
# 1. Get the workflow summary
revyl workflow report <workflow-name>

# 2. Identify which tests failed from the summary table
# 3. For each failed test, get detailed report:
revyl test report <failed-test-1> --json
revyl test report <failed-test-2> --json

# 4. Classify each failure independently
# 5. Produce a triage summary table:
#    | Test | Failure | Classification | Confidence | Action |
```

### Auto-Fix Flaky Tests

```bash
# 1. Analyze and identify FLAKY TEST failures
# 2. Read the current test YAML (from revyl test report or local file)
# 3. Rewrite the validation description to be broader
# 4. Push the updated test:
revyl test push <test-name> --force

# 5. Re-run to verify:
revyl test run <test-name>
```

## Output Format

When presenting analysis results, use this structure:

```
## Test: <test-name>
**Result:** <PASS/FAIL> (<validations_passed>/<total_validations> validations, <passed_steps>/<total_steps> steps)

### Failed Step <N>: <description>
**Classification:** <REAL BUG | FLAKY TEST | INFRA ISSUE | TEST IMPROVEMENT> (<confidence>)

**What the AI saw:**
> <validation_reasoning summary>

**Analysis:**
<Your reasoning for the classification>

**Suggested fix:**
<Specific actionable recommendation>
```

## Tips

- Always read `validation_reasoning` first — it's the AI's eyewitness account of what was on screen
- Compare the `description` (what was expected) against `validation_reasoning` (what was seen) — the gap tells you the classification
- If `reflection_reasoning` on surrounding steps shows the app responding correctly to inputs, the app is probably fine and the test is flaky
- A test with all instructions passing but a validation failing is more likely flaky or a real bug; a test with instruction failures is more likely infra or navigation issues
- When triaging a workflow, handle INFRA ISSUEs first (re-run them), then REAL BUGs (file them), then FLAKY TESTs (fix and re-push)
- Confidence should be HIGH when the evidence clearly points one way, MEDIUM when there's ambiguity, LOW when you'd want to see screenshots to be sure
