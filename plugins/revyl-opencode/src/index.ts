import type { Plugin, Skill } from "@opencode/plugin"
import { readdirSync, readFileSync } from "node:fs"
import { join } from "node:path"
import { fileURLToPath } from "node:url"
import { type Device, RevylDevices } from "./rpc.ts"

export const HOSTED_MCP_URL = "https://backend.revyl.ai/api/v1/mcp"
export const MCP_SERVER_NAME = "revyl"
const HOSTED_SKILL_ID = "revyl-workspace"
const skillsDirectory = fileURLToPath(new URL("../skills", import.meta.url))

type McpMode = "hosted" | "local" | false

export function readBundledSkills(directory = skillsDirectory): Skill.Info[] {
  return readdirSync(directory)
    .sort()
    .map((id) => {
      const path = join(directory, id, "SKILL.md")
      const markdown = readFileSync(path, "utf8")
      const match = /^---\n([\s\S]*?)\n---\n/.exec(markdown)
      if (!match) throw new Error(`${path} has no frontmatter`)
      const fields = Object.fromEntries(
        match[1]
          .split("\n")
          .map((line) => /^([\w-]+):\s*(.*)$/.exec(line))
          .filter((field) => field !== null)
          .map((field) => [field[1], field[2].trim()]),
      )
      if (fields.name !== id || !fields.description) throw new Error(`${path} needs name: ${id} and a description`)
      return {
        id,
        name: id,
        description: fields.description,
        autoinvoke: fields["disable-model-invocation"] !== "true",
        path,
        content: markdown.slice(match[0].length).trim(),
      } as Skill.Info
    })
}

function readMcpMode(options: Record<string, unknown>): McpMode {
  const mode = options.mcp ?? "hosted"
  if (mode === "hosted" || mode === "local" || mode === false) return mode
  throw new Error('opencode-revyl: options.mcp must be "hosted", "local", or false')
}

export function mcpServerConfig(mode: Exclude<McpMode, false>) {
  if (mode === "hosted") return { type: "remote" as const, url: HOSTED_MCP_URL }
  return {
    type: "local" as const,
    command: ["revyl", "mcp", "serve", "--tool-profile", "dev"],
    environment: { REVYL_AGENT: "opencode" },
  }
}

type DeviceChange = { readonly started: Device } | { readonly stopped: string }

export function readDeviceChange(call: { tool: string; status: string; input: unknown; result?: { output?: unknown } }): DeviceChange | undefined {
  if (call.status !== "completed") return undefined
  const output = (call.result?.output ?? {}) as Record<string, unknown>
  if (call.tool === `${MCP_SERVER_NAME}_start_device_session`) {
    const viewerUrl = output.viewer_url ?? output.computer_url
    if (typeof output.session_id !== "string" || typeof viewerUrl !== "string") return undefined
    return { started: { revylSessionID: output.session_id, platform: String(output.platform ?? "device"), viewerUrl } }
  }
  if (call.tool === `${MCP_SERVER_NAME}_stop_device_session`) {
    if (output.stopped === false) return undefined
    const sessionID = output.session_id ?? (call.input as Record<string, unknown> | undefined)?.session_id
    return typeof sessionID === "string" ? { stopped: sessionID } : undefined
  }
  return undefined
}

export function revylCommandPrompt(serverStatus: string | undefined, request: string): string {
  const task = request.trim() || "Show what Revyl can do in this project: list my Revyl apps, then suggest next steps."
  switch (serverStatus) {
    case "connected":
    case "pending":
      return `Use Revyl's MCP tools from the \`${MCP_SERVER_NAME}\` server for this request: ${task}\n\nStart a device session, build, or test only when I ask for one, and stop any session you start once the work is done.`
    case "needs_auth":
      return `Tell me that Revyl's MCP server needs sign-in: run /mcps, select \`${MCP_SERVER_NAME}\`, and sign in with my Revyl account, or run \`opencode mcp auth ${MCP_SERVER_NAME}\` in a terminal. Then I can rerun /revyl ${request.trim()}. Do nothing else.`
    case "disabled":
    case "failed":
      return `Tell me that Revyl's MCP server \`${MCP_SERVER_NAME}\` is ${serverStatus} in OpenCode, so /revyl can't use it, and how to check it with \`opencode mcp list\`. Do nothing else.`
    default:
      return `Use the Revyl CLI (\`revyl\`) and the revyl-cli skills for this request: ${task}\n\nIf \`revyl\` isn't installed or signed in, tell me how to install it and run \`revyl auth login\`. Start a device session, build, or test only when I ask for one.`
  }
}

const server: Plugin.Plugin = {
  id: "revyl",
  async setup(ctx) {
    const mode = readMcpMode(ctx.options)
    const skills = readBundledSkills()
    let registeredHosted = false

    if (mode !== false) {
      await ctx.mcp.transform((editor) => {
        registeredHosted = false
        if (editor.get(MCP_SERVER_NAME)) return
        editor.set(MCP_SERVER_NAME, mcpServerConfig(mode))
        registeredHosted = mode === "hosted"
      })
    }

    await ctx.skill.transform((editor) => {
      for (const skill of skills) {
        if (!editor.get(skill.id)) editor.add(skill)
      }
    })

    const devices = new Map<string, Map<string, Device>>()
    const deviceRpc = await ctx.rpc.register(RevylDevices, {
      list: async (input) => ({ devices: [...(devices.get((input as { sessionID: string }).sessionID)?.values() ?? [])] }),
    })
    await ctx.tool.hook("execute.after", async (call) => {
      const change = readDeviceChange(call)
      if (!change) return
      const sessionDevices = devices.get(call.sessionID) ?? new Map<string, Device>()
      devices.set(call.sessionID, sessionDevices)
      if ("started" in change) sessionDevices.set(change.started.revylSessionID, change.started)
      else sessionDevices.delete(change.stopped)
      await deviceRpc.events.emit("changed", { sessionID: call.sessionID, ...("started" in change ? { started: change.started } : {}) })
    })

    await ctx.command.transform((editor) => {
      editor.add({
        name: "revyl",
        description: "Use Revyl cloud devices, builds, tests, and Atlas from this session",
        execute: async ({ sessionID, prompt, delivery }) => {
          const servers = await ctx.mcp.list()
          const status = servers.data.find((entry) => entry.name === MCP_SERVER_NAME)?.status.status
          const attachHostedSkill = registeredHosted && (status === "connected" || status === "pending")
          await ctx.session.prompt({
            ...prompt,
            sessionID,
            delivery,
            text: revylCommandPrompt(status, prompt.text),
            skills: attachHostedSkill ? [...(prompt.skills ?? []), { id: HOSTED_SKILL_ID as Skill.ID }] : prompt.skills,
          })
        },
      })
    })
  },
}

export default server
