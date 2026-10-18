// Builds website navigation from the repository's existing documentation.
// docs.json owns the page list and order; each page's frontmatter title owns
// its label. Nothing here copies or rewrites the Markdown sources.
import { translatedSource, navLabel } from './locales.mts'
import { existsSync, readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import type { DefaultTheme } from 'vitepress'

export const repoRoot = fileURLToPath(new URL('../../', import.meta.url))

interface DocsTab {
  tab: string
  groups: { group: string; pages: string[] }[]
}

interface DocsJson {
  navigation: { tabs: DocsTab[] }
  redirects?: { source: string; destination: string }[]
}

export function readDocsJson(): DocsJson {
  return JSON.parse(readFileSync(resolve(repoRoot, 'docs.json'), 'utf8'))
}

/** Unquotes a YAML scalar: double quotes use JSON-compatible escapes, single quotes double ''. */
export function unquote(value: string): string {
  if (value.startsWith('"')) return JSON.parse(value)
  if (value.startsWith("'")) return value.slice(1, -1).replace(/''/g, "'")
  return value
}

/** Reads `title:` from a page's YAML frontmatter, falling back to its first H1. */
export function pageTitle(page: string): string {
  const file = resolve(repoRoot, `${page}.md`)
  if (!existsSync(file)) throw new Error(`docs.json references a missing page: ${page}.md`)
  const source = readFileSync(file, 'utf8')
  const frontmatter = /^---\r?\n([\s\S]*?)\r?\n---/.exec(source)?.[1] ?? ''
  const title = /^title:\s*(.+)$/m.exec(frontmatter)?.[1]?.trim()
  if (title) return unquote(title)
  const heading = /^#\s+(.+)$/m.exec(source)?.[1]?.trim()
  if (heading) return heading
  throw new Error(`Page has no title: ${page}.md`)
}

/** Maps a docs.json page id to its website route. `dir/index` becomes `dir/`. */
export function pageLink(page: string): string {
  return `/${page.replace(/(^|\/)index$/, '$1')}`
}

type Lang = 'en' | 'zh'

function localized(page: string, lang: Lang): string {
  return lang === 'zh' ? translatedSource(page) : page
}

function route(page: string, lang: Lang): string {
  return pageLink(lang === 'zh' ? `zh/${page}` : page)
}

/** Every page listed in docs.json, in tab, group and page order. */
export function listedPages(): string[] {
  return readDocsJson().navigation.tabs.flatMap((tab) => tab.groups.flatMap((group) => group.pages))
}

/** One top-nav item per tab: it opens the tab's first page and stays active on every page of the tab. */
export function docsNav(lang: Lang = 'en'): DefaultTheme.NavItem[] {
  return readDocsJson().navigation.tabs.map((tab) => {
    const links = tab.groups.flatMap((group) => group.pages).map((page) => route(page, lang))
    const paths = links.map((link) => link.replace(/[.*+?^${}()|[\]\\]/g, '\\$&').replace(/\/$/, '(/|/index)?'))
    return { text: navLabel(tab.tab, lang), link: links[0], activeMatch: `^(${paths.join('|')})(\\.html)?$` }
  })
}

/**
 * A multi-sidebar keyed by each page's source path, so a page shows only its own tab's groups.
 * VitePress picks the key that the page's source path starts with, and `.md` keeps `install.md` apart from `install-options.md`.
 */
export function docsSidebar(lang: Lang = 'en'): DefaultTheme.SidebarMulti {
  const sidebar: DefaultTheme.SidebarMulti = {}
  for (const tab of readDocsJson().navigation.tabs) {
    const items = tab.groups.map((group) => ({
      text: navLabel(group.group, lang),
      collapsed: false,
      items: group.pages.map((page) => ({ text: pageTitle(localized(page, lang)), link: route(page, lang) })),
    }))
    for (const page of tab.groups.flatMap((group) => group.pages)) sidebar[`/${lang === 'zh' ? 'zh/' : ''}${page}.md`] = items
  }
  return sidebar
}

/** The first sentence of a page's opening paragraph, with Markdown links and code marks removed. */
export function summary(page: string): string {
  const source = readFileSync(resolve(repoRoot, `${page}.md`), 'utf8')
  const body = source.replace(/^---[\s\S]*?\n---\s*/, '')
  const paragraph = body
    .split(/\n\s*\n/)
    .map((block) => block.trim())
    .find((block) => block && !/^(#|\||```|!\[|<|\[\/\/\]|>|-|\d+\.)/.test(block))
  if (!paragraph) return ''
  const text = paragraph
    .replace(/!\[[^\]]*\]\([^)]*\)/g, '')
    .replace(/\[([^\]]+)\]\([^)]*\)/g, '$1')
    .replace(/[`*_]/g, '')
    .replace(/\s+/g, ' ')
  const sentence = /^(.+?(?:[。！？]|[.!?](?=\s|$)))/.exec(text)?.[1] ?? text
  return sentence.length > 180 ? `${sentence.slice(0, 177).trimEnd()}…` : sentence
}

/**
 * Redirects in docs.json that point a legacy path (such as `README`) at a page.
 * `.md` sources are served by the raw Markdown copies, so they are skipped.
 */
export function legacyRedirects(): { from: string; to: string }[] {
  return (readDocsJson().redirects ?? [])
    .filter((r) => !r.source.endsWith('.md'))
    .map((r) => ({ from: r.source.replace(/^\//, ''), to: pageLink(r.destination.replace(/^\//, '')) }))
}
