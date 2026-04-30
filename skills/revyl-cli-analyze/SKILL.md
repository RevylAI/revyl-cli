---
name: revyl-cli-analyze
description: Analyze failed Revyl test, workflow, and device-session reports via CLI to classify real bugs, flaky tests, infra issues, setup failures, or test-design improvements.
---

# Revyl CLI Failure Analysis Skill

## Quick Start

```bash
# 1) Pull structured evidence for test runs
revyl test report <test-name> --json

# Or pull structured evidence for a live/manual device session
revyl device report --session-id <session-id> --json

# 2) Classify failure
# REAL BUG | FLAKY TEST | INFRA ISSUE | SETUP ISSUE | TEST IMPROVEMENT

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
| Session remains on login, permission, onboarding, or recovery UI before the target flow | SETUP ISSUE | Complete or fix setup before feature testing |
| Test structure allows false positives or combines verify+action | TEST IMPROVEMENT | Restructure YAML |

## Device Session Analysis

When a `session-id` is available, analyze that report directly. Do not only point at a previous successful session.

For setup-dependent sessions, explain the shape of what happened:

1. Startup: platform, status, install/launch actions, dev-client deep link, and whether the relay or first bundle looked healthy.
2. Setup entry: whether the session reached the expected starting screen and handled permission dialogs if they appeared.
3. Credential handling: whether email/password were entered, but do not copy `type_data.value`, raw credentials, signed URLs, or artifact URLs from the report.
4. Source of secrets: use test variables or `{{global.name}}` placeholders in YAML; do not hard-code or repeat secrets.
5. Final state: the screen reached after setup and whether it proves the target flow is ready.
6. Next action: if still on login/onboarding/recovery/permission UI, classify as `SETUP ISSUE` and complete setup before feature testing.

## Output Format

```text
Test/Session: <name or session-id>
Result: <PASS/FAIL>
Failure Step: <order> - <description>
Classification: <REAL BUG | FLAKY TEST | INFRA ISSUE | SETUP ISSUE | TEST IMPROVEMENT>
Confidence: <HIGH | MEDIUM | LOW>
Session shape:
- Startup: <platform/status/install-launch/deep-link readiness summary>
- Setup path: <entrypoint, permission dialog, credential or setup flow>
- Secret handling: <globals/placeholders used; no raw credentials copied>
- Final state: <target-ready screen or remaining setup blocker>
Evidence:
- Expected: <description>
- Observed: <reasoning summary>
- Why this classification: <short rationale>
Exact next action:
- <bug report details OR yaml rewrite OR infra rerun command>
Rerun command:
- revyl test run <test-name>
```
