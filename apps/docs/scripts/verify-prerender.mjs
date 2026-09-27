#!/usr/bin/env node
// Asserts that `next build` actually wrote a prerendered page for every route.
//
// This guards a regression that is invisible in the build log. A single
// request-scoped API anywhere in the `[lang]/[[...slug]]` render tree — in the
// page, in a layout, or in the `not-found` boundary — makes Next render every
// docs page per request. The build summary still prints `● (SSG)` and still
// lists all the paths, but no `.html`/`.rsc` files are emitted and every
// response comes back `Cache-Control: no-store`.
//
// The cost lands hardest on the generated API reference: those pages render a
// whole OpenAPI surface, so a navigation that should take 30 ms took 2–3 s.
//
// Usage, after `pnpm build`:
//
//   node scripts/verify-prerender.mjs

import fs from "node:fs"
import path from "node:path"
import { fileURLToPath } from "node:url"

const here = path.dirname(fileURLToPath(import.meta.url))
const root = path.resolve(here, "..")
const contentRoot = path.join(root, "content/docs")
const appRoot = path.join(root, ".next/server/app")

if (!fs.existsSync(appRoot)) {
  console.error(`No build output at ${path.relative(root, appRoot)}. Run \`pnpm build\` first.`)
  process.exit(1)
}

function walk(dir) {
  const out = []
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    const full = path.join(dir, entry.name)
    if (entry.isDirectory()) out.push(...walk(full))
    else if (entry.name.endsWith(".mdx")) out.push(full)
  }
  return out
}

function slugFor(file) {
  let rel = path.relative(contentRoot, file).split(path.sep).join("/")
  rel = rel.replace(/\.zh\.mdx$/, "").replace(/\.mdx$/, "").replace(/(^|\/)index$/, "")
  return rel.replace(/\/$/, "")
}

// Every page is generated in both locales: an untranslated page falls back to
// the default language, so `/zh/<slug>` is a real route either way.
const slugs = [...new Set(walk(contentRoot).map(slugFor))].sort()
const locales = ["en", "zh"]

const missing = []
for (const locale of locales) {
  for (const slug of slugs) {
    // `.../en.html` for the locale root, `.../en/<slug>.html` otherwise.
    const file = slug
      ? path.join(appRoot, locale, `${slug}.html`)
      : path.join(appRoot, `${locale}.html`)
    if (!fs.existsSync(file)) missing.push(path.relative(root, file))
    else if (slug.startsWith("api-reference/")) {
      const html = fs.readFileSync(file, "utf8")
      if (/<form(?:\s|>)/i.test(html) || /<input(?:\s|>)/i.test(html)) {
        throw new Error("API reference must not collect credentials or send requests: " + file)
      }
    }
  }
}

const expected = slugs.length * locales.length
if (missing.length > 0) {
  console.error(
    `\n${missing.length} of ${expected} routes were not prerendered. ` +
      `The docs route is rendering per request:`,
  )
  for (const file of missing.slice(0, 12)) console.error(`  ${file}`)
  if (missing.length > 12) console.error(`  … and ${missing.length - 12} more`)
  console.error(
    "\nLook for a request-scoped API (headers, cookies, searchParams, " +
      "`dynamic = \"force-dynamic\"`) in app/[lang]/**.",
  )
  process.exit(1)
}
console.log(`${expected} routes prerendered (${slugs.length} pages, ${locales.length} locales).`)
