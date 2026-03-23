# YAML Tests

Define, version-control, and sync your Revyl tests as YAML files.

## Quick start

```bash
revyl init                          # creates .revyl/config.yaml
revyl test create login-flow        # creates .revyl/tests/login-flow.yaml
# edit the YAML file
revyl test validate                 # check syntax
revyl test push                     # push to Revyl
revyl test run login-flow           # run it
```

## Project structure

```
your-app/
  .revyl/
    config.yaml              # project config (see config.yaml example)
    tests/
      login-flow.yaml        # one file per test
      checkout.yaml
```

## Test format

Every test file has `test.metadata`, `test.build`, and `test.blocks`:

```yaml
test:
  metadata:
    name: my-test
    platform: ios            # ios or android
  build:
    name: my-app-build       # build name in Revyl
  blocks:
    - type: instructions
      step_description: "Tap the Login button"
    - type: validation
      step_description: "The home screen is visible"
```

## Block types

| Type | Purpose | Required fields |
|------|---------|-----------------|
| `instructions` | Perform an action | `step_description` |
| `validation` | Assert something is true | `step_description` |
| `extraction` | Extract data into a variable | `step_description`, `variable_name` |
| `manual` | Built-in actions (wait, navigate, etc.) | `step_type`, `step_description` |
| `if` | Conditional branch | `condition`, `then` (blocks) |
| `while` | Loop | `condition`, `body` (blocks) |
| `code_execution` | Run a script | `script` or `step_description` |
| `module_import` | Reuse a shared module | `module` or `module_id` |

## Variables

Use `{{variable-name}}` (kebab-case) in step descriptions:

```yaml
test:
  metadata:
    name: login
    platform: ios
  build:
    name: my-app
  variables:
    username: "testuser@example.com"
    password: "secret123"
  blocks:
    - type: instructions
      step_description: "Enter '{{username}}' in the email field"
```

## Syncing

```bash
revyl test push          # local -> remote
revyl test pull          # remote -> local
revyl test diff          # show differences
revyl test push --force  # overwrite remote
```

## Examples

- [`config.yaml`](config.yaml) -- project configuration
- [`login-flow.yaml`](login-flow.yaml) -- simple login test
- [`checkout-with-variables.yaml`](checkout-with-variables.yaml) -- variables and env vars
- [`conditional-flow.yaml`](conditional-flow.yaml) -- if/while control flow
