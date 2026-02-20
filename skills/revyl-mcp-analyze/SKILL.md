---
name: revyl-mcp-analyze
description: Analyze failed Revyl MCP test executions and classify them as real bugs, flaky tests, infra issues, or test-design improvements.
---

# Revyl MCP Analyze Skill

## Evidence Flow

1. Start from task ID after `run_test`.
2. Pull status payload:
   - `get_test_status(task_id="...")`
3. Inspect failed step expectations and observed reasoning.
4. Classify:
   - REAL BUG
   - FLAKY TEST
   - INFRA ISSUE
   - TEST IMPROVEMENT
5. Recommend exact next tool/action.

## Classification Rules

- REAL BUG: actions complete but app behavior contradicts expected outcome.
- FLAKY TEST: app behavior is acceptable but validation is too brittle.
- INFRA ISSUE: no meaningful app steps executed or setup/device/build failure.
- TEST IMPROVEMENT: test structure is weak or allows ambiguity.

## Required Output

```text
Task: <task-id>
Classification: <REAL BUG | FLAKY TEST | INFRA ISSUE | TEST IMPROVEMENT>
Confidence: <HIGH | MEDIUM | LOW>
Evidence:
- Expected: <summary>
- Observed: <summary>
- Why: <short rationale>
Next action:
- <specific update_test / rerun / bug filing action>
```

