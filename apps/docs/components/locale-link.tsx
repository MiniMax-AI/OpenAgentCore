import type { AnchorHTMLAttributes, ReactNode } from "react"
import { i18n } from "@/lib/i18n"
import { prefixLocale } from "@/lib/locale-link"

type LocaleLinkProps = AnchorHTMLAttributes<HTMLAnchorElement> & {
  lang: string
  children?: ReactNode
}

/**
 * MDX anchor override for the docs routes.
 *
 * The active language is passed in by the route, which knows it from the URL,
 * so no client-side context is required and this stays a server component. It
 * renders the same plain anchor Fumadocs uses by default, after adding the
 * locale prefix — see `prefixLocale`.
 */
export function LocaleLink({ lang, href, children, ...rest }: LocaleLinkProps) {
  return (
    <a href={href ? prefixLocale(href, lang, i18n) : href} {...rest}>
      {children}
    </a>
  )
}
