# Revyl for VS Code

Opens your mobile app's Revyl Atlas map and live cloud iOS, Android, web and
macOS sessions in a Revyl panel beside your code and inside GitHub Copilot
Chat, through Revyl's hosted MCP server.

The extension registers that server with VS Code, adds the Revyl panel, and
adds one agent skill that tells Copilot how to use the server's tools. It runs
no local commands, starts no processes, and stores no credentials of its own:
the panel uses the sign-in VS Code holds for the server.

## Requirements

- VS Code 1.109 or later.
- GitHub Copilot, signed in. Copilot Free is enough.
- A Revyl account.

VSCodium and other builds that turn on **Chat: Disable AI Features**
(`chat.disableAIFeatures`) by default also turn off the MCP support Revyl
signs in through. Turn that setting off to use the extension there.

## Getting started

1. Install the extension.
2. In Copilot Chat, ask for something Revyl does, such as "open my Revyl Atlas".
   VS Code asks you to sign in to Revyl in your browser, where you choose the
   organization to connect.
3. If Copilot says it can't reach Revyl, run **MCP: List Servers** from the
   Command Palette, choose **Revyl**, then **Authenticate**.

Atlas and device views open inside the chat. VS Code marks this MCP Apps
support as experimental. Each tool also returns a link that opens the same
view in your browser. Device sessions use your organization's device time, so
Copilot starts one only when you ask.

## Revyl panel

**Revyl: Open Atlas** and **Revyl: Open Device Session** in the Command
Palette open Atlas or your sessions in an editor tab beside your code. Copilot
can open or focus the same tab with the `#revylPanel` tool, for example when
you ask it to show a running session beside the code. The panel never starts a
device session.

The first time, VS Code asks you to allow the Revyl extension to use your
Revyl sign-in. If you have not signed in to the Revyl server yet, it starts
the server and asks you to sign in first.

## Network endpoints

| Endpoint                              | Purpose                                                   |
| ------------------------------------- | --------------------------------------------------------- |
| `https://backend.revyl.ai/api/v1/mcp` | Revyl's hosted MCP server                                 |
| `https://auth.revyl.ai/oauth/2.1`     | OAuth sign-in for that server                             |
| `https://app.revyl.ai`                | The Atlas and device views shown in the chat and the panel |

## Tools

The tools are listed in
[Revyl's IDE and MCP setup guide](https://docs.revyl.com/agents/mcp), which
also covers other editors and the Revyl CLI.

## Maintainers

`skills/revyl-workspace/SKILL.md` is generated from the Grok plugin's skill
by `npm run sync-skill`, and `npm test` fails when it is stale. Changes merged
to staging get an automatic patch-version bump in `package.json`; for a minor
or major release, raise the version in the same change. Publishing runs only
from the **Publish VS Code Extension** workflow
(`.github/workflows/publish-vscode-extension.yml`), started by hand.

One-time setup before the first release:

1. **Visual Studio Marketplace.** At
   [marketplace.visualstudio.com/manage](https://marketplace.visualstudio.com/manage),
   create the publisher with ID `revyl` (the `publisher` in `package.json`;
   an ID can't change later) and add the people who may manage it. On that
   publisher's **Trusted Publishing** page, create a policy for repository
   owner `RevylAI`, repository `cognisim-monorepo`, workflow
   `publish-vscode-extension.yml` and environment `vscode-extension-publish`.
   The workflow publishes through that policy, so no Marketplace token is
   stored. Because the repository is private, run the workflow within 48 hours
   of creating the policy; the first successful publish activates it for good.
2. **Open VSX.** Create an eclipse.org account with the GitHub username of
   the account that will publish, sign in to open-vsx.org with that GitHub
   account, link the Eclipse account under Settings, and sign the Publisher
   Agreement. Generate an access token under Settings → Access Tokens, then
   run `npx ovsx create-namespace revyl -p <token>`. To show the extension as
   verified there, claim the namespace as the Open VSX wiki's Namespace Access
   page describes.
3. **GitHub.** Create the `vscode-extension-publish` environment, limit it to
   the `main` branch, and add the Open VSX token as its `OVSX_PAT` secret.

To release, wait until the version in `package.json` has reached `main`, then
run the workflow on `main` with that version and the registries to publish
to. The Marketplace's verified-publisher badge for revyl.ai becomes available
after the publisher has had an extension listed for six months.

## License

MIT
