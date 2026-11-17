import { randomBytes } from "node:crypto";
import * as vscode from "vscode";
import type { RevylServer, WorkspaceTarget } from "./revylServer";

type PanelMessage = { type: "session-request" } | { type: "open-link"; url: unknown };

/** One editor tab that frames the Revyl workspace beside the code. */
export class RevylPanel implements vscode.Disposable {
  private panel: vscode.WebviewPanel | undefined;
  private frameUrl: string | undefined;
  private signInFailed = false;

  constructor(
    private readonly server: RevylServer,
    private readonly iconPath: vscode.Uri,
  ) {}

  /** Shows a target in the panel, opening the panel or focusing it. */
  async open(target: WorkspaceTarget): Promise<void> {
    const frameUrl = (await this.server.workspaceFrameUrl(target)).href;
    const panel = this.panel ?? this.createPanel();
    panel.title = target.view === "device" ? "Revyl Device" : "Revyl Atlas";
    if (frameUrl !== this.frameUrl || this.signInFailed) {
      this.frameUrl = frameUrl;
      this.signInFailed = false;
      panel.webview.html = workspacePanelHtml(new URL(frameUrl), randomBytes(16).toString("base64"));
    }
    panel.reveal();
  }

  dispose(): void {
    this.panel?.dispose();
  }

  private createPanel(): vscode.WebviewPanel {
    const panel = vscode.window.createWebviewPanel(
      "revyl.workspace",
      "Revyl",
      vscode.ViewColumn.Beside,
      { enableScripts: true, retainContextWhenHidden: true, localResourceRoots: [] },
    );
    panel.iconPath = this.iconPath;
    panel.webview.onDidReceiveMessage((message: PanelMessage) => this.answer(panel, message));
    panel.onDidDispose(() => {
      this.panel = undefined;
      this.frameUrl = undefined;
    });
    this.panel = panel;
    return panel;
  }

  private async answer(panel: vscode.WebviewPanel, message: PanelMessage): Promise<void> {
    if (message.type === "session-request") {
      try {
        const ticket = await this.server.sessionTicket();
        this.signInFailed = !ticket;
        await panel.webview.postMessage({ type: "session-ticket", ticket });
      } catch (error) {
        this.signInFailed = true;
        const message = error instanceof Error ? error.message : String(error);
        await panel.webview.postMessage({ type: "session-ticket", ticket: "", error: `Could not sign in to Revyl: ${message}` });
      }
    } else if (message.type === "open-link") {
      const url = typeof message.url === "string" ? URL.parse(message.url) : null;
      const opened = url?.protocol === "https:" && !url.username && !url.password
        && await vscode.env.openExternal(vscode.Uri.parse(url.href));
      if (!opened) await panel.webview.postMessage({ type: "open-link-failed" });
    }
  }
}

/**
 * The panel page: the framed workspace and a relay that answers it as an MCP
 * Apps host's bridge page would, with the theme, sign-in tickets, and links.
 */
export function workspacePanelHtml(frameUrl: URL, nonce: string): string {
  const origin = frameUrl.origin;
  return `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta http-equiv="Content-Security-Policy" content="default-src 'none'; frame-src ${origin}; style-src 'nonce-${nonce}'; script-src 'nonce-${nonce}'">
<style nonce="${nonce}">
html,body{margin:0;padding:0;width:100%;height:100%;overflow:hidden}
body{display:flex;flex-direction:column}
iframe{display:block;width:100%;flex:1;border:0}
#error{padding:8px 12px;font:var(--vscode-font-size) var(--vscode-font-family);color:var(--vscode-errorForeground);background:var(--vscode-inputValidation-errorBackground);border-bottom:1px solid var(--vscode-inputValidation-errorBorder)}
</style>
</head>
<body>
<div id="error" role="alert" hidden></div>
<iframe id="workspace" title="Revyl workspace" src="${frameUrl.href}" referrerpolicy="no-referrer" allow="fullscreen; clipboard-write" sandbox="allow-scripts allow-same-origin allow-forms allow-popups allow-popups-to-escape-sandbox allow-downloads"></iframe>
<script nonce="${nonce}">
const vscode = acquireVsCodeApi();
const origin = ${JSON.stringify(origin)};
const frame = document.getElementById("workspace");
let framedPageReady = false;
function theme() {
  const classes = document.body.classList;
  return classes.contains("vscode-light") || classes.contains("vscode-high-contrast-light") ? "light" : "dark";
}
function sendTheme() {
  if (framedPageReady) frame.contentWindow.postMessage({type:"revyl-workspace-theme", theme:theme()}, origin);
}
new MutationObserver(sendTheme).observe(document.body, {attributes:true, attributeFilter:["class"]});
window.addEventListener("message", event => {
  if (event.source === frame.contentWindow) {
    if (event.origin !== origin) return;
    const type = event.data?.type;
    if (type === "revyl-workspace-ready") { framedPageReady = true; sendTheme(); }
    else if (type === "revyl-workspace-session-request") vscode.postMessage({type:"session-request"});
    else if (type === "revyl-workspace-open-link") vscode.postMessage({type:"open-link", url:event.data.url});
    return;
  }
  if (event.origin !== window.origin) return;
  const message = event.data;
  if (message?.type === "session-ticket") {
    const error = document.getElementById("error");
    error.textContent = message.error ?? "";
    error.hidden = !message.error;
    frame.contentWindow.postMessage(message.ticket
      ? {type:"revyl-workspace-session-ticket", ticket:message.ticket}
      : {type:"revyl-workspace-session-unavailable"}, origin);
  } else if (message?.type === "open-link-failed") {
    frame.contentWindow.postMessage({type:"revyl-workspace-open-link-failed"}, origin);
  }
});
</script>
</body>
</html>
`;
}
