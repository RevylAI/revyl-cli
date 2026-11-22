import assert from "node:assert/strict"
import { test } from "node:test"
import { staleSkills } from "../scripts/sync-skills.mjs"
import plugin, { HOSTED_MCP_URL, readDeviceChange } from "./index.ts"

type Prompt = { sessionID: string; text: string; delivery: string; skills?: Array<{ id: string }> }

async function load(options: Record<string, unknown> = {}, existing: { mcp?: string[]; skills?: string[] } = {}) {
  const servers = new Map<string, unknown>((existing.mcp ?? []).map((name) => [name, { type: "local", command: ["mine"] }]))
  const skills = new Map<string, any>((existing.skills ?? []).map((id) => [id, { id, content: "user copy" }]))
  const commands = new Map<string, any>()
  const prompts: Prompt[] = []
  const events: unknown[] = []
  const hooks = new Map<string, (input: any) => Promise<void>>()
  let devices: any
  let status: string | undefined
  const ctx = {
    options,
    rpc: { register: async (_: unknown, handlers: any) => ((devices = handlers), { events: { emit: async (_: string, data: unknown) => void events.push(data) } }) },
    tool: { hook: async (name: string, callback: (input: any) => Promise<void>) => void hooks.set(name, callback) },
    mcp: {
      transform: async (callback: (editor: unknown) => void) =>
        callback({ get: (name: string) => servers.get(name), set: (name: string, config: unknown) => servers.set(name, config) }),
      list: async () => ({ data: status ? [{ name: "revyl", status: { status } }] : [] }),
    },
    skill: {
      transform: async (callback: (editor: unknown) => void) =>
        callback({ get: (id: string) => skills.get(id), add: (skill: any) => skills.set(skill.id, skill) }),
    },
    command: {
      transform: async (callback: (editor: unknown) => void) => callback({ add: (command: any) => commands.set(command.name, command) }),
    },
    session: { prompt: async (input: Prompt) => void prompts.push(input) },
  }
  await plugin.setup(ctx as never)
  const runRevyl = async (serverStatus: string | undefined, text: string) => {
    status = serverStatus
    await commands.get("revyl").execute({ sessionID: "ses_1", prompt: { text }, delivery: "steer" })
    return prompts.at(-1)!
  }
  const callTool = (tool: string, input: unknown, output: unknown) =>
    hooks.get("execute.after")!({ tool, sessionID: "ses_1", input, status: "completed", result: { output } })
  const listDevices = async () => (await devices.list({ sessionID: "ses_1" })).devices
  return { servers, skills, runRevyl, callTool, listDevices, events }
}

test("registers the hosted server unless the user already configured one or opted out", async () => {
  assert.deepEqual((await load()).servers.get("revyl"), { type: "remote", url: HOSTED_MCP_URL })
  assert.deepEqual((await load({}, { mcp: ["revyl"] })).servers.get("revyl"), { type: "local", command: ["mine"] })
  assert.deepEqual((await load({ mcp: "local" })).servers.get("revyl"), {
    type: "local",
    command: ["revyl", "mcp", "serve", "--tool-profile", "dev"],
    environment: { REVYL_AGENT: "opencode" },
  })
  assert.equal((await load({ mcp: false })).servers.size, 0)
  await assert.rejects(load({ mcp: "remote" }), /options\.mcp must be/)
})

test("adds the bundled skills without replacing ones the user installed", async () => {
  const { skills } = await load({}, { skills: ["revyl-cli-dev-loop"] })

  assert.deepEqual([...skills.keys()].sort(), [
    "revyl-cli-atlas",
    "revyl-cli-auth-bypass",
    "revyl-cli-create",
    "revyl-cli-dev-loop",
    "revyl-workspace",
  ])
  assert.equal(skills.get("revyl-cli-dev-loop").content, "user copy")
  const workspace = skills.get("revyl-workspace")
  assert.equal(workspace.autoinvoke, true)
  assert.match(workspace.description, /^Open an app's Revyl Atlas map/)
  assert.match(workspace.path, /skills\/revyl-workspace\/SKILL\.md$/)
  assert.match(workspace.content, /^# Revyl Workspace/)
  assert.equal(skills.get("revyl-cli-auth-bypass").autoinvoke, false)
})

test("/revyl routes the request by the Revyl server's connection state", async () => {
  const { runRevyl } = await load()

  const connected = await runRevyl("connected", "start an Android session for Fennel")
  assert.match(connected.text, /start an Android session for Fennel/)
  assert.deepEqual(connected.skills, [{ id: "revyl-workspace" }])
  assert.equal(connected.sessionID, "ses_1")

  assert.match((await runRevyl("connected", "")).text, /list my Revyl apps/)
  assert.deepEqual((await runRevyl("pending", "list apps")).skills, [{ id: "revyl-workspace" }])

  const signIn = await runRevyl("needs_auth", "list apps")
  assert.match(signIn.text, /run \/mcps, select `revyl`/)
  assert.match(signIn.text, /opencode mcp auth revyl/)
  assert.equal(signIn.skills, undefined)

  assert.match((await runRevyl("failed", "")).text, /is failed in OpenCode/)
  assert.match((await runRevyl(undefined, "list apps")).text, /Revyl CLI/)
})

test("tracks the device sessions each OpenCode session starts and stops", async () => {
  const { callTool, listDevices, events } = await load()
  const viewerUrl = "https://app.revyl.ai/sessions/dev-1"

  await callTool("revyl_start_device_session", { platform: "android" }, { session_id: "dev-1", platform: "android", viewer_url: viewerUrl })
  await callTool("revyl_list_apps", {}, { apps: [] })
  const started = { revylSessionID: "dev-1", platform: "android", viewerUrl }
  assert.deepEqual(await listDevices(), [started])
  assert.deepEqual(events, [{ sessionID: "ses_1", started }])

  await callTool("revyl_stop_device_session", { session_id: "dev-1" }, { session_id: "dev-1", stopped: true })
  assert.deepEqual(await listDevices(), [])
  assert.deepEqual(events.at(-1), { sessionID: "ses_1" })
})

test("reads a Mac computer's link and ignores failed starts and unfinished stops", () => {
  const computer = { session_id: "mac-1", platform: "macos", computer_url: "https://app.revyl.ai/computers/mac-1" }
  assert.deepEqual(readDeviceChange({ tool: "revyl_start_device_session", status: "completed", input: {}, result: { output: computer } }), {
    started: { revylSessionID: "mac-1", platform: "macos", viewerUrl: computer.computer_url },
  })
  assert.equal(readDeviceChange({ tool: "revyl_start_device_session", status: "error", input: {} }), undefined)
  const stopping = { session_id: "dev-1", request_accepted: true, stopped: false }
  assert.equal(readDeviceChange({ tool: "revyl_stop_device_session", status: "completed", input: {}, result: { output: stopping } }), undefined)
})

test("bundled skills match their sources", () => {
  assert.deepEqual(staleSkills(), [])
})
