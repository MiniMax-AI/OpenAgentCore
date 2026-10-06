// Unit tests for the navigation that the website derives from docs.json.
import assert from 'node:assert/strict'
import { existsSync } from 'node:fs'
import { resolve } from 'node:path'
import test from 'node:test'
import { docsSidebar, legacyRedirects, pageLink, pageTitle, readDocsJson, repoRoot, unquote } from '../.vitepress/docs-nav.mts'

test('page ids map to clean routes', () => {
  assert.equal(pageLink('docs/getting-started/index'), '/docs/getting-started/')
  assert.equal(pageLink('docs/architecture'), '/docs/architecture')
  assert.equal(pageLink('contracts/agents-api/index'), '/contracts/agents-api/')
})

test('YAML titles are unquoted with their escapes', () => {
  assert.equal(unquote('"Core\\u2013Runtime protocol"'), 'Core–Runtime protocol')
  assert.equal(unquote("'It''s quoted'"), "It's quoted")
  assert.equal(unquote('Plain title'), 'Plain title')
})

test('every docs.json page exists and has a title', () => {
  for (const group of readDocsJson().navigation.groups) {
    for (const page of group.pages) {
      assert.ok(existsSync(resolve(repoRoot, `${page}.md`)), `${page}.md exists`)
      assert.ok(pageTitle(page).length > 0, `${page} has a title`)
    }
  }
})

test('the sidebar follows docs.json order', () => {
  const groups = readDocsJson().navigation.groups
  const sidebar = docsSidebar()
  assert.deepEqual(sidebar.map((g) => g.text), groups.map((g) => g.group))
  assert.deepEqual(sidebar[0].items.map((i) => i.link), groups[0].pages.map(pageLink))
})

test('legacy README paths redirect to section indexes', () => {
  const redirects = legacyRedirects()
  assert.ok(redirects.length > 0)
  for (const { from, to } of redirects) {
    assert.ok(!from.endsWith('.md'), `${from} is not a Markdown copy`)
    assert.ok(to.startsWith('/'), `${to} is a site route`)
  }
})
