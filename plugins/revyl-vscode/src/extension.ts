import * as vscode from "vscode";
import { RevylPanel } from "./panel";
import { RevylServer, type WorkspaceTarget } from "./revylServer";

export const REVYL_MCP_SERVER_URL = "https://backend.revyl.ai/api/v1/mcp";
const REVYL_MCP_SERVER_LABEL = "Revyl";

export function activate(context: vscode.ExtensionContext): void {
  const panel = new RevylPanel(
    new RevylServer(REVYL_MCP_SERVER_URL, `${context.extension.id.toLowerCase()}/${REVYL_MCP_SERVER_LABEL}`),
    vscode.Uri.joinPath(context.extensionUri, "assets", "icon.png"),
  );
  const openFromCommand = (target: WorkspaceTarget) => () =>
    panel.open(target).catch((error: unknown) => {
      void vscode.window.showErrorMessage(`Could not open Revyl: ${error instanceof Error ? error.message : String(error)}`);
    });
  context.subscriptions.push(
    vscode.lm.registerMcpServerDefinitionProvider("revyl", {
      provideMcpServerDefinitions: () => [
        new vscode.McpHttpServerDefinition(REVYL_MCP_SERVER_LABEL, vscode.Uri.parse(REVYL_MCP_SERVER_URL)),
      ],
    }),
    vscode.commands.registerCommand("revyl.openAtlas", openFromCommand({ view: "atlas" })),
    vscode.commands.registerCommand("revyl.openDeviceSession", openFromCommand({ view: "device" })),
    vscode.lm.registerTool<WorkspaceTarget>("revyl_open_panel", {
      prepareInvocation: () => ({ invocationMessage: "Opening the Revyl panel" }),
      async invoke({ input }) {
        await panel.open(input.session_id && !input.view ? { ...input, view: "device" } : input);
        return new vscode.LanguageModelToolResult([
          new vscode.LanguageModelTextPart("Opened the Revyl panel in an editor tab beside the code."),
        ]);
      },
    }),
    panel,
  );
}
