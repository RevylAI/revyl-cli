# Mobile Automation Code: Maestro, Appium, XCUITest, Espresso, Detox

Load this after the root skill detects native or cross-platform mobile
automation. The root skill's plan, mapping rules, data rules and validation
pitfalls still apply. If the user only wants the existing Maestro or Appium
suite to run on Revyl devices, see the root skill's "Do Not Convert" section
first.

## Read Before Mapping

1. Each flow or test class, plus the subflows, helpers and robots it calls.
2. Setup: Maestro `onFlowStart`, `runFlow` login subflows and `env`; XCTest
   `setUp` and `launchArguments`/`launchEnvironment`; Espresso rules and
   `@Before`; Detox `beforeAll` and `device.launchApp` params; Appium
   capabilities such as `noReset`, `autoGrantPermissions`, `appium:app`.
3. The app's string resources or source for any accessibility ID or test ID you
   need to turn into a visible label.

## Mapping

| Source construct | Revyl |
| --- | --- |
| Maestro `appId`; Appium `bundleId`/`appPackage` | Identifies the app; pick the matching Revyl app for `build.name` |
| `launchApp` at the start, `clearState`, `app.launch()`, `device.launchApp({newInstance})` | Drop. Each Revyl run installs the app fresh and launches it |
| `launchApp`/`stopApp` mid-flow, `terminate()`, `device.reloadReactNative` | `manual` `kill_app` then `open_app` (empty value) |
| `launchArguments`, `launchEnvironment`, Detox `launchArgs`, Maestro `launchApp.arguments` | Not YAML steps. Run-level `--launch-arg`, `--launch-env` or org launch variables (`revyl-cli-create` Tool Map); REVIEW when they carry auth |
| `tapOn`, `inputText`, `eraseText`, `.tap()`, `typeText`, `perform(click())`, `element(by.id).tap()` chains for one goal | One `instructions` block naming the goal and visible labels |
| `tapOn: { id: ... }`, `accessibilityIdentifier`, `withId(R.id.x)`, `by.id('x')`, `~id` | Use the visible label from app source or strings; else the purpose plus REVIEW |
| `assertVisible`, `XCTAssertTrue(el.exists)`, `check(matches(isDisplayed()))`, `expect(el).toBeVisible()` | `validation` quoting the text |
| `assertNotVisible`, `waitForNonExistence`, `doesNotExist()`, `not.toBeVisible()` | `validation` that the item is not shown |
| `scrollUntilVisible`, `swipe` to find, `scrollTo()`, `whileElement(...).scroll` | Drop; name the target and the runner scrolls. Keep an explicit swipe only when the swipe itself is the behavior under test |
| `extendedWaitUntil`, `waitForAnimationToEnd`, `waitForExistence(timeout:)`, `waitFor(...).withTimeout`, IdlingResources | Drop the wait. When it waits for a named element that marks a checkpoint, keep that element as a `validation` |
| `eraseText`, `clearText()`, `replaceText`, `clearAndType` before input | Say "replace any existing value in <field> with ..." in the instruction; forms may be prefilled |
| Relational selectors (`below:`, `rightOf:`, `childOf:`, `withAncestor`, `hasDescendant`) | Describe the relation in words ("the value under 'ORDER ID'"); use an `extraction` when the step reads it |
| Maestro regex text (`".*4242.*"`), escaped literals (`"\\$45.00"`), `index:` | Plain language: "contains '4242'", "$45.00"; replace an index with the target's purpose and add REVIEW |
| `back`, `pressBack()`, `device.pressBack()` | Instruction "Go back to <screen>" |
| `pressKey: Home`, `XCUIDevice.shared.press(.home)` | `manual` `go_home` |
| `openLink`, `device.openURL`, deep link intents | `manual` `navigate` with the URL or deep link |
| `setLocation`, `device.setLocation` | `manual` `set_location` with `lat,lng` |
| System alerts (`addUIInterruptionMonitor`, permission taps) | An instruction to allow or deny the prompt when it appears, inside the step that triggers it |
| `runFlow` subflow, robot or helper reused in 2+ flows | A module; import it with `module_import` |
| `runFlow: { when: { visible: x } }`, `if` on element existence | Login wrapped this way becomes an unconditional sign-in (see the root skill). Otherwise, an "if <x> is shown, <goal>" clause inside the surrounding instruction when the branch is a few actions; an `if` with `then:` and a condition describing the screen when the branch holds assertions or several goals |
| `repeat`, loops over items | `while` with `body:` only when the count varies; otherwise state the goal ("add three items") |
| `copyTextFrom` + `${maestro.copiedText}`, reading a label into a variable | `extraction` with `variable_name`, then `{{name}}` |
| `${ENV_VAR}`, `env:` blocks, `--env` | Test variables with `--var` at run time, or `{{global.x}}` for shared secrets |
| `runScript`, `evalScript`, `http.post` setup | `code_execution` with a saved script, or a CI step before the run |
| `takeScreenshot` | Drop; reports keep screenshots. If it marks a visual check, add a `validation` |
| Platform branches (`platform: iOS`, `#if os(iOS)`) | One Revyl test per platform when the flows differ; one shared test when only selectors differ |

## Example

Source (Maestro):

```yaml
appId: com.example.shop
---
- launchApp:
    clearState: true
- runFlow: subflows/login.yaml
- tapOn:
    id: "tab-search"
- tapOn: "Search"
- inputText: "Beetle"
- assertVisible: "Hercules Beetle"
- tapOn: "Hercules Beetle"
- assertVisible: "\\$45.00"
```

Revyl:

```yaml
- type: module_import
  module: "shop-login"
- type: instructions
  step_description: "Open the Search tab and search for 'Beetle'."
- type: validation
  step_description: "'Hercules Beetle' appears in the search results."
- type: instructions
  step_description: "Open the 'Hercules Beetle' result."
- type: validation
  step_description: "The product page shows 'Hercules Beetle' and '$45.00'."
```
