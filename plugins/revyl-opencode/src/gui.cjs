const { Command, Plugin } = require("@opencode/plugin/gui")
const { Browser } = require("@opencode/plugin/gui/browser")

module.exports = Plugin.define({
  os: ["macos", "linux", "windows"],
  i18n: { en: { "command.open": "Revyl: Show device" } },
  setup(ctx) {
    const browser = ctx.use(Browser)
    const show = (session, device) => {
      const live = browser()
      if (session && device && live.status === "active" && live.value.canOpen(session, device.viewerUrl)) {
        live.value.open(session, device.viewerUrl)
      }
    }
    const devices = () => ctx.client.rpc({ id: "revyl.devices", methods: { list: {} }, events: { changed: {} } })
    const stop = devices().events.on("changed", (event) => {
      show(ctx.sessions.list().find((session) => session.id === event.data.sessionID), event.data.started)
    })
    ctx.add(Command, () => ({
      id: "open",
      title: ctx.t("command.open"),
      enabled: !!ctx.sessions.current(),
      run: async () => {
        const session = ctx.sessions.current()
        if (!session?.location) return
        const { devices: list } = await devices().list({ sessionID: session.id }, { location: session.location })
        show(session, list.at(-1))
      },
    }))
    return stop
  },
})
