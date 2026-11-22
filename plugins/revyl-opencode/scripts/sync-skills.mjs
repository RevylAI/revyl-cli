#!/usr/bin/env node
// Copies the skills this plugin bundles from their sources: the hosted-MCP
// revyl-workspace skill from plugins/revyl-mcp and the recommended CLI skills
// from skills/. With --check, fails instead of writing when a copy is stale.
import { cpSync, existsSync, readdirSync, readFileSync, rmSync, statSync } from "node:fs"
import { join, relative } from "node:path"
import { fileURLToPath } from "node:url"

const pluginRoot = fileURLToPath(new URL("..", import.meta.url))
const cliRoot = join(pluginRoot, "..", "..")
export const SKILL_SOURCES = {
  "revyl-workspace": join(cliRoot, "plugins", "revyl-mcp", "skills", "revyl-workspace"),
  "revyl-cli-dev-loop": join(cliRoot, "skills", "revyl-cli-dev-loop"),
  "revyl-cli-atlas": join(cliRoot, "skills", "revyl-cli-atlas"),
  "revyl-cli-create": join(cliRoot, "skills", "revyl-cli-create"),
  "revyl-cli-auth-bypass": join(cliRoot, "skills", "revyl-cli-auth-bypass"),
}
const skillsRoot = join(pluginRoot, "skills")

function listFiles(directory) {
  if (!existsSync(directory)) return []
  return readdirSync(directory, { recursive: true })
    .map((entry) => join(directory, entry))
    .filter((path) => statSync(path).isFile())
    .map((path) => relative(directory, path))
    .sort()
}

export function staleSkills() {
  const bundled = existsSync(skillsRoot) ? readdirSync(skillsRoot).sort() : []
  const stale = bundled.filter((name) => !(name in SKILL_SOURCES))
  for (const [name, source] of Object.entries(SKILL_SOURCES)) {
    const target = join(skillsRoot, name)
    const sourceFiles = listFiles(source)
    const same =
      JSON.stringify(sourceFiles) === JSON.stringify(listFiles(target)) &&
      sourceFiles.every((file) => readFileSync(join(source, file)).equals(readFileSync(join(target, file))))
    if (!same) stale.push(name)
  }
  return stale
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  if (process.argv.includes("--check")) {
    const stale = staleSkills()
    if (stale.length > 0) {
      console.error(`Bundled skills are stale (${stale.join(", ")}); run node scripts/sync-skills.mjs`)
      process.exit(1)
    }
  } else {
    rmSync(skillsRoot, { recursive: true, force: true })
    for (const [name, source] of Object.entries(SKILL_SOURCES)) cpSync(source, join(skillsRoot, name), { recursive: true })
  }
}
