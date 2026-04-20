# Embedding the Live Device Stream

Every active Revyl device session streams the screen in real time over [WHEP](https://www.ietf.org/archive/id/draft-murillo-whep-03.html) (WebRTC-HTTP Egress Protocol). You can embed this stream in your own dashboard, CI viewer, or internal tool.

## Getting the stream URL

### Python SDK

```python
from revyl import DeviceClient

with DeviceClient.start(platform="ios", app_url="...") as device:
    whep_url = device.wait_for_stream(timeout=30)
    print(whep_url)
    # https://customer-xxx.cloudflarestream.com/<id>/webRTC/play
```

### CLI

```bash
revyl device info --json | jq -r '.whep_url'
```

---

## Embed in a web page (vanilla JS)

This is the minimal code to render the live stream in a `<video>` element. The WHEP handshake is a single HTTP POST — no libraries required.

```html
<video id="device-stream" autoplay playsinline muted></video>

<script>
  const WHEP_URL = "YOUR_WHEP_URL_HERE";

  async function startStream() {
    const pc = new RTCPeerConnection({
      iceServers: [{ urls: "stun:stun.cloudflare.com:3478" }],
    });

    pc.addTransceiver("video", { direction: "recvonly" });

    pc.ontrack = (event) => {
      document.getElementById("device-stream").srcObject = event.streams[0];
    };

    const offer = await pc.createOffer();
    await pc.setLocalDescription(offer);

    const resp = await fetch(WHEP_URL, {
      method: "POST",
      headers: { "Content-Type": "application/sdp" },
      body: offer.sdp,
    });

    const answerSdp = await resp.text();
    await pc.setRemoteDescription({ type: "answer", sdp: answerSdp });
  }

  startStream();
</script>
```

### What this does

1. Creates an `RTCPeerConnection` with Cloudflare's STUN server
2. Adds a receive-only video transceiver
3. Generates an SDP offer and POSTs it to the WHEP URL
4. Sets the SDP answer from the response
5. The `ontrack` callback wires the incoming video to the `<video>` element

---

## Embed in React

```tsx
import { useEffect, useRef } from "react";

function DeviceStream({ whepUrl }: { whepUrl: string }) {
  const videoRef = useRef<HTMLVideoElement>(null);

  useEffect(() => {
    if (!whepUrl) return;

    const pc = new RTCPeerConnection({
      iceServers: [{ urls: "stun:stun.cloudflare.com:3478" }],
    });

    pc.addTransceiver("video", { direction: "recvonly" });

    pc.ontrack = (event) => {
      if (videoRef.current) {
        videoRef.current.srcObject = event.streams[0];
      }
    };

    (async () => {
      const offer = await pc.createOffer();
      await pc.setLocalDescription(offer);

      const resp = await fetch(whepUrl, {
        method: "POST",
        headers: { "Content-Type": "application/sdp" },
        body: offer.sdp,
      });

      const answer = await resp.text();
      await pc.setRemoteDescription({ type: "answer", sdp: answer });
    })();

    return () => pc.close();
  }, [whepUrl]);

  return <video ref={videoRef} autoPlay playsInline muted />;
}
```

---

## Embed via iframe (zero JS)

Cloudflare Stream also exposes an iframe-compatible player. Replace `webRTC/play` with `iframe` in the URL:

```
Stream URL:  https://customer-xxx.cloudflarestream.com/<id>/webRTC/play
Iframe URL:  https://customer-xxx.cloudflarestream.com/<id>/iframe
```

```html
<iframe
  src="https://customer-xxx.cloudflarestream.com/<id>/iframe"
  style="width: 400px; height: 800px; border: none"
  allow="autoplay"
></iframe>
```

---

## Full Python example

Start a session, get the stream URL, do some work, then stop.

```python
from revyl import DeviceClient

APP_URL = "https://example.com/your-app.tar.gz"

with DeviceClient.start(platform="ios", app_url=APP_URL) as device:
    whep_url = device.wait_for_stream(timeout=30)

    if whep_url:
        print(f"Live stream: {whep_url}")
        # Pass this URL to your dashboard, CI viewer, Slack bot, etc.

    device.tap("Login button")
    device.screenshot("after_login.png")

    # Stream stays live until the session ends
```

---

## Notes

- The stream URL becomes available a few seconds after the session starts. Use `wait_for_stream()` to poll for it.
- The stream is live for the lifetime of the session and stops automatically when the session ends.
- For production use, add retry logic on the WHEP POST (the stream source may briefly restart during the session). See `useWHEPPlayback.ts` in the Revyl frontend for a hardened implementation with reconnection and stall detection.
- Safari requires H.264 codec preference -- set it via `transceiver.setCodecPreferences()` if you need Safari support.
