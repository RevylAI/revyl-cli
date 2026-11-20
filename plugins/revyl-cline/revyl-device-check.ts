import type { AgentPlugin } from "@cline/sdk"
import { execFile } from "node:child_process"
import { resolve } from "node:path"
import { promisify } from "node:util"

const runFile = promisify(execFile)
const publishCommand = /(?:^|&&|\|\||[;|(\n])\s*(?:\w+=(?:"[^"]*"|'[^']*'|\S*)\s+)*(?:git\s+(?:-C\s+("[^"]*"|'[^']*'|\S+)\s+)?push\b|gh\s+pr\s+create\b)/
const directoryChange = /(?:^|&&|\|\||[;|(\n])\s*(?:cd|pushd)\s+("[^"]*"|'[^']*'|[^\s;&|)]+)/g

function publishDirectory(script: string, cwd: string): string | undefined {
  const command = script
    .replace(/<<-?\s*(["']?)(\w+)\1[^\n]*\n[\s\S]*?\n\s*\2(?=\n|$)/g, " ")
    .replace(/"[^"]*"|'[^']*'/g, (quoted) => quoted.replace(/[\n;&|()]/g, " "))
  const publish = publishCommand.exec(command)
  if (!publish) return undefined
  const publishArguments = command.slice(publish.index + publish[0].length).split(/&&|\|\||[;|\n)]/)[0]
  const publishesNothing = publish[0].includes("push") ? /(?:^|\s)(?:--dry-run|-n|--delete|-d|--help|-h)(?=\s|$)/ : /(?:^|\s)(?:--dry-run|--help|-h)(?=\s|$)/
  if (publishesNothing.test(publishArguments)) return undefined
  const unquote = (path: string) => path.replace(/^(["'])(.*)\1$/, "$2")
  const changes = [...command.slice(0, publish.index).matchAll(directoryChange)].map((change) => unquote(change[1]))
  return resolve(cwd, ...changes, unquote(publish[1] ?? "."))
}

const offeredCommits = new Set<string>()
let sessionDirectory = process.cwd()

function commandTexts(input: unknown): string[] {
  if (typeof input === "string") return [input]
  if (Array.isArray(input)) return input.flatMap(commandTexts)
  if (!input || typeof input !== "object") return []
  const { commands, command, cmd, args } = input as { commands?: unknown; command?: unknown; cmd?: unknown; args?: unknown }
  if (commands !== undefined) return commandTexts(commands)
  const text = typeof command === "string" ? command : typeof cmd === "string" ? cmd : undefined
  if (text === undefined) return []
  return [Array.isArray(args) ? [text, ...args].join(" ") : text]
}

async function readGit(cwd: string, args: string[]): Promise<string | undefined> {
  try {
    const { stdout } = await runFile("git", args, { cwd, timeout: 5_000 })
    return stdout.trim() || undefined
  } catch {
    return undefined
  }
}

const plugin: AgentPlugin = {
  name: "revyl-device-check",
  manifest: { capabilities: ["hooks"] },
  setup(_api, ctx) {
    if (ctx.cwd) sessionDirectory = ctx.cwd
  },
  hooks: {
    async afterTool({ toolCall, input, result }) {
      if (toolCall.toolName !== "run_commands" || result.isError) return
      if (!Array.isArray(result.output) || !result.output.every((operation) => operation?.success === true)) return
      const cwd = commandTexts(input).map((command) => publishDirectory(command, sessionDirectory)).find(Boolean)
      if (!cwd) return
      const commit = await readGit(cwd, ["rev-parse", "HEAD"])
      const branch = await readGit(cwd, ["rev-parse", "--abbrev-ref", "HEAD"])
      if (!commit || !branch || branch === "HEAD" || offeredCommits.has(commit)) return
      offeredCommits.add(commit)
      return {
        appendContext: `Revyl: commit ${commit.slice(0, 7)} is now on branch ${branch}. If this repository's app is on Revyl and Revyl's MCP tools are connected, offer once to check this change on a Revyl cloud device, and start nothing without a yes. With a yes, find the app with list_apps, wait for this branch's build with list_builds and get_build, then call start_device_session with that build_id. If no build of this commit appears, say that Revyl isn't building this branch rather than starting an older build.`,
      }
    },
  },
}

export default plugin
