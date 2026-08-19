---
name: revyl-cli-atlas-review
description: Inspect exact Atlas evidence and manage grounded annotation feedback when the user explicitly requests a feedback mutation.
---

# Revyl Atlas Review Skill

Use this write-capable leaf only when the user explicitly asks to add or manage
Atlas feedback. Inspect first with `revyl-cli-atlas`: resolve the app, open the
relevant screenshots, select the exact observation, and list existing feedback
before mutating anything.

Use concrete visual targets such as “the blue Continue button at the bottom,”
not semantic guesses. Preview ambiguous targets before creation or movement:

```bash
PREVIEW_FILE=$(mktemp -t atlas-annotation-preview.XXXXXX.png)
revyl atlas annotations create \
  --app <app-id> \
  --observation <observation-id> \
  --target "<visible element and location>" \
  --dry-run \
  --preview-out "$PREVIEW_FILE" \
  --json
```

Open the marked preview and verify the pin. Create only after it is correct:

```bash
revyl atlas annotations list --app <app-id> --observation <observation-id> --json
revyl atlas annotations create --app <app-id> --observation <observation-id> \
  --target "<visible element and location>" --body "<actionable feedback>" --json
```

Save the printed request ID. If transport fails, retry with the same body,
target, observation, and `--client-request-id`; changing the payload with that
ID is a conflict. Replies use the same recovery rule. Never automatically
retry a version conflict: read the current thread and decide against that
state. Move always grounds against the thread's immutable observation.

Use `list` one page at a time and follow `next_cursor` deliberately. Statuses
are `open`, `resolved`, or `dismissed`; `closed` aggregates the latter two.
Deleting always requires `--yes`. Before deleting, remember that deleting a
root comment removes the full thread from internal and public-share surfaces.

Return the focused Atlas URL after a successful mutation. Keep customer
screenshots and marked previews temporary and never expose signed URLs, bodies,
targets, or request IDs in committed artifacts.
