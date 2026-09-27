/**
 * The part of the site's i18n configuration that link rewriting depends on.
 *
 * Passed in rather than imported so this module stays a pure function with no
 * runtime dependency, which lets `locale-link.test.ts` load it directly under
 * `node --test` without a bundler or path-alias resolution.
 */
export type LocaleLinkConfig = {
  languages: readonly string[]
  defaultLanguage: string
}

/**
 * Add the active locale prefix to a root-relative link written in MDX, so that
 * navigation inside a translated page stays in that language.
 *
 * Fumadocs' loader keeps the default language prefix-free
 * (`hideLocale: "default-locale"`), so `[Configure](/configure)` written in
 * `configure.zh.mdx` would otherwise render as `<a href="/configure">`: the
 * reader leaves the Chinese manual and lands on the English page with no
 * visible reason. The docs route therefore renders every MDX anchor through
 * `LocaleLink`, so pages can be written with locale-free internal links.
 *
 * Deliberately left untouched:
 *   - absolute URLs with a scheme (`https:`, `mailto:`, `tel:`, …)
 *   - protocol-relative URLs (`//host/path`)
 *   - in-page anchors (`#section`)
 *   - relative paths (`./x`, `../x`)
 *   - paths that already carry a declared locale
 *   - every link on the default language, whose URLs stay prefix-free
 */
export function prefixLocale(
  href: string,
  lang: string,
  { languages, defaultLanguage }: LocaleLinkConfig,
): string {
  if (!href) return href
  if (lang === defaultLanguage) return href
  if (/^[a-z][a-z0-9+.-]*:/i.test(href)) return href
  if (href.startsWith("//")) return href
  if (href.startsWith("#")) return href
  if (!href.startsWith("/")) return href

  const first = href.split("/").filter(Boolean)[0]
  if (first && languages.includes(first)) return href

  // The root page is served at `/zh`, not `/zh/`, so a link to the root — with
  // or without an anchor or query — drops the separator rather than keeping it.
  if (href === "/") return `/${lang}`
  if (href.startsWith("/#") || href.startsWith("/?")) return `/${lang}${href.slice(1)}`

  return `/${lang}${href}`
}