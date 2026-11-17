#!/usr/bin/env node
// Writes skills/revyl-workspace/SKILL.md from the Grok plugin's copy, which is
// already written for a host that renders the embedded views. Only the host
// name in the description differs. With --check, fails instead of writing when
// the copy is stale.
import { readFileSync, writeFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

const SOURCE_PATH = fileURLToPath(
  new URL('../../revyl-grok/skills/revyl-workspace/SKILL.md', import.meta.url),
);
const TARGET_PATH = fileURLToPath(
  new URL('../skills/revyl-workspace/SKILL.md', import.meta.url),
);
const SOURCE_HOST = ' inside Grok,';
const TARGET_HOST = ' inside VS Code,';

export function renderVscodeSkill(grokSkill) {
  const occurrences = grokSkill.split(SOURCE_HOST).length - 1;
  if (occurrences !== 1) {
    throw new Error(
      `Expected "${SOURCE_HOST.trim()}" once in the Grok skill, found ${occurrences}; update scripts/sync-skill.mjs`,
    );
  }
  return grokSkill.replace(SOURCE_HOST, TARGET_HOST);
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const expected = renderVscodeSkill(readFileSync(SOURCE_PATH, 'utf8'));
  if (!process.argv.includes('--check')) {
    writeFileSync(TARGET_PATH, expected);
  } else if (readFileSync(TARGET_PATH, 'utf8') !== expected) {
    console.error('skills/revyl-workspace/SKILL.md is stale; run npm run sync-skill');
    process.exit(1);
  }
}
