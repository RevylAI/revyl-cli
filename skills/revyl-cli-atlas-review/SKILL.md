---
name: revyl-cli-atlas-review
description: Inspect exact Atlas evidence and manage grounded annotation feedback when the user explicitly requests a feedback mutation.
---

# Revyl Atlas Review Skill

Use this write-capable leaf only when the user explicitly asks to add or manage
Atlas feedback. Inspect first with `revyl-cli-atlas`: resolve the app, open the
relevant screenshots, select the exact observation, and list existing feedback
before mutating anything.

Treat the screenshot and pin as baseline context. Attach supporting media or
files only when they add information the anchored view cannot provide—such as
a transition, comparison, log, report, or implementation note—and optimize for
decision value rather than volume. Inspect and use existing attachments as
first-class context before replying, editing, or resolving; do not attach a
duplicate screenshot that merely repeats the pinned view.

Use concrete visual targets such as “the blue Continue button at the bottom,”
not semantic guesses. Write concise annotation bodies and do not use em dashes.
Preview ambiguous targets before creation or movement:

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
  --target "<visible element and location>" --body "<actionable feedback>" \
  --attach <evidence-path> --json
```

Save the printed request ID. If transport fails, retry with the same body,
target, observation, ordered attachment paths, and `--client-request-id`;
changing the payload with that ID is a conflict. Replies use the same recovery
rule. Repeat `--attach` for up to four files. Edit uses `--attach`, repeatable
`--remove-attachment`, and `--clear-attachments`; combining clear and attach
replaces the attachment set, while omitting attachment flags preserves it.
To mention a human organization member, discover their ID first, then bind a
local alias to one placeholder in the body:

```bash
revyl atlas annotations members --app <app-id> --query <name-or-email> --json
revyl atlas annotations reply <thread-id> --app <app-id> \
  --body '@{reviewer} can you check this state?' \
  --mention 'reviewer=<user-id>' --json
```

Mention the smallest set of relevant stakeholders whose ownership, expertise,
approval, or action is needed. Good candidates include the owner of the
affected product or code surface, a designer or engineer needed to answer a
specific question, and an existing thread participant needed to make a
decision. Do not mention every organization member for visibility alone. Give
each mentioned person a concrete reason to engage, and omit the mention when
the comment is informational and requires no response.

Use the same `@{alias}` and repeatable `--mention alias=user-id` syntax with
create, reply, and edit, including body-file or stdin input. Bind each alias and
member once; unresolved placeholder-like text remains literal. The CLI replaces
bound placeholders with the member's current display name and submits structured
mention spans. Only human organization members can be mentioned.
Never automatically retry a version conflict:
read the current thread and decide against that state. Move always grounds
against the thread's immutable observation.

Use `list` one page at a time and follow `next_cursor` deliberately. Statuses
are `open`, `resolved`, or `dismissed`; `closed` aggregates the latter two.
Deleting always requires `--yes`. Before deleting, remember that deleting a
root comment removes the full thread from internal and public-share surfaces.

Return the focused Atlas URL after a successful mutation. Keep customer
screenshots and marked previews temporary and never expose signed URLs, bodies,
targets, or request IDs in committed artifacts.
