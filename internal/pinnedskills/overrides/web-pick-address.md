2. Pick an address, using the first of these that works:
   - **The local dev server, through a tunnel.** When the app has a dev command, ask the user once to open it through a temporary public HTTPS tunnel, and name a deployed address too when you found one. With a yes, start the dev server if nothing answers on its port, then start the tunnel, such as `cloudflared tunnel --url http://localhost:<port>`. Anyone with the tunnel's link can open it while it runs.
   - **A deployed or preview address** that serves this app, from the README or deployment config such as `vercel.json` or `netlify.toml`.
   - **Neither:** stop and tell the user why. Never open someone else's site or a placeholder.
