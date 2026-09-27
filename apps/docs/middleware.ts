import { NextResponse, type NextRequest } from "next/server"
import { i18n } from "@/lib/i18n"
import { legacyTarget } from "@/lib/legacy-redirects"

export default function middleware(request: NextRequest) {
  const pathname = request.nextUrl.pathname
  const segments = pathname.split("/").filter(Boolean)
  const pathLocale = i18n.languages.find((locale) => segments[0] === locale)
  const rest = `/${segments.slice(pathLocale ? 1 : 0).join("/")}`

  // Retired slugs first, so a reader following a link into the previous manual
  // reaches the page that replaced it instead of a 404. Handled here rather than
  // in `next.config.mjs` to keep the locale prefix and the redirect in one hop:
  // `/en/api-keys` collapses to `/bootstrap-projects-keys` directly, instead of
  // first stripping the prefix and then redirecting a second time.
  const legacy = legacyTarget(rest)
  if (legacy) {
    const target =
      pathLocale && pathLocale !== i18n.defaultLanguage
        ? `/${pathLocale}${legacy === "/" ? "" : legacy}`
        : legacy
    const url = new URL(target, request.url)
    // `new URL(path, base)` drops the base query, which would silently lose
    // campaign and referral parameters on the redirect.
    url.search = request.nextUrl.search
    return NextResponse.redirect(url, 308)
  }

  // The default language is served prefix-free, so its canonical URL has to
  // reach the `[lang]` route without a redirect the reader can see.
  if (!pathLocale) {
    const url = new URL(request.url)
    url.pathname = `/${i18n.defaultLanguage}${pathname === "/" ? "" : pathname}`
    return NextResponse.rewrite(url)
  }

  if (pathLocale === i18n.defaultLanguage) {
    // An explicit default-language prefix is not the canonical URL, so this is
    // permanent: the docs route serves the default language prefix-free.
    const stripped = pathname.slice(`/${pathLocale}`.length)
    const target = stripped || "/"
    const url = new URL(target, request.url)
    url.search = request.nextUrl.search
    return NextResponse.redirect(url, 308)
  }

  return NextResponse.next()
}

export const config = {
  // Locale routing must not touch API routes, Next's own assets, or anything
  // served as a file from `public/`. Without the `images/` exclusion the rewrite
  // turns `/images/x.png` into `/en/images/x.png`, which matches no route and
  // breaks every screenshot in the manual; the extension guard covers the rest,
  // including `favicon.ico`, `icon.png`, `sitemap.xml` and `robots.txt`, which
  // must stay outside `[lang]` or crawlers get a 404.
  //
  // The `api/` and `api$` forms are deliberate rather than a bare `api`: a
  // prefix match would also exclude `/api-keys`, a retired page this middleware
  // has to redirect. Same reasoning for `images/` and `images$`.
  matcher: ["/((?!api/|api$|_next/|images/|images$|.*\\.[a-zA-Z0-9]+$).*)", "/"],
}
