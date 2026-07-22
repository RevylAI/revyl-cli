# Atlas

Atlas is Revyl's app map. It turns observed test and session activity into a searchable graph of screens, variants, and transitions so you can understand what Revyl has actually seen in your app.

Use Atlas when you want to answer questions like:

- Which screens and flows has Revyl covered?
- What paths lead to checkout, onboarding, settings, or another product area?
- Did this report explore a new state, or only revisit known screens?
- Are there duplicate or weakly-supported screen clusters worth reviewing?

## How Atlas Gets Data

Atlas is built from real device observations. Test runs, workflows, and exploratory sessions produce reports. Those reports contain screenshots, actions, transitions, and metadata. Atlas layers that evidence into an app-level graph.

Atlas groups similar screens together even when the content changes, so the map stays focused on your app's structure instead of every user, list item, or loading state.

The map is only as complete as the explored surface area. A newly uploaded build may have no Atlas data until at least one run or session observes it. After a run completes, Atlas can also lag briefly while report data is processed.

## Open Atlas in the Dashboard

In the dashboard, open an app and go to **Atlas** to see the app-level map. From a report, use the Atlas action to jump into the map filtered to the app and build that produced the run.

The dashboard view is best for visual review: scan major areas, inspect screenshots, and follow transitions between screens.

## Terms and View Options

Atlas uses a few map-specific terms:

- **Screen cluster** - one logical screen, grouped from many observed screenshots that appear to be the same place in the app.
- **Variant** - a related state of a screen, such as loading, empty, logged-out, error, permission prompt, or populated content.
- **Observation** - one real screenshot/action moment from a test run or session.
- **Transition** - a path between screens, usually tied to the tap, swipe, or other action that moved the app forward.
- **Coverage** - the set of screens and transitions touched by a test, workflow, session, build, or time window.
- **Product area** - an Atlas grouping for screens that belong to the same part of the app, such as onboarding, checkout, or settings.

The dashboard gives you a few ways to inspect the same data:

- **Map** - shows the app as a flow graph, useful for seeing how screens connect.
- **Grid** - shows the same screen clusters without the graph layout, useful for scanning screenshots quickly.
- **Show variants** - expands each screen into its observed states so you can inspect edge cases and dynamic UI.
- **Heat** - colors screens and paths by activity density, making frequently observed areas stand out.
- **Clusters** - draws product-area groupings behind the graph so large maps are easier to reason about.
- **Compact** - tightens the layout for dense apps; switch back to original spacing if you want a more literal graph shape.
- **Details panel** - opens when you select a screen and shows screenshots, variants, evidence, and incoming/outgoing transitions.

## CLI Usage

The Atlas CLI is meant to be a small product-understanding surface for coding
agents. Start broad, then inspect screens with visual evidence.

```bash
revyl atlas apps --search "Riot"
revyl atlas overview --app "Riot"
revyl atlas map --app "Riot"
revyl atlas search "settings social profile" --app "Riot" --screenshots
revyl atlas screen <screen-id> --app "Riot" --screenshots --screenshot-dir /tmp/atlas-shots
```

Use `overview` for a compact summary of the app, `map` for the tree-style
structure, and `graph --json` when you need the exact graph payload used by the
frontend Atlas view.

The focused inspection commands are:

- `screen <screen-id>` - inspect one screen, including semantic details and sample observations.
- `observations <screen-id>` - list grouped screenshot evidence for a screen.
- `neighbors <screen-id>` - show incoming and outgoing screen transitions.
- `observation <observation-id>` - inspect one screenshot observation.
- `candidates <screen-id>` - inspect Atlas identity candidates and match decisions.

Add `--screenshots` to include presigned screenshot URLs. Add
`--screenshot-dir <dir>` to download those screenshots and include
`local_screenshot_path` fields in JSON output.

Advanced exact-data command:

```bash
revyl atlas graph --app "Riot" --json
```

`graph` returns the same canonical graph/structure payload that the frontend
uses for the Atlas tree and graph views.

## Troubleshooting

If Atlas looks empty, run at least one test or session against the app/build first, then wait briefly for report processing.

If the graph looks too broad, narrow it from the dashboard by build, report, or surface scope.

If a screen looks duplicated, inspect its variants and observations from the details panel.

## Related

- [Running Tests](tests/running-tests.md)
- [Device Automation](device/index.md)
- [MCP Setup](https://docs.revyl.ai/cli/mcp-setup)
- [Command Reference](COMMANDS.md)
