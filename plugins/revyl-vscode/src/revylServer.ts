import * as vscode from "vscode";

export interface WorkspaceTarget {
  view?: "atlas" | "device";
  app_id?: string;
  session_id?: string;
}

interface ToolResult {
  content?: { type: string; text?: string }[];
  structuredContent?: Record<string, unknown>;
  _meta?: Record<string, unknown>;
  isError?: boolean;
}

interface ProtectedResourceMetadata {
  resource?: string;
  authorization_servers?: string[];
  scopes_supported?: string[];
}

const OPEN_WORKSPACE_TOOL = "open_revyl_workspace";
const SESSION_TICKET_TOOL = "create_revyl_workspace_session_ticket";
const SESSION_TICKET_META_KEY = "revyl/workspaceSessionTicket";
const PANEL_INTENT = "Open the Revyl panel in VS Code";
const REQUEST_TIMEOUT_MS = 30_000;
const UUID = "[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}";
const WORKSPACE_PATH = new RegExp(`^(?:/atlas|/sessions(?:/${UUID})?|/apps/${UUID}/atlas)$`);
// The frontend lets an editor frame only its chromeless copies of the
// workspace pages, which sit at the same paths under this prefix.
const EMBEDDED_PREFIX = "/workspace";

export class RevylSignInError extends Error {}

/** Calls Revyl's hosted MCP server as the person VS Code signed in for it. */
export class RevylServer {
  constructor(
    private readonly mcpUrl: string,
    private readonly mcpServerDefinitionId: string,
  ) {}

  /** The chromeless workspace page the panel frames for a target. */
  async workspaceFrameUrl(target: WorkspaceTarget): Promise<URL> {
    const result = await this.callTool(OPEN_WORKSPACE_TOOL, { ...target, intent: PANEL_INTENT });
    const raw = result.structuredContent?.workspace_url;
    const url = typeof raw === "string" ? URL.parse(raw) : null;
    if (!url || url.protocol !== "https:" || url.search || url.hash || !WORKSPACE_PATH.test(url.pathname)) {
      throw new Error("Revyl returned an unsupported workspace URL.");
    }
    return new URL(EMBEDDED_PREFIX + url.pathname, url.origin);
  }

  /** A one-time ticket the framed page trades for a session of its own. */
  async sessionTicket(): Promise<string> {
    const ticket = (await this.callTool(SESSION_TICKET_TOOL, {}))._meta?.[SESSION_TICKET_META_KEY];
    return typeof ticket === "string" ? ticket : "";
  }

  private async callTool(name: string, args: Record<string, unknown>, renewSignIn = false): Promise<ToolResult> {
    const response = await fetch(this.mcpUrl, {
      method: "POST",
      headers: {
        accept: "application/json, text/event-stream",
        authorization: `Bearer ${await this.accessToken(renewSignIn)}`,
        "content-type": "application/json",
        "mcp-protocol-version": "2025-06-18",
        "user-agent": `Visual Studio Code/${vscode.version}`,
      },
      body: JSON.stringify({ jsonrpc: "2.0", id: 1, method: "tools/call", params: { name, arguments: args } }),
      signal: AbortSignal.timeout(REQUEST_TIMEOUT_MS),
    });
    if (response.status === 401) {
      if (!renewSignIn) return this.callTool(name, args, true);
      throw new RevylSignInError("Revyl did not accept the sign-in. Sign in again.");
    }
    if (!response.ok) throw new Error(`Revyl returned HTTP ${response.status}.`);
    const body = (await response.json()) as { result?: ToolResult; error?: { message?: string } };
    const result = body.result;
    if (body.error || !result) throw new Error(body.error?.message ?? "Revyl returned no result.");
    if (result.isError) {
      throw new Error(result.content?.find((item) => item.type === "text")?.text ?? "The Revyl tool call failed.");
    }
    return result;
  }

  // VS Code signs the MCP server in through an authentication provider it
  // names by the server's authorization server and resource, and registers
  // only once it connects the server. Asking that provider shares the
  // server's sign-in instead of starting a second one.
  private async accessToken(renewSignIn: boolean): Promise<string> {
    if (vscode.workspace.getConfiguration("chat").get<boolean>("disableAIFeatures")) {
      throw new RevylSignInError(
        "Revyl signs in through the editor's MCP support, which is off while Chat: Disable AI Features (chat.disableAIFeatures) is on. Turn that setting off and try again.",
      );
    }
    await vscode.commands.executeCommand("workbench.mcp.startServer", this.mcpServerDefinitionId, {
      waitForLiveTools: true,
    });
    const mcpUrl = new URL(this.mcpUrl);
    const response = await fetch(new URL(`/.well-known/oauth-protected-resource${mcpUrl.pathname}`, mcpUrl), {
      signal: AbortSignal.timeout(REQUEST_TIMEOUT_MS),
    });
    if (!response.ok) throw new Error(`Revyl's sign-in details returned HTTP ${response.status}.`);
    const metadata = (await response.json()) as ProtectedResourceMetadata;
    const authorizationServer = metadata.authorization_servers?.[0];
    if (!authorizationServer || !metadata.resource) throw new Error("Revyl's sign-in details are incomplete.");
    try {
      const providerId = `${authorizationServer} ${metadata.resource}`;
      const scopes = metadata.scopes_supported ?? [];
      const session = renewSignIn
        ? await vscode.authentication.getSession(providerId, scopes, {
            forceNewSession: { detail: "Revyl no longer accepts your saved sign-in. Sign in again to open the Revyl panel." },
          })
        : await vscode.authentication.getSession(providerId, scopes, { createIfNone: true });
      return session.accessToken;
    } catch (error) {
      throw new RevylSignInError(`Sign in to Revyl first: ${error instanceof Error ? error.message : String(error)}`);
    }
  }
}
