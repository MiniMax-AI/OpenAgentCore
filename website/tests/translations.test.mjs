import assert from 'node:assert/strict'
import { createHash } from 'node:crypto'
import { existsSync, readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import test from 'node:test'
import { authoredPages, translatedSource, sourceRoute } from '../.vitepress/locales.mts'
import { repoRoot } from '../.vitepress/docs-nav.mts'

test('every authored document has a current Chinese translation', () => {
  for (const page of authoredPages()) {
    const file = resolve(repoRoot, translatedSource(page))
    assert.ok(existsSync(file), `Missing translation: ${page}`)
    const translation = readFileSync(file, 'utf8')
    const source = readFileSync(resolve(repoRoot, page))
    const hash = createHash('sha256').update(source).digest('hex')
    assert.match(translation, new RegExp(`^source: ["']?${page}["']?$`, 'm'), page)
    assert.match(translation, new RegExp(`^source_hash: ["']?${hash}["']?$`, 'm'), `Stale translation: ${page}`)
    const fences = text => [...text.matchAll(/^```[^\n]*\n[\s\S]*?^```/gm)].map(match => match[0])
    assert.deepEqual(fences(translation), fences(source.toString()), `Executable examples changed: ${page}`)
    const generated = text => [...text.matchAll(/^\[\/\/\]: # \(BEGIN [^\n]+\n[\s\S]*?^\[\/\/\]: # \(END [^\n]+/gm)].map(match => match[0])
    assert.deepEqual(generated(translation), generated(source.toString()), `Generated reference changed: ${page}`)
  }
})

test('Chinese source paths map to matching locale routes', () => {
  assert.equal(sourceRoute(translatedSource('docs/architecture.md')), 'zh/docs/architecture.md')
  assert.equal(sourceRoute(translatedSource('contracts/agents-api/admin-api.md')), 'zh/contracts/agents-api/admin-api.md')
})

test('translations retain inline literals and document structure', async () => {
  const { createMarkdownRenderer } = await import('vitepress')
  const md = await createMarkdownRenderer(repoRoot)
  function inventory(source) {
    const result = new Map()
    const tokens = md.parse(source.replace(/^---[\s\S]*?\n---\s*/, ''), {})
    function visit(tokens) {
      for (const token of tokens) {
        const key = token.type === 'code_inline' ? `literal: ${token.content}`
          : ['paragraph_open', 'list_item_open', 'tr_open', 'fence'].includes(token.type) ? token.type : undefined
        if (key) result.set(key, (result.get(key) ?? 0) + 1)
        if (token.children) visit(token.children)
      }
    }
    visit(tokens)
    return result
  }
  for (const page of authoredPages()) {
    const expected = inventory(readFileSync(resolve(repoRoot, page), 'utf8'))
    const actual = inventory(readFileSync(resolve(repoRoot, translatedSource(page)), 'utf8'))
    for (const [key, count] of expected) assert.ok((actual.get(key) ?? 0) >= count, `${page}: missing ${key}`)
  }
})
