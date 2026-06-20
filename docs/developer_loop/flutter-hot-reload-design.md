# Flutter hot reload via reverse forwarding — design & backend contract

Status: **CLI scaffolding landed (gated off); backend work required to enable.**

## Why this exists

Revyl streams code to cloud devices for JS frameworks (Expo / React Native) but
rebuilds Flutter on every change. The blocker is not Flutter — Flutter has
first-class hot reload — but the **direction** of Revyl's relay.

- **JS (works today):** Metro runs on the laptop; the **device pulls** the
  bundle. The relay is built for exactly this — the device opens a connection
  and the backend forwards it to a port on the laptop.
- **Flutter (stock):** `flutter attach` runs on the laptop and **connects into**
  the Dart VM Service that the running app exposes **on the device**. The host
  is the client; the device is the server.

So Flutter needs the laptop to reach a port **on the device** — the mirror image
of what the relay does today. We call that **reverse forwarding**.

The data plane is already bidirectional (the relay carries websocket frames both
ways). The missing capability is **connection initiation**: today only the
device can open a stream, and the relay only ever dials a port on the laptop.
Reverse mode adds laptop-initiated streams whose target is a device-side port.

## Architecture

```
FileWatcher ──▶ FlutterAttachDevServer.Reload()  (sends "r" to flutter attach)
                         │
                 flutter attach --debug-url http://127.0.0.1:NNN/
                         │ dials
                         ▼
        ReverseRelayTunnelBackend  (local TCP listener 127.0.0.1:NNN)
                         │ proxies raw bytes over the relay websocket
                         ▼
        Revyl backend (REVERSE MODE)  ──▶ dials device VM Service port
                         ▼
                 Dart VM Service (on the cloud device, debug build)
```

The proxy is **raw TCP**, so it transparently carries the VM Service's HTTP
upgrade and websocket frames — no protocol awareness needed in the relay.

## What this PR lands (CLI, `revyl-cli`)

All additive and **gated** behind `FlutterProvider.IsSupported() == false`, so
user-facing behavior is unchanged (Flutter still routes to the rebuild loop).

| Area | File | What |
|------|------|------|
| Reverse tunnel contract | `internal/hotreload/tunnel.go` | `ReverseTunnelBackend` interface |
| Reload contract | `internal/hotreload/devserver.go` | `Reloadable` interface |
| Reverse proxy | `internal/hotreload/reverse_relay.go` | `ReverseRelayTunnelBackend` (local listener ↔ relay) |
| Attach dev server | `internal/hotreload/providers/flutter_devserver.go` | `FlutterAttachDevServer` (drives `flutter attach`) |
| Provider wiring | `internal/hotreload/providers/flutter_provider.go` | `CreateDevServer`, `DevLoopStyle() == "attach"` |
| Dev-loop styles | `internal/hotreload/provider.go` | `ProviderDevLoopStyler`, `ProviderDevLoopStyle()` |
| API params | `internal/api/client.go` | `Mode`, `DevicePort`, `DeviceVMServicePort` |

### Still TODO in the CLI (follow-up PRs, after backend lands)
1. **Manager attach branch** — in `Manager.Start` ([manager.go]), add an
   `attach`-style path: discover device VM Service port → `StartReverse` →
   `SetDebugURL` → `flutter attach`. Reuse the existing health/reconnect path.
2. **`revyl dev` routing** — flip Flutter off the rebuild loop when the backend
   advertises reverse support; keep rebuild as graceful fallback.
3. **File-change classification** — `*.dart` → reload; `pubspec.yaml`/native →
   fall back to rebuild. Reuse `FlutterWatchConfig()`.
4. **Debug build variant** — ensure the dev build is `--debug` with the VM
   Service enabled, marked hot-reload-capable.
5. **Flip `IsSupported()` → true** and update the support tables in
   `dev-setup.md`, `builds/index.md`, `dev-loop.md`.

## Backend contract (work required outside this repo)

### 1. Reverse-mode relay sessions

`POST /api/v1/hotreload/relays` accepts new fields:

```jsonc
{
  "provider": "flutter",
  "platform": "ios",
  "mode": "reverse",        // NEW — "forward" (default) | "reverse"
  "device_port": 0          // NEW — VM Service port to dial; see discovery
}
```

Response may include:

```jsonc
{
  "relay_id": "...",
  "connect_url": "wss://...",
  "connect_token": "...",
  "transport": "reverse",
  "device_vm_service_port": 54321   // NEW — discovered VM Service port
}
```

In reverse mode the backend must:
- accept **CLI-initiated** streams over the relay websocket, and
- for each stream, **dial the device's VM Service port** and bridge bytes.

### 2. Reverse data-plane protocol

Reuses the existing envelope shape, with three kinds the **CLI sends** and the
backend acts on (mirror of the forward `http.request.*` / `ws.*` kinds):

| Kind | Direction | Meaning |
|------|-----------|---------|
| `device.dial.start` | CLI → backend | Open a new stream; backend dials the device VM Service port |
| `device.dial.data`  | both ways | Base64 byte chunk for `stream_id` |
| `device.dial.close` | both ways | Half/!full close for `stream_id` |
| `stream.error`      | backend → CLI | Dial/transport error for `stream_id` |

Framing matches forward mode: `stream_id`, `body_chunk_b64`, base64 chunks
sized ~32 KiB. Heartbeat (`ping`/`pong`) is reused unchanged.

### 3. VM Service discovery (device fleet)

When a Flutter **debug** build launches on a cloud device, the fleet must:
- launch with the Dart VM Service **enabled and listening** (debug/JIT),
- discover its port/auth path, and
- report it — either inline on the relay session (`device_vm_service_port`) or
  via a separate poll endpoint. **Open question: which?**

### 4. Auth

Laptop-initiated streams into a device port are a new privilege. Reuse the relay
`connect_token` (`ConnectAuthHeader`) and scope reverse sessions to the
authenticated user's own device session.

## Rollout / gating

1. Backend ships reverse mode behind a capability flag.
2. CLI flips `IsSupported()` only when the backend advertises reverse support
   (so older backends keep the rebuild loop).
3. Validate first against a **local emulator** (no relay) to de-risk the
   attach + reload mechanics, then through the relay.

## Open questions

1. VM Service port discovery: inline on the session, or a separate poll?
2. Reverse streams over the existing relay websocket (new kinds), or a separate
   session type?
3. iOS debug signing for VM-Service-enabled simulator builds.
4. Reload vs hot-restart vs rebuild classification — owned by CLI, but the
   fleet must support reinstall-less hot restart for the middle tier.
