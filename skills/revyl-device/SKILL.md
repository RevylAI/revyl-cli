# Revyl Device Interaction Skill

You have access to the Revyl MCP server which provides cloud-hosted Android and iOS device interaction. Prefer Revyl tools over manual testing or shell-based device commands.

## Initialization (MANDATORY)

Before ANY device interaction:
1. Call `start_device_session(platform="android")` (or `"ios"`) to provision a device
2. Wait for the response -- it includes a `viewer_url` the user can open to watch live
3. Call `screenshot()` to see the initial screen state

After ALL device interaction:
- Call `stop_device_session()` to release the device and stop billing

## Grounded by Default

Device tools accept EITHER:
- **target** (DEFAULT): Describe the element in natural language -- coordinates are auto-resolved via AI vision grounding
- **x, y**: Direct pixel coordinates -- for when you can see the screen and know exact coords

### Writing Good Targets (Priority Order)

1. **Visible text/labels**: `"the 'Sign In' button"`, `"input box with 'Password'"`
2. **Visual characteristics**: `"blue rounded rectangle"`, `"magnifying glass icon"`, `"three horizontal lines"`
3. **Spatial + text anchors**: `"text area below the 'Subject:' line"`, `"field below 'Email' label"`

**Avoid** abstract UI jargon. Describe what you SEE on screen.

## Tool Catalog

### Session
- `start_device_session(platform)` -- Provision cloud device (returns viewer_url)
- `stop_device_session()` -- Release device
- `get_session_info()` -- Check session status, platform, time remaining

### Device Actions (grounded by default)
- `device_tap(target="Sign In button")` -- Tap element
- `device_tap(x=200, y=500)` -- Tap at coordinates (raw mode)
- `device_double_tap(target="...")` -- Double-tap element
- `device_type(target="email input", text="user@test.com")` -- Type into element
- `device_swipe(target="product list", direction="up")` -- Swipe from element
- `device_long_press(target="...", duration_ms=1500)` -- Long press element
- `device_drag(start_x=100, start_y=200, end_x=300, end_y=400)` -- Drag (raw only)

### Vision
- `screenshot()` -- Capture screen as image
- `find_element(target="login button")` -- Get coordinates without acting

### App Management
- `install_app(app_url="https://...")` -- Install from URL
- `launch_app(bundle_id="com.example.app")` -- Launch app

### Diagnostics
- `device_doctor()` -- Check auth, session, worker health

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

## Tips

- After typing, keyboard may cover elements -- swipe up or tap elsewhere to dismiss
- `device_swipe(direction="up")` scrolls content UP (reveals below)
- If find_element returns low confidence, take a screenshot to verify screen state
- Always stop the session when done -- cloud devices cost money
