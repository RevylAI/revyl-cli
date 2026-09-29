# Compare iOS and Android with Atlas

Use `revyl-cli-cross-platform-review` to find meaningful differences within
confirmed matching screens of the same product. The agent discovers observation
pairs even when Atlas groups or names the screens differently. Figma and a local
dev server are not required; existing captures in the selected environment are
enough to start.

App names and platform metadata can establish an obvious iOS/Android pair. When
they do not, the agent asks which apps to compare. Each platform has its own
explicit build selection. It does not treat unmatched screens as missing features
or infer completeness from either Atlas graph.

Choose findings in chat, a private HTML report with both platforms side by side,
or explicitly requested Atlas comments with comparison attachments. Start with
a report when deciding whether the findings are useful. Annotation titles give
the gist immediately, followed by only essential context.

Reports use Revyl's square-cornered, compact visual style: short findings,
focused comparison crops, and minimal metadata. PDF is available on request;
the agent exports locally and checks the rendered pages for legibility.

Before reporting, the agent checks variants and counterexamples for every
candidate finding. Captured state differences are not automatically different
defaults or defects. Reports separate demonstrated problems from unresolved
questions and link to the exact evidence; zero findings is a valid result.

## Share the skill file before a release

The [combined handoff](review-handoff/START-HERE.md) offers both workflows in
one ZIP and guides the user to the relevant setup. See
[packaging instructions](figma-review.md#share-before-a-cli-release).
Use the single-skill handoff below when only platform comparison is wanted.

Send `revyl-cli/skills/revyl-cli-cross-platform-review/SKILL.md` as `SKILL.md`
with this prompt. The recipient does not need this source repository:

```text
Use the attached Revyl cross-platform review skill. Read SKILL.md and install it
at ~/.claude/skills/revyl-cli-cross-platform-review/SKILL.md for future use,
preserving any existing customized copy. Follow it in this session too.

Check/install the Revyl CLI using its official installer if needed and help me
sign in. Use existing Atlas evidence in my intended organization/environment.
Help identify the iOS and Android versions of the same app: infer the pair when
names and platform metadata are clear, otherwise ask me. Select the builds and
find corresponding screens yourself; do not assume the graphs match one-to-one.

Only highlight meaningful differences within confirmed matching states. Never
report unmatched screens as missing functionality. Offer a local side-by-side
report, chat findings, or short Atlas comments with comparison attachments.
Keep findings private unless I choose publication. Do not start device runs.
```

This is a local Claude Code handoff. Other capable agents can follow the skill
using their supported installation directory. For CLI setup see the
[CLI README](../README.md#installation), and for client configuration see
[MCP Setup](https://docs.revyl.com/cli/mcp-setup).

## Install from a CLI that contains the skill

Check `revyl skill list --all` for the name before using:

```bash
revyl skill install --name revyl-cli-cross-platform-review --agent claude-code --global --yes
```

Then ask the agent to compare the product's iOS and Android apps. This is an
optional workflow and does not change the default installed skill selection.

## Evaluate usefulness

The customer outcome is less manual platform comparison and more actionable
drift findings; the product outcome is repeat Atlas reviews. Measure accepted
findings per review and independent repeat use. Guard against false screen
matches, expected native differences, duplicate annotations, and any missing-
screen claims. No baseline is assumed. Existing CLI command-lifecycle events
cover invocation success/failure; designer acceptance and matching quality need
explicit review feedback, not inference from command counts.
