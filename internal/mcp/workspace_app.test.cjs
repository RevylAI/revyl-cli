const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");
const vm = require("node:vm");

const origin = "https://workspace.example.com";
const sessionID = "12345678-1234-1234-1234-123456789abc";
// The frame loads the frontend's chromeless copy of each page.
const embedded = origin + "/workspace";
const html = fs.readFileSync(path.join(__dirname, "workspace_app.html"), "utf8");
const script = html.match(/<script>([\s\S]*?)<\/script>/)[1]
  .replaceAll("__REVYL_ORIGIN__", JSON.stringify(origin));
// What the hosted server serves: it turns the ticket hand-off on.
const sessionTicketsOff = "const sessionTicketsOffered = false;";
assert.equal(script.split(sessionTicketsOff).length, 2);
const scriptOfferingSessionTickets = script.replace(sessionTicketsOff, "const sessionTicketsOffered = true;");

function createWorkspace({ sessionTicketsOffered = false } = {}) {
  const nodes = new Map();
  const messages = [];
  const timers = new Map();
  let timerID = 0;
  let receive;
  const parent = { postMessage: message => messages.push(message) };
  const context = {
    URL,
    window: { parent, addEventListener: (_, handler) => { receive = handler; } },
    document: { getElementById: id => {
      if (!nodes.has(id)) nodes.set(id, {
        disabled: true,
        addEventListener: function (_, handler) { this.click = handler; },
      });
      return nodes.get(id);
    } },
    setTimeout: callback => {
      const id = ++timerID;
      timers.set(id, () => { timers.delete(id); callback(); });
      return id;
    },
    clearTimeout: id => timers.delete(id),
  };
  vm.runInNewContext(sessionTicketsOffered ? scriptOfferingSessionTickets : script, context);
  return {
    nodes, messages, timers,
    send: (message, source = parent, eventOrigin) => receive({ source, origin: eventOrigin, data: { jsonrpc: "2.0", ...message } }),
    initialize: (hostContext = {}) => receive({ source: parent, data: { jsonrpc: "2.0", id: 1, result: { hostContext } } }),
    result: payload => receive({ source: parent, data: {
      jsonrpc: "2.0", method: "ui/notifications/tool-result", params: { structuredContent: payload },
    } }),
  };
}

test("shows minimal loading until the first result without workspace chrome", () => {
  const app = createWorkspace();
  assert.equal(app.nodes.get("workspace").src, undefined);
  assert.equal(app.nodes.get("workspace").hidden, true);
  assert.equal(app.nodes.get("state").hidden, false);
  assert.equal(app.nodes.get("external").hidden, true);
  assert.match(app.nodes.get("status").textContent, /Loading/);
  assert.equal(app.messages.length, 1);
  assert.equal(app.messages[0].method, "ui/initialize");
  app.nodes.get("external").click();
  assert.equal(app.messages.length, 1);
  app.initialize();
  assert.equal(app.messages[1].method, "ui/notifications/initialized");
  assert.equal(app.nodes.get("state").hidden, false);
  assert.match(app.nodes.get("status").textContent, /Loading/);
  assert.equal(app.nodes.get("workspace").src, undefined);
  app.result({ workspace_url: origin + "/sessions" });
  assert.equal(app.nodes.get("workspace").src, embedded + "/sessions");
  assert.equal(app.nodes.get("workspace").hidden, false);
  assert.equal(app.nodes.get("state").hidden, true);
  assert.equal(app.nodes.get("external").hidden, true);
  app.nodes.get("external").click();
  assert.equal(app.messages.length, 2);
  assert.doesNotMatch(html, /<nav|id="atlas"|id="sessions"|<strong>Revyl workspace/);
  assert.match(html, /iframe\{[^}]*width:100%;height:100%;border:0/);
  assert.match(html, /#state\{[^}]*place-items:center/);
  assert.match(html, /\[hidden\]\{display:none!important\}/);
});

test("initial deep link selects the requested session instead of the launcher's Atlas result", () => {
  for (const beforeInitialize of [true, false]) {
    const app = createWorkspace();
    if (beforeInitialize) app.result({ workspace_url: origin + "/atlas" });
    app.initialize({ "openai/deepLink": { url: "/sessions/" + sessionID } });
    if (!beforeInitialize) app.result({ workspace_url: origin + "/atlas" });
    assert.equal(app.nodes.get("workspace").src, embedded + "/sessions/" + sessionID);
    assert.equal(app.nodes.get("workspace").hidden, false);
  }
});

test("host deep-link updates navigate the existing panel and can reselect the same route", () => {
  const app = createWorkspace();
  app.initialize();
  app.result({ workspace_url: origin + "/atlas" });
  const frame = app.nodes.get("workspace");
  const paths = [];
  Object.defineProperty(frame, "src", {
    get: () => paths.at(-1),
    set: value => paths.push(value),
  });
  const navigate = path => app.send({ method: "ui/notifications/host-context-changed", params: { "openai/deepLink": { url: path } } });
  navigate("/sessions/" + sessionID);
  navigate("/apps/" + sessionID + "/atlas");
  navigate("/apps/" + sessionID + "/atlas");
  assert.deepEqual(paths, [embedded + "/sessions/" + sessionID, embedded + "/apps/" + sessionID + "/atlas", embedded + "/apps/" + sessionID + "/atlas"]);
  app.result({ workspace_url: origin + "/atlas" });
  app.send({ method: "ui/notifications/host-context-changed", params: { theme: "dark" } });
  assert.equal(paths.length, 3);
});

test("queues a host deep link before initialization without loading a page early", () => {
  const app = createWorkspace();
  app.send({ method: "ui/notifications/host-context-changed", params: { "openai/deepLink": { url: "/sessions/" + sessionID } } });
  app.result({ workspace_url: origin + "/atlas" });
  assert.equal(app.nodes.get("workspace").src, undefined);
  app.initialize({ "openai/deepLink": { url: "/" } });
  assert.equal(app.nodes.get("workspace").src, embedded + "/sessions/" + sessionID);
});

test("the default host path leaves the tool result in control", () => {
  const app = createWorkspace();
  app.initialize({ "openai/deepLink": { url: "/" } });
  assert.equal(app.nodes.get("workspace").src, undefined);
  app.result({ viewer_url: origin + "/sessions/" + sessionID });
  assert.equal(app.nodes.get("workspace").src, embedded + "/sessions/" + sessionID);
});

test("a queued default deep link preserves initialization context and queued results", () => {
  for (const initialPath of ["/", "/sessions/" + sessionID]) {
    const app = createWorkspace();
    app.send({ method: "ui/notifications/host-context-changed", params: { "openai/deepLink": { url: "/" } } });
    app.result({ workspace_url: origin + "/atlas" });
    app.initialize({ "openai/deepLink": { url: initialPath } });
    assert.equal(app.nodes.get("workspace").src, embedded + (initialPath === "/" ? "/atlas" : initialPath));
    assert.equal(app.nodes.get("workspace").hidden, false);
  }
});

test("a selected deep link does not block later device results or failures", () => {
  const app = createWorkspace();
  app.initialize({ "openai/deepLink": { url: "/apps/" + sessionID + "/atlas" } });
  app.result({ success: true, viewer_url: origin + "/sessions/" + sessionID });
  assert.equal(app.nodes.get("workspace").src, embedded + "/sessions/" + sessionID);
  app.result({ success: false });
  assert.equal(app.nodes.get("workspace").hidden, true);
});

test("deep links retain source, protocol, origin, path and credential restrictions", () => {
  const app = createWorkspace();
  app.initialize();
  app.result({ workspace_url: origin + "/atlas" });
  const params = { "openai/deepLink": { url: "/sessions/" + sessionID } };
  app.send({ method: "ui/notifications/host-context-changed", params }, {});
  app.send({ jsonrpc: "1.0", method: "ui/notifications/host-context-changed", params });
  assert.equal(app.nodes.get("workspace").src, embedded + "/atlas");
  for (const url of ["https://evil.example/atlas", "//evil.example/atlas", "/admin", "/foo/../atlas", "/%61tlas", "/atlas?token=secret", "/atlas#token", "/sessions/not-a-uuid", 7, undefined]) {
    app.send({ method: "ui/notifications/host-context-changed", params: { "openai/deepLink": { url } } });
    assert.equal(app.nodes.get("workspace").src, embedded + "/atlas");
    assert.equal(app.nodes.get("workspace").hidden, true);
    assert.match(app.nodes.get("status").textContent, /unsupported workspace URL/);
  }
  app.send({ method: "ui/notifications/host-context-changed", params });
  assert.equal(app.nodes.get("workspace").src, embedded + "/sessions/" + sessionID);
  assert.equal(app.nodes.get("workspace").hidden, false);
});

test("ignores frame and foreign message sources including initialize replies", () => {
  const app = createWorkspace();
  app.send({ id: 1, result: {} }, {});
  assert.equal(app.messages.length, 1);
  assert.match(app.nodes.get("status").textContent, /Loading/);
  app.initialize();
  app.result({ workspace_url: origin + "/atlas" });
  app.send({ method: "ui/notifications/tool-result", params: { structuredContent: { workspace_url: origin + "/sessions" } } }, {});
  assert.equal(app.nodes.get("workspace").src, embedded + "/atlas");
  app.send({ method: "ui/notifications/tool-result", params: { isError: true } }, {});
  app.send({ jsonrpc: "1.0", method: "ui/notifications/tool-result", params: { isError: true } });
  assert.equal(app.nodes.get("state").hidden, true);
  assert.equal(app.nodes.get("workspace").hidden, false);
});

test("queues initial results and accepts structured or text device results", () => {
  const app = createWorkspace();
  app.result({ workspace_url: origin + "/apps/" + sessionID + "/atlas" });
  assert.equal(app.nodes.get("workspace").src, undefined);
  assert.equal(app.nodes.get("state").hidden, false);
  app.initialize();
  assert.equal(app.nodes.get("workspace").src, embedded + "/apps/" + sessionID + "/atlas");
  const viewer = origin + "/sessions/" + sessionID;
  app.send({ method: "ui/notifications/tool-result", params: { content: [
    { type: "text", text: JSON.stringify({ success: true, viewer_url: viewer }) },
  ] } });
  assert.equal(app.nodes.get("workspace").src, embedded + "/sessions/" + sessionID);
  assert.equal(app.nodes.get("state").hidden, true);
  app.send({ method: "ui/notifications/tool-result", params: { isError: true } });
  app.nodes.get("external").click();
  assert.equal(app.messages.at(-1).params.url, viewer);
});

test("rejects untrusted URLs without changing the frame or external link", () => {
  const app = createWorkspace();
  app.initialize();
  app.result({ workspace_url: origin + "/atlas" });
  const invalid = [
    "javascript:alert(1)", "data:text/html,test", "/sessions", "//workspace.example.com/sessions",
    "https://evil.example/sessions/" + sessionID,
    "https://workspace.example.com.evil.example/sessions/" + sessionID,
    "https://user:secret@workspace.example.com/sessions/" + sessionID,
    origin + "/sessions/" + sessionID + "?token=secret",
    origin + "/sessions/" + sessionID + "#token",
    origin + "/sessions/" + sessionID + "?",
    origin + "/sessions/" + sessionID + "#",
    origin + "/sessions/not-a-uuid",
    origin + "/sessions/" + sessionID + "/extra",
    origin + "/admin", origin + "/foo/../atlas", origin + "/%61tlas",
    " " + origin + "/atlas", origin + "/atlas\n",
  ];
  for (const url of invalid) {
    app.result({ workspace_url: url });
    app.result({ viewer_url: url });
    assert.equal(app.nodes.get("workspace").src, embedded + "/atlas", url);
    assert.equal(app.nodes.get("workspace").hidden, true, url);
    assert.equal(app.nodes.get("state").hidden, false, url);
    assert.equal(app.nodes.get("external").hidden, false, url);
    assert.match(app.nodes.get("status").textContent, /unsupported workspace URL/);
  }
  for (const path of ["/atlas", "/sessions", "/apps/" + sessionID + "/atlas"]) {
    app.result({ viewer_url: origin + path });
    assert.equal(app.nodes.get("workspace").src, embedded + "/atlas");
  }
  app.nodes.get("external").click();
  assert.equal(app.messages.at(-1).params.url, origin + "/atlas");
});

test("initialization timeout shows a safe browser fallback and can recover", () => {
  const app = createWorkspace();
  const callback = [...app.timers.values()][0];
  callback();
  assert.equal(app.nodes.get("workspace").hidden, true);
  assert.equal(app.nodes.get("state").hidden, false);
  assert.equal(app.nodes.get("external").hidden, false);
  assert.match(app.nodes.get("status").textContent, /has not connected/);
  app.nodes.get("external").click();
  assert.equal(app.messages.at(-1).method, "ui/open-link");
  assert.equal(app.messages.at(-1).params.url, origin + "/atlas");
  app.result({ workspace_url: origin + "/atlas" });
  app.initialize();
  assert.equal(app.nodes.get("workspace").hidden, false);
  assert.equal(app.nodes.get("state").hidden, true);
});

test("repeated tool results preserve the live iframe", () => {
  const app = createWorkspace();
  app.initialize();
  const frame = app.nodes.get("workspace");
  let currentURL = frame.src;
  let navigations = 0;
  Object.defineProperty(frame, "src", {
    get: () => currentURL,
    set: value => { currentURL = value; navigations++; },
  });
  const result = { viewer_url: origin + "/sessions/" + sessionID };
  app.result(result);
  app.result(result);
  assert.equal(navigations, 1);
});

test("initialization failure offers ui/open-link without making tool calls", () => {
  const app = createWorkspace();
  app.send({ id: 1, error: { code: -1 } });
  assert.equal(app.messages.length, 1);
  assert.equal(app.nodes.get("workspace").hidden, true);
  assert.equal(app.nodes.get("state").hidden, false);
  assert.equal(app.nodes.get("external").hidden, false);
  assert.equal(app.nodes.get("external").disabled, false);
  assert.match(app.nodes.get("status").textContent, /could not initialize/);
  app.nodes.get("external").click();
  assert.equal(app.messages.at(-1).method, "ui/open-link");
  assert.equal(app.messages.at(-1).params.url, origin + "/atlas");
  assert.equal(app.nodes.get("workspace").src, undefined);
});

test("repeated tool results do not reset navigation inside the frame", () => {
  const app = createWorkspace();
  app.initialize();
  app.result({ workspace_url: origin + "/atlas" });
  const frame = app.nodes.get("workspace");
  let visibleURL = origin + "/apps/" + sessionID + "/atlas";
  const srcAttribute = frame.src;
  Object.defineProperty(frame, "src", {
    get: () => srcAttribute,
    set: value => { visibleURL = value; },
  });
  app.result({ workspace_url: origin + "/atlas" });
  assert.equal(visibleURL, origin + "/apps/" + sessionID + "/atlas");
});

test("Open in browser uses the host bridge and recovers from failure or timeout", () => {
  assert.match(html, /<button id="external"[^>]*hidden>/);
  assert.doesNotMatch(html, /target="_blank"/);
  const app = createWorkspace();
  app.nodes.get("external").click();
  assert.equal(app.messages.length, 1);
  app.initialize();
  app.result({ workspace_url: origin + "/atlas" });
  app.send({ method: "ui/notifications/tool-result", params: { isError: true } });
  app.nodes.get("external").click();
  const request = app.messages.at(-1);
  assert.equal(request.method, "ui/open-link");
  assert.equal(request.params.url, origin + "/atlas");
  assert.equal(app.nodes.get("external").disabled, true);
  app.send({ id: request.id, error: { code: -1 } }, {});
  assert.equal(app.nodes.get("external").disabled, true);
  app.send({ id: request.id, error: { code: -1 } });
  assert.equal(app.nodes.get("external").disabled, false);
  assert.match(app.nodes.get("browser-status").textContent, /could not open/);
  app.nodes.get("external").click();
  [...app.timers.values()][0]();
  assert.equal(app.nodes.get("external").disabled, false);
  assert.match(app.nodes.get("browser-status").textContent, /did not respond/);
  app.nodes.get("external").click();
  app.send({ id: app.messages.at(-1).id, result: {} });
  assert.equal(app.nodes.get("external").disabled, false);
  assert.match(app.nodes.get("browser-status").textContent, /completed/);
  assert.match(app.nodes.get("status").textContent, /Could not open the workspace/);
});

test("device startup renders only its viewer, never an initial Atlas page", () => {
  for (const beforeInitialize of [false, true]) {
    const app = createWorkspace();
    const navigations = [];
    const frame = app.nodes.get("workspace");
    assert.equal(frame.src, undefined);
    Object.defineProperty(frame, "src", {
      get: () => navigations.at(-1),
      set: value => navigations.push(value),
    });
    const payload = { success: true, viewer_url: origin + "/sessions/" + sessionID };
    if (beforeInitialize) app.result(payload);
    assert.equal(navigations.length, 0);
    app.initialize();
    if (!beforeInitialize) app.result(payload);
    assert.deepEqual(navigations, [embedded + "/sessions/" + sessionID]);
    assert.equal(frame.hidden, false);
    assert.equal(app.nodes.get("state").hidden, true);
  }
});

test("device failure and missing viewer results show errors without loading Atlas", () => {
  for (const result of [
    { isError: true },
    { structuredContent: { success: false, viewer_url: origin + "/sessions/" + sessionID } },
    { structuredContent: { success: true } },
    { structuredContent: { success: true, workspace_url: origin + "/atlas" } },
    { structuredContent: {} },
    { content: [{ type: "text", text: JSON.stringify({ success: false }) }] },
    { content: [{ type: "text", text: "invalid JSON" }] },
  ]) {
    const app = createWorkspace();
    app.initialize();
    app.send({ method: "ui/notifications/tool-result", params: result });
    assert.match(app.nodes.get("status").textContent, /Could not open/);
    assert.equal(app.nodes.get("workspace").src, undefined);
    assert.equal(app.nodes.get("workspace").hidden, true);
    assert.equal(app.nodes.get("state").hidden, false);
    assert.equal(app.nodes.get("external").hidden, false);
    assert.equal(app.nodes.get("external").disabled, false);
    app.nodes.get("external").click();
    assert.equal(app.messages.at(-1).method, "ui/open-link");
    assert.ok([origin + "/atlas", origin + "/sessions"].includes(app.messages.at(-1).params.url));
  }
});

test("device startup failure falls back to Sessions, never a failed viewer", () => {
  const app = createWorkspace();
  app.result({ success: false, viewer_url: origin + "/sessions/" + sessionID });
  app.initialize();
  assert.equal(app.nodes.get("workspace").src, undefined);
  app.nodes.get("external").click();
  assert.equal(app.messages.at(-1).params.url, origin + "/sessions");
});

test("rejected initial URLs cannot reach the browser fallback", () => {
  const app = createWorkspace();
  app.initialize();
  app.result({ workspace_url: "https://evil.example/atlas" });
  assert.equal(app.nodes.get("workspace").src, undefined);
  app.nodes.get("external").click();
  assert.equal(app.messages.at(-1).params.url, origin + "/atlas");
});

test("an error replaces a loaded frame and a valid result restores it", () => {
  const app = createWorkspace();
  app.initialize();
  app.result({ workspace_url: origin + "/atlas" });
  app.send({ method: "ui/notifications/tool-result", params: { isError: true } });
  assert.equal(app.nodes.get("workspace").hidden, true);
  assert.equal(app.nodes.get("state").hidden, false);
  app.result({ workspace_url: origin + "/sessions" });
  assert.equal(app.nodes.get("workspace").hidden, false);
  assert.equal(app.nodes.get("workspace").src, embedded + "/sessions");
  assert.equal(app.nodes.get("state").hidden, true);
  assert.equal(app.nodes.get("external").hidden, true);
});

function withFrameWindow(app) {
  const received = [];
  const contentWindow = { postMessage: (message, targetOrigin) => received.push(JSON.stringify({ ...message, targetOrigin })) };
  app.nodes.get("workspace").contentWindow = contentWindow;
  const fromFrame = (data, eventOrigin = origin) => app.send(data, contentWindow, eventOrigin);
  return { received, fromFrame };
}

test("opens the browser on the dashboard page while the frame shows its chromeless copy", () => {
  const app = createWorkspace();
  app.initialize();
  app.result({ isError: true });
  app.result({ workspace_url: origin + "/apps/" + sessionID + "/atlas" });
  assert.equal(app.nodes.get("workspace").src, embedded + "/apps/" + sessionID + "/atlas");
  app.result({ success: false });
  app.nodes.get("external").click();
  assert.equal(app.messages.at(-1).method, "ui/open-link");
  assert.equal(app.messages.at(-1).params.url, origin + "/sessions");
});

test("sends the host theme once the framed page says it is ready, and again when it changes", () => {
  const app = createWorkspace();
  const { received, fromFrame } = withFrameWindow(app);
  app.initialize({ theme: "light" });
  app.result({ workspace_url: origin + "/atlas" });
  received.length = 0;
  fromFrame({ type: "revyl-workspace-ready" });
  assert.deepEqual(received, [JSON.stringify({ type: "revyl-workspace-theme", theme: "light", targetOrigin: origin })]);
  app.send({ method: "ui/notifications/host-context-changed", params: { theme: "dark" } });
  assert.equal(received.at(-1), JSON.stringify({ type: "revyl-workspace-theme", theme: "dark", targetOrigin: origin }));
});

test("holds the theme until the framed page first reports ready, then follows the host", () => {
  const theme = value => JSON.stringify({ type: "revyl-workspace-theme", theme: value, targetOrigin: origin });
  const app = createWorkspace();
  const { received, fromFrame } = withFrameWindow(app);
  app.initialize({ theme: "dark" });
  app.result({ workspace_url: origin + "/atlas" });
  app.send({ method: "ui/notifications/host-context-changed", params: { theme: "light" } });
  assert.equal(received.length, 0);
  fromFrame({ type: "revyl-workspace-ready" });
  assert.deepEqual(received, [theme("light")]);
  app.send({ method: "ui/notifications/host-context-changed", params: { "openai/deepLink": { url: "/atlas" } } });
  app.send({ method: "ui/notifications/host-context-changed", params: { theme: "dark" } });
  assert.deepEqual(received, [theme("light"), theme("dark")]);
  fromFrame({ type: "revyl-workspace-ready" });
  assert.deepEqual(received, [theme("light"), theme("dark"), theme("dark")]);
});

test("sends no theme the host did not name and answers only the framed page", () => {
  const app = createWorkspace();
  const { received, fromFrame } = withFrameWindow(app);
  app.initialize({ theme: "sepia" });
  app.result({ workspace_url: origin + "/atlas" });
  fromFrame({ type: "revyl-workspace-ready" });
  assert.equal(received.length, 0);
  app.send({ method: "ui/notifications/host-context-changed", params: { theme: "dark" } });
  received.length = 0;
  fromFrame({ type: "revyl-workspace-ready" }, "https://elsewhere.example.com");
  fromFrame({ type: "something-else" });
  app.send({ type: "revyl-workspace-ready" }, {});
  assert.equal(received.length, 0);
  const messagesToHost = app.messages.length;
  fromFrame({ id: 1, result: {} });
  fromFrame({ method: "ui/notifications/tool-result", params: { isError: true } });
  assert.equal(app.messages.length, messagesToHost);
  assert.equal(app.nodes.get("workspace").hidden, false);
});

test("asks the host to open a link the framed page cannot open itself", () => {
  const app = createWorkspace();
  const { received, fromFrame } = withFrameWindow(app);
  app.initialize();
  app.result({ workspace_url: origin + "/atlas" });
  const before = app.messages.length;
  fromFrame({ type: "revyl-workspace-open-link", url: origin + "/apps/" + sessionID + "/builds?tab=steps" });
  fromFrame({ type: "revyl-workspace-open-link", url: "https://docs.example.com/atlas" });
  const opened = app.messages.slice(before);
  assert.deepEqual(opened.map(message => message.method), ["ui/open-link", "ui/open-link"]);
  assert.equal(opened[0].params.url, origin + "/apps/" + sessionID + "/builds?tab=steps");
  assert.equal(opened[1].params.url, "https://docs.example.com/atlas");
  assert.notEqual(opened[0].id, opened[1].id);
  app.send({ id: opened[0].id, result: {} });
  assert.equal(received.length, 0);
  assert.equal(app.timers.size, 1);
  assert.equal(app.nodes.get("workspace").hidden, false);
});

test("tells the framed page when the host refuses or never answers a link", () => {
  const failed = JSON.stringify({ type: "revyl-workspace-open-link-failed", targetOrigin: origin });
  const app = createWorkspace();
  const { received, fromFrame } = withFrameWindow(app);
  app.initialize();
  app.result({ workspace_url: origin + "/atlas" });
  const before = app.messages.length;
  fromFrame({ type: "revyl-workspace-open-link", url: "https://docs.example.com/refused" });
  fromFrame({ type: "revyl-workspace-open-link", url: "https://docs.example.com/unanswered" });
  const [refused, unanswered] = app.messages.slice(before);
  app.send({ id: refused.id, error: { message: "denied" } });
  assert.deepEqual(received, [failed]);
  app.send({ id: refused.id, error: { message: "denied again" } });
  assert.deepEqual(received, [failed]);
  [...app.timers.values()].forEach(fire => fire());
  assert.deepEqual(received, [failed, failed]);
  app.send({ id: unanswered.id, result: {} });
  assert.deepEqual(received, [failed, failed]);
  fromFrame({ type: "revyl-workspace-open-link", url: "https://docs.example.com/refused-in-result" });
  app.send({ id: app.messages.at(-1).id, result: { isError: true } });
  assert.deepEqual(received, [failed, failed, failed]);
  assert.equal(app.timers.size, 0);
  assert.equal(app.nodes.get("workspace").hidden, false);
  assert.equal(app.nodes.get("state").hidden, true);
});

test("opens nothing for unsafe links or for anything but the framed page", () => {
  const app = createWorkspace();
  const { received, fromFrame } = withFrameWindow(app);
  app.initialize();
  app.result({ workspace_url: origin + "/atlas" });
  const before = app.messages.length;
  const unsafe = [
    "http://elsewhere.example.com/", "javascript:alert(1)", "https://user:secret@docs.example.com/",
    "blob:" + origin + "/2f6c1b0e", "data:text/html,hello", "not a url", "", null, 7,
  ];
  for (const url of unsafe) fromFrame({ type: "revyl-workspace-open-link", url });
  assert.equal(received.length, unsafe.length);
  fromFrame({ type: "revyl-workspace-open-link", url: "https://docs.example.com/" }, "https://elsewhere.example.com");
  app.send({ type: "revyl-workspace-open-link", url: "https://docs.example.com/" }, {}, origin);
  app.send({ type: "revyl-workspace-open-link", url: "https://docs.example.com/" });
  assert.equal(received.length, unsafe.length);
  assert.equal(app.messages.length, before);
});

function requestTicket(app, fromFrame) {
  const before = app.messages.length;
  fromFrame({ type: "revyl-workspace-session-request" });
  return app.messages.slice(before);
}

test("fetches a sign-in ticket for the framed page and hands it over once", () => {
  const app = createWorkspace({ sessionTicketsOffered: true });
  const { received, fromFrame } = withFrameWindow(app);
  app.initialize();
  app.result({ workspace_url: origin + "/atlas" });
  const [call, ...extra] = requestTicket(app, fromFrame);
  assert.equal(extra.length, 0);
  assert.equal(call.method, "tools/call");
  assert.equal(call.params.name, "create_revyl_workspace_session_ticket");
  assert.equal(requestTicket(app, fromFrame).length, 0);
  app.send({ id: call.id, result: {
    content: [{ type: "text", text: "Issued a sign-in ticket to the Revyl workspace." }],
    structuredContent: { expires_at: "2026-10-08T12:00:00Z" },
    _meta: { "revyl/workspaceSessionTicket": "ticket-value" },
  } });
  assert.deepEqual(received, [JSON.stringify({ type: "revyl-workspace-session-ticket", ticket: "ticket-value", targetOrigin: origin })]);
  assert.equal(app.timers.size, 0);
  app.send({ id: call.id, result: { _meta: { "revyl/workspaceSessionTicket": "replayed" } } });
  assert.equal(received.length, 1);
  assert.equal(app.nodes.get("workspace").hidden, false);
  assert.equal(requestTicket(app, fromFrame).length, 1);
});

test("tells the framed page to sign itself in when the host offers no ticket", () => {
  const unavailable = JSON.stringify({ type: "revyl-workspace-session-unavailable", targetOrigin: origin });
  const answers = [
    { error: { code: -32602, message: "Unknown tool" } },
    { result: { isError: true, content: [{ type: "text", text: "Try again." }] } },
    { result: { structuredContent: { expires_at: "2026-10-08T12:00:00Z" } } },
    { result: { _meta: { "revyl/workspaceSessionTicket": 7 } } },
    { result: { _meta: { "revyl/workspaceSessionTicket": "" } } },
  ];
  for (const answer of answers) {
    const app = createWorkspace({ sessionTicketsOffered: true });
    const { received, fromFrame } = withFrameWindow(app);
    app.initialize();
    app.result({ workspace_url: origin + "/atlas" });
    const [call] = requestTicket(app, fromFrame);
    app.send({ id: call.id, ...answer });
    assert.deepEqual(received, [unavailable]);
  }
  const app = createWorkspace({ sessionTicketsOffered: true });
  const { received, fromFrame } = withFrameWindow(app);
  app.initialize();
  app.result({ workspace_url: origin + "/atlas" });
  requestTicket(app, fromFrame);
  [...app.timers.values()].forEach(fire => fire());
  assert.deepEqual(received, [unavailable]);
  assert.equal(requestTicket(app, fromFrame).length, 1);
});

test("issues no ticket to anything but the framed page", () => {
  const app = createWorkspace({ sessionTicketsOffered: true });
  const { received, fromFrame } = withFrameWindow(app);
  app.initialize();
  app.result({ workspace_url: origin + "/atlas" });
  const before = app.messages.length;
  fromFrame({ type: "revyl-workspace-session-request" }, "https://elsewhere.example.com");
  app.send({ type: "revyl-workspace-session-request" }, {}, origin);
  app.send({ type: "revyl-workspace-session-request" });
  assert.equal(app.messages.length, before);
  assert.equal(received.length, 0);
});

test("tells the framed page at once that the CLI's server has no ticket", () => {
  const app = createWorkspace();
  const { received, fromFrame } = withFrameWindow(app);
  app.initialize();
  app.result({ workspace_url: origin + "/atlas" });
  assert.equal(requestTicket(app, fromFrame).length, 0);
  assert.deepEqual(received, [JSON.stringify({ type: "revyl-workspace-session-unavailable", targetOrigin: origin })]);
  assert.equal(app.timers.size, 0);
});

test("drops a ticket request when the frame is navigated to another document", () => {
  const app = createWorkspace({ sessionTicketsOffered: true });
  const { received, fromFrame } = withFrameWindow(app);
  app.initialize();
  app.result({ workspace_url: origin + "/atlas" });
  const [abandoned] = requestTicket(app, fromFrame);
  app.result({ workspace_url: origin + "/sessions" });
  assert.equal(app.timers.size, 0);
  const [fresh] = requestTicket(app, fromFrame);
  assert.equal(fresh.params.name, "create_revyl_workspace_session_ticket");
  assert.notEqual(fresh.id, abandoned.id);
  app.send({ id: abandoned.id, result: { _meta: { "revyl/workspaceSessionTicket": "for-the-old-document" } } });
  assert.equal(received.length, 0);
  app.send({ id: fresh.id, result: { _meta: { "revyl/workspaceSessionTicket": "for-the-new-document" } } });
  assert.deepEqual(received, [JSON.stringify({ type: "revyl-workspace-session-ticket", ticket: "for-the-new-document", targetOrigin: origin })]);
  app.result({ workspace_url: origin + "/sessions" });
  const [again, ...extra] = requestTicket(app, fromFrame);
  assert.equal(extra.length, 0);
  app.result({ workspace_url: origin + "/sessions" });
  assert.equal(requestTicket(app, fromFrame).length, 0);
  app.send({ id: again.id, result: { _meta: { "revyl/workspaceSessionTicket": "same-document" } } });
  assert.equal(received.length, 2);
});

test("offers the browser when the host initializes and then sends nothing, and still renders a late result", () => {
  const app = createWorkspace();
  app.initialize();
  assert.match(app.nodes.get("status").textContent, /Loading Revyl workspace/);
  assert.equal(app.timers.size, 1);
  [...app.timers.values()][0]();
  assert.match(app.nodes.get("status").textContent, /Still waiting for the workspace/);
  assert.equal(app.nodes.get("external").hidden, false);
  assert.equal(app.nodes.get("external").disabled, false);
  app.nodes.get("external").click();
  assert.equal(app.messages.at(-1).method, "ui/open-link");
  assert.equal(app.messages.at(-1).params.url, origin + "/atlas");
  app.result({ workspace_url: origin + "/sessions" });
  assert.equal(app.nodes.get("workspace").src, embedded + "/sessions");
  assert.equal(app.nodes.get("workspace").hidden, false);
  assert.equal(app.nodes.get("state").hidden, true);
});

test("stops waiting once a result, a queued result, a deep link, or a bad result arrives", () => {
  const arrivals = [
    app => app.result({ workspace_url: origin + "/atlas" }),
    app => app.result({ isError: true }),
    app => app.result({ workspace_url: "https://elsewhere.example.com/atlas" }),
    app => app.send({ method: "ui/notifications/host-context-changed", params: { "openai/deepLink": { url: "/sessions" } } }),
  ];
  for (const arrive of arrivals) {
    const app = createWorkspace();
    app.initialize();
    assert.equal(app.timers.size, 1);
    arrive(app);
    assert.equal(app.timers.size, 0);
    assert.doesNotMatch(app.nodes.get("status").textContent, /Still waiting/);
  }
  const queued = createWorkspace();
  queued.result({ workspace_url: origin + "/atlas" });
  queued.initialize();
  assert.equal(queued.timers.size, 0);
  const linked = createWorkspace();
  linked.initialize({ "openai/deepLink": { url: "/sessions" } });
  assert.equal(linked.timers.size, 0);
  assert.equal(linked.nodes.get("workspace").src, embedded + "/sessions");
});

test("keeps its loading line for as long as the host says a tool call is under way", () => {
  for (const method of ["ui/notifications/tool-input", "ui/notifications/tool-input-partial"]) {
    const app = createWorkspace();
    app.initialize();
    assert.equal(app.timers.size, 1);
    app.send({ method, params: { arguments: { platform: "ios" } } });
    assert.equal(app.timers.size, 0);
    assert.match(app.nodes.get("status").textContent, /Loading Revyl workspace/);
    assert.equal(app.nodes.get("external").hidden, true);
    app.result({ success: true, viewer_url: origin + "/sessions/" + sessionID });
    assert.equal(app.nodes.get("workspace").src, embedded + "/sessions/" + sessionID);
    const early = createWorkspace();
    early.send({ method, params: { arguments: {} } });
    early.initialize();
    assert.equal(early.timers.size, 0);
    assert.match(early.nodes.get("status").textContent, /Loading Revyl workspace/);
  }
});

test("offers the browser when the host cancels the tool call it was waiting on", () => {
  const app = createWorkspace();
  app.initialize();
  app.send({ method: "ui/notifications/tool-input", params: { arguments: {} } });
  app.send({ method: "ui/notifications/tool-cancelled", params: { reason: "user action" } });
  assert.equal(app.timers.size, 0);
  assert.match(app.nodes.get("status").textContent, /request was cancelled/);
  assert.equal(app.nodes.get("external").hidden, false);
  assert.equal(app.nodes.get("external").disabled, false);
  app.result({ workspace_url: origin + "/atlas" });
  assert.equal(app.nodes.get("workspace").hidden, false);

  const showing = createWorkspace();
  showing.initialize();
  showing.result({ workspace_url: origin + "/sessions" });
  showing.send({ method: "ui/notifications/tool-input", params: { arguments: {} } });
  showing.send({ method: "ui/notifications/tool-cancelled", params: {} });
  assert.equal(showing.nodes.get("workspace").hidden, false);
  assert.equal(showing.nodes.get("state").hidden, true);

  const early = createWorkspace();
  early.send({ method: "ui/notifications/tool-input", params: { arguments: {} } });
  early.send({ method: "ui/notifications/tool-cancelled", params: {} });
  early.initialize();
  assert.equal(early.timers.size, 1);
});

test("ignores the host's announcement of the ticket call's own result, whichever mark survives", () => {
  const ticketResults = [
    { content: [{ type: "text", text: "Issued a sign-in ticket to the Revyl workspace." }], structuredContent: { kind: "workspace_session_ticket" }, _meta: { "revyl/workspaceSessionTicket": "ticket-value" } },
    { isError: true, content: [{ type: "text", text: "Try again." }], structuredContent: { kind: "workspace_session_ticket" }, _meta: { "revyl/workspaceSessionTicket": "" } },
    { content: [{ type: "text", text: "Issued a sign-in ticket to the Revyl workspace." }], structuredContent: { kind: "workspace_session_ticket" } },
    { isError: true, content: [{ type: "text", text: "Try again." }], _meta: { "revyl/workspaceSessionTicket": "" } },
  ];
  for (const announced of ticketResults) {
    const app = createWorkspace({ sessionTicketsOffered: true });
    const { received, fromFrame } = withFrameWindow(app);
    app.initialize();
    app.result({ workspace_url: origin + "/sessions" });
    const [call] = requestTicket(app, fromFrame);
    app.send({ method: "ui/notifications/tool-result", params: announced });
    assert.equal(app.nodes.get("workspace").hidden, false);
    assert.equal(app.nodes.get("workspace").src, embedded + "/sessions");
    app.send({ id: call.id, result: announced });
    assert.equal(received.length, 1);
    app.send({ method: "ui/notifications/tool-result", params: announced });
    assert.equal(app.nodes.get("workspace").hidden, false);
    assert.equal(app.nodes.get("state").hidden, true);

    const moved = createWorkspace({ sessionTicketsOffered: true });
    const frame = withFrameWindow(moved);
    moved.initialize();
    moved.result({ workspace_url: origin + "/sessions" });
    requestTicket(moved, frame.fromFrame);
    moved.result({ workspace_url: origin + "/atlas" });
    moved.send({ method: "ui/notifications/tool-result", params: announced });
    assert.equal(moved.nodes.get("workspace").src, embedded + "/atlas");
    assert.equal(moved.nodes.get("workspace").hidden, false);
  }
});

test("shows other tools' results while a ticket request is pending", () => {
  const app = createWorkspace({ sessionTicketsOffered: true });
  const { fromFrame } = withFrameWindow(app);
  app.initialize();
  app.result({ workspace_url: origin + "/sessions" });
  requestTicket(app, fromFrame);
  app.result({ isError: true, content: [{ type: "text", text: "The device session did not start." }] });
  assert.equal(app.nodes.get("workspace").hidden, true);
  assert.match(app.nodes.get("status").textContent, /Could not open the workspace/);

  const listing = createWorkspace({ sessionTicketsOffered: true });
  const frame = withFrameWindow(listing);
  listing.initialize();
  listing.result({ workspace_url: origin + "/sessions" });
  requestTicket(listing, frame.fromFrame);
  listing.result({ sessions: [] });
  assert.match(listing.nodes.get("status").textContent, /no viewer or workspace URL/);
});

test("reports no link failure to a document that did not ask for the link", () => {
  const app = createWorkspace();
  const { received, fromFrame } = withFrameWindow(app);
  app.initialize();
  app.result({ workspace_url: origin + "/atlas" });
  fromFrame({ type: "revyl-workspace-open-link", url: "https://docs.example.com/guide" });
  const asked = app.messages.at(-1);
  assert.equal(asked.method, "ui/open-link");
  assert.equal(app.timers.size, 1);
  app.result({ workspace_url: origin + "/sessions" });
  assert.equal(app.timers.size, 0);
  app.send({ id: asked.id, error: { code: -1, message: "refused" } });
  assert.equal(received.length, 0);
});
