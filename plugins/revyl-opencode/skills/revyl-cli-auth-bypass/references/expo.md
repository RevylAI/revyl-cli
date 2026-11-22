# Expo or Expo Router

Use this reference only for an Expo app. Apply the shared contract, implementation rules, and verification in the parent skill.

- Handle the initial URL and runtime `Linking` URL events near the root layout.
- For Expo Router, add `app/revyl-auth.tsx` as a backstop that calls the same handler so `myapp://revyl-auth?...` does not land on an unmatched-route screen while the dev client is already running.
- Expo JS does not receive launch vars through `process.env`. Read them once at app start through a launch-config bridge, or verify the token with a staging backend. Demo fallback tokens are acceptable only for sample apps.
  - iOS: Revyl passes launch vars as `-KEY value` launch arguments, which land in `NSUserDefaults`; read them with React Native's `Settings.get(key)`. Plain values come back as strings, but a value in property-list syntax can come back as another type, so convert it with `String(value)` before comparing.
  - Android: Revyl passes launch vars as string extras on the launch intent; read them with a small local Expo module (`npx create-expo-module@latest --local`) whose function returns `appContext.currentActivity?.intent?.getStringExtra(key)`, then rebuild the dev client.
  - Follow the Expo recipe in [Auth bypass deep links](https://docs.revyl.com/devices/auth-bypass#framework-recipes) for the full snippet.
- Launch vars are fixed when the session starts. A Metro or `revyl dev` reload keeps the old values; start a new session after changing one.
- After `revyl device kill-app`, a cold custom-scheme deep link can open the Expo dev launcher instead of the app. Run `revyl device launch`, wait for the project to load, then navigate.
- A cloud device cannot reach a backend on `localhost`. If the backend runs only locally, expose it through a public HTTPS tunnel and point the dev build at it.
- Bug Bazaar is the reference shape: root provider, `app/revyl-auth.tsx` backstop, launch-var gate, allowlisted role/redirect handling, and visible accepted/rejected state.
