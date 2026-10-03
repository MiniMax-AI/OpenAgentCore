// Checks a finished build: run `pnpm build` first. Every docs.json page has
// an HTML page and a raw Markdown copy, legacy paths redirect, both landing
// pages exist and llms.txt lists every page.
import assert from 'node:assert/strict'
import { existsSync, readdirSync, readFileSync, statSync } from 'node:fs'
import { resolve } from 'node:path'
import test from 'node:test'
import { fileURLToPath } from 'node:url'
import { legacyRedirects, readDocsJson } from '../.vitepress/docs-nav.mts'

const dist = fileURLToPath(new URL('../.vitepress/dist/', import.meta.url))
const pages = readDocsJson().navigation.groups.flatMap((g) => g.pages)
const basePath = new URL(process.env.WEBSITE_URL ?? 'http://localhost/').pathname
const base = basePath.endsWith('/') ? basePath : `${basePath}/`

function html(page) {
  return resolve(dist, `${page}.html`)
}

test('the build output exists', () => {
  assert.ok(existsSync(resolve(dist, 'index.html')), 'run `pnpm build` before `pnpm test`')
})

test('both landing pages are built', () => {
  for (const [file, marker] of [['index.html', 'lang="en"'], ['zh/index.html', 'lang="zh-CN"']]) {
    const source = readFileSync(resolve(dist, file), 'utf8')
    assert.match(source, /class="landing"/, `${file} renders the landing page`)
    assert.ok(source.includes(marker), `${file} has ${marker}`)
  }
})

test('every documentation page has HTML and a Markdown copy', () => {
  for (const page of pages) {
    assert.ok(existsSync(html(page)), `${page}.html`)
    assert.ok(existsSync(resolve(dist, `${page}.md`)), `${page}.md`)
  }
})

test('generated navigation and assets stay under the site base and resolve to files', () => {
  let checked = 0
  for (const file of readdirSync(dist, { recursive: true }).filter((file) => file.endsWith('.html'))) {
    const source = readFileSync(resolve(dist, file), 'utf8')
    for (const [, href] of source.matchAll(/\b(?:href|src)="(\/[^"\s]*)"/g)) {
      assert.ok(href.startsWith(base) && !href.startsWith('//'), `${file}: ${href} must start with ${base}`)
      const path = decodeURI(href.split(/[?#]/)[0].slice(base.length))
      const candidates = [resolve(dist, path), resolve(dist, `${path}.html`), resolve(dist, path, 'index.html')]
      assert.ok(candidates.some((candidate) => existsSync(candidate) && statSync(candidate).isFile()), `${file}: ${href} must resolve to a generated file`)
      checked++
    }
  }
  assert.ok(checked > 0, 'generated pages must contain local navigation and assets')
})

test('legacy paths redirect', () => {
  for (const { from, to } of legacyRedirects()) {
    const source = readFileSync(resolve(dist, `${from}.html`), 'utf8')
    const target = `${base}${to.replace(/^\//, '')}`
    assert.ok(source.includes(`content="0; url=${target}"`), `${from} → ${target}`)
  }
})

test('llms.txt lists every page', () => {
  const llms = readFileSync(resolve(dist, 'llms.txt'), 'utf8')
  for (const page of pages) assert.ok(llms.includes(`](${base}${page}.md)`), `${page} in llms.txt uses ${base}`)
})

test('Mermaid fences become diagrams, not code blocks', () => {
  const source = readFileSync(html('docs/architecture'), 'utf8')
  assert.match(source, /class="oac-mermaid/)
  assert.doesNotMatch(source, /language-mermaid/)
})

test('Chinese documentation preserves routes, source copies and heading anchors', async () => {
  const { authoredPages } = await import('../.vitepress/locales.mts')
  for (const file of authoredPages()) {
    const page = file.replace(/\.md$/, '')
    const chinese = `zh/${page}`
    assert.ok(existsSync(html(chinese)), `${chinese}.html`)
    assert.ok(existsSync(resolve(dist, `${chinese}.md`)), `${chinese}.md`)
    const anchors = source => [...source.matchAll(/class="header-anchor" href="#([^"]+)"/g)].map(match => match[1])
    assert.deepEqual(anchors(readFileSync(html(chinese), 'utf8')), anchors(readFileSync(html(page), 'utf8')), `Heading anchors differ: ${page}`)
  }
})

test('Chinese landing and top navigation link to Chinese documentation', () => {
  const source = readFileSync(resolve(dist, 'zh/index.html'), 'utf8')
  for (const page of ['docs/getting-started/', 'docs/api/public-agent-api', 'docs/architecture', 'contracts/agents-api/harness-capabilities']) {
    assert.ok(source.includes(`href="${base}zh/${page}"`), page)
  }
})
