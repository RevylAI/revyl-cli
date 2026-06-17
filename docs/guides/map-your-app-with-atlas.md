# Map your app with Atlas

Atlas is generated automatically from what Revyl observes on real devices. You do not draw the map yourself. You improve it by letting Revyl see more of your app.

Use this guide to create an initial Atlas map, inspect what Revyl reached, and decide what to cover next.

## What you need

Before Atlas can show anything useful, you need:

- A Revyl app with an uploaded build. If you have not done this yet, start with [Getting Started](../getting-started.md) or [Build Guides](../builds/index.md).
- At least one observation from a session, test run, or workflow. A newly uploaded build has no Atlas map until Revyl has seen it run.
- A way through authentication if your app requires login, such as test credentials or an auth bypass.

Agent-driven exploration also requires the Revyl CLI to be installed, authenticated, and initialized for your app. See the [CLI README](../README.md) and [Run your app on a cloud device](run-your-app-from-anywhere.md).

## 1. Choose how to generate observations

Atlas is built from observed app usage. Pick the path that matches where you are.

### Dashboard session

Use this when you already have an uploaded build and want the fastest first map.

Start a session from the dashboard, then move through the app like a new user or tester would.

Try to reach:

- The home screen.
- Each main tab or navigation area.
- Important flows such as onboarding, checkout, search, settings, or item creation.
- Common states such as logged-out, empty, populated, loading, permission prompts, and errors.

The goal is not to validate every detail yet. The goal is to give Atlas enough observations to understand the app's shape.

### Test or workflow run

Use this when you already have a smoke test or workflow. Run it once, then use Atlas to see which screens and transitions it covered.

Tests are best for durable coverage. Sessions are better for discovering what the app contains.

### Agent exploration

Use this after the CLI/dev loop is configured. Give your agent a broad exploration goal instead of a precise test script:

```text
Map the app for Atlas coverage. Start from launch, reach the home screen, identify the main navigation areas, then visit each tab and major flow. Prefer broad coverage over deep validation. Stop when you have covered the primary product areas, and summarize what you reached and what blocked you.
```

If the agent cannot launch the app or control a device yet, set up the CLI first.

## 2. Open Atlas

Open your app in the dashboard, then go to **Atlas**.

Use filters when you want a narrower view:

- Filter by build to inspect what changed in a specific upload.
- Filter by report to see what one test or session covered.
- Filter by time range to focus on recent activity.

From a report, use the Atlas action to jump into the map already scoped to that run.

## 3. Read the map

Atlas is evidence-backed. If Revyl has not seen a screen or transition, Atlas cannot know about it yet.

Use the map to answer three questions:

1. **What did Revyl cover?** Look for the major screens and paths your tests or session visited.
2. **What is missing?** Find important product areas that have no screenshots or transitions.
3. **What changed?** Compare variants and recent observations after a new build or test run.

Select a screen to inspect the screenshots and evidence behind it before deciding whether a gap is real.

For the full concept reference, see [Atlas](../atlas.md).

## 4. Turn gaps into coverage

When Atlas shows a missing path, choose the smallest useful way to fill it:

- Use another exploratory session when the area is still unknown or you want broad coverage.
- Create a short test when the path matters on every release.
- Add tests to a workflow when several paths should run together.

After the next run processes, return to Atlas and confirm the new screens, variants, and transitions appear.

## Troubleshooting

If Atlas is empty, run at least one session or test against the app/build first, then wait briefly for report processing.

If a product area is missing, Revyl has probably not reached it yet. Run a focused session or create a small test for that path.

If the map looks too broad, filter by build, report, or time range.

If a screen appears duplicated, inspect its variants and observations. Dynamic content, loading states, and error states may produce related but distinct screen clusters.

## Related

- [Getting Started](../getting-started.md)
- [CLI README](../README.md)
- [Atlas](../atlas.md)
- [Run your app on a cloud device](run-your-app-from-anywhere.md)
- [Running Tests](../tests/running-tests.md)
- [Device Quickstart](../device/quickstart.md)
