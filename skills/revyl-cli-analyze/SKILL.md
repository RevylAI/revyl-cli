---
name: revyl-cli-analyze
description: Analyze failed Revyl test and workflow reports via CLI to classify real bugs, flaky tests, infra issues, or test-design improvements.
---

# Revyl CLI Failure Analysis Skill

## Quick Start

```bash
# 1) Pull structured evidence
revyl test report <test-name> --json

# 2) Classify failure
# REAL BUG | FLAKY TEST | INFRA ISSUE | TEST IMPROVEMENT

# 3) Apply fix and rerun
revyl test run <test-name>
```

For workflow-level triage:

```bash
revyl workflow report <workflow-name>
revyl workflow report <workflow-name> --json
```

## Decision Matrix

| Signal | Classification | Action |
|---|---|---|
| Instructions succeed but final state contradicts expected behavior | REAL BUG | File defect with expected vs actual evidence |
| App behavior acceptable but assertion wording too brittle | FLAKY TEST | Rewrite validation wording |
| No steps executed or setup failed | INFRA ISSUE | Re-run and inspect environment/device/build setup |
| Test structure allows false positives or combines verify+action | TEST IMPROVEMENT | Restructure YAML |

## Output Format

```text
Test: <name>
Result: <PASS/FAIL>
Failure Step: <order> - <description>
Classification: <REAL BUG | FLAKY TEST | INFRA ISSUE | TEST IMPROVEMENT>
Confidence: <HIGH | MEDIUM | LOW>
Evidence:
- Expected: <description>
- Observed: <reasoning summary>
- Why this classification: <short rationale>
Exact next action:
- <bug report details OR yaml rewrite OR infra rerun command>
Rerun command:
- revyl test run <test-name>
```

