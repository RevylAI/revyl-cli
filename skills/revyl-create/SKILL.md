# Revyl Test Authoring Skill

Create high-quality E2E mobile tests by analyzing an app's source code, planning test coverage, and writing YAML tests that catch real bugs. This skill teaches the full methodology — from reading source code to iterating on test results.

## When to Use

- When asked to create E2E tests for a mobile app
- When asked to improve test coverage or write regression tests
- When an app's source code is available for analysis
- When converting manual QA steps or bug reports into automated tests

## Phase 1 — Discover the App (Source Code Analysis)

Before writing any test YAML, systematically read the app's source code. This is what separates tests that catch bugs from tests that just exercise happy paths.

### 1a. Screen Inventory

Find every screen/page in the app. What to look for depends on the framework:

- **React Native / Expo**: React Navigation stacks, tab bars, drawers (`createStackNavigator`, `Tab.Navigator`), Expo Router file-based routing (`app/` directory)
- **Flutter**: `Navigator`, `GoRouter`, `MaterialApp` routes, named routes in `onGenerateRoute`
- **SwiftUI**: `NavigationStack`, `TabView`, `NavigationLink`, `.sheet()` / `.fullScreenCover()` modifiers
- **Kotlin / Jetpack Compose**: `NavHost`, `composable()` destinations, `BottomNavigation`
- **UIKit / Storyboard**: `UINavigationController`, `UITabBarController`, segues, storyboard files

Also look for:
- **Route definitions**: Route arrays, deep link configs, URL scheme handlers
- **Screen components**: Files in `screens/`, `pages/`, `views/`, or `features/` directories

Build a map:

| Screen Name | File Path | What It Shows | Entry Points |
|---|---|---|---|
| Home | `app/(tabs)/index.tsx` | Main content feed | Tab bar, app launch |
| Product Detail | `app/product/[id].tsx` | Item info, purchase button | Tap item from list |
| Cart | `app/cart/index.tsx` | Cart items, checkout | Cart icon, add to cart |

### 1b. Navigation Graph

Trace how screens connect:
- Tab bars (what tabs exist, which is default)
- Stack navigators (push/pop relationships)
- Modal screens (presented over other screens)
- Deep links (URL schemes that jump to specific screens)

Identify: What screen does the app open to? What are dead ends? What flows require going "back"?

### 1c. User Flows

Map the critical paths users take through the app:

1. **Golden paths** (most common): Browse → Product → Add to Cart → Checkout → Order Confirmation
2. **Secondary paths**: Search → Results → Product Detail
3. **Edge paths** (rare but important): Filter → Empty results, Cart → Remove all items → Empty cart

For each flow, note the sequence of screens and the actions that transition between them.

### 1d. Data Flows

Trace where data comes from and how it transforms — this is where bugs hide:
- **API calls**: What endpoints does the app hit? What data does it display?
- **Local state**: Cart state, form state, filter state — how is it managed?
- **Derived values**: Tax calculations, totals, formatted prices, filtered lists
- **Data passed between screens**: Does the product detail page receive the full product or just an ID?

### 1e. UI Element Labels

Find the exact text shown in the UI by reading the source:
- Button labels: `"ADD TO CART"`, `"PLACE ORDER"`, `"Sign In"`
- Placeholder text: `"Search products..."`, `"Enter email"`
- Section headers, tab labels, navigation titles
- Status messages: `"Order placed!"`, `"No results found"`

Use these exact strings in test instructions and validations.

### 1f. Edge Cases from Code

Search the source for test targets:
- **Conditional rendering**: `if`, ternary operators, `switch` — each branch is a potential test scenario
- **Error handling**: `try/catch`, error states, fallback UI — test that errors display correctly
- **Empty states**: What happens when a list has no items? When the cart is empty?
- **Loading states**: Spinners, skeleton screens — these exist but should NOT be validated (they're transient)
- **Permission checks**: Login guards, role-based access
- **Boundary conditions**: Max cart quantity, very long text, special characters in search

### 1g. Known Issues

Search for comments that reveal test targets:
```
TODO, FIXME, HACK, BUG, XXX, WORKAROUND
```

Each of these is a potential test scenario — the developer already knows something is wrong or fragile.

## Phase 2 — Plan Test Coverage

Before writing any YAML, create a test plan. This prevents writing redundant tests and ensures coverage of high-risk areas.

### Prioritize by Risk

Not all flows are equal. Prioritize by user impact:

1. **Critical** (must test): Cart, checkout, payment, authentication — bugs here lose money or lock users out
2. **High** (should test): Search, filtering, navigation — bugs here frustrate users
3. **Medium** (nice to test): Settings, profile, cosmetic features — bugs here are annoying but not blocking
4. **Low** (test if time permits): Edge cases in non-critical flows

### One Test = One User Story

Each test should represent a complete user journey, not a unit check:
- GOOD: `add-to-cart` — user browses, selects product, adds to cart, verifies cart contents
- BAD: `button-renders` — check if a button exists (this is a unit test, not E2E)

### Plan the Coverage

For each critical flow, plan at minimum:
- **Happy path**: The flow works correctly end to end
- **One error path**: Something goes wrong (wrong data, missing items, failed validation)
- **One boundary condition**: Edge of expected behavior (empty state, max items, special characters)

### Identify Shared Modules

If 3+ tests share the same opening flow (e.g., login, navigate to a specific screen), plan a module for it. Check existing modules first:

```bash
revyl module list
```

### Output: Test Plan Table

| Test Name | Flow Being Tested | What Bug It Would Catch | Key Validations |
|---|---|---|---|
| `add-to-cart` | Browse → Product → Cart | Wrong product added, wrong price | Product name and price in cart |
| `filter-category` | Shop → Filter by category | Filter shows wrong items | Only filtered items visible |
| `search-rare` | Search → Trending tag → Results | Search tag doesn't filter correctly | Results match the tag |
| `checkout-flow` | Cart → Checkout → Order | Payment calc wrong, order not placed | Order confirmation, correct total |

## Phase 3 — Write Tests (The Golden Rules)

### Rule 1: KISS — The Executing Agent Is Not a Reasoning Model

The AI agent that runs your test is following instructions literally. Keep it simple:

- **Flat `instructions` blocks**. One clear sentence per action.
- **Avoid `if`/`while`** unless absolutely necessary — they can render as empty arrays and confuse execution. Use them only for genuinely variable flows (e.g., dismiss an optional popup).
- **Fewer blocks = better**. Every block is a chance for misinterpretation. If three steps always happen together, consider whether they can be one instruction.
- **Think like a user, not a developer**: "Tap the Login button" not "Invoke the auth handler"

### Rule 2: Write Validations That Catch Bugs, Not Flakiness

Validation descriptions are the #1 source of flaky tests. Get them right:

**Validate OUTCOMES, not transient states:**
```yaml
# BAD — spinner vanishes before screenshot capture
- type: validation
  step_description: "A loading spinner is shown"

# GOOD — validate the result, not the loading state
- type: validation
  step_description: "Search results are displayed"
```

**Use category-level descriptions, not exact item lists:**
```yaml
# BAD — if any valid beetle isn't in this list, the test fails incorrectly
- type: validation
  step_description: "Only Hercules Beetle, Goliath Beetle, and Stag Beetle are shown"

# GOOD — catches the real bug (non-beetles showing) without false failures
- type: validation
  step_description: "Only beetle products are displayed, no non-beetle products are showing"
```

**Validate data integrity, not just presence:**
```yaml
# OK — checks the product exists
- type: validation
  step_description: "The cart contains an item"

# BETTER — catches wrong-product and wrong-price bugs
- type: validation
  step_description: "The cart shows Orchid Mantis at $62.00"
```

**Separate verify from action — NEVER combine them:**
```yaml
# BAD — the AI may tap the button without strictly verifying the total
- type: instructions
  step_description: "Verify the total is $77.76 then tap PLACE ORDER"

# GOOD — validation is a separate, enforced check
- type: validation
  step_description: "The order total displays $77.76 including tax"
- type: instructions
  step_description: "Tap the PLACE ORDER button"
```

**Use negative validations to catch unexpected errors:**
```yaml
- type: validation
  step_description: "No error messages or alerts are displayed"
```

### Rule 3: Source-Informed Precision

Use what you learned in Phase 1:

- **Exact text from source**: If the button says `"ADD TO CART"` in the source, write `"ADD TO CART"`, not `"Add to Cart"` or `"add to cart"`
- **Reference visible labels**: Never reference internal component names or test IDs — the AI agent sees the screen like a user
- **Note scroll position**: If an item is near the bottom of a list (from source data), add a scroll instruction before tapping it

### Rule 4: Don't Add open_app

The app opens automatically when a test starts. Never begin with a manual `open_app` block unless you're explicitly testing an app-switching or relaunch scenario.

### Rule 5: Variables and Extraction

Use `{{variable-name}}` (kebab-case, must be defined before use):

```yaml
blocks:
  - type: extraction
    step_description: "Extract the order confirmation number from the screen"
    variable_name: "order-number"

  - type: validation
    step_description: "The confirmation number {{order-number}} is displayed in the order history"
```

Use `code_execution` for server-side setup (API calls, database seeding, generating test data).

## Phase 4 — Create and Push

### CLI Workflow

```bash
# Create the test (registers it with Revyl)
revyl test create <name> --platform ios

# Edit the YAML locally
# File is at .revyl/tests/<name>.yaml

# Push your changes
revyl test push <name> --force
```

### MCP Workflow

```
# 1. Check for reusable modules
list_modules()

# 2. Get a module snippet if needed
insert_module_block(name="login-flow")

# 3. Validate your YAML before creating
validate_yaml(content="...")

# 4. Create the test with YAML content
create_test(name="add-to-cart", platform="ios", yaml_content="...")

# 5. Update later if needed
update_test(test_name_or_id="add-to-cart", yaml_content="...", force=true)
```

### Pre-Push Checklist

Before creating or pushing a test, verify:

1. Login steps include credentials (or use variables/modules)
2. All `{{variables}}` are extracted before use
3. Validations describe VISIBLE elements only
4. Instructions are specific enough to be actionable
5. Test does NOT assume pre-existing app state (e.g., items already in cart)
6. Platform is specified (`ios` or `android`)
7. Build name matches a configured app
8. No `open_app` block at the start (unless testing app launch)
9. No combined verify+action instructions

## Phase 5 — Run, Analyze, Improve

Tests are never done after the first write. Use the feedback loop:

```
Write test → Run → Analyze failures → Fix → Re-run
```

### Run the Test

```bash
revyl test run <name>
```

### Analyze Results

```bash
# Quick overview
revyl test report <name>

# Structured data for analysis
revyl test report <name> --json
```

Use the **revyl-analyze** skill to classify failures. The four categories:

| Classification | What It Means | Action |
|---|---|---|
| **REAL BUG** | The app has a defect | File the bug. The test is good. |
| **FLAKY TEST** | App is correct but validation is too specific | Broaden the validation description, push, re-run |
| **INFRA ISSUE** | Test couldn't run (device/build problem) | Re-run. If persistent, investigate infra. |
| **TEST IMPROVEMENT** | Test design weakness (combined steps, vague checks) | Restructure the test, push, re-run |

### Fix and Iterate

```bash
# Edit the local YAML file
# Push the updated test
revyl test push <name> --force

# Re-run to verify the fix
revyl test run <name>
```

Iterate until the test:
- **Passes** on correct app behavior
- **Fails** on actual bugs (not flaky validations)

## YAML Quick Reference

### Core Block Types

| Block Type | Fields | Example |
|---|---|---|
| `instructions` | `type`, `step_description` | `step_description: "Tap the Login button"` |
| `validation` | `type`, `step_description` | `step_description: "The dashboard is displayed"` |
| `extraction` | `type`, `step_description`, `variable_name` | `variable_name: "otp-code"` |

**Built-in retry**: Both `instructions` and `validation` blocks have built-in retry logic. The execution agent will automatically re-attempt actions and re-check validations before failing. You do NOT need to add manual wait blocks for normal loading times — the retry handles it.

### Manual (System-Level) Block Types

Manual blocks perform system-level actions outside the app UI. Use these when you need to control the device or environment directly.

| Step Type | Fields | What It Does | When to Use |
|---|---|---|---|
| `wait` | `step_description: "3"` | Pause execution for N seconds | Only after `kill_app` (app needs restart time), or when a previous test run failed due to a significant timing issue. **Do not** add waits "just in case" — the built-in retry on instructions/validations handles normal delays. |
| `navigate` | `step_description: "myapp://settings"` | Open a URL or deep link | Testing deep links, jumping directly to a screen via URL scheme, opening external links |
| `open_app` | `step_description: "com.other.app"` | Launch an app by bundle ID | Switching to a **different** app mid-test (e.g., testing share-to-another-app). The test app opens automatically at start — never use this for the test app itself. |
| `kill_app` | (no step_description needed) | Force-quit the current app | Testing app restart behavior, clearing in-memory state, verifying data persistence after kill |
| `go_home` | (no step_description needed) | Press the device home button | Testing app backgrounding, verifying notification behavior, switching away from the app |
| `set_location` | `step_description: "37.7749,-122.4194"` | Set the device's GPS location (lat,lng) | Testing location-dependent features (store locators, delivery zones, geo-fenced content) |

**Example — testing a deep link:**
```yaml
- type: manual
  step_type: navigate
  step_description: "myapp://product/123"
- type: validation
  step_description: "The product detail page is displayed for the correct item"
```

**Example — testing app restart persistence:**
```yaml
- type: instructions
  step_description: "Add an item to the cart"
- type: manual
  step_type: kill_app
- type: manual
  step_type: wait
  step_description: "3"
- type: manual
  step_type: open_app
- type: validation
  step_description: "The cart still contains the previously added item"
```

**Example — testing location-based features:**
```yaml
- type: manual
  step_type: set_location
  step_description: "40.7128,-74.0060"
- type: validation
  step_description: "The app shows New York area results"
```

### Control Flow & Advanced Block Types

| Block Type | Fields | Example |
|---|---|---|
| `if` | `type`, `condition`, `then`, `else` (optional) | Conditional branching |
| `while` | `type`, `condition`, `body` | Loop until condition is false |
| `code_execution` | `type`, `step_description` (script UUID), `variable_name` (optional) | Server-side script execution |
| `module_import` | `type`, `step_description` (name), `module_id` (UUID) | Import reusable block group |

### Full Test Template

```yaml
test:
  metadata:
    name: "test-name"
    platform: "ios"
    tags:
      - "smoke"
  build:
    name: "app-name"
  blocks:
    - type: validation
      step_description: "The main screen is displayed"

    - type: instructions
      step_description: "Tap on the first item"

    - type: validation
      step_description: "The item detail screen is displayed with the correct name and price"

    - type: instructions
      step_description: "Tap the ADD TO CART button"

    - type: validation
      step_description: "The cart contains the item with the correct price"
```

## Recipes

### Recipe: Full App Test Suite from Source Code

The end-to-end workflow for analyzing an app and creating a complete test suite.

**Step 1 — Discover**
```
Read the app source code following Phase 1:
  - Map all screens (navigation config, route files)
  - Trace user flows (golden paths + edge paths)
  - Note exact UI labels from source
  - Identify edge cases from conditional logic
```

**Step 2 — Plan**
```
Create a test plan table (Phase 2):
  - Prioritize flows by risk (checkout > search > cosmetic)
  - One test per user story
  - Plan happy path + error path + boundary for critical flows
  - Check for existing modules: revyl module list
```

**Step 3 — Write and Create**
```
For each test in the plan:
  1. Write the YAML following the golden rules (Phase 3)
  2. Create: revyl test create <name> --platform ios
  3. Push: revyl test push <name> --force
```

**Step 4 — Group into Workflow**
```
After all tests are created:
  - Add them to a workflow for batch execution
  - Run the full suite: revyl workflow run <workflow-name>
```

**Step 5 — Iterate**
```
Analyze the full suite results:
  - revyl workflow report <workflow-name> --json
  - Classify each failure (revyl-analyze skill)
  - Fix flaky tests, file real bugs
  - Re-run until suite is green on correct app behavior
```

### Recipe: Single Feature Test

Read one feature's source code and create one targeted test.

```
1. Identify the feature's files (screen component, state management, API calls)
2. Map the user flow for that feature
3. Note exact labels and expected outcomes from source
4. Write a test covering happy path + one edge case
5. Create, push, run, iterate
```

### Recipe: Bug Reproduction Test

Given a bug report, create a test that reproduces it.

```
1. Read the bug report: what steps reproduce it? What's the expected vs actual behavior?
2. Find the relevant source code to understand the root cause
3. Write a test that follows the reproduction steps exactly
4. The validation should check for the EXPECTED behavior (so the test fails when the bug is present)
5. Run to confirm the test fails (proves the bug exists)
6. After the bug is fixed, the test becomes a regression test (passes on correct behavior)
```

### Recipe: Convert Manual QA Steps to Revyl Test

Take a list of manual test steps and convert them to YAML.

```
1. Read the manual steps
2. Map each step to a block type:
   - "Click X" / "Tap X" / "Enter Y" → instructions
   - "Verify X" / "Check that X" / "Assert X" → validation
   - "Wait 3 seconds" → manual (wait)
   - "Note the value of X" → extraction
3. Read the source code to get exact UI labels for each step
4. Write broad validations (don't copy the QA doc's exact wording if it's too specific)
5. Create, push, run, iterate
```

## Anti-Patterns (What NOT to Do)

These are real lessons from test authoring — each one caused test failures or missed bugs.

### Don't list exact items in validations when the set can vary

The `filter-category` lesson: A validation that lists specific product names fails when the app correctly shows additional valid products not in the list.

```yaml
# BAD
- type: validation
  step_description: "Only Hercules Beetle, Goliath Beetle, and Stag Beetle are shown"

# GOOD
- type: validation
  step_description: "Only beetle products are displayed, no non-beetle products are showing"
```

### Don't combine "verify then tap" in one instruction

The `goliath-checkout` lesson: When verify and action are in the same instruction block, the AI may execute the action without strictly checking the verification condition. The verification becomes advisory, not enforced.

```yaml
# BAD — AI may tap without verifying the total
- type: instructions
  step_description: "Verify the 'PLACE ORDER' button displays the total '$77.76' including tax, then tap it"

# GOOD — validation is enforced before proceeding
- type: validation
  step_description: "The PLACE ORDER button displays the total $77.76 including tax"
- type: instructions
  step_description: "Tap the PLACE ORDER button"
```

### Don't use if/while for simple flows

They can render as empty block arrays, confusing the execution agent. Use them only for genuinely variable scenarios (e.g., dismissing an optional popup that may or may not appear).

```yaml
# BAD — unnecessary conditional
- type: if
  condition: "Is the shop screen visible?"
  then:
    - type: instructions
      step_description: "Tap on the first product"

# GOOD — just assert and act
- type: validation
  step_description: "The shop screen is displayed"
- type: instructions
  step_description: "Tap on the first product"
```

### Don't validate transient states

Loading spinners, "Searching..." text, and progress indicators vanish in seconds. By the time the AI captures the screen, they're gone.

```yaml
# BAD — spinner is gone before the screenshot
- type: validation
  step_description: "A loading spinner is displayed"

# GOOD — validate the result that appears after loading
- type: validation
  step_description: "The search results are displayed"
```

### Don't assume scroll position

Be explicit about scroll direction and distance when targeting items that may not be visible on the initial viewport.

```yaml
# BAD — item may be off-screen
- type: instructions
  step_description: "Tap on Orchid Mantis"

# GOOD — scroll first, then tap
- type: instructions
  step_description: "Scroll down on the product list to find Orchid Mantis and tap on it"
```

### Don't start with open_app

The app opens automatically. Adding `open_app` as the first block can cause a double-launch.

```yaml
# BAD
blocks:
  - type: manual
    step_type: open_app
    step_description: "com.example.app"
  - type: instructions
    step_description: "Tap Login"

# GOOD
blocks:
  - type: instructions
    step_description: "Tap Login"
```

## Tips

- Start with Phase 1 even if you think you know the app — source code reveals edge cases you'd never find by guessing
- When in doubt about a validation, make it broader — you can always tighten it later after a passing run
- **Don't add wait blocks preemptively** — instructions and validations have built-in retry logic that handles normal loading times. Only add a `wait` block if a previous test run failed due to a significant timing issue (e.g., a slow API call or animation), or after a `kill_app` block where the app needs time to restart
- Use `set_location` to test geo-dependent features, `navigate` to test deep links, and `open_app` to test multi-app flows — these system-level capabilities are often overlooked but catch important bugs
- Use the `revyl-analyze` skill after every test run to classify failures systematically
- Check `revyl module list` before writing login/onboarding steps — there may already be a module for it
- Use `validate_yaml()` (MCP) or `revyl test validate` (CLI) to catch syntax errors before running
- Tag tests with categories (`smoke`, `regression`, `checkout`) so you can run targeted suites
- Keep test names descriptive and kebab-case: `add-to-cart`, `filter-category`, `search-rare`
