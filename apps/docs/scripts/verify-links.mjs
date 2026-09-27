#!/usr/bin/env node
// Fails when a page links to a route that does not exist.
//
// The manual is a reading path: install, configure, bootstrap a Project, make a
// request, add capacity. A link that 404s breaks that path silently, because the
// page it lives on still renders. This resolves every root-relative link against
// the page files themselves, so it needs no running server.
//
// `content/docs/api-reference/*` is generated in English only, and fumadocs
// serves the default language for a missing translation, so a Chinese page may
// link to the English route and have it resolve. A link is therefore accepted
// when the target exists in either locale.
//
// Markdown that is not a page - the README - is checked too, against the
// filesystem, because its links are relative file paths. Removing a file it
// points at is otherwise invisible until a reviewer clicks it.
//
// Read-only. Run from apps/docs: node scripts/verify-links.mjs

import fs from "node:fs"
import path from "node:path"
import { fileURLToPath } from "node:url"

const here = path.dirname(fileURLToPath(import.meta.url))
const contentRoot = path.resolve(here, "../content/docs")
const appRoot = path.resolve(here, "..")

function mdxFiles(dir) {
  const out = []
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    const full = path.join(dir, entry.name)
    if (entry.isDirectory()) out.push(...mdxFiles(full))
    else if (entry.name.endsWith(".mdx")) out.push(full)
  }
  return out
}

// index.mdx is the directory itself; index.zh.mdx is its Chinese counterpart.
function routeOf(file) {
  const rel = path.relative(contentRoot, file).replace(/\.mdx$/, "")
  const parts = rel.split(path.sep)
  let name = parts.pop()
  const isZh = name.endsWith(".zh")
  if (isZh) name = name.slice(0, -3)
  const slug = name === "index" ? parts.join("/") : [...parts, name].join("/")
  const base = slug ? `/${slug}` : "/"
  return isZh ? (base === "/" ? "/zh" : `/zh${base}`) : base
}

const files = mdxFiles(contentRoot)
const routes = new Set(files.map(routeOf))

function resolves(target) {
  const [rawPath, anchor] = target.split("#")
  const clean = rawPath.split("?")[0].replace(/^\/en(?=\/|$)/, "") || "/"
  if (clean.startsWith("/images/")) return fs.existsSync(path.join(appRoot, "public", clean))
  const route = clean.length > 1 ? clean.replace(/\/+$/, "") : clean
  const bare = route === "/zh" ? "/" : route.startsWith("/zh/") ? route.slice(3) : route
  const targetFile = files.find(file => routeOf(file) === bare)
  if (!targetFile) return false
  if (!anchor) return true
  const text = fs.readFileSync(targetFile, "utf8")
  const seen = new Map()
  const anchors = []
  let fence = false
  for (const line of text.split("\n")) {
    if (/^\s*(```|~~~)/.test(line)) { fence = !fence; continue }
    if (fence) continue
    const heading = line.match(/^#{1,6} (.+)$/)
    if (!heading) continue
    const value = heading[1].replace(/`/g, "").replace(/\[([^\]]+)\]\([^)]+\)/g, "$1")
      .toLowerCase().replace(/[^\p{L}\p{N}_\s-]/gu, "").replace(/\s/g, "-")
    const previous = seen.get(value) ?? 0
    seen.set(value, previous + 1)
    anchors.push(value + (previous ? "-" + previous : ""))
  }
  return anchors.includes(decodeURIComponent(anchor))
}

const link = /\]\(([^)\s]+)/g
let checked = 0
let broken = 0

for (const file of files) {
  const source = fs.readFileSync(file, "utf8")
  for (const [, target] of source.matchAll(link)) {
    if (!target.startsWith("/")) continue
    checked += 1
    if (!resolves(target)) {
      console.error(
        `BROKEN   ${path.relative(contentRoot, file)}\n` +
          `  claim: every root-relative link points at a page that exists\n` +
          `  link:  ${target}`,
      )
      broken += 1
    }
  }
}

// This package also ships markdown that is not a page. Its links are relative
// file paths, so they are resolved against the filesystem instead of the routes.
const packageDocs = fs
  .readdirSync(appRoot, { withFileTypes: true })
  .filter((entry) => entry.isFile() && entry.name.endsWith(".md"))
  .map((entry) => path.join(appRoot, entry.name))

let fileLinks = 0
for (const file of packageDocs) {
  const source = fs.readFileSync(file, "utf8")
  for (const [, target] of source.matchAll(link)) {
    if (target.startsWith("/") || /^[a-z][a-z0-9+.-]*:/i.test(target)) continue
    fileLinks += 1
    const resolved = path.resolve(path.dirname(file), target.split("#")[0])
    if (!fs.existsSync(resolved)) {
      console.error(
        `BROKEN   ${path.relative(appRoot, file)}\n` +
          `  claim: every relative link points at a file that exists\n` +
          `  link:  ${target}`,
      )
      broken += 1
    }
  }
}

if (broken > 0) {
  console.error(`\n${broken} of ${checked + fileLinks} links are unresolvable.`)
  process.exit(1)
}
console.log(
  `${checked} root-relative links resolve across ${files.length} pages, and ` +
    `${fileLinks} relative links resolve across ${packageDocs.length} package files.`,
)
