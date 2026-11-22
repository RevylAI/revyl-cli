import { Plugin, usePlugin } from "@opencode/plugin/tui"
import { createResource, For, onCleanup, Show } from "solid-js"
import { RevylDevices } from "./rpc.ts"

function DeviceList(props: { sessionID: string }) {
  const context = usePlugin()
  const rpc = () => context.client.rpc(RevylDevices)
  const [list, { refetch }] = createResource(
    () => props.sessionID,
    async (sessionID) =>
      (await rpc().list({ sessionID }, { location: context.location ?? context.data.location.default() })).devices,
  )
  onCleanup(rpc().events.on("changed", (event) => event.data.sessionID === props.sessionID && void refetch()))
  return (
    <Show when={list()?.length}>
      <box flexDirection="column">
        <text fg={context.theme.text.base}>
          <b>Revyl devices</b>
        </text>
        <For each={list()}>
          {(device) => (
            <text fg={context.theme.text.base}>
              {device.platform} {device.viewerUrl}
            </text>
          )}
        </For>
      </box>
    </Show>
  )
}

export default Plugin.define({
  id: "revyl",
  setup(context) {
    return context.ui.slot({
      append: "sidebar.content",
      render: (input) => <DeviceList sessionID={input.sessionID} />,
    })
  },
})
