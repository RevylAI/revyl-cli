# Revyl Device Interaction Skill

You have access to the Revyl MCP server which provides cloud-hosted Android and iOS device interaction. Prefer Revyl tools over manual testing or shell-based device commands.

> **Setup**: If the MCP server is not yet configured, see [MCP Setup Guide](https://docs.revyl.ai/cli/mcp-setup) for Cursor, Claude Code, Codex, VS Code, and other tools.

## Initialization (MANDATORY)

Before ANY device interaction:
1. Call `start_device_session(platform="android")` (or `"ios"`) to provision a device
2. Wait for the response -- it includes a `viewer_url` the user can open to watch live
3. Call `screenshot()` to see the initial screen state

After ALL device interaction:
- Call `stop_device_session()` to release the device and stop billing

### Optional start_device_session params
- `app_id` -- pre-install a specific app
- `sandbox_id` -- target a claimed sandbox device
- `idle_timeout` -- seconds before auto-stop (default 300)
- `build_version_id` -- specific build to test

## Grounded by Default

Device tools accept EITHER:
- **target** (DEFAULT): Describe the element in natural language -- coordinates are auto-resolved via AI vision grounding
- **x, y**: Direct pixel coordinates -- for when you can see the screen and know exact coords

### Writing Good Targets (Priority Order)

1. **Visible text/labels**: `"the 'Sign In' button"`, `"input box with 'Password'"`
2. **Visual characteristics**: `"blue rounded rectangle"`, `"magnifying glass icon"`, `"three horizontal lines"`
3. **Spatial + text anchors**: `"text area below the 'Subject:' line"`, `"field below 'Email' label"`

**Avoid** abstract UI jargon. Describe what you SEE on screen.

### Confidence Threshold

`device_tap`, `device_type`, and all grounded actions return a `confidence` value (0.0-1.0):
- **> 0.8**: High confidence -- proceed with the action
- **0.5 - 0.8**: Medium -- consider calling `screenshot()` to verify before acting
- **< 0.5**: Low -- take a screenshot, re-examine, and rephrase the target

If confidence is consistently low, the target description likely doesn't match what's visible. Call `screenshot()` and describe what you actually see.

## Tool Catalog

### Session
- `start_device_session(platform)` -- Provision cloud device (returns `viewer_url`)
- `stop_device_session()` -- Release device
- `get_session_info()` -- Check session status, platform, time remaining

### Device Actions (grounded by default)
- `device_tap(target="Sign In button")` -- Tap element
- `device_tap(x=200, y=500)` -- Tap at coordinates (raw mode)
- `device_double_tap(target="...")` -- Double-tap element
- `device_type(target="email input", text="user@test.com")` -- Type into element
- `device_type(target="email input", text="user@test.com", clear_first=false)` -- Append text
- `device_swipe(target="product list", direction="up")` -- Swipe from element
- `device_long_press(target="...", duration_ms=1500)` -- Long press element
- `device_drag(start_x=100, start_y=200, end_x=300, end_y=400)` -- Drag (raw only)

### Vision
- `screenshot()` -- Capture screen as image (rendered natively by MCP clients)

### App Management
- `install_app(app_url="https://...")` -- Install from URL (.apk or .ipa)
- `install_app(build_version_id="ver-abc123")` -- Install a previously uploaded build (resolves the download URL automatically)
- `launch_app(bundle_id="com.example.app")` -- Launch app

After a successful install, the response includes `bundle_id` -- use it directly with `launch_app`.

### Diagnostics
- `device_doctor()` -- Check auth, session, worker, grounding, environment health

## Swipe Direction Semantics

| Direction | Finger Movement | Content Effect |
|-----------|----------------|----------------|
| `"up"` | Finger moves UP | Scrolls content DOWN (reveals below) |
| `"down"` | Finger moves DOWN | Scrolls content UP (reveals above) |
| `"left"` | Finger moves LEFT | Scrolls content RIGHT |
| `"right"` | Finger moves RIGHT | Scrolls content LEFT |

## Viewer URL

Every `start_device_session` response includes a `viewer_url`. Share this with the user:
- They can watch the device live in a browser as you interact
- Useful for demos, debugging, and collaboration
- The URL is also available via `get_session_info()`

## Core Workflow Pattern

For EVERY interaction:
1. `screenshot()` -- see current state
2. Decide action (use target for grounded, x/y for raw)
3. Execute action (device_tap, device_type, device_swipe)
4. `screenshot()` -- verify the action worked
5. If not as expected, adjust and retry

## Common Recipes

### Login Flow
```
start_device_session(platform="android")
screenshot()
device_type(target="email input", text="user@test.com")
device_type(target="password input", text="pass123")
device_tap(target="Sign In button")
screenshot()  -- verify logged in
```

### Form Filling
```
screenshot()
device_type(target="first name field", text="John")
device_type(target="last name field", text="Doe")
device_type(target="email field", text="john@example.com")
device_tap(target="Submit button")
screenshot()  -- verify submission
```

### Navigation Testing
```
screenshot()
device_tap(target="hamburger menu icon")
screenshot()  -- see menu opened
device_tap(target="Settings")
screenshot()  -- verify on settings page
```

### Scroll and Find
```
screenshot()  -- element not visible
device_swipe(target="content area", direction="up")  -- scroll down
screenshot()  -- check if element visible now
device_tap(target="target element")
```

### Retry Recipe (Element Not Found)
```
-- First attempt failed: "could not locate 'Submit button'"
screenshot()  -- see what's actually on screen
-- Maybe button says "Send" instead:
device_tap(target="Send button")
screenshot()  -- verify
```

### Upload Build and Install
```
-- Upload a local build to the platform
upload_build(file_path="/path/to/app.apk", app_id="app-abc123")
-- Response includes version_id, e.g. "ver-xyz789"

-- Start a device and install the uploaded build
start_device_session(platform="android")
install_app(build_version_id="ver-xyz789")
-- Response includes bundle_id, e.g. "com.example.app"
launch_app(bundle_id="com.example.app")
screenshot()
```

## Troubleshooting

### 1. "no active device session"
**Cause**: No session started, or session expired due to idle timeout.
**Fix**: Call `start_device_session(platform="android")`. Sessions auto-terminate after 5 minutes of inactivity.

### 2. "could not locate '<element>'"
**Cause**: The grounding model couldn't find your target on screen.
**Fix**:
1. Call `screenshot()` to see the current screen
2. Rephrase the target to match what's actually visible
3. Use more specific descriptions (e.g., "blue 'Next' button" not just "Next")

### 3. "worker returned 5xx"
**Cause**: The device worker encountered an error.
**Fix**:
1. Call `device_doctor()` to check worker health
2. If worker is unreachable, call `stop_device_session()` then `start_device_session()` to get a fresh device

### 4. "grounding request failed"
**Cause**: Network issue or grounding service unavailable.
**Fix**: Call `device_doctor()` -- it checks grounding API health. Wait and retry if transient.

### 5. "device started but worker not ready"
**Cause**: Device provisioned but the worker didn't come up in time.
**Fix**: The device was auto-cancelled. Try `start_device_session()` again. If persistent, call `device_doctor()`.

## Tips

- After typing, keyboard may cover elements -- swipe up or tap elsewhere to dismiss
- If grounding returns low confidence, take a screenshot to verify screen state
- Always stop the session when done -- cloud devices cost money
- Use `get_session_info()` to check how long your session has been running
- Share the `viewer_url` with the user so they can watch live

## Test Variable Management

Test variables use `{{variable-name}}` syntax in step descriptions and are substituted at runtime. They are **different** from environment variables (`set_env_var`) which are encrypted and injected at app launch.

### When to Use Test Variables

- **Credentials**: `{{username}}`, `{{password}}` -- pre-set values injected into steps
- **Dynamic data**: `{{otp-code}}`, `{{order-id}}` -- extracted during test execution
- **Configuration**: `{{api-url}}`, `{{env-name}}` -- test-specific settings

### Variable Tools

- `list_variables(test_name_or_id)` -- List all variables for a test
- `set_variable(test_name_or_id, name, value)` -- Add or update a variable (upserts)
- `delete_variable(test_name_or_id, name)` -- Delete a variable
- `delete_all_variables(test_name_or_id)` -- Clear all variables

### Naming Rules

Variable names **must** be kebab-case: lowercase letters, numbers, and hyphens only.
- Valid: `username`, `otp-code`, `api-url-v2`
- Invalid: `userName`, `otp_code`, `API-URL`

### Common Pattern: Create Test with Variables

When creating a test that uses `{{variable-name}}` syntax:

1. **Create the test** with `create_test` -- YAML with `{{variable}}` references is accepted (the validator emits warnings, not errors)
2. **Set variables** with `set_variable` -- create each variable the test references
3. **Run the test** with `run_test` -- the runtime substitutes all `{{variable}}` placeholders

```
create_test(name="login", platform="android", yaml_content="...")
set_variable(test_name_or_id="login", name="username", value="testuser@example.com")
set_variable(test_name_or_id="login", name="password", value="s3cret")
run_test(test_name_or_id="login")
```

### Extraction Variables

For variables populated during test execution (extraction blocks), create the variable **name-only** (no value). The runtime fills the value from the extraction step:

```
set_variable(test_name_or_id="login", name="otp-code")
```

The YAML extraction block defines `variable_name: otp-code`, and the extracted value is stored automatically.

### Variables in Code Execution Scripts

Code execution scripts can read **all test variables** via environment variables or a JSON file. Variables are injected automatically — no setup needed.

- **Env vars**: Prefixed with `REVYL_VAR_`, non-alphanumeric chars become `_` (e.g., `{{generated-email}}` → `REVYL_VAR_generated_email`)
- **JSON file**: `_variables.json` in the working directory, preserves original names
- **Setting a variable**: Print to stdout — the `variable_name` field on the node captures it

```python
# Read a variable
import os
email = os.environ.get("REVYL_VAR_generated_email", "")
```

```javascript
// Read a variable
const email = process.env.REVYL_VAR_generated_email || "";
```

## Workflow Test Management

Workflows are collections of tests. You can modify which tests belong to a workflow after creation.

### Workflow Test Tools

- `add_tests_to_workflow(workflow_name_or_id, test_names_or_ids)` -- Add tests (deduped, skips duplicates)
- `remove_tests_from_workflow(workflow_name_or_id, test_names_or_ids)` -- Remove tests

### Common Pattern: Build a Workflow

```
create_workflow(name="smoke-tests")
add_tests_to_workflow(workflow_name_or_id="smoke-tests", test_names_or_ids=["login-flow", "checkout", "search"])
```

### Common Pattern: Update Workflow Tests

```
add_tests_to_workflow(workflow_name_or_id="smoke-tests", test_names_or_ids=["new-payment-test"])
remove_tests_from_workflow(workflow_name_or_id="smoke-tests", test_names_or_ids=["deprecated-test"])
```

Both tools accept test names (from `.revyl/config.yaml` aliases) or raw UUIDs.
