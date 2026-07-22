package mcp

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	screenshotAppURI      = "ui://revyl/screenshot.html"
	screenshotAppMIMEType = "text/html;profile=mcp-app"
)

const screenshotAppHTML = `<!doctype html>
<html>
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<style>
:root{
  color-scheme:light dark;
  --accent:#6553c6;
  --surface:rgba(255,255,255,.92);
  --surface-muted:#f4f2fa;
  --text:#24202f;
  --text-muted:#736d80;
  --border:rgba(49,40,75,.14);
  --viewer-border:rgba(101,83,198,.28);
  --shadow:0 10px 30px rgba(28,23,50,.14),0 2px 7px rgba(28,23,50,.08);
  --warning:#b4671e
}
@media (prefers-color-scheme:dark){
  :root{
    --accent:#c0b5ff;
    --surface:rgba(31,27,43,.94);
    --surface-muted:#282334;
    --text:#f5f1ff;
    --text-muted:#aaa3b8;
    --border:rgba(226,216,255,.14);
    --viewer-border:rgba(192,181,255,.28);
    --shadow:0 12px 34px rgba(0,0,0,.32),0 2px 8px rgba(0,0,0,.2);
    --warning:#efa65e
  }
}
*{box-sizing:border-box}
html,body{margin:0;padding:0;background:transparent}
body{
  display:flex;
  justify-content:center;
  align-items:flex-start;
  min-height:64px;
  padding:10px;
  color:var(--text);
  font-family:Inter,ui-sans-serif,-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif
}
.viewer{
  width:min(100%,460px);
  overflow:hidden;
  border:1px solid var(--viewer-border);
  border-radius:14px;
  background:var(--surface);
  box-shadow:var(--shadow)
}
.screenshot-stage{
  position:relative;
  min-height:176px;
  overflow:hidden;
  background:var(--surface-muted)
}
#screenshot{
  display:block;
  width:100%;
  height:auto
}
#status{
  display:flex;
  align-items:center;
  justify-content:center;
  min-height:176px;
  gap:14px;
  padding:28px;
  text-align:left
}
#status.error .spinner{
  border-color:var(--warning);
  animation:none
}
#status.error .spinner::after{
  color:var(--warning);
  content:"!";
  font-size:15px;
  font-weight:750;
  line-height:1
}
.spinner{
  display:grid;
  width:28px;
  height:28px;
  flex:0 0 auto;
  place-items:center;
  border:2px solid var(--border);
  border-top-color:var(--accent);
  border-radius:50%;
  animation:spin .9s linear infinite
}
.status-copy{display:flex;min-width:0;flex-direction:column;gap:4px}
.status-title{font-size:13px;font-weight:650}
.status-detail{color:var(--text-muted);font-size:12px;line-height:1.45}
.viewer-actions{
  display:flex;
  flex-direction:column;
  gap:8px;
  padding:12px;
  border-top:1px solid var(--border)
}
.viewer-action-row{display:flex;gap:8px}
.viewer-action{
  flex:1;
  border:0;
  border-radius:9px;
  padding:9px 12px;
  background:var(--accent);
  color:Canvas;
  font:inherit;
  font-size:12px;
  font-weight:700;
  cursor:pointer
}
.viewer-action:disabled{cursor:wait;opacity:.65}
.viewer-url{
  width:100%;
  min-width:0;
  border:1px solid var(--border);
  border-radius:8px;
  padding:7px 9px;
  background:var(--surface-muted);
  color:var(--text-muted);
  font:11px ui-monospace,SFMono-Regular,Consolas,monospace
}
#viewer-action-status{
  min-height:1.2em;
  color:var(--text-muted);
  font-size:11px
}
[hidden]{display:none!important}
@keyframes spin{to{transform:rotate(360deg)}}
@media (prefers-reduced-motion:reduce){
  .spinner{animation:none}
}
@media (max-width:360px){
  body{padding:6px}
  .viewer{border-radius:12px}
}
</style>
</head>
<body>
<main class="viewer" aria-label="Revyl device screenshot viewer">
  <div class="screenshot-stage">
    <div id="status" role="status" aria-live="polite">
      <span class="spinner" aria-hidden="true"></span>
      <span class="status-copy">
        <span id="status-title" class="status-title">Preparing screenshot</span>
        <span id="status-detail" class="status-detail">Waiting for the device image…</span>
      </span>
    </div>
    <img id="screenshot" alt="Revyl device screenshot" hidden>
  </div>
  <div id="viewer-actions" class="viewer-actions" hidden>
    <div class="viewer-action-row">
      <button id="open-viewer" class="viewer-action" type="button">Open live device</button>
    </div>
    <input id="viewer-url" class="viewer-url" type="url" readonly aria-label="Revyl live device URL">
    <span id="viewer-action-status" role="status" aria-live="polite"></span>
  </div>
</main>
<script>
const statusNode=document.getElementById("status");
const statusTitleNode=document.getElementById("status-title");
const statusDetailNode=document.getElementById("status-detail");
const imageNode=document.getElementById("screenshot");
const viewerActionsNode=document.getElementById("viewer-actions");
const viewerURLNode=document.getElementById("viewer-url");
const openViewerNode=document.getElementById("open-viewer");
const viewerActionStatusNode=document.getElementById("viewer-action-status");
const initializeId=1;
const openLinkId=2;
let viewerURL="";
function send(message){window.parent.postMessage(message,"*")}
function parseStructuredResult(params){
  if(params?.structuredContent&&typeof params.structuredContent==="object"){
    return params.structuredContent;
  }
  const text=(params?.content||[]).find((item)=>item?.type==="text")?.text;
  if(!text)return null;
  try{return JSON.parse(text)}catch{return null}
}
function extractViewerURL(payload){
  return payload?.outcome?.viewer_url||payload?.result?.viewer_url||payload?.viewer_url||"";
}
function safeViewerURL(raw){
  try{
    const parsed=new URL(raw);
    const localHTTP=parsed.protocol==="http:"&&["localhost","127.0.0.1","::1"].includes(parsed.hostname);
    return parsed.protocol==="https:"||localHTTP?parsed.toString():"";
  }catch{return ""}
}
function showViewerAction(raw){
  viewerURL=safeViewerURL(raw);
  if(!viewerURL)return;
  viewerURLNode.value=viewerURL;
  viewerActionsNode.hidden=false;
}
openViewerNode.addEventListener("click",()=>{
  if(!viewerURL)return;
  openViewerNode.disabled=true;
  viewerActionStatusNode.textContent="Asking Cursor to open the live device…";
  send({jsonrpc:"2.0",id:openLinkId,method:"ui/open-link",params:{url:viewerURL}});
});
imageNode.addEventListener("load",()=>{
  imageNode.hidden=false;
  statusNode.hidden=true;
});
imageNode.addEventListener("error",()=>{
  imageNode.hidden=true;
  statusNode.hidden=false;
  statusNode.className="error";
  statusTitleNode.textContent="Screenshot unavailable";
  statusDetailNode.textContent="The returned image could not be displayed.";
});
window.addEventListener("message",(event)=>{
  const message=event.data;
  if(message?.id===initializeId&&message?.result){
    send({jsonrpc:"2.0",method:"ui/notifications/initialized"});
    return;
  }
  if(message?.id===openLinkId){
    openViewerNode.disabled=false;
    if(message?.error){
      viewerActionStatusNode.textContent="Cursor could not open the link. Copy the URL instead.";
    }else{
      viewerActionStatusNode.textContent="Live device opened.";
    }
    return;
  }
  if(message?.method!=="ui/notifications/tool-result")return;
  const structured=parseStructuredResult(message.params);
  showViewerAction(extractViewerURL(structured));
  const image=(message.params?.content||[]).find((item)=>item?.type==="image");
  if(!image?.data){
    imageNode.hidden=true;
    statusNode.hidden=false;
    if(viewerURL){
      statusNode.className="";
      statusTitleNode.textContent="Live device ready";
      statusDetailNode.textContent="Open the viewer to watch the session. A screenshot will appear after the app is installed.";
    }else{
      statusNode.className="error";
      statusTitleNode.textContent="Screenshot unavailable";
      statusDetailNode.textContent="The tool completed without returning an image.";
    }
    return;
  }
  imageNode.src="data:"+(image.mimeType||"image/png")+";base64,"+image.data;
});
send({
  jsonrpc:"2.0",
  id:initializeId,
  method:"ui/initialize",
  params:{
    protocolVersion:"2026-01-26",
    appInfo:{name:"revyl-screenshot",version:"1.0.0"},
    appCapabilities:{availableDisplayModes:["inline","fullscreen"]}
  }
});
</script>
</body>
</html>`

// screenshotAppToolMeta links a visual tool to the inline screenshot app.
func screenshotAppToolMeta() mcp.Meta {
	return mcp.Meta{
		"ui": map[string]any{
			"resourceUri": screenshotAppURI,
		},
	}
}

// registerScreenshotAppResource serves the static inline screenshot viewer.
func (s *Server) registerScreenshotAppResource() {
	s.mcpServer.AddResource(&mcp.Resource{
		URI:         screenshotAppURI,
		Name:        "Revyl screenshot viewer",
		Title:       "Revyl Device Screenshot",
		Description: "Renders native screenshot content returned by Revyl visual tools.",
		MIMEType:    screenshotAppMIMEType,
	}, s.handleScreenshotAppResource)
}

// handleScreenshotAppResource returns the static MCP App HTML document.
func (s *Server) handleScreenshotAppResource(
	ctx context.Context,
	request *mcp.ReadResourceRequest,
) (*mcp.ReadResourceResult, error) {
	return &mcp.ReadResourceResult{
		Contents: []*mcp.ResourceContents{
			{
				URI:      screenshotAppURI,
				MIMEType: screenshotAppMIMEType,
				Text:     screenshotAppHTML,
			},
		},
	}, nil
}
