---
name: revyl-cli-atlas-review
description: Inspect exact Atlas evidence and manage grounded annotation feedback when the user explicitly requests a feedback mutation.
disable-model-invocation: true
---

# Revyl Atlas Review Skill

Use this write-capable leaf only when the user explicitly asks to add or manage
Atlas feedback. Inspect first with `revyl-cli-atlas`: resolve the app, open the
relevant screenshots, select the exact observation, and list existing feedback
before mutating anything.

For each reviewed screen, expand variants with `--include-variants` on screen
and observations inspection, and open the relevant state screenshots before
forming a finding. A representative image does not stand for every variant.
Anchor feedback to the exact inspected observation, including when the issue
only occurs in a variant; never generalize it to uninspected states.

Treat the screenshot and pin as baseline context. Attach supporting media or
files only when they add information the anchored view cannot provide—such as
a transition, comparison, log, report, or implementation note—and optimize for
decision value rather than volume. Inspect and use existing attachments as
first-class context before replying, editing, or resolving; do not attach a
duplicate screenshot that merely repeats the pinned view.

## Write for scanning

Put one finding in each annotation. Start with `<Type>: <short summary>` that
stands on its own, ideally 3–8 words after the colon. Use `Blocker`, `Issue`,
or `Polish` for supported problems; `Feedback` for a suggestion and `Question`
for uncertain intent. These are body labels, not new severity values.

Follow with at most two short sentences only when they add necessary context:
what differs, the user impact, or the decision needed. Aim for 40 words or fewer
in total; a clear headline alone is enough. Cut preambles, repeated screen
descriptions, IDs, test narration, and speculative explanations. Preserve any
uncertainty that changes the conclusion. Do not use em dashes.

```text
Issue: Save button does nothing
Tapping Save leaves the form unchanged. Expected the updated profile.

Question: Card title wraps differently
The reference uses two lines; this state uses three. Is that intentional?
```

The pin provides the app screenshot. Add a relevant design crop, comparison,
or clip when it helps explain the finding; put longer analysis in an attachment
or linked report instead of the comment. Do not duplicate the pinned screenshot.
Replies lead with the new result or decision, such as `Still present: Save has
no effect`, and add only new context rather than repeating the root finding.

## Ground the annotation

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
revyl atlas annotations list --app <app-id> --observation <observation-id> --status all --json
revyl atlas annotations create --app <app-id> --observation <observation-id> \
  --target "<visible element and location>" --body "<actionable feedback>" \
  --severity <blocker|issue|polish> --attach <evidence-path> \
  --client-request-id <saved-uuid> --json
```

When the annotation reports a problem, set `--severity`: `blocker` blocks
shipping, `issue` is wrong but shippable, `polish` is cosmetic. Omit severity
for questions and discussion; change it later with
`revyl atlas annotations severity <thread-id> --app <app-id> --severity <value>`
(or `--clear`).

Generate and save a UUID before each create/reply and pass it through
`--client-request-id` on the first attempt. Capture stdout, stderr, and exit
status separately; parse stdout as JSON without `2>&1`. The request-ID diagnostic
belongs to stderr. Empty or unparseable output is not proof the write failed:
inspect the captured results and read back server state before retrying. If a
retry is still necessary, use the same saved UUID, body, target, observation,
and ordered attachment paths. Changing the payload with that ID is a conflict.
Never reissue a mutation with a new UUID merely to debug its output. Do not
automatically delete suspected duplicates; reconcile and report the state first.
Repeat `--attach` for up to four files. Edit uses `--attach`, repeatable
`--remove-attachment`, and `--clear-attachments`; combining clear and attach
replaces the attachment set, while omitting attachment flags preserves it.
To mention a human organization member, discover their ID first, then bind a
local alias to one placeholder in the body:

```bash
revyl atlas annotations members --app <app-id> --query <name-or-email> --json
revyl atlas annotations reply <thread-id> --app <app-id> \
  --body '@{reviewer} can you check this state?' \
  --mention 'reviewer=<user-id>' --client-request-id <saved-uuid> --json
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
