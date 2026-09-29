# Review Figma designs against Atlas

Use `revyl-cli-figma-review` in a local agent with shell access, image viewing,
and a Figma connection. It guides setup, selection of design references and app
evidence, and delivery of findings. Existing Atlas captures are sufficient to
start; no app checkout or new device session is required.

Choose frames or a component family, then the question: design drift, repeated
inconsistencies, platform differences, changes between builds, or another goal.
Receive findings in chat, a private HTML report with images from both sources,
or explicitly requested Atlas comments with selected attachments. Visual
comparison does not establish which code component or token was used.

Reports use Revyl's square-cornered, compact visual style: short findings,
focused comparison crops, and minimal metadata. PDF is available on request;
the agent exports locally and checks the rendered pages for legibility.

Before reporting, the agent checks the applicable design variant, observed app
state, and counterexamples for every candidate finding. Reports separate
demonstrated problems from unresolved questions and link to exact evidence;
zero findings is a valid result.

## Share before a CLI release

For a single ZIP covering both review workflows, use the
[combined handoff](review-handoff/START-HERE.md). Package that file at the ZIP
root alongside `skills/` containing the complete `revyl-cli-figma-review`,
`revyl-cli-cross-platform-review`, `revyl-cli-atlas`, and
`revyl-cli-atlas-review` folders from `revyl-cli/skills/`. Include only these
instructions and skill assets, never local configuration or customer evidence.
The single-skill handoff below remains available for a focused introduction.

Attach `revyl-cli/skills/revyl-cli-figma-review/SKILL.md` from this source tree
as a file named `SKILL.md` and send the prompt below. The entrypoint is
self-contained and loads existing Atlas guidance through the CLI. Do not send
a raw URL to an unpublished branch or imply the released CLI contains the new
skill. The recipient does not need access to this repository.

Paste into Claude Code with the file attached or available locally:

```text
Help me get started with the attached Revyl Figma review skill.

Read the attached SKILL.md and install it as
~/.claude/skills/revyl-cli-figma-review/SKILL.md for future use. Preserve any
existing customized skill; don't overwrite it without asking. Follow the
attached instructions in this session even if skill discovery needs a reload.

Check whether the Revyl CLI is available and install it if needed using the
official installer. Check Revyl sign-in and guide me through connecting and
authenticating Figma MCP for this client if it isn't ready. Reuse working setup
and only stop for steps that need me, such as browser authentication.

Ask for my Figma link and help me choose a frame or component review and what
to look for. Use existing Atlas evidence. Offer findings in chat, a local
side-by-side report, or short Atlas comments with design attachments. Keep
findings private unless I choose publication. Don't start new device runs or
modify the designs.
```

This prompt authorizes local setup, not publication of customer evidence.
For an agent other than Claude Code, adapt the skill installation directory to
that client's supported location. A hosted chat without a local shell cannot
perform the same installation on the user's computer.

## Install from a CLI that includes the skill

Check `revyl skill list --all` first. Once `revyl-cli-figma-review` appears:

```bash
revyl skill install --name revyl-cli-figma-review --agent claude-code --global --yes
```

Then ask the agent to use `revyl-cli-figma-review` and share a Figma link. The
skill checks the remaining prerequisites. For CLI installation, see the
[CLI README](../README.md#installation); for agent and optional Revyl MCP setup,
see [MCP Setup](https://docs.revyl.com/cli/mcp-setup). Figma authentication is
separate; the skill points to Figma's official instructions for the active client.

## Evaluate a first review

The customer outcome is less setup and manual evidence collection; the product
outcome is independent, repeat use of Atlas for design review. Measure time to
the first useful review and whether the user repeats it on another scope or
build. Track accepted findings, unsupported findings, duplicate comments, and
setup interruptions as quality checks. No baseline is assumed.

Existing `cli_command_started`, `cli_command_completed`, and
`cli_command_failed` events cover CLI invocation outcomes, including skill
installation and Atlas commands. They do not establish that an agent completed
a Figma comparison or that a designer accepted its findings. Evaluate those
outcomes through user feedback during the handoff; do not infer them from
command counts or add customer design content to telemetry.
