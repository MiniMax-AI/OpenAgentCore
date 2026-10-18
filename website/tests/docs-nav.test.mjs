// Unit tests for the navigation that the website derives from docs.json.
import assert from 'node:assert/strict'
import { existsSync } from 'node:fs'
import { resolve } from 'node:path'
import test from 'node:test'
import { translatedSource } from '../.vitepress/locales.mts'
import { docsNav, docsSidebar, legacyRedirects, listedPages, pageLink, pageTitle, readDocsJson, repoRoot, summary, unquote } from '../.vitepress/docs-nav.mts'

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

test('every docs.json page exists, has a title and a Chinese mirror', () => {
  for (const page of listedPages()) {
    assert.ok(existsSync(resolve(repoRoot, `${page}.md`)), `${page}.md exists`)
    assert.ok(pageTitle(page).length > 0, `${page} has a title`)
    assert.ok(pageTitle(translatedSource(page)).length > 0, `${page} has a Chinese title`)
  }
  assert.equal(new Set(listedPages()).size, listedPages().length, 'no page is listed twice')
})

test('the top nav has one item per tab and opens its first page', () => {
  const tabs = readDocsJson().navigation.tabs
  assert.deepEqual(tabs.map((t) => t.tab), ['Docs', 'API', 'Extend'])
  assert.deepEqual(docsNav().map((n) => n.text), ['Docs', 'API', 'Extend'])
  assert.deepEqual(docsNav('zh').map((n) => n.text), ['文档', 'API', '扩展'])
  docsNav().forEach((item, i) => assert.equal(item.link, pageLink(tabs[i].groups[0].pages[0])))
  assert.equal(docsNav('zh')[0].link, '/zh/docs/getting-started/')
})

test('a tab is active exactly on its own pages', () => {
  const tabs = readDocsJson().navigation.tabs
  const nav = docsNav()
  tabs.forEach((tab, i) => {
    for (const other of tabs.keys()) {
      for (const page of tabs[other].groups.flatMap((g) => g.pages)) {
        const route = pageLink(page)
        assert.equal(new RegExp(nav[i].activeMatch).test(route), other === i, `${route} against ${tab.tab}`)
      }
    }
  })
})

test('each page shows only its own tab in the sidebar, labelled by page titles', () => {
  const tabs = readDocsJson().navigation.tabs
  for (const lang of ['en', 'zh']) {
    const sidebar = docsSidebar(lang)
    for (const tab of tabs) {
      const expected = tab.groups.map((g) => g.pages.map((p) => pageTitle(lang === 'zh' ? translatedSource(p) : p)))
      for (const page of tab.groups.flatMap((g) => g.pages)) {
        const items = sidebar[`/${lang === 'zh' ? 'zh/' : ''}${page}.md`]
        assert.deepEqual(items.map((g) => g.items.map((i) => i.text)), expected, `${lang} ${page}`)
      }
    }
  }
})

test('summaries are one sentence taken from the page opening', () => {
  assert.ok(summary('docs/architecture').length > 0)
  for (const page of listedPages()) assert.ok(summary(page).length > 0, `${page} has a summary`)
})

test('legacy README paths redirect to section indexes', () => {
  const redirects = legacyRedirects()
  assert.ok(redirects.length > 0)
  for (const { from, to } of redirects) {
    assert.ok(!from.endsWith('.md'), `${from} is not a Markdown copy`)
    assert.ok(to.startsWith('/'), `${to} is a site route`)
  }
})
