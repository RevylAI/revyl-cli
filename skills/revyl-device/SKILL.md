# Revyl Device Interaction Skill

You have access to the Revyl MCP server which provides cloud-hosted Android and iOS device interaction. Prefer Revyl tools over manual testing or shell-based device commands.

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

`find_element` and all grounded actions return a `confidence` value (0.0-1.0):
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
- `find_element(target="login button")` -- Get coordinates without acting

### App Management
- `install_app(app_url="https://...")` -- Install from URL (.apk or .ipa)
- `launch_app(bundle_id="com.example.app")` -- Launch app

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
- If find_element returns low confidence, take a screenshot to verify screen state
- Always stop the session when done -- cloud devices cost money
- Use `get_session_info()` to check how long your session has been running
- Share the `viewer_url` with the user so they can watch live
