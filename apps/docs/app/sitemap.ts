import type { MetadataRoute } from "next"
import { i18n } from "@/lib/i18n"
import { absoluteDocsUrl } from "@/lib/site"
import { source } from "@/lib/source"

/**
 * Sitemap for the manual, built from Fumadocs' page list at build time.
 *
 * One entry per logical page: every language variant of a page is declared as
 * an hreflang alternate of the same entry, so a search engine reads the English
 * and Chinese pages as one article in two languages rather than as duplicates.
 */
export default function sitemap(): MetadataRoute.Sitemap {
  // Collapse the language variants into one group per slug.
  const bySlug = new Map<string, Map<string, string>>()

  for (const { language, pages } of source.getLanguages()) {
    for (const page of pages) {
      const slugKey = page.slugs.join("/")
      const languages = bySlug.get(slugKey) ?? new Map<string, string>()
      languages.set(language, page.url)
      bySlug.set(slugKey, languages)
    }
  }

  const entries: MetadataRoute.Sitemap = []

  for (const languages of bySlug.values()) {
    // Canonical is the default language when it exists, otherwise the first
    // translation available.
    const canonicalRelative =
      languages.get(i18n.defaultLanguage) ?? languages.values().next().value
    if (!canonicalRelative) continue

    const alternates: Record<string, string> = {}
    for (const [lang, relative] of languages) {
      alternates[lang] = absoluteDocsUrl(relative)
    }
    alternates["x-default"] = absoluteDocsUrl(canonicalRelative)

    entries.push({
      url: absoluteDocsUrl(canonicalRelative),
      alternates: { languages: alternates },
    })
  }

  return entries
}