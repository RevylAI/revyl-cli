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

1. **CI upload** — Add a step (or the reusable job in
   [`../../ci-github-actions/upload-for-cursor-proof.yml`](../../ci-github-actions/upload-for-cursor-proof.yml))
   so every PR build is uploaded with `revyl build upload --json` and
   `REVYL_API_KEY`. GitHub Actions metadata is stamped automatically.
2. **Revyl in Cursor** — Install the Revyl plugin / MCP and set `REVYL_API_KEY`
   (and your app id) for Automations. See
   [MCP setup](https://docs.revyl.ai/integrations/mcp-setup).
3. **Create the Automation** at [cursor.com/automations](https://cursor.com/automations):
   - Paste the contents of `PROMPT.md` as the prompt / instructions.
   - Enable **Comment on pull request** and the **Revyl MCP** tools.
   - Prefer trigger **CI completed** / **Workflow run completed** on the upload
     workflow; fall back to **Pull request pushed** if needed (see
     `AUTOMATION_SPEC.md`).
   - Add secret `REVYL_API_KEY` and variable `REVYL_APP_ID`.

## Flow

```text
PR push → your CI builds → revyl build upload → Cursor Automation
  → match build by commit → revyl device start --build-version-id …
  → screenshot / validation → session share + publish → PR comment
```

## Related

- Product docs: [Cursor proof without the GitHub App](https://docs.revyl.ai/integrations/cursor-proof)
- Twin path with Revyl GitHub App: [Use your own CI](https://docs.revyl.ai/integrations/github#use-your-own-ci)
