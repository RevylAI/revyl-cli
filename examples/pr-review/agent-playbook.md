# CLAUDE.md -- PR Review Playbook (Template)

Copy this file into your repo root as `CLAUDE.md` and customize it for your app.

## App

| Field | Value |
|-------|-------|
| App | Your App Name |
| Platform | ios / android |
| Bundle ID | com.example.yourapp |
| Key screens | Home, Search, Detail, Settings |

## Flow

1. **Read the diff** -- identify which screens and behaviors changed.
2. **Start a device:**
   ```bash
   revyl device start --platform ios --app-id $REVYL_APP_ID --json
   ```
3. **Observe-act-verify loop:**
   ```bash
   revyl device screenshot --json              # observe
   revyl device tap --target "element" --json   # act
   revyl device screenshot --json              # verify
   ```
4. **Get the report:**
   ```bash
   revyl device report --json
   ```
5. **Post results** as a PR comment.

## Other useful commands

```bash
revyl device type --target "field" --text "hello" --json
revyl device swipe --direction up --json
revyl device launch --bundle-id com.example.yourapp --json
```

## PR Comment Format

```markdown
## Mobile PR Review

**Changes:** [what the PR changes]

### Validation
[step-by-step observations]

### Session Recording
[link from revyl device report]

### Summary
- **Device:** iOS/Android cloud device via Revyl
- **Result:** All changes validated / Issues found
```

## Rules

- One action at a time, screenshot before and after
- Use descriptive `--target` values (e.g. "Search bar", "Submit button")
- Always pass `--json` to revyl commands
