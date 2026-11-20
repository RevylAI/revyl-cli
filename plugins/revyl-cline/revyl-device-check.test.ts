import assert from "node:assert/strict"
import { execFileSync } from "node:child_process"
import { randomUUID } from "node:crypto"
import { mkdtempSync, rmSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import { afterEach, test } from "node:test"
import kiloPlugin from "../revyl-kilo/server.ts"
import plugin from "./revyl-device-check.ts"

export function createRepository(): { directory: string; commit: string; remove: () => void } {
  const directory = mkdtempSync(join(tmpdir(), "revyl-device-check-"))
  const git = (...args: string[]) => execFileSync("git", args, { cwd: directory, encoding: "utf8" }).trim()
  git("init", "--quiet", "--initial-branch", "main")
  git("-c", "user.name=Revyl Test", "-c", "user.email=test@example.com", "commit", "--quiet", "--allow-empty", "-m", `Change login screen ${randomUUID()}`)
  git("checkout", "--quiet", "-b", "feature/login")
  return { directory, commit: git("rev-parse", "HEAD"), remove: () => rmSync(directory, { recursive: true, force: true }) }
}

const repositories: Array<{ remove: () => void }> = []
afterEach(() => repositories.splice(0).forEach((repository) => repository.remove()))

async function runCommands(directory: string, input: unknown, options: { toolName?: string; isError?: boolean; commandFailed?: boolean } = {}) {
  plugin.setup!({} as never, { cwd: directory })
  const output = [{ query: "git push", result: "pushed", success: !options.commandFailed }]
  return plugin.hooks!.afterTool!({
    toolCall: { type: "tool-call", toolCallId: "c", toolName: options.toolName ?? "run_commands", input },
    input,
    result: { toolUseId: "c", toolName: "run_commands", input, output, isError: options.isError ?? false },
  } as never)
}

test("a successful push offers a device check of the pushed commit once", async () => {
  const repository = createRepository()
  repositories.push(repository)

  const first = await runCommands(repository.directory, { commands: ["git add -A", "git push -u origin feature/login"] })
  const second = await runCommands(repository.directory, { commands: ["git push"] })

  assert.match(first?.appendContext ?? "", new RegExp(`^Revyl: commit ${repository.commit.slice(0, 7)} is now on branch feature/login\\.`))
  assert.match(first?.appendContext ?? "", /start nothing without a yes/)
  assert.equal(second, undefined)
})

test("every run_commands input shape is read", async () => {
  for (const input of ["gh pr create --fill", { command: "git push" }, { commands: [{ command: "gh", args: ["pr", "create"] }] }]) {
    const repository = createRepository()
    repositories.push(repository)

    const result = await runCommands(repository.directory, input)

    assert.match(result?.appendContext ?? "", /^Revyl: commit/, JSON.stringify(input))
  }
})

test("other tools, failed commands, unpublished work and detached heads add nothing", async () => {
  const repository = createRepository()
  repositories.push(repository)
  const detached = createRepository()
  repositories.push(detached)
  execFileSync("git", ["checkout", "--quiet", "--detach"], { cwd: detached.directory })

  assert.equal(await runCommands(repository.directory, { commands: ["git push"] }, { toolName: "read_files" }), undefined)
  assert.equal(await runCommands(repository.directory, { commands: ["git push"] }, { isError: true }), undefined)
  assert.equal(await runCommands(repository.directory, { commands: ["git push"] }, { commandFailed: true }), undefined)
  assert.equal(await runCommands(repository.directory, { commands: ["git status", "echo git pushed"] }), undefined)
  assert.equal(await runCommands(detached.directory, { commands: ["git push"] }), undefined)
})

test("a push from another repository names that repository's commit", async () => {
  const workspace = createRepository()
  const pushed = createRepository()
  repositories.push(workspace, pushed)

  const result = await runCommands(workspace.directory, { commands: [`cd ${pushed.directory} && git push`] })

  assert.match(result?.appendContext ?? "", new RegExp(`^Revyl: commit ${pushed.commit.slice(0, 7)} `))
})

test("the offer matches the Kilo plugin's word for word", async () => {
  const repository = createRepository()
  repositories.push(repository)
  const kiloHooks = await kiloPlugin.server({ directory: repository.directory } as never)
  const kiloOutput = { title: "", output: "", metadata: { exit: 0 } }

  await kiloHooks["tool.execute.after"]!({ tool: "bash", sessionID: "s", callID: "c", args: { command: "git push" } }, kiloOutput)
  const cline = await runCommands(repository.directory, { commands: ["git push"] })

  assert.equal(cline?.appendContext, kiloOutput.output.trimStart())
})
