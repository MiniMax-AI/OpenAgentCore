import "./global.css"
import type { Metadata } from "next"
import type { ReactNode } from "react"
import { DocsLayout } from "fumadocs-ui/layouts/docs"
import { baseOptions } from "./layout.config"
import { source } from "@/lib/source"
import { DocsProviders } from "./docs-providers"

export const metadata: Metadata = {
  title: { template: "%s | OpenAgentCore Docs", default: "OpenAgentCore Docs" },
  description: "Documentation for OpenAgentCore.",
}

export default function RootLayout({ children }: { children: ReactNode }) {
  return (
    <html lang="en" suppressHydrationWarning>
      <body suppressHydrationWarning>
        <DocsProviders>
          <DocsLayout
            tree={source.pageTree}
            {...baseOptions}
            // Every visible sidebar link would otherwise prefetch its whole
            // page: expanding one API tag downloaded several MB of operation
            // schemas before any click. Pages load on click instead.
            sidebar={{ prefetch: false }}
          >
            {children}
          </DocsLayout>
        </DocsProviders>
      </body>
    </html>
  )
}
