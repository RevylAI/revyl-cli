import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import Module, { createRequire } from 'node:module';
import { test } from 'node:test';

const require = createRequire(import.meta.url);
const manifest = JSON.parse(readFileSync(new URL('../package.json', import.meta.url), 'utf8'));
const WORKSPACE_ORIGIN = 'https://app.revyl.ai';
const SESSION_ID = '0b6f5d0e-2a3b-4c5d-8e9f-0a1b2c3d4e5f';

class FakeMcpHttpServerDefinition {
  constructor(label, uri) {
    this.label = label;
    this.uri = uri;
  }
}

class FakeWebview {
  html = '';
  posted = [];
  onDidReceiveMessage(listener) {
    this.listener = listener;
  }
  postMessage(message) {
    this.posted.push(message);
    return Promise.resolve(true);
  }
}

function fakeVscode() {
  const fake = {
    registrations: { mcp: [], commands: new Map(), tools: new Map() },
    panels: [],
    executedCommands: [],
    openedExternally: [],
    errors: [],
    settings: {},
    sessionRequests: [],
    version: '1.109.0',
    ViewColumn: { Beside: -2 },
    McpHttpServerDefinition: FakeMcpHttpServerDefinition,
    LanguageModelTextPart: class { constructor(value) { this.value = value; } },
    LanguageModelToolResult: class { constructor(content) { this.content = content; } },
    Uri: {
      parse: (value) => new URL(value),
      joinPath: (base, ...parts) => new URL(parts.join('/'), base),
    },
    lm: {
      registerMcpServerDefinitionProvider(id, provider) {
        fake.registrations.mcp.push({ id, provider });
        return { dispose() {} };
      },
      registerTool(name, tool) {
        fake.registrations.tools.set(name, tool);
        return { dispose() {} };
      },
    },
    commands: {
      registerCommand(id, handler) {
        fake.registrations.commands.set(id, handler);
        return { dispose() {} };
      },
      async executeCommand(...args) {
        fake.executedCommands.push(args);
      },
    },
    authentication: {
      async getSession(providerId, scopes, options) {
        fake.authenticatedAs = { providerId, scopes };
        fake.sessionRequests.push(options);
        return { accessToken: 'access-token' };
      },
    },
    env: {
      async openExternal(uri) {
        fake.openedExternally.push(uri.href);
        return true;
      },
    },
    workspace: {
      getConfiguration: (section) => ({ get: (key) => fake.settings[`${section}.${key}`] }),
    },
    window: {
      showErrorMessage(message) {
        fake.errors.push(message);
      },
      createWebviewPanel(viewType, title, column, options) {
        const panel = {
          viewType,
          title,
          options,
          webview: new FakeWebview(),
          revealed: 0,
          reveal() { this.revealed += 1; },
          onDidDispose(listener) { this.disposeListener = listener; },
          dispose() { this.disposeListener?.(); },
        };
        fake.panels.push(panel);
        return panel;
      },
    },
  };
  return fake;
}

function loadExtensionWithFakeVscode(fake) {
  const originalLoad = Module._load;
  Module._load = function load(request, ...rest) {
    return request === 'vscode' ? fake : originalLoad.call(this, request, ...rest);
  };
  delete require.cache[require.resolve('../out/extension.js')];
  delete require.cache[require.resolve('../out/panel.js')];
  delete require.cache[require.resolve('../out/revylServer.js')];
  try {
    return require('../out/extension.js');
  } finally {
    Module._load = originalLoad;
  }
}

function activate(fake = fakeVscode()) {
  const extension = loadExtensionWithFakeVscode(fake);
  const context = {
    subscriptions: [],
    extension: { id: 'revyl.revyl' },
    extensionUri: new URL('file:///extension/'),
  };
  extension.activate(context);
  return { fake, context };
}

function fakeRevylFetch(t, { workspaceUrl = `${WORKSPACE_ORIGIN}/atlas`, ticket = 'one-time-ticket', toolStatuses = [] } = {}) {
  const calls = [];
  t.mock.method(globalThis, 'fetch', async (url, init) => {
    const href = String(url);
    if (href.endsWith('/.well-known/oauth-protected-resource/api/v1/mcp')) {
      return Response.json({
        resource: 'https://backend.revyl.ai/api/v1/mcp',
        authorization_servers: ['https://auth.revyl.ai/oauth/2.1'],
        scopes_supported: ['workspace'],
      });
    }
    const body = JSON.parse(init.body);
    calls.push({ headers: init.headers, ...body.params });
    const status = toolStatuses.shift();
    if (status) return new Response('', { status });
    if (body.params.name === 'open_revyl_workspace') {
      return Response.json({ jsonrpc: '2.0', id: 1, result: { content: [], structuredContent: { workspace_url: workspaceUrl } } });
    }
    return Response.json({ jsonrpc: '2.0', id: 1, result: { content: [], _meta: { 'revyl/workspaceSessionTicket': ticket } } });
  });
  return calls;
}

test('activation registers the contributed MCP provider with only the hosted Revyl server', async () => {
  const { fake } = activate();

  assert.deepEqual(
    fake.registrations.mcp.map(({ id }) => id),
    manifest.contributes.mcpServerDefinitionProviders.map(({ id }) => id),
  );
  const definitions = await fake.registrations.mcp[0].provider.provideMcpServerDefinitions();
  assert.equal(definitions.length, 1);
  assert.ok(definitions[0] instanceof FakeMcpHttpServerDefinition);
  assert.equal(definitions[0].uri.href, 'https://backend.revyl.ai/api/v1/mcp');
});

test('the hosted server matches the one the Grok plugin connects to', () => {
  const grokConfig = JSON.parse(
    readFileSync(new URL('../../revyl-grok/.mcp.json', import.meta.url), 'utf8'),
  );
  const { fake } = activate();
  const [definition] = fake.registrations.mcp[0].provider.provideMcpServerDefinitions();

  assert.equal(definition.uri.href, grokConfig.mcpServers['revyl-workspace'].url);
});

test('activation implements every contributed command and chat tool', () => {
  const { fake } = activate();

  assert.deepEqual([...fake.registrations.commands.keys()], manifest.contributes.commands.map(({ command }) => command));
  assert.deepEqual([...fake.registrations.tools.keys()], manifest.contributes.languageModelTools.map(({ name }) => name));
});

test('opening Atlas frames its chromeless page after signing in through the MCP server', async (t) => {
  const calls = fakeRevylFetch(t);
  const { fake } = activate();

  await fake.registrations.commands.get('revyl.openAtlas')();

  assert.deepEqual(fake.executedCommands, [['workbench.mcp.startServer', 'revyl.revyl/Revyl', { waitForLiveTools: true }]]);
  assert.deepEqual(fake.authenticatedAs, {
    providerId: 'https://auth.revyl.ai/oauth/2.1 https://backend.revyl.ai/api/v1/mcp',
    scopes: ['workspace'],
  });
  assert.equal(calls[0].name, 'open_revyl_workspace');
  assert.equal(calls[0].arguments.view, 'atlas');
  assert.equal(calls[0].headers.authorization, 'Bearer access-token');
  const [panel] = fake.panels;
  assert.equal(panel.title, 'Revyl Atlas');
  assert.match(panel.webview.html, new RegExp(`<iframe id="workspace"[^>]* src="${WORKSPACE_ORIGIN}/workspace/atlas"`));
  assert.match(panel.webview.html, new RegExp(`frame-src ${WORKSPACE_ORIGIN};`));
});

test('opening the panel with the editor AI features off explains the setting instead of waiting for sign-in', async (t) => {
  const calls = fakeRevylFetch(t);
  const fake = fakeVscode();
  fake.settings['chat.disableAIFeatures'] = true;
  activate(fake);

  await fake.registrations.commands.get('revyl.openAtlas')();

  assert.equal(fake.errors.length, 1);
  assert.match(fake.errors[0], /chat\.disableAIFeatures/);
  assert.deepEqual(fake.executedCommands, []);
  assert.deepEqual(calls, []);
  assert.equal(fake.panels.length, 0);
});

test('a rejected sign-in is renewed once before the panel gives up', async (t) => {
  const calls = fakeRevylFetch(t, { toolStatuses: [401] });
  const { fake } = activate();

  await fake.registrations.commands.get('revyl.openAtlas')();

  assert.equal(calls.length, 2);
  assert.deepEqual(fake.sessionRequests.map((options) => Object.keys(options)), [['createIfNone'], ['forceNewSession']]);
  assert.equal(fake.panels.length, 1);
  assert.deepEqual(fake.errors, []);
});

test('a sign-in the server keeps rejecting is reported instead of retried forever', async (t) => {
  const calls = fakeRevylFetch(t, { toolStatuses: [401, 401] });
  const { fake } = activate();

  await fake.registrations.commands.get('revyl.openAtlas')();

  assert.equal(calls.length, 2);
  assert.equal(fake.panels.length, 0);
  assert.match(fake.errors[0], /did not accept the sign-in/);
});

test('a failed sign-in ticket shows the error in the panel', async (t) => {
  fakeRevylFetch(t, { toolStatuses: [undefined, 500] });
  const { fake } = activate();
  await fake.registrations.commands.get('revyl.openAtlas')();
  const { webview } = fake.panels[0];

  await webview.listener({ type: 'session-request' });

  assert.deepEqual(webview.posted, [
    { type: 'session-ticket', ticket: '', error: 'Could not sign in to Revyl: Revyl returned HTTP 500.' },
  ]);
  assert.match(webview.html, /<div id="error" role="alert" hidden><\/div>/);
});

test('reopening the same view after a failed sign-in reloads the panel to sign in again', async (t) => {
  fakeRevylFetch(t, { toolStatuses: [undefined, 500] });
  const { fake } = activate();
  const openAtlas = fake.registrations.commands.get('revyl.openAtlas');
  await openAtlas();
  const { webview } = fake.panels[0];
  const failedPage = webview.html;

  await webview.listener({ type: 'session-request' });
  await openAtlas();
  const reloadedPage = webview.html;
  await openAtlas();

  assert.equal(fake.panels.length, 1);
  assert.notEqual(reloadedPage, failedPage);
  assert.equal(webview.html, reloadedPage);
});

test('the chat tool reuses the open panel for a device session', async (t) => {
  fakeRevylFetch(t, { workspaceUrl: `${WORKSPACE_ORIGIN}/sessions/${SESSION_ID}` });
  const { fake } = activate();
  const tool = fake.registrations.tools.get('revyl_open_panel');

  await tool.invoke({ input: {} });
  await tool.invoke({ input: { view: 'device', session_id: SESSION_ID } });

  assert.equal(fake.panels.length, 1);
  assert.equal(fake.panels[0].revealed, 2);
  assert.equal(fake.panels[0].title, 'Revyl Device');
  assert.match(fake.panels[0].webview.html, new RegExp(`src="${WORKSPACE_ORIGIN}/workspace/sessions/${SESSION_ID}"`));
});

test('the chat tool opens the device view for a session id given without a view', async (t) => {
  const calls = fakeRevylFetch(t, { workspaceUrl: `${WORKSPACE_ORIGIN}/sessions/${SESSION_ID}` });
  const { fake } = activate();

  await fake.registrations.tools.get('revyl_open_panel').invoke({ input: { session_id: SESSION_ID } });

  assert.equal(calls[0].arguments.view, 'device');
  assert.equal(calls[0].arguments.session_id, SESSION_ID);
  assert.equal(fake.panels[0].title, 'Revyl Device');
});

test('a workspace URL outside the known views is never framed', async (t) => {
  fakeRevylFetch(t, { workspaceUrl: `${WORKSPACE_ORIGIN}/settings?next=/atlas` });
  const { fake } = activate();

  await assert.rejects(fake.registrations.tools.get('revyl_open_panel').invoke({ input: {} }), /unsupported workspace URL/);
  assert.equal(fake.panels.length, 0);
});

test('the panel answers a sign-in request with a ticket and opens only https links', async (t) => {
  fakeRevylFetch(t, { ticket: 'one-time-ticket' });
  const { fake } = activate();
  await fake.registrations.commands.get('revyl.openAtlas')();
  const { webview } = fake.panels[0];

  await webview.listener({ type: 'session-request' });
  await webview.listener({ type: 'open-link', url: 'javascript:alert(1)' });
  await webview.listener({ type: 'open-link', url: `${WORKSPACE_ORIGIN}/atlas` });

  assert.deepEqual(webview.posted, [{ type: 'session-ticket', ticket: 'one-time-ticket' }, { type: 'open-link-failed' }]);
  assert.deepEqual(fake.openedExternally, [`${WORKSPACE_ORIGIN}/atlas`]);
});
