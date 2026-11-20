import assert from "node:assert/strict"
import { execFileSync } from "node:child_process"
import { randomUUID } from "node:crypto"
import { mkdtempSync, rmSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import { afterEach, test } from "node:test"
import plugin from "./server.ts"

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

async function runBash(directory: string, args: Record<string, unknown>, exit = 0): Promise<string> {
  const hooks = await plugin.server({ directory } as never)
  const output = { title: "", output: "pushed", metadata: { exit } }
  await hooks["tool.execute.after"]!({ tool: "bash", sessionID: "s", callID: "c", args }, output)
  return output.output
}

test("a successful push offers a device check of the pushed commit once", async () => {
  const repository = createRepository()
  repositories.push(repository)
  const hooks = await plugin.server({ directory: repository.directory } as never)
  const call = async () => {
    const output = { title: "", output: "pushed", metadata: { exit: 0 } }
    await hooks["tool.execute.after"]!({ tool: "bash", sessionID: "s", callID: "c", args: { command: "git push -u origin feature/login" } }, output)
    return output.output
  }

  const first = await call()
  const second = await call()

  assert.match(first, new RegExp(`^pushed\\n\\nRevyl: commit ${repository.commit.slice(0, 7)} is now on branch feature/login\\.`))
  assert.match(first, /start nothing without a yes/)
  assert.equal(second, "pushed")
})

test("opening a pull request from a relative workdir offers a device check", async () => {
  const repository = createRepository()
  repositories.push(repository)

  const output = await runBash(join(repository.directory, ".."), { command: "gh pr create --fill", workdir: repository.directory.split("/").at(-1) })

  assert.match(output, /Revyl: commit [0-9a-f]{7} is now on branch feature\/login/)
})

test("a push from another repository names that repository's commit", async () => {
  const workspace = createRepository()
  const pushed = createRepository()
  repositories.push(workspace, pushed)

  const viaCd = await runBash(workspace.directory, { command: `cd "${pushed.directory}" && git push` })
  const viaGitC = await runBash(workspace.directory, { command: `git -C ${pushed.directory} push` })

  assert.match(viaCd, new RegExp(`Revyl: commit ${pushed.commit.slice(0, 7)} `))
  assert.match(viaGitC, new RegExp(`Revyl: commit ${pushed.commit.slice(0, 7)} `))
})

test("a push on its own line or after environment assignments offers a device check", async () => {
  for (const command of ["git status\ngit push", "git commit -m 'Add --dry-run flag' && git push", "npm publish --dry-run && git push", "gh pr create -d --fill", "GIT_SSH_COMMAND='ssh -i key' GIT_TRACE=1 git push"]) {
    const repository = createRepository()
    repositories.push(repository)

    const output = await runBash(repository.directory, { command })

    assert.match(output, /Revyl: commit [0-9a-f]{7} is now on branch feature\/login/, command)
  }
})

test("commands that publish nothing, failed pushes and detached heads add nothing", async () => {
  const repository = createRepository()
  repositories.push(repository)
  const detached = createRepository()
  repositories.push(detached)
  execFileSync("git", ["checkout", "--quiet", "--detach"], { cwd: detached.directory })

  assert.equal(await runBash(repository.directory, { command: "git status && echo git pushed" }), "pushed")
  assert.equal(await runBash(repository.directory, { command: "echo git push" }), "pushed")
  for (const command of ["git push --dry-run", "git push -n", "git push origin --delete old-branch", "git push -d origin old-branch", "git push --help", "gh pr create --help"]) {
    assert.equal(await runBash(repository.directory, { command }), "pushed", command)
  }
  assert.equal(await runBash(repository.directory, { command: "echo 'build ok\ngit push'" }), "pushed")
  assert.equal(await runBash(repository.directory, { command: "cat > release.sh <<'EOF'\ngit push\nEOF\nchmod +x release.sh" }), "pushed")
  assert.equal(await runBash(repository.directory, { command: "git push" }, 1), "pushed")
  assert.equal(await runBash(detached.directory, { command: "git push" }), "pushed")
})
