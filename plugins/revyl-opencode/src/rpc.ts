import type { Rpc } from "@opencode/plugin/rpc"

export interface Device {
  readonly revylSessionID: string
  readonly platform: string
  readonly viewerUrl: string
}

const device = {
  type: "object",
  properties: {
    revylSessionID: { type: "string" },
    platform: { type: "string" },
    viewerUrl: { type: "string" },
  },
  required: ["revylSessionID", "platform", "viewerUrl"],
  additionalProperties: false,
} as const

export const RevylDevices = {
  id: "revyl.devices",
  methods: {
    list: {
      input: {
        type: "object",
        properties: { sessionID: { type: "string" } },
        required: ["sessionID"],
        additionalProperties: false,
      },
      output: {
        type: "object",
        properties: { devices: { type: "array", items: device } },
        required: ["devices"],
        additionalProperties: false,
      },
    },
  },
  events: {
    changed: {
      schema: {
        type: "object",
        properties: { sessionID: { type: "string" }, started: device },
        required: ["sessionID"],
        additionalProperties: false,
      },
    },
  },
} satisfies Rpc.Definition
