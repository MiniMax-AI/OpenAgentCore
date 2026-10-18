// Every authored page under docs/ and contracts/agents-api/, English or Chinese, belongs to a docs.json tab.
import assert from 'node:assert/strict'
import { readdirSync } from 'node:fs'
import { resolve } from 'node:path'
import test from 'node:test'
import { listedPages, repoRoot } from '../.vitepress/docs-nav.mts'

test('no authored page is missing from docs.json', () => {
  const listed = new Set(listedPages())
  const pages = ['docs', 'contracts/agents-api'].flatMap((dir) =>
    readdirSync(resolve(repoRoot, dir), { recursive: true })
      .filter((file) => file.endsWith('.md') && !file.startsWith('assets/'))
      .map((file) => `${dir}/${file.replace(/\.md$/, '')}`),
  )
  const english = pages.map((page) => (/\/zh\//.test(page) ? page.replace(/\/zh\//, '/') : page))
  assert.deepEqual([...new Set(english)].filter((page) => !listed.has(page)).sort(), [])
})
