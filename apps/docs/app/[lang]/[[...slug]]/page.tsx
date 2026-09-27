import type { Metadata } from "next"
import { notFound } from "next/navigation"
import { ImageZoom } from "fumadocs-ui/components/image-zoom"
import defaultMdxComponents from "fumadocs-ui/mdx"
import { DocsBody, DocsDescription, DocsPage, DocsTitle } from "fumadocs-ui/page"
import { APIPage } from "@/components/api-page"
import { LocaleLink } from "@/components/locale-link"
import { i18n, type Lang } from "@/lib/i18n"
import { docsAlternates } from "@/lib/site"
import { source } from "@/lib/source"

function asLang(value: string): Lang {
  if (!(i18n.languages as readonly string[]).includes(value)) notFound()
  return value as Lang
}

function pageSlug(slug?: string[]) {
  return slug ?? []
}

export default async function DocsPageRoute({
  params,
}: {
  params: Promise<{ lang: string; slug?: string[] }>
}) {
  const { lang: rawLang, slug } = await params
  const lang = asLang(rawLang)
  const page = source.getPage(pageSlug(slug), lang)
  if (!page) notFound()

  const MDX = page.data.body
  return (
    <DocsPage toc={page.data.toc}>
      <DocsTitle>{page.data.title}</DocsTitle>
      <DocsDescription>{page.data.description}</DocsDescription>
      <DocsBody>
        <MDX
          components={{
            ...defaultMdxComponents,
            // Generated API reference pages. Read-only: they render the contract.
            APIPage,
            // Screenshots open at full size; the prose column is too narrow to
            // read a console capture at 1:1.
            img: ImageZoom,
            // Keep internal navigation inside the reader's language. Pages are
            // written with locale-free links — `[Configure](/configure)` — and
            // the prefix is added here, because a translated page would
            // otherwise silently hand the reader the default language.
            a: (props) => <LocaleLink {...props} lang={lang} />,
          }}
        />
      </DocsBody>
    </DocsPage>
  )
}

export function generateStaticParams() {
  return source.generateParams()
}

export async function generateMetadata({
  params,
}: {
  params: Promise<{ lang: string; slug?: string[] }>
}): Promise<Metadata> {
  const { lang: rawLang, slug } = await params
  const slugs = pageSlug(slug)
  const page = source.getPage(slugs, asLang(rawLang))
  if (!page) return {}
  return {
    title: page.data.title,
    description: page.data.description,
    // Declare the translations as alternates of one article, and name the
    // canonical URL, so search engines do not index the English and Chinese
    // pages as duplicates of each other.
    alternates: docsAlternates(slugs),
  }
}
