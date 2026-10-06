// Builds website navigation from the repository's existing documentation.
// docs.json owns the page list and order; each page's frontmatter title owns
// its label. Nothing here copies or rewrites the Markdown sources.
import { translatedSource, groupLabel } from './locales.mts'
import { existsSync, readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import type { DefaultTheme } from 'vitepress'

export const repoRoot = fileURLToPath(new URL('../../', import.meta.url))

interface DocsJson {
  navigation: { groups: { group: string; pages: string[] }[] }
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

export function docsSidebar(lang: 'en' | 'zh' = 'en'): DefaultTheme.SidebarItem[] {
  return readDocsJson().navigation.groups.map((group) => ({
    text: groupLabel(group.group, lang),
    collapsed: false,
    items: group.pages.map((page) => ({ text: pageTitle(lang === 'zh' ? translatedSource(page) : page), link: pageLink(lang === 'zh' ? `zh/${page}` : page) })),
  }))
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
