# Web E2E Code: Playwright, Cypress, Selenium, WebdriverIO

Load this after the root skill detects browser automation code. The root skill's
plan, mapping rules, data rules and validation pitfalls still apply.

## Read Before Mapping

1. The spec files, in the order the runner would execute them.
2. Shared fixtures and hooks: `beforeEach`/`beforeAll`, Playwright fixtures,
   Cypress `support/` commands, WebdriverIO hooks. Login and seeding usually live
   there, not in the spec.
3. Page objects and custom commands the specs call. Their method bodies hold
   the visible labels and texts your steps should name.
4. Config: base URL per environment, viewport, projects (browsers or mobile
   emulation), retries. Only the base URL and viewport matter for Revyl.

## Choose the Target

- **Same web app.** Use `platform: web` and a Revyl Web app as `build.name`. The
  first step is usually a `manual` `navigate` to the start URL when it is not the
  app's landing page. Web tests allow only `wait`, `navigate`, `back` and `end`
  manual steps.
- **A mobile app with the same flows.** Use `platform: ios` or `android`. Web
  navigation (URLs, header links, browser back) becomes the app's navigation
  (tabs, rows, the back button). Keep the source step titles as comments and add
  REVIEW items where the app's path or copy is assumed.

## Mapping

| Source construct | Revyl |
| --- | --- |
| `test.step('<title>')`, `describe`/`it` titles | A `# <title>` comment, then the blocks for that goal |
| `page.goto(url)`, `cy.visit`, `driver.get`, `browser.url` | Web: `manual` `navigate` with the URL, using a variable for the host if it varies by environment. Mobile: drop if it opens the start screen, else an instruction to reach that screen |
| `click`, `fill`/`type`, `check`, `selectOption`, `hover` chains for one goal | One `instructions` block naming the goal and visible labels. `fill`, `clear()` and `cy.clear` replace existing text, so say "replace any existing value" |
| `getByRole('button', { name })`, `getByText`, `getByLabel` | Use the `name`/text as the visible label |
| `getByTestId`, `data-cy`, CSS, XPath, `By.id` | Find the label in the page object or app source; else describe the purpose and add REVIEW |
| Scoped locators (`card.getByText`, `.locator(...).filter({ hasText })`, `within`, `cy.get(x).find(y)`) | Name the container and the item in words ("the 'Starter Kit' card shows 'Shipped'") |
| `expect(...).toBeVisible()`, `should('be.visible')`, `assert ... is_displayed` | `validation` quoting the asserted text |
| `toHaveText`/`toContainText`, `should('contain', x)` | `validation` quoting `x` |
| `toHaveCount(n)` | `validation` saying "exactly n" |
| `not.toBeVisible()`, `should('not.exist')` | `validation` that the item is not shown |
| `toHaveURL`, `cy.url()` | Validate the screen the URL represents, not the URL; REVIEW if no visible equivalent |
| `page.goBack()`, `cy.go('back')`, a "Go back" button | Web: `manual` `back`. Mobile: one back tap may land somewhere else, so state the destination ("return to the Transactions list") and name that list or tab in the next validation |
| `waitFor*`, `cy.wait`, `sleep`, `setTimeout`, retries | Drop |
| `page.route`, `cy.intercept`, request stubs, `page.request`/`cy.request` | No device equivalent. Seeding via API becomes a `code_execution` script or CI step; mocks go to REVIEW |
| Cookies, local storage, `addInitScript`, auth `storageState` | Login module or test-only sign-in (`revyl-cli-auth-bypass`); REVIEW |
| New tabs, popups, iframes, file downloads, clipboard | Web: describe the user goal and add REVIEW. Mobile: usually not applicable; REVIEW |
| Screenshot or visual diff assertions | A `validation` of what the screenshot is meant to show, plus REVIEW |
| `test.skip`, `test.fixme`, commented-out `expect` | Leave out; REVIEW with the linked ticket if any |
| Page-object or fixture helpers you were not given (`orders.getLineItem(name, price)`, `login.login(email)`) | Ask for the source when it decides behavior (login); otherwise infer the visible meaning from the name and arguments and add REVIEW |
| `test.describe.parallel`, `test.each` / data tables | One Revyl test per data row that changes behavior, or one test with `{{variables}}` run with `--var` per row |

## Example

Source (Playwright, abbreviated):

```ts
await test.step('Open orders and check the shipment', async () => {
  await nav.clickProfileLink();
  await nav.clickOrdersLink();
  const card = orders.getShipmentCard('Starter Kit');
  await expect(card).toHaveCount(1);
  await expect(card.getByText('Shipped')).toBeVisible();
});
```

Revyl:

```yaml
# Open orders and check the shipment
- type: instructions
  step_description: "Open the Orders list from the profile menu."
- type: validation
  step_description: "Exactly one 'Starter Kit' shipment card is shown, with status 'Shipped'."
```
