import type { Plugin } from "@kilocode/plugin"
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

async function readGit(cwd: string, args: string[]): Promise<string | undefined> {
  try {
    const { stdout } = await runFile("git", args, { cwd, timeout: 5_000 })
    return stdout.trim() || undefined
  } catch {
    return undefined
  }
}

const server: Plugin = async ({ directory }) => {
  const offeredCommits = new Set<string>()
  return {
    "tool.execute.after": async (input, output) => {
      if (input.tool !== "bash" || output.metadata?.exit !== 0) return
      const cwd = publishDirectory(String(input.args?.command ?? ""), resolve(directory, input.args?.workdir ?? "."))
      if (!cwd) return
      const commit = await readGit(cwd, ["rev-parse", "HEAD"])
      const branch = await readGit(cwd, ["rev-parse", "--abbrev-ref", "HEAD"])
      if (!commit || !branch || branch === "HEAD" || offeredCommits.has(commit)) return
      offeredCommits.add(commit)
      output.output += `\n\nRevyl: commit ${commit.slice(0, 7)} is now on branch ${branch}. If this repository's app is on Revyl and Revyl's MCP tools are connected, offer once to check this change on a Revyl cloud device, and start nothing without a yes. With a yes, find the app with list_apps, wait for this branch's build with list_builds and get_build, then call start_device_session with that build_id. If no build of this commit appears, say that Revyl isn't building this branch rather than starting an older build.`
    },
  }
}

export default { id: "revyl-device-check", server }
