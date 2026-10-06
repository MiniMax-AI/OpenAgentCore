// Build-time data for the landing page's documentation index. The page list
// and order come from docs.json; titles and summaries come from each page, so
// new documentation appears on the landing page without editing the website.
import { translatedSource, groupLabel } from '../locales.mts'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { defineLoader } from 'vitepress'
import { pageLink, pageTitle, readDocsJson, repoRoot } from '../docs-nav.mts'

export interface DocsPage {
  title: string
  link: string
  summary: string
  zh: { title: string; link: string; summary: string }
}

export interface DocsGroup {
  group: string
  zhGroup: string
  pages: DocsPage[]
}

declare const data: DocsGroup[]
export { data }

/** The first sentence of the page body, with Markdown links and code marks removed. */
function summary(page: string): string {
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

export default defineLoader({
  watch: [resolve(repoRoot, 'docs.json'), resolve(repoRoot, 'docs/**/*.md'), resolve(repoRoot, 'contracts/**/*.md')],
  load(): DocsGroup[] {
    return readDocsJson().navigation.groups.map((group) => ({
      group: group.group,
      zhGroup: groupLabel(group.group, 'zh'),
      pages: group.pages.map((page) => ({ title: pageTitle(page), link: pageLink(page), summary: summary(page), zh: { title: pageTitle(translatedSource(page)), link: pageLink(`zh/${page}`), summary: summary(translatedSource(page)) } })),
    }))
  },
})
