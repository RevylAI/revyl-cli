# Manual and BDD Cases: Gherkin, CSV and Test-Management Exports, Docs

Load this after the root skill detects Gherkin, a spreadsheet or export of test
cases, or prose test documents. The root skill's plan, mapping rules, data rules
and validation pitfalls still apply.

Manual cases are already intent-level, which makes them the easiest source, but
they often leave the start state, data and expected copy implicit. Most of the
work is making those explicit and flagging what is missing.

## Read the Export's Structure

Identify the columns or fields before mapping. Common shapes:

| Source | Case fields | Step fields |
| --- | --- | --- |
| TestRail CSV/XML | ID (`C123`), Title, Section, Priority, Type, Preconditions, References | `Steps` and `Expected Result` (text), or separated `Steps (Step)` / `Steps (Expected Result)` rows |
| Zephyr Scale / Squad | Key, Name, Folder, Objective, Precondition, Labels | Test Script (Step-by-Step): Step, Test Data, Expected Result |
| Xray | Issue key, Summary, Pre-condition | Action, Data, Expected Result |
| qTest, Azure Test Plans, spreadsheets | Name, Description, Precondition | Step Description, Expected Result |

Multi-step cases often span several rows with an empty case column on
continuation rows; group them by the last non-empty case ID. Preserve the case
ID in the Revyl test's comment header and in a tag when the user wants
traceability.

## Mapping

| Source | Revyl |
| --- | --- |
| Case title | Test name (lowercase, stable, prefixed if requested) and `metadata.description` |
| Section, folder, labels, priority, Gherkin `@tags` | `metadata.tags` (lowercase, no spaces) |
| Preconditions / Pre-condition / Objective | Plan row: state the run must start in. Logged-in state becomes the login module; data state becomes a variable, `code_execution` script or CI step; anything else goes to REVIEW |
| Step / Action | `instructions`, merged with adjacent steps that serve one goal |
| Test Data / Data | Inline literal if harmless and stable; `{{variable}}` if it varies; `{{global.x}}` if secret |
| Expected Result | `validation` placed right after its step, quoting exact copy when given |
| Expected Result that is vague ("works", "page loads") | A validation of the screen's purpose ("the cart shows the added item"), plus REVIEW asking for the exact expectation |
| Steps that need a human (verify an email, check a printed receipt, judge visual quality, call support) | Leave out or replace with what is visible on device; REVIEW |
| Duplicate or near-identical cases | One Revyl test; record the merge in the plan |

## Gherkin

| Gherkin | Revyl |
| --- | --- |
| `Feature` | Group of tests; the feature name prefixes test names or becomes a tag |
| `Background` | Shared setup: a module when it is UI steps (login), otherwise variables or a script. Import it first in each scenario's test. "The app is freshly installed" and "I am on the home screen" need no block, because every run starts from a fresh install on the launch screen |
| `Scenario` | One Revyl test |
| `Given` | Start state: module import, variables, setup script, or an instruction that reaches the start screen |
| `When` / `And` after `When` | `instructions`, one per user goal |
| `Then` / `And` after `Then` / `But` | `validation` |
| `Scenario Outline` + `Examples` | By default one test with a `{{variable}}` per placeholder, declared under `test.variables` with the first row as defaults; list one `revyl test run <test> --var ...` per remaining row. Use one test per row instead when rows change the path or the expected screen |
| Step definitions code | Read it when the step text hides behavior ("Given a premium user" may call an API); map that behavior, not the phrase |
| Doc strings and data tables | Inline in the instruction when short; otherwise variables |

## Plain-Language Documents

For READMEs, wikis, QA checklists or acceptance criteria: split into cases at
each separately verifiable outcome, write the inferred steps and expectations
into the plan for the user to confirm, and mark every inferred expectation in
REVIEW. Do not invent assertions the document does not state.

## Example

Source (TestRail CSV, two rows of case C1042):

```text
ID,Title,Preconditions,Steps,Expected Result
C1042,Filter shop by category,User is on the shop home screen,Tap the Beetles chip,Only beetles are listed
,,,Tap All Bugs,All products are listed again
```

Revyl:

```yaml
# Source: testrail-export.csv:C1042
test:
  metadata:
    name: filter-shop-by-category
    platform: ios
    tags: [c1042]
  build:
    name: "<Revyl app name>"
  blocks:
    - type: instructions
      step_description: "Filter the shop to the 'Beetles' category."
    - type: validation
      step_description: "Only beetle products are listed."
    - type: instructions
      step_description: "Clear the filter by choosing 'All Bugs'."
    - type: validation
      step_description: "Products from every category are listed again."
```
