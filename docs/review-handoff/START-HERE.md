# Get started with Revyl visual reviews

Give this ZIP's local path to Claude Code and say:

> Open this ZIP, read START-HERE.md, and help me get started.

If dragging the ZIP does not insert its path, paste the path instead. You do not
need to extract it yourself. Claude needs local shell and image-viewing access.
This package contains instructions, not the CLI, credentials, or app captures.

## Instructions for Claude

Guide the user through a first useful review. Keep setup conversational and
brief; do not make them read the skill files or run commands themselves.

1. Inspect the archive and extract it to a new local directory without
   overwriting existing files. Reject entries that escape that directory or
   contain symlinks. Locate the four bundled skill folders under `skills/`.
   Install each folder at `~/.claude/skills/<skill-name>/` if absent. Leave any
   differing existing installation intact and use the bundled copy for this
   session; do not make overwriting it a prerequisite. Read the bundled files
   directly now rather than requiring a restart for skill discovery.
2. Reuse an already stated goal. Otherwise ask one short question:
   **"What would you like to review: Figma vs the app, or iOS vs Android? If
   you're unsure, I can help choose a small starting point."** Do not configure
   both workflows up front. If they want both, complete one focused review
   first, then reuse setup and evidence where appropriate.
3. Read the selected workflow: `skills/revyl-cli-figma-review/SKILL.md` or
   `skills/revyl-cli-cross-platform-review/SKILL.md`. Read the bundled
   `skills/revyl-cli-atlas/SKILL.md` for inspection and, only when comments are
   requested, `skills/revyl-cli-atlas-review/SKILL.md`. Use these bundled copies
   where a workflow asks to load guidance through `revyl skill show`; an older
   CLI's embedded guidance must not replace the bundle's current instructions.
   Follow the selected skill to check/install the CLI and guide Revyl sign-in.
   Local setup is intended; let the user complete browser authentication.
   Configure and authenticate Figma only for the Figma workflow. No source
   checkout, local dev server, Revyl MCP, or new device run is needed.
4. Resolve the intended organization, app/platforms, and builds using supplied
   context and available metadata; ask only when ambiguous. Use
   `revyl build list --app <app-id> --json` to help select builds rather than
   asking a beginner to find IDs. Check actual captures for the selected build:
   an app's `atlas_ready` flag or latest upload alone does not prove usable
   evidence exists. If empty, explain the scope limitation and help select
   existing evidence without silently changing builds or running an exploration.
5. Start with a small feature or about 3–5 candidate state pairs; that is a
   discovery sample, not a completion target. Complete the selected skill's
   required counterexample and state-verification pass before reporting any
   finding. Investigate thoroughly within scope and present concisely. Expand
   variants and open screenshots, not just descriptions. Confirm downloads
   contain a decodable image before treating them as evidence. If a Figma image
   response is HTTP 202/pending or empty, retry at most three times over at most
   30 seconds with bounded request timeouts, then explain the blocker and offer
   exported images. Do not retry permission or authentication failures as if
   they were pending renders. Cross-platform reviews must never report
   unmatched screens or variants as missing functionality.
6. Recommend a private local side-by-side report for the first review, while
   letting the user choose chat or Atlas comments. If undecided, return chat
   findings privately. Honor PDF requests and use the selected skill's lean
   Revyl report style: square corners, brief findings, focused evidence, and
   readable print layout. Pairing screens and producing the HTML/PDF report are your
   work using the supplied evidence; do not invent a CLI compare/report-export
   command. Publish annotations or attachments only when selected for the
   scoped review. Do not start device sessions or modify Figma. End with the
   result and a plain-language prompt the user can reuse next time.

Install only into the user's local agent environment. A shell in a hosted chat
does not install anything on their computer. Preserve existing authentication
and routing, and never ask the user to paste secrets into chat.
