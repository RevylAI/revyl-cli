---
name: revyl-cli-auth-bypass-expo
description: Implement test-only auth bypass deep links for Expo and Expo Router apps using Revyl launch variables.
---

# Revyl Expo Auth Bypass Skill

Use this skill when an Expo or Expo Router app needs a test-only way to start Revyl runs from an authenticated state. This is app code guidance, not a Revyl authentication shortcut.

## Contract

Use one app-specific deep link shape:

```text
myapp://revyl-auth?token=<token>&role=<role>&redirect=<allowlisted-route>
```

Gate the handler with Revyl launch variables:

```bash
revyl global launch-var create REVYL_AUTH_BYPASS_ENABLED=true
revyl global launch-var create REVYL_AUTH_BYPASS_TOKEN=<test-only-token>
```

Then start the Expo dev-client session with those launch vars before opening the auth link:

```bash
revyl dev --no-build \
  --launch-var REVYL_AUTH_BYPASS_ENABLED \
  --launch-var REVYL_AUTH_BYPASS_TOKEN

revyl device navigate \
  --url "myapp://revyl-auth?token=$REVYL_AUTH_BYPASS_TOKEN&role=buyer&redirect=%2Fcheckout"
```

Do not commit real tokens. Use Revyl launch vars, Revyl global vars, CI secrets, or a staging backend token exchange.

## Dev Loop Integration

This skill handles the app implementation. After the hook exists, use
`revyl-cli-dev-loop` for the everyday device loop:

```bash
# Install both skills when setting up an agent for Expo authenticated-state work.
revyl skill install --name revyl-cli-dev-loop --force
revyl skill install --name revyl-cli-auth-bypass-expo --force

# Create launch vars once per org/environment.
export REVYL_AUTH_BYPASS_TOKEN="<test-only-token>"
revyl global launch-var create REVYL_AUTH_BYPASS_ENABLED=true
revyl global launch-var create REVYL_AUTH_BYPASS_TOKEN="$REVYL_AUTH_BYPASS_TOKEN"

# If a key already exists, update its value instead.
revyl global launch-var update REVYL_AUTH_BYPASS_TOKEN --value "$REVYL_AUTH_BYPASS_TOKEN"

# Start a fresh dev loop with the launch vars attached.
export REVYL_CONTEXT="${USER:-agent}-expo-auth-$$"
revyl dev --context "$REVYL_CONTEXT" --no-build \
  --launch-var REVYL_AUTH_BYPASS_ENABLED \
  --launch-var REVYL_AUTH_BYPASS_TOKEN
```

Wait for `Dev loop ready`, the viewer URL, and a screenshot showing the normal
Expo app UI. Then open the app-specific auth link from a separate shell:

```bash
revyl device navigate \
  --url "myapp://revyl-auth?token=$REVYL_AUTH_BYPASS_TOKEN&role=buyer&redirect=%2Fcheckout"
revyl device screenshot --out /tmp/revyl-auth-bypass.png
```

Launch vars apply only when the device session starts. If `revyl dev` reused an
old session, stop it and start a fresh loop with the launch vars.

## Implementation Steps

1. Confirm the app is Expo or Expo Router and identify its URL scheme.
2. Add a root-layout auth-bypass hook that handles both the initial URL and runtime `Linking` URL events.
3. Add an Expo Router backstop route such as `app/revyl-auth.tsx` that calls the same handler. This prevents `myapp://revyl-auth?...` from landing on an unmatched-route screen while the dev client is already running.
4. Verify the handler is disabled outside simulator, debug, staging, or explicit test builds.
5. Validate the token, role, and redirect before changing app state.
6. Create the app's normal test session state and route only to an allowlisted destination.
7. Show accepted and rejected states visibly in test builds, such as an Account screen status, banner, toast, or debug panel.

## Expo Router Pattern

Keep the app-specific session creation small and explicit:

```tsx
import { useEffect } from "react";
import * as Linking from "expo-linking";
import { router } from "expo-router";

const allowedRedirects = new Map([
  ["/account", "/(tabs)/account"],
  ["/cart", "/cart"],
  ["/checkout", "/checkout"],
]);
const allowedRoles = new Set(["buyer", "support"]);

function launchValue(key: string) {
  return process.env[key];
}

export function handleRevylAuthBypass(rawURL: string) {
  const url = new URL(rawURL);
  if (url.protocol !== "myapp:" || url.hostname !== "revyl-auth") {
    return false;
  }

  const enabled = launchValue("REVYL_AUTH_BYPASS_ENABLED") === "true";
  const expectedToken = launchValue("REVYL_AUTH_BYPASS_TOKEN");
  const token = url.searchParams.get("token");
  const role = url.searchParams.get("role") || "buyer";
  const redirect = url.searchParams.get("redirect") || "/account";
  const route = allowedRedirects.get(redirect);

  if (!enabled) throw new Error("Revyl auth bypass is disabled");
  if (!expectedToken || token !== expectedToken) throw new Error("Bad Revyl auth bypass token");
  if (!allowedRoles.has(role)) throw new Error("Role is not allowlisted");
  if (!route) throw new Error("Redirect is not allowlisted");

  createTestSession({ role });
  router.replace(route);
  return true;
}

export function useRevylAuthBypass() {
  useEffect(() => {
    Linking.getInitialURL().then(url => {
      if (url) handleRevylAuthBypass(url);
    });

    const subscription = Linking.addEventListener("url", event => {
      handleRevylAuthBypass(event.url);
    });

    return () => subscription.remove();
  }, []);
}
```

For managed Expo apps, JavaScript may not receive native launch values automatically. Prefer a small native launch-config bridge or verify the token with a staging backend. Demo fallback tokens are acceptable only for sample apps.

## Backstop Route

For Expo Router, add `app/revyl-auth.tsx` and call the same handler from route params. If handling fails, route to a visible debug/account screen so the agent can see why the bypass was rejected instead of guessing.

## Files To Add Or Update

When implementing auth bypass in your Expo app, keep the changes close to the
app's routing and auth surfaces. For Expo Router apps, the usual edit set is:

- `app/_layout.tsx`: install the provider or hook near the root so initial and runtime deep links are handled.
- `app/revyl-auth.tsx`: add a route backstop that calls the same handler and shows rejected state visibly.
- `src/auth/revylAuthBypass.tsx` or similar: keep token, role, redirect validation, and test-session creation in one small module.
- Account, debug, or settings screen: show accepted/rejected auth-bypass state in test builds.

For non-Router Expo apps, put the same `Linking` handler in your root component
and route with your existing navigation ref.

## Verification

Run the valid case and each failure case before relying on the bypass:

```bash
revyl device navigate --url "myapp://revyl-auth?token=<token>&role=buyer&redirect=%2Fcheckout"
revyl device screenshot --out /tmp/revyl-auth-bypass-valid.png

revyl device navigate --url "myapp://revyl-auth?token=wrong-token&role=buyer&redirect=%2Fcheckout"
revyl device navigate --url "myapp://revyl-auth?token=<token>&role=admin&redirect=%2Fcheckout"
revyl device navigate --url "myapp://revyl-auth?token=<token>&role=buyer&redirect=%2Fadmin"
```

Expected results:

- Valid token, role, and redirect signs in and routes to the target screen.
- Wrong token is rejected visibly.
- Disabled or missing `REVYL_AUTH_BYPASS_ENABLED` is rejected visibly.
- Unknown role is rejected visibly.
- Unknown redirect is rejected visibly.
- Production builds cannot activate the handler.

## Guardrails

1. Never ship an unconditional production bypass.
2. Never paste real passwords or durable tokens into YAML, docs, PRs, or screenshots.
3. Keep auth bypass separate from normal user auth code where possible.
4. Prefer allowlists over arbitrary route names or role strings.
5. For Expo dev-client runs, open the normal Revyl dev loop first, wait until the app is loaded, then send the auth-bypass link.
