package mcp

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/revyl/cli/internal/auth"
	"github.com/revyl/cli/internal/config"
)

const (
	authAppURI      = "ui://revyl/authorize.html"
	authAppMIMEType = "text/html;profile=mcp-app"

	// authorizationCreateTimeout bounds the extra call the gate makes, so an
	// unreachable backend delays one failing tool call rather than hanging it.
	authorizationCreateTimeout = 10 * time.Second

	// authorizationReuseMargin keeps a cached authorization only while enough of
	// its life remains for someone to actually approve it.
	authorizationReuseMargin = 60 * time.Second
)

// authAppHTML renders the inline authorization card.
//
// The host opens the link, not this process, which is the property that makes
// the card work from a cloud agent: the approval page lands in the user's own
// browser rather than inside a VM with no display.
const authAppHTML = `<!doctype html>
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
  --shadow:0 10px 30px rgba(28,23,50,.14),0 2px 7px rgba(28,23,50,.08)
}
@media (prefers-color-scheme:dark){
  :root{
    --accent:#c0b5ff;
    --surface:rgba(31,27,43,.94);
    --surface-muted:#282334;
    --text:#f5f1ff;
    --text-muted:#aaa3b8;
    --border:rgba(226,216,255,.14);
    --shadow:0 12px 34px rgba(0,0,0,.32),0 2px 8px rgba(0,0,0,.2)
  }
}
*{box-sizing:border-box}
html,body{margin:0;padding:0;background:transparent}
body{
  display:flex;
  justify-content:center;
  align-items:flex-start;
  padding:10px;
  color:var(--text);
  font-family:Inter,ui-sans-serif,-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif
}
.card{
  width:min(100%,460px);
  padding:16px;
  border:1px solid var(--border);
  border-radius:14px;
  background:var(--surface);
  box-shadow:var(--shadow)
}
.title{margin:0 0 4px;font-size:14px;font-weight:650}
.detail{margin:0 0 12px;color:var(--text-muted);font-size:12px;line-height:1.45}
.code{
  display:block;
  margin-bottom:12px;
  padding:10px;
  border-radius:9px;
  background:var(--surface-muted);
  font-family:ui-monospace,SFMono-Regular,Menlo,monospace;
  font-size:18px;
  letter-spacing:.18em;
  text-align:center
}
.action{
  width:100%;
  padding:9px 12px;
  border:0;
  border-radius:9px;
  background:var(--accent);
  color:#fff;
  font-size:13px;
  font-weight:600;
  cursor:pointer
}
.action[disabled]{opacity:.6;cursor:default}
.url{
  width:100%;
  margin-top:8px;
  padding:7px 9px;
  border:1px solid var(--border);
  border-radius:8px;
  background:transparent;
  color:var(--text-muted);
  font-family:ui-monospace,SFMono-Regular,Menlo,monospace;
  font-size:11px
}
.status{display:block;margin-top:8px;color:var(--text-muted);font-size:11px;min-height:14px}
</style>
</head>
<body>
<main class="card" aria-label="Authorize Revyl">
  <p id="title" class="title">Authorize Revyl</p>
  <p id="detail" class="detail">Approve this CLI in your browser to continue.</p>
  <code id="user-code" class="code" hidden></code>
  <button id="authorize" class="action" type="button" hidden>Authorize with Revyl</button>
  <input id="authorize-url" class="url" type="url" readonly aria-label="Revyl authorization URL" hidden>
  <span id="status" class="status" role="status" aria-live="polite"></span>
</main>
<script>
const titleNode=document.getElementById("title");
const detailNode=document.getElementById("detail");
const userCodeNode=document.getElementById("user-code");
const authorizeNode=document.getElementById("authorize");
const authorizeURLNode=document.getElementById("authorize-url");
const statusNode=document.getElementById("status");
const initializeId=1;
const openLinkId=2;
let authorizationURL="";
function send(message){window.parent.postMessage(message,"*")}
function parseStructuredResult(params){
  if(params?.structuredContent&&typeof params.structuredContent==="object"){
    return params.structuredContent;
  }
  const text=(params?.content||[]).find((item)=>item?.type==="text")?.text;
  if(!text)return null;
  try{return JSON.parse(text)}catch{return null}
}
function safeURL(raw){
  try{
    const parsed=new URL(raw);
    const localHTTP=parsed.protocol==="http:"&&["localhost","127.0.0.1","::1"].includes(parsed.hostname);
    return parsed.protocol==="https:"||localHTTP?parsed.toString():"";
  }catch{return ""}
}
authorizeNode.addEventListener("click",()=>{
  if(!authorizationURL)return;
  authorizeNode.disabled=true;
  statusNode.textContent="Asking your editor to open the approval page…";
  send({jsonrpc:"2.0",id:openLinkId,method:"ui/open-link",params:{url:authorizationURL}});
});
window.addEventListener("message",(event)=>{
  const message=event.data;
  if(message?.id===initializeId&&message?.result){
    send({jsonrpc:"2.0",method:"ui/notifications/initialized"});
    return;
  }
  if(message?.id===openLinkId){
    authorizeNode.disabled=false;
    statusNode.textContent=message?.error
      ?"Could not open the page. Copy the URL below instead."
      :"Approve in your browser, then run the tool again.";
    return;
  }
  if(message?.method!=="ui/notifications/tool-result")return;
  const structured=parseStructuredResult(message.params);
  const outcome=structured?.outcome||structured||{};
  authorizationURL=safeURL(outcome.authorization_url||"");
  const userCode=outcome.authorization_code||"";
  if(outcome.reason){detailNode.textContent=outcome.reason}
  if(userCode){
    userCodeNode.textContent=userCode;
    userCodeNode.hidden=false;
  }
  if(!authorizationURL){
    titleNode.textContent="Revyl authorization needed";
    return;
  }
  authorizeNode.hidden=false;
  authorizeURLNode.value=authorizationURL;
  authorizeURLNode.hidden=false;
  detailNode.textContent=userCode
    ?"Approve this CLI in your browser, confirming the code below."
    :"Approve this CLI in your browser to continue.";
});
send({
  jsonrpc:"2.0",
  id:initializeId,
  method:"ui/initialize",
  params:{
    protocolVersion:"2026-01-26",
    appInfo:{name:"revyl-authorize",version:"1.0.0"},
    appCapabilities:{availableDisplayModes:["inline"]}
  }
});
</script>
</body>
</html>`

// pendingAuthorization caches one live approval request across tool calls.
//
// Without it every gated tool call would register a new authorization and print
// a different code, which is both wasteful and confusing to approve.
type pendingAuthorization struct {
	mu            sync.Mutex
	authorization *auth.DeviceAuthorization
	expiresAt     time.Time
}

// current returns a cached authorization that still has time to be approved.
//
// Returns:
//   - *auth.DeviceAuthorization: The live request, or nil when none is usable.
func (p *pendingAuthorization) current() *auth.DeviceAuthorization {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.authorization == nil || time.Now().Add(authorizationReuseMargin).After(p.expiresAt) {
		return nil
	}
	return p.authorization
}

// store caches a freshly created authorization.
//
// Parameters:
//   - authorization: The request to reuse until it nears expiry.
func (p *pendingAuthorization) store(authorization *auth.DeviceAuthorization) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.authorization = authorization
	p.expiresAt = authorization.PollDeadline(time.Now())
}

// authorizationForGate returns an approval request to offer the user.
//
// Reuses a live request when one exists, and treats failure to create one as
// absence rather than an error: the gate must still report the original
// authentication problem when the backend cannot be reached.
//
// Parameters:
//   - ctx: Context for cancellation.
//
// Returns:
//   - *auth.DeviceAuthorization: A request to approve, or nil when unavailable.
func (s *Server) authorizationForGate(ctx context.Context) *auth.DeviceAuthorization {
	if existing := s.pendingAuth.current(); existing != nil {
		return existing
	}

	clientInstanceID, err := s.authManager.GetOrCreateClientInstanceID()
	if err != nil {
		return nil
	}

	deviceAuth := auth.NewDeviceAuth(auth.DeviceAuthConfig{
		BackendURL:       config.GetBackendURL(s.devMode),
		ClientInstanceID: clientInstanceID,
		DeviceLabel:      auth.CurrentDeviceLabel(),
	})

	createCtx, cancel := context.WithTimeout(ctx, authorizationCreateTimeout)
	defer cancel()

	authorization, err := deviceAuth.CreateAuthorization(createCtx)
	if err != nil {
		return nil
	}
	s.pendingAuth.store(authorization)
	return authorization
}

// authenticationGateResult builds the tool result for a blocked call.
//
// This is the single place the gate's transport-level response is shaped. When
// Cursor ships Multi Round-Trip Requests, this returns an input_required
// response instead of an error carrying a card, and no tool changes.
//
// The card is attached per result rather than per tool, because a tool-level
// link would render it on successful calls too. Hosts that only honour the
// tool-level association ignore this and show nothing, which is why the same
// URL and code also travel in the outcome envelope and the failure message:
// the agent can then surface a link without any app support at all.
//
// Parameters:
//   - failure: The authentication failure the gate produced.
//
// Returns:
//   - *mcp.CallToolResult: An error result, carrying the inline authorization
//     card when there is a request the user can actually approve.
func authenticationGateResult(failure *devAuthenticationFailure) *mcp.CallToolResult {
	result := &mcp.CallToolResult{IsError: true}
	if failure == nil || failure.Authorization == nil {
		return result
	}
	result.Meta = mcp.Meta{
		"ui": map[string]any{
			"resourceUri": authAppURI,
		},
	}
	return result
}

// authorizationInstruction names the approval a person can complete right now.
//
// Parameters:
//   - authorization: The pending request, or nil when none could be created.
//
// Returns:
//   - string: A sentence naming the URL and code, or empty when there is none.
func authorizationInstruction(authorization *auth.DeviceAuthorization) string {
	if authorization == nil {
		return ""
	}
	return fmt.Sprintf(
		"approve at %s (code %s)",
		authorization.VerificationURIComplete,
		authorization.UserCode,
	)
}

// registerAuthAppResource serves the static inline authorization card.
func (s *Server) registerAuthAppResource() {
	s.mcpServer.AddResource(&mcp.Resource{
		URI:         authAppURI,
		Name:        "Revyl authorization",
		Title:       "Authorize Revyl",
		Description: "Lets the user approve this CLI in a browser when it has no credential.",
		MIMEType:    authAppMIMEType,
	}, s.handleAuthAppResource)
}

// handleAuthAppResource returns the static MCP App HTML document.
func (s *Server) handleAuthAppResource(
	ctx context.Context,
	request *mcp.ReadResourceRequest,
) (*mcp.ReadResourceResult, error) {
	return &mcp.ReadResourceResult{
		Contents: []*mcp.ResourceContents{
			{
				URI:      authAppURI,
				MIMEType: authAppMIMEType,
				Text:     authAppHTML,
			},
		},
	}, nil
}
