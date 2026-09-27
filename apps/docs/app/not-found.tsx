"use client"

import Link from "next/link"
import { usePathname } from "next/navigation"
import { i18n } from "@/lib/i18n"

const COPY = {
  en: {
    title: "Page not found",
    body: "The page you are looking for does not exist, or it moved when the manual was reorganised.",
    back: "Back to the manual",
  },
  zh: {
    title: "页面不存在",
    body: "你访问的页面不存在，或已随手册改版移动到别处。",
    back: "返回手册首页",
  },
} as const

/**
 * 404 for the whole site. Two things about its placement are deliberate.
 *
 * It lives at the app root, not at `app/[lang]/`. A `not-found` boundary inside
 * the segment that also holds the root layout is never compiled as a boundary,
 * so the version that used to sit there was dead code: every 404 got Next's
 * built-in notice instead.
 *
 * It is a client component that reads the language from the URL. Taking the
 * language from the request on the server — `headers()`, or any other
 * request-scoped API — opts *every sibling route* out of static generation,
 * because the boundary belongs to the `[lang]/[[...slug]]` render tree. That is
 * not a small regression: it made all 102 docs pages re-render per request, and
 * the generated API reference pages carry whole OpenAPI surfaces, so a single
 * navigation took two to three seconds instead of thirty milliseconds.
 *
 * The cost of the trade is that this notice arrives with hydration rather than
 * in the first response, and outside the docs chrome: without a root layout
 * Next renders a root `not-found` in its own shell. A 404 is `noindex` and a
 * dead end, so paying there to keep every real page prerendered is the right
 * way round.
 *
 * If this moves back to the server, `pnpm build` must still write prerendered
 * `.html` files under `.next/server/app/en/` — the build log reporting
 * `● (SSG)` is not sufficient, it says that either way.
 */
export default function NotFound() {
  const pathname = usePathname()
  const first = pathname.split("/").filter(Boolean)[0]
  const locale =
    first && (i18n.languages as readonly string[]).includes(first)
      ? first
      : i18n.defaultLanguage

  const text = COPY[locale as keyof typeof COPY] ?? COPY.en
  const home = locale === i18n.defaultLanguage ? "/" : `/${locale}`

  return (
    <main className="flex min-h-screen flex-col items-center justify-center gap-4 px-4 py-24 text-center">
      <h1 className="text-3xl font-semibold">{text.title}</h1>
      <p className="text-fd-muted-foreground">{text.body}</p>
      <Link
        href={home}
        className="inline-flex items-center rounded-md bg-fd-primary px-4 py-2 text-sm font-medium text-fd-primary-foreground transition-colors hover:bg-fd-primary/90"
      >
        {text.back}
      </Link>
    </main>
  )
}
