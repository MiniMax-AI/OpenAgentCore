import { existsSync } from "node:fs"
import { join } from "node:path"
import { i18n } from "./i18n"
import { source } from "./source"

/**
 * Canonical origin of the published manual, used only to build absolute URLs
 * for crawler metadata: the per-page canonical link, the hreflang alternates
 * and the sitemap. Set `DOCS_SITE_ORIGIN` to the real origin before publishing
 * the manual; the default matches the local review server.
 */
export const SITE_ORIGIN = (
  process.env.DOCS_SITE_ORIGIN ?? "http://127.0.0.1:4001"
).replace(/\/+$/, "")

/**
 * Absolute URL of a page from its Fumadocs-relative url (`/configure`,
 * `/zh/configure`). The home page arrives as `/`, which would serialize with a
 * trailing slash that the route does not serve; strip it so the sitemap entry
 * and the page's own canonical link stay byte-identical.
 */
export function absoluteDocsUrl(relative: string): string {
  return `${SITE_ORIGIN}${relative === "/" ? "" : relative}`
}

/** Content directories, so the check works from the repo root or from the app. */
function contentRoots(): string[] {
  return [
    join(process.cwd(), "content", "docs"),
    join(process.cwd(), "apps", "docs", "content", "docs"),
  ]
}

function pageStem(slugs: string[]): string {
  return slugs.length === 0 ? "index" : slugs.join("/")
}

/**
 * Whether a translated source file exists for a page.
 *
 * `source.getPage(slugs, lang)` answers for the default language when a
 * translation is missing, so it cannot tell "translated" from "fell back".
 * The file system can.
 */
export function hasLocalizedPage(slugs: string[], lang: string): boolean {
  const stem = pageStem(slugs)
  const candidates =
    lang === i18n.defaultLanguage
      ? [`${stem}.mdx`, `${stem}/index.mdx`]
      : [`${stem}.${lang}.mdx`, `${stem}/index.${lang}.mdx`]

  return contentRoots().some((root) =>
    candidates.some((candidate) => existsSync(join(root, candidate))),
  )
}

/**
 * Next.js `metadata.alternates` for a docs page:
 *   - `canonical` points at the default-language version, so search engines
 *     consolidate ranking signals on one URL instead of treating the English
 *     and Chinese pages as duplicates
 *   - `languages` declares every translation under its hreflang code
 *   - `x-default` points at the canonical URL for unmatched audiences
 *
 * A page translated into one language still gets a valid block; only what
 * exists is declared.
 */
export function docsAlternates(slugs: string[]): {
  canonical: string
  languages: Record<string, string>
} {
  const languages: Record<string, string> = {}

  for (const lang of i18n.languages) {
    if (!hasLocalizedPage(slugs, lang)) continue
    const page = source.getPage(slugs, lang)
    if (!page) continue
    languages[lang] = absoluteDocsUrl(page.url)
  }

  const canonical =
    languages[i18n.defaultLanguage] ?? Object.values(languages)[0]
  if (canonical) languages["x-default"] = canonical

  return { canonical: canonical ?? absoluteDocsUrl("/"), languages }
}
