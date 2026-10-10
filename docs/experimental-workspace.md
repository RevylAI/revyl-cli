# Experimental embedded workspace

The local CLI prototype is explicitly opt-in: add `--experimental-workspace`
to `revyl mcp serve` (for example, `revyl mcp serve --profile core
--experimental-workspace`). Existing profiles behave as before when omitted.
This does not install or change a plugin package. For MCP client installation,
see [MCP Setup](https://docs.revyl.com/cli/mcp-setup).

In a compatible MCP Apps host, `open_revyl_workspace` opens Atlas by default.
Its optional inputs are `view` (`atlas` or `device`), `app_id` (a UUID for Atlas),
and `session_id` (a UUID for the device view). Empty arguments are supported for
global and thread entrypoints. Device view without an ID opens Sessions; it
does not provision a device. In legacy, core, and full profiles, the existing
`start_device_session` tool also displays its returned viewer in this workspace.
With the workspace enabled, that tool never auto-opens a local browser, even
when `no_open` is omitted or false. Without the flag, existing browser-opening
behavior is unchanged.

For a ChatGPT installation, set `REVYL_CHATGPT_PLUGIN_ID` on the MCP server to
the plugin ID from its listing URL (for example, `Plugin_example`, not a Revyl
app ID or an MCP connection ID). The workspace tool then also returns
`chatgpt_url`, a clickable **Open in ChatGPT** link carrying only the selected
app-relative path. This is an explicit user-click handoff: a tool call does not
guarantee that ChatGPT navigates an already-open panel automatically. Other
hosts do not need this setting and receive the ordinary `workspace_url` only.
ChatGPT may open this link in a new ChatGPT tab; frontend sign-in must also be
available there. A development-only, tab-local sign-in is not production
cross-tab authentication.

The bridge consumes ChatGPT's `openai/deepLink` host context on initialization
and subsequent `ui/notifications/host-context-changed` notifications. A selected
deep link takes precedence over the launcher's default Atlas result and can
navigate an existing panel, including after manual navigation. Only the same
allowlisted Atlas/session paths are accepted; query strings, fragments,
external URLs, and malformed IDs remain rejected. An unrelated host-context
update does not navigate the frame.

The frame shows the frontend's chromeless copy of each allowlisted page, at the
same path under `/workspace`, which is the only prefix the frontend lets a host
frame. Tool results, deep links, and **Open in browser** keep naming the normal
dashboard page. When the framed page reports it is ready, the bridge sends it
the host's `light` or `dark` theme, and again whenever the host changes it; a
host that names no theme leaves the page following the system. A link the
framed page cannot show, such as another dashboard page or an external site, is
passed to the host through `ui/open-link`, because the host sandboxes the frame
without pop-ups; only web URLs without credentials are forwarded, and plain
HTTP only for the configured origin. The framed page is told when the host
refuses a link or does not answer within ten seconds.

If the host initializes the bridge and then sends no tool input, result, or
deep link for 20 seconds, the bridge offers **Open in browser** instead of
staying on its loading line. A result that arrives later still renders. Once
the host reports a tool call under way the bridge waits for its result without
a limit, since a device takes as long to start as it takes. If the host then
cancels that call while nothing is shown, the bridge says so and offers **Open
in browser**.

Inside a host the frontend's sign-in cookie is third-party and may not be sent.
A server that signs its own callers in turns `sessionTicketsOffered` on when it
serves the bridge page. The bridge then answers the framed page's request for a
session by calling `create_revyl_workspace_session_ticket` and passing the
one-time ticket from the result's `_meta` to the page. The CLI's server leaves
it off, so here the bridge tells the page at once that no ticket is available
and the page uses its own sign-in. If the host also announces that call's
result as a tool result, the bridge ignores it rather than treating it as a
workspace to show.

The frame fills the entire panel without a wrapper navigation bar or persistent
status line. A minimal centered loading state remains until the first tool
result, so device startup does not first load Atlas. Navigate using the embedded
Revyl page; repeated tool results preserve its current frame.

Initialization failure or timeout, tool errors, failed device startup, missing
viewer URLs, and rejected URLs replace the frame with a centered failure state.
Its **Open in browser** button asks the host to open a validated URL through
`ui/open-link`, never a raw popup. The fallback uses the last validated workspace
URL, or Atlas before one is available; failed device starts and missing device
viewer URLs fall back to Sessions. Rejected URLs are never used as fallbacks.
A subsequent valid result restores the full-panel frame.

The thin iframe uses the CLI's configured frontend origin (`REVYL_APP_URL`,
the `--dev` frontend, or the normal frontend default). The override must be an
HTTP(S) origin, without credentials, paths, query strings, or fragments. Tool
inputs cannot choose arbitrary URLs. Browser authentication remains separate
from CLI authentication; no API keys or sign-in tokens are embedded. The host
must support nested frames and the frontend must permit that host to embed it.
The wrapper cannot inspect a cross-origin page's sign-in or load state. If
browser cookie or framing restrictions block that page after a valid tool
result, use the normal authenticated Atlas or session URL from the tool result.
Fullscreen is preferred, with inline supported.

ChatGPT requires a browser-reachable HTTPS frontend and an iframe allowlist.
The resource supplies both MCP Apps CSP metadata and ChatGPT's compatibility
alias. A private MCP tunnel transports tool calls and UI resources, not the
embedded frontend: a connected tunnel does not make localhost reachable from
ChatGPT's sandbox. Use an authenticated HTTPS development preview when the
host does not delegate local-network access; do not disable browser security.

This is a navigation prototype, not a new authentication or device-execution
path. Validation is local: Go tests cover the tool/resource contracts and
profile compatibility, and `make -C revyl-cli test-workspace` checks bridge
ordering, message isolation, and URL filtering. The CLI's `make test` and
Linux CLI CI include those Node bridge tests.

The core/full profiles accept object-valued `params` for their composite tools.
For exact-build device and test workflows, `manage_builds(action="list",
params={app_id, platform, limit})` returns that app's version IDs, upload times,
and current-version markers, with `has_more` identifying a truncated first page.
Omitting `app_id` preserves the app catalog listing. Pin the selected version
ID before starting work; do not substitute a different build or infer complete
history from a truncated result.

The pilot outcome is a usable Atlas or device view without leaving the host,
reducing external-browser handoffs. Verify the rendered graph and advancing
device video, not just a successful tool response. Guard against authorization
failures, unintended provisioning, and leaked sessions. Existing frontend
`atlas_view_changed` and `device_session_opened` events and centralized CLI
lifecycle events remain unchanged; this prototype does not add ChatGPT-specific
analytics attribution.
