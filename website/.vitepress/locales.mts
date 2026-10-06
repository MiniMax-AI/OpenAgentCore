import { readdirSync } from 'node:fs'
import { resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const root = fileURLToPath(new URL('../../', import.meta.url))
const languagePaths = [['docs/zh/', 'zh/docs/'], ['contracts/agents-api/zh/', 'zh/contracts/agents-api/']] as const
export function translatedSource(page: string): string {
  for (const [source] of languagePaths) {
    const english = source.replace('zh/', '')
    if (page.startsWith(english)) return source + page.slice(english.length)
  }
  throw new Error(`No translation directory for ${page}`)
}
export function sourceRoute(page: string): string {
  for (const [source, route] of languagePaths) if (page.startsWith(source)) return route + page.slice(source.length)
  return page
}
export function routeSource(page: string): string {
  for (const [source, route] of languagePaths) if (page.startsWith(route)) return source + page.slice(route.length)
  return page
}
export function authoredPages(): string[] {
  return ['docs', 'contracts/agents-api'].flatMap((dir) => readdirSync(resolve(root, dir), { recursive: true })
    .filter((file) => typeof file === 'string' && file.endsWith('.md') && !file.startsWith('zh/') && file !== 'harness-catalog.md')
    .map((file) => `${dir}/${file}`))
}
export function translationRewrites(): Record<string, string> {
  return Object.fromEntries(authoredPages().map((page) => [translatedSource(page), `zh/${page}`]))
}

export function groupLabel(group: string, lang: 'en' | 'zh'): string {
  const labels: Record<string, string> = { 'Get started': '开始使用', 'Operate': '部署与运维', 'API guides': 'API 指南', 'Architecture and extensions': '架构与扩展' }
  return lang === 'zh' ? labels[group] ?? group : group
}
