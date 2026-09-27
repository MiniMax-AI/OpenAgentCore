"use client"

import Image from "next/image"
import Link from "next/link"
import { useParams, usePathname, useRouter } from "next/navigation"
import { FrameworkProvider, type Framework } from "fumadocs-core/framework"
import { RootProvider } from "fumadocs-ui/provider/base"
import { useCallback, type ReactNode } from "react"
import { i18n, type Lang } from "@/lib/i18n"

const locales = [
  { name: "English", locale: "en" },
  { name: "中文", locale: "zh" },
]

const translations = {
  en: {
    toc: "On this page",
    chooseLanguage: "Choose language",
    nextPage: "Next page",
    previousPage: "Previous page",
  },
  zh: {
    toc: "本页目录",
    chooseLanguage: "选择语言",
    nextPage: "下一页",
    previousPage: "上一页",
  },
} as const

function isLocale(segment: string | undefined): boolean {
  return Boolean(segment) && (i18n.languages as readonly string[]).includes(segment as string)
}

function pathForLocale(pathname: string, locale: string) {
  const segments = pathname.split("/").filter(Boolean)
  // Drop a leading locale segment, whichever language it names — matching on a
  // literal "en"/"zh" pair here would silently break when a language is added.
  if (isLocale(segments[0])) segments.shift()
  const rest = segments.join("/")
  if (locale === i18n.defaultLanguage) return rest ? `/${rest}` : "/"
  return rest ? `/${locale}/${rest}` : `/${locale}`
}

export function DocsProviders({ lang, children }: { lang: Lang; children: ReactNode }) {
  const router = useRouter()
  const pathname = pathForLocale(usePathname(), lang)
  // Navigation must use the public path during both SSR and hydration.
  const usePublicPathname = useCallback(() => pathname, [pathname])

  return (
    <FrameworkProvider
      usePathname={usePublicPathname}
      useParams={useParams}
      useRouter={useRouter}
      Link={Link as Framework["Link"]}
      Image={Image as Framework["Image"]}
    >
      <RootProvider
        // The manual ships without site search: Fumadocs' search route needs a
        // tokenizer for the Chinese pages, because the default one strips Han
        // characters and would return nothing for a Chinese query. Turning the
        // toggle on without also adding `app/api/search/route.ts` would give the
        // reader a search box that never finds anything.
        search={{ enabled: false }}
        i18n={{
          locale: lang,
          locales,
          translations: translations[lang],
          onLocaleChange: (locale) => router.push(pathForLocale(pathname, locale)),
        }}
      >
        {children}
      </RootProvider>
    </FrameworkProvider>
  )
}
