"use strict";

// Revyl destination-tunnel forwarder. Reads one JSON config line on stdin,
// listens on each declared loopback address, and carries every accepted TCP
// connection to the developer's relay as one WebSocket stream named by its
// selector. The developer's CLI checks the key and selector before dialing.
// An optional HTTP proxy listener sends CONNECT and absolute-URI requests for
// a declared host:port into the tunnel and passes every other request to the
// proxy the guest used before, so undeclared ports keep their route.

const fs = require("node:fs");
const net = require("node:net");
const readline = require("node:readline");

const PATH = "/_revyl/tunnel/v1";
const KEY_HEADER = "X-Revyl-Tunnel-Key";
const SUBPROTOCOL = "revyl-tunnel.v1";
const RELAY_HOST = /^hr-[a-z0-9-]+\.relay(-staging)?\.revyl\.ai$/;
const MAX_MESSAGE_BYTES = 32 * 1024;
const MAX_STREAMS = 64;
const MAX_BUFFERED_BYTES = 4 * 1024 * 1024;
const MAX_DOWNSTREAM_BUFFERED_BYTES = 32 * 1024 * 1024;
const FLUSH_TIMEOUT_MS = 30 * 1000;
const RELAY_PROBE_INTERVAL_MS = 30 * 1000;
const RELAY_PROBE_TIMEOUT_MS = 10 * 1000;
const RELAY_REVOKED_PROBES = 3;
const RELAY_UNHELD_PROBES = 6;
const NORMAL_CLOSURE = 1000;
const MAX_PROXY_HEAD_BYTES = 16 * 1024;
const MAX_PROXY_CONNECTIONS = 256;
const UPSTREAM_CONNECT_TIMEOUT_MS = 10 * 1000;
const CONNECTION_ESTABLISHED = "HTTP/1.1 200 Connection established\r\n\r\n";
const BAD_GATEWAY = "HTTP/1.1 502 Bad Gateway\r\nConnection: close\r\nContent-Length: 0\r\n\r\n";

const counts = { opened: 0, refused: 0, failed: 0, bytes_up: 0, bytes_down: 0 };
const servers = [];
let active = 0;
let proxied = 0;
let statusPath = null;
let generation = null;

function reply(message) {
  const line = JSON.stringify(generation === null ? message : { ...message, generation }) + "\n";
  process.stdout.write(line);
  if (statusPath) {
    try {
      fs.writeFileSync(statusPath, line);
    } catch {}
  }
}

function finish(code) {
  for (const server of servers) server.close();
  reply({ ok: true, stopped: true, counts });
  process.exit(code);
}

function carry(config, selector, socket, initial = null) {
  if (active >= MAX_STREAMS) {
    counts.refused += 1;
    socket.destroy();
    return;
  }
  active += 1;
  socket.pause();
  const url = `wss://${config.relay_host}${PATH}?dst=${encodeURIComponent(selector)}`;
  const relay = new WebSocket(url, {
    protocols: [SUBPROTOCOL],
    headers: { [KEY_HEADER]: config.key },
  });
  relay.binaryType = "arraybuffer";
  let opened = false;
  let done = false;
  // A normal relay close flushes what the app hasn't read yet before ending
  // its connection; every other end resets it.
  const end = (flush) => {
    if (done) return;
    done = true;
    active -= 1;
    if (flush) {
      socket.end();
      setTimeout(() => socket.destroy(), FLUSH_TIMEOUT_MS).unref();
    } else {
      socket.destroy();
    }
    if (relay.readyState === WebSocket.OPEN || relay.readyState === WebSocket.CONNECTING) relay.close();
  };
  relay.addEventListener("open", () => {
    opened = true;
    counts.opened += 1;
    if (initial?.length) {
      for (let offset = 0; offset < initial.length; offset += MAX_MESSAGE_BYTES) {
        relay.send(initial.subarray(offset, offset + MAX_MESSAGE_BYTES));
      }
      counts.bytes_up += initial.length;
    }
    socket.resume();
  });
  relay.addEventListener("message", (event) => {
    const chunk = Buffer.from(event.data);
    counts.bytes_down += chunk.length;
    socket.write(chunk);
    if (socket.writableLength > MAX_DOWNSTREAM_BUFFERED_BYTES) end(false);
  });
  relay.addEventListener("close", (event) => end(event.code === NORMAL_CLOSURE));
  relay.addEventListener("error", () => {
    if (!opened && !done) counts.failed += 1;
    end(false);
  });
  socket.on("data", (data) => {
    for (let offset = 0; offset < data.length; offset += MAX_MESSAGE_BYTES) {
      const chunk = data.subarray(offset, offset + MAX_MESSAGE_BYTES);
      relay.send(chunk);
      counts.bytes_up += chunk.length;
    }
    if (relay.bufferedAmount > MAX_BUFFERED_BYTES) {
      socket.pause();
      const wait = setInterval(() => {
        if (done || relay.bufferedAmount <= MAX_BUFFERED_BYTES / 2) {
          clearInterval(wait);
          if (!done) socket.resume();
        }
      }, 20);
    }
  });
  socket.on("close", () => end(false));
  socket.on("error", () => end(false));
}

// Exits once the relay reports for several probes in a row that its session is
// gone, or that no CLI has held it for longer than a CLI retries a dropped
// relay connection, so a forwarder whose CLI was killed doesn't keep its ports
// while a relay restart doesn't end the tunnel.
function watchRelay(config) {
  let revoked = 0;
  let unheld = 0;
  setInterval(async () => {
    let status;
    try {
      const response = await fetch(`https://${config.relay_host}${PATH}`, { signal: AbortSignal.timeout(RELAY_PROBE_TIMEOUT_MS) });
      status = response.status;
      await response.body?.cancel();
    } catch {
      return;
    }
    revoked = status === 404 ? revoked + 1 : 0;
    unheld = status === 404 || status === 503 ? unheld + 1 : 0;
    if (revoked < RELAY_REVOKED_PROBES && unheld < RELAY_UNHELD_PROBES) return;
    for (const server of servers) server.close();
    reply({ ok: false, error: "relay_gone", counts });
    process.exit(6);
  }, RELAY_PROBE_INTERVAL_MS).unref();
}

function parseUpstream(value) {
  if (typeof value !== "string") return null;
  const separator = value.lastIndexOf(":");
  return { host: value.slice(0, separator), port: Number(value.slice(separator + 1)) };
}

// The request head with Connection: close, so a client never sends a later
// request for another host down a connection already routed for this one.
function closingHead(lines, requestLine) {
  const headers = lines.slice(1).filter((line) => line && !/^(proxy-)?connection:/i.test(line));
  return Buffer.from([requestLine, ...headers, "Connection: close", "", ""].join("\r\n"), "latin1");
}

function pipeTo(socket, upstream, head, onConnect) {
  const target = net.connect({ host: upstream.host, port: upstream.port });
  const timer = setTimeout(() => target.destroy(), UPSTREAM_CONNECT_TIMEOUT_MS);
  target.once("connect", () => {
    clearTimeout(timer);
    if (onConnect) socket.write(onConnect);
    if (head.length) target.write(head);
    socket.pipe(target);
    target.pipe(socket);
    socket.resume();
  });
  target.on("error", () => {
    if (target.bytesRead > 0) socket.destroy();
    else if (!socket.destroyed) socket.end(BAD_GATEWAY);
  });
  target.on("close", () => {
    if (socket.destroyed) return;
    socket.end();
    setTimeout(() => socket.destroy(), FLUSH_TIMEOUT_MS).unref();
  });
  socket.on("close", () => target.destroy());
}

function route(config, proxy, socket, head) {
  const end = head.indexOf("\r\n\r\n") + 4;
  const rest = head.subarray(end);
  const lines = head.subarray(0, end).toString("latin1").split("\r\n");
  const [method, target, version] = lines[0].split(" ");
  let host;
  let port;
  let path = null;
  try {
    if (method === "CONNECT") {
      const url = new URL(`http://${target}`);
      host = url.hostname;
      port = Number(url.port);
    } else {
      const url = new URL(target);
      if (url.protocol !== "http:") throw new Error("scheme");
      host = url.hostname;
      port = Number(url.port || 80);
      path = `${url.pathname}${url.search}`;
    }
  } catch {
    socket.end(BAD_GATEWAY);
    return;
  }
  const destination = `${host}:${port}`.toLowerCase();
  if (proxy.selectors.includes(destination)) {
    if (method === "CONNECT") {
      socket.write(CONNECTION_ESTABLISHED);
      carry(config, destination, socket, rest);
    } else {
      carry(config, destination, socket, Buffer.concat([closingHead(lines, `${method} ${path} ${version}`), rest]));
    }
    return;
  }
  const upstream = parseUpstream(method === "CONNECT" ? proxy.upstream_connect : proxy.upstream_http);
  if (upstream) {
    const forwarded = method === "CONNECT" ? head : Buffer.concat([closingHead(lines, lines[0]), rest]);
    pipeTo(socket, upstream, forwarded, null);
  } else if (method === "CONNECT") {
    pipeTo(socket, { host: host.replace(/^\[|\]$/g, ""), port }, rest, CONNECTION_ESTABLISHED);
  } else {
    pipeTo(socket, { host: host.replace(/^\[|\]$/g, ""), port }, Buffer.concat([closingHead(lines, `${method} ${path} ${version}`), rest]), null);
  }
}

function serveProxy(config, proxy, socket) {
  if (proxied >= MAX_PROXY_CONNECTIONS) {
    socket.destroy();
    return;
  }
  proxied += 1;
  socket.on("close", () => {
    proxied -= 1;
  });
  socket.on("error", () => socket.destroy());
  let head = Buffer.alloc(0);
  const read = (data) => {
    head = Buffer.concat([head, data]);
    if (head.indexOf("\r\n\r\n") < 0) {
      if (head.length > MAX_PROXY_HEAD_BYTES) socket.destroy();
      return;
    }
    socket.off("data", read);
    socket.pause();
    route(config, proxy, socket, head);
  };
  socket.on("data", read);
}

function listen(config, listener, handle) {
  return new Promise((resolve, reject) => {
    const server = net.createServer(handle);
    server.once("error", reject);
    server.listen({ host: listener.host, port: listener.port, exclusive: true }, () => {
      server.removeListener("error", reject);
      servers.push(server);
      resolve(server.address().port);
    });
  });
}

async function start(line) {
  const config = JSON.parse(line);
  if (typeof config.status_path === "string") statusPath = config.status_path;
  if (Number.isInteger(config.generation)) generation = config.generation;
  if (typeof config.start_by_ms === "number" && Date.now() > config.start_by_ms) {
    reply({ ok: false, error: "started_late" });
    process.exit(4);
  }
  if (typeof config.relay_host !== "string" || !RELAY_HOST.test(config.relay_host)) {
    reply({ ok: false, error: "invalid_relay" });
    process.exit(2);
  }
  if (typeof config.key !== "string" || config.key.length < 32) {
    reply({ ok: false, error: "invalid_key" });
    process.exit(2);
  }
  const proxy = config.proxy ?? null;
  for (const listener of [...config.listeners, ...(proxy ? [proxy] : [])]) {
    try {
      const port = await listen(config, listener, listener === proxy
        ? (socket) => serveProxy(config, proxy, socket)
        : (socket) => carry(config, listener.selector, socket));
      if (listener === proxy) proxy.port = port;
    } catch (error) {
      reply({ ok: false, error: error.code === "EADDRINUSE" ? "address_in_use" : "listen_failed", host: listener.host, port: listener.port });
      process.exit(3);
    }
  }
  reply(proxy ? { ok: true, listening: config.listeners.length, proxy_port: proxy.port } : { ok: true, listening: config.listeners.length });
  watchRelay(config);
  if (typeof config.stop_accepting_at_ms === "number") {
    setTimeout(() => {
      for (const server of servers) server.close();
      if (active === 0) finish(0);
    }, Math.max(0, config.stop_accepting_at_ms - Date.now()));
  }
  if (typeof config.exit_at_ms === "number") {
    setTimeout(() => finish(0), Math.max(0, config.exit_at_ms - Date.now()));
  }
}

const input = readline.createInterface({ input: process.stdin });
let configured = false;
input.on("line", (line) => {
  if (configured) return;
  configured = true;
  start(line).catch(() => {
    reply({ ok: false, error: "invalid_config" });
    process.exit(2);
  });
});
input.on("close", () => {
  if (!configured) process.exit(2);
  if (process.env.REVYL_TUNNEL_EXIT_ON_STDIN_CLOSE === "1") finish(0);
});
process.on("SIGTERM", () => finish(0));
