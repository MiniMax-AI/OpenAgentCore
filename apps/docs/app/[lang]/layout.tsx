import "../global.css"
import type { Metadata } from "next"
import { notFound } from "next/navigation"
import type { ReactNode } from "react"
import { DocsLayout } from "fumadocs-ui/layouts/docs"
import { baseOptions } from "../layout.config"
import { source } from "@/lib/source"
import { i18n, type Lang } from "@/lib/i18n"
import { DocsProviders } from "./docs-providers"

export const metadata: Metadata = {
  title: {
    template: "%s | OpenAgentCore Docs",
    default: "OpenAgentCore Docs",
  },
  description: "Documentation for OpenAgentCore.",
}

export function generateStaticParams() {
  return i18n.languages.map((lang) => ({ lang }))
}

export default async function LangLayout({
  params,
  children,
}: {
  params: Promise<{ lang: string }>
  children: ReactNode
}) {
  const { lang: rawLang } = await params
  if (!(i18n.languages as readonly string[]).includes(rawLang)) notFound()
  const lang = rawLang as Lang

  return (
    <html lang={lang} suppressHydrationWarning>
      {/* Browser extensions may add attributes before React hydrates the body. */}
      <body suppressHydrationWarning>
        <DocsProviders lang={lang}>
          <DocsLayout
            tree={source.getPageTree(lang)}
            {...baseOptions}
            nav={{
              ...baseOptions.nav,
              // The title links to this language's home, not always to English.
              url: lang === i18n.defaultLanguage ? "/" : `/${lang}`,
            }}
          >
            {children}
          </DocsLayout>
        </DocsProviders>
      </body>
    </html>
  )
}
