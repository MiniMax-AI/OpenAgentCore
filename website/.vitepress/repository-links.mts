// Rewrites relative links from a published page to a repository-only file
// (a root guide, a YAML contract, a source file) into a GitHub URL, so the
// Markdown sources stay unchanged and work both on GitHub and on the website.
import { sourceRoute, routeSource } from './locales.mts'
import { existsSync, statSync } from 'node:fs'
import { dirname, relative, resolve } from 'node:path'
import type MarkdownIt from 'markdown-it'

const published = /^(docs|contracts)\/.+\.md$/

export function repositoryLinks(md: MarkdownIt, options: { repoRoot: string; blobBase: string; treeBase: string }) {
  md.core.ruler.push('oac_repository_links', (state) => {
    const page: string | undefined = state.env?.relativePath
    if (!page) return
    const sourcePage = routeSource(page)
    const pageDir = dirname(resolve(options.repoRoot, sourcePage))
    for (const block of state.tokens) {
      for (const token of block.children ?? []) {
        if (token.type !== 'link_open') continue
        const href = token.attrGet('href')
        if (!href || /^([a-z][a-z0-9+.-]*:|#|\/)/i.test(href)) continue
        const [path, hash = ''] = href.split('#')
        const target = resolve(pageDir, decodeURI(path))
        const repoPath = relative(options.repoRoot, target)
        if (repoPath.startsWith('..') || !existsSync(target)) continue
        if (published.test(repoPath)) {
          token.attrSet('href', `/${sourceRoute(repoPath).replace(/\.md$/, '').replace(/\/index$/, '/')}${hash ? `#${hash}` : ''}`)
          continue
        }
        const base = statSync(target).isDirectory() ? options.treeBase : options.blobBase
        token.attrSet('href', `${base}/${repoPath}${hash ? `#${hash}` : ''}`)
      }
    }
  })
}
