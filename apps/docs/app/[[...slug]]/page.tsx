import type { Metadata } from "next"
import { notFound } from "next/navigation"
import { ImageZoom } from "fumadocs-ui/components/image-zoom"
import defaultMdxComponents from "fumadocs-ui/mdx"
import { DocsBody, DocsDescription, DocsPage, DocsTitle } from "fumadocs-ui/page"
import { APIPage } from "@/components/api-page"
import { absoluteDocsUrl } from "@/lib/site"
import { source } from "@/lib/source"

function pageSlug(slug?: string[]) {
  return slug ?? []
}

export default async function DocsPageRoute({
  params,
}: {
  params: Promise<{ slug?: string[] }>
}) {
  const { slug } = await params
  const page = source.getPage(pageSlug(slug))
  if (!page) notFound()

  const MDX = page.data.body
  return (
    <DocsPage toc={page.data.toc} full={page.data.full}>
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
  params: Promise<{ slug?: string[] }>
}): Promise<Metadata> {
  const { slug } = await params
  const slugs = pageSlug(slug)
  const page = source.getPage(slugs)
  if (!page) return {}
  return {
    title: page.data.title,
    description: page.data.description,
    alternates: { canonical: absoluteDocsUrl(page.url) },
  }
}
