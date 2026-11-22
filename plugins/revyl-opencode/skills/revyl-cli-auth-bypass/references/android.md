# Native Android

Use this reference only for a native Android app. Apply the shared contract, implementation rules, and verification in the parent skill.

- Register an intent filter on the activity that receives app links: `scheme=myapp` `host=revyl-auth`.
- Capture launch extras in `onCreate` before handling links, and handle `onNewIntent`.
- Revyl launch variables arrive as string extras on the launch intent (`REVYL_AUTH_BYPASS_ENABLED`, `REVYL_AUTH_BYPASS_TOKEN`).
