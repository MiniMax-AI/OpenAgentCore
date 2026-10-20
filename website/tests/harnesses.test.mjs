// The landing page's harness table must match the repository's declarations:
// ids and labels from the generated client catalog, protocols from Model execution.
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import test from 'node:test'
import { repoRoot } from '../.vitepress/docs-nav.mts'
import { harnesses } from '../.vitepress/theme/landing-content.ts'

test('harness ids and labels match the generated client catalog', () => {
  const source = readFileSync(resolve(repoRoot, 'packages/agents-client/src/harness-catalog.ts'), 'utf8')
  const expected = Object.fromEntries([...source.matchAll(/^  "([a-z_]+)": "([^"]+)",$/gm)].map((m) => [m[1], m[2]]))
  assert.deepEqual(Object.fromEntries(harnesses.map((h) => [h.id, h.label])), expected)
})

test('harness protocols match Model execution, default first', () => {
  const doc = readFileSync(resolve(repoRoot, 'contracts/agents-api/model-execution.md'), 'utf8')
  const table = doc.split('| Harness | Supported protocols, default first |')[1].split('\n\n')[0]
  const rows = Object.fromEntries(
    [...table.matchAll(/^\| ([^|]+) \| ([^|]+) \|/gm)]
      .filter(([, name]) => !name.startsWith('---'))
      .map(([, name, protocols]) => [name.trim(), [...protocols.matchAll(/`([a-z_]+)`/g)].map((m) => m[1])]),
  )
  for (const h of harnesses) assert.deepEqual([...h.protocols], rows[h.label], h.id)
})
