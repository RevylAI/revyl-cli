# Expo or Expo Router

Use this reference only for an Expo app. Apply the shared contract, implementation rules, and verification in the parent skill.

- Handle the initial URL and runtime `Linking` URL events near the root layout.
- For Expo Router, add `app/revyl-auth.tsx` as a backstop that calls the same handler so `myapp://revyl-auth?...` does not land on an unmatched-route screen while the dev client is already running.
- Managed Expo JS may not receive native launch values automatically. Prefer a small native launch-config bridge or verify the token with a staging backend. Demo fallback tokens are acceptable only for sample apps.
- Bug Bazaar is the reference shape: root provider, `app/revyl-auth.tsx` backstop, launch-var gate, allowlisted role/redirect handling, and visible accepted/rejected state.
