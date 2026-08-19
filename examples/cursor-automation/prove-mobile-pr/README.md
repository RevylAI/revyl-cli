# Cursor Automation — prove a mobile PR (BYO-CI)

Install today by creating an Automation from these files at
[cursor.com/automations](https://cursor.com/automations). No Revyl GitHub App
required. CI builds the app, uploads it to Revyl, Cursor proves it on a cloud
device, and the Automation comments on the PR.

## Files

| File | Role |
| --- | --- |
| [PROMPT.md](PROMPT.md) | Full agent prompt (paste into the Automation instructions) |
| [AUTOMATION_SPEC.md](AUTOMATION_SPEC.md) | Triggers, tools, secrets, identity notes |

## Install

1. **CI upload** — Edit the job that already produces a simulator `.app` or
   installable `.apk`. Add `revyl build upload` only. Do not add a second
   workflow file. Use
   `--version "${{ github.event.pull_request.head.sha || github.sha }}"` and
   secret `REVYL_API_KEY`.
2. **Revyl in Cursor** — Install the Revyl plugin, run `revyl auth login`,
   mint a CLI API key from Settings, and add the Automation.
3. **Create the Automation** at [cursor.com/automations](https://cursor.com/automations):
   - Paste the contents of `PROMPT.md` as the prompt / instructions.
   - Enable **Comment on pull request** and **shell**. Do not enable Revyl MCP.
   - Trigger **CI completed** / **Workflow run completed** on the upload job.
   - Add secret `REVYL_API_KEY` and variable `REVYL_APP_ID`.

The Automation runner installs the CLI with `install.sh`. The desktop plugin
pin does not apply there.

## Flow

```text
PR push → your CI builds → revyl build upload → Cursor Automation
  → match build by commit → revyl device start --build-version-id …
  → screenshot / validation → session share + publish → PR comment
```

## Related

- Product docs: [Cursor proof without the GitHub App](https://docs.revyl.ai/guides/cursor-proof)
- Twin path with Revyl GitHub App: [Use your own CI](https://docs.revyl.ai/integrations/github#use-your-own-ci)
