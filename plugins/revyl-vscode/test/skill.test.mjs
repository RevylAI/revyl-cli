import assert from 'node:assert/strict';
import { existsSync, readFileSync } from 'node:fs';
import path from 'node:path';
import { test } from 'node:test';
import { fileURLToPath } from 'node:url';

import { renderVscodeSkill } from '../scripts/sync-skill.mjs';

const packageRoot = fileURLToPath(new URL('..', import.meta.url));
const manifest = JSON.parse(readFileSync(path.join(packageRoot, 'package.json'), 'utf8'));

test('each contributed skill exists and is named after its folder', () => {
  for (const { path: skillPath } of manifest.contributes.chatSkills) {
    const file = path.join(packageRoot, skillPath);
    assert.ok(existsSync(file), `${skillPath} is missing`);
    const name = readFileSync(file, 'utf8').match(/^---\nname: (.+)\n/)?.[1];
    assert.equal(name, path.basename(path.dirname(file)));
  }
});

test('the workspace skill is the synced Grok skill', () => {
  const grokSkill = readFileSync(
    path.join(packageRoot, '..', 'revyl-grok', 'skills', 'revyl-workspace', 'SKILL.md'),
    'utf8',
  );
  const vscodeSkill = readFileSync(
    path.join(packageRoot, 'skills', 'revyl-workspace', 'SKILL.md'),
    'utf8',
  );

  assert.equal(vscodeSkill, renderVscodeSkill(grokSkill), 'run npm run sync-skill');
  assert.match(vscodeSkill, /inside VS Code,/);
});
