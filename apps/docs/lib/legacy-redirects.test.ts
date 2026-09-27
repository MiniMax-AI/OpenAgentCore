import assert from "node:assert/strict"
import { existsSync } from "node:fs"
import { join } from "node:path"
import test from "node:test"
import { i18n } from "./i18n.ts"
import { LEGACY_TARGETS, legacyTarget } from "./legacy-redirects.ts"

// Resolved from either the repo root or the app directory, so the suite runs
// whether `node --test` is started from the app or from the repository root.
const contentRoots = [
  join(process.cwd(), "content", "docs"),
  join(process.cwd(), "apps", "docs", "content", "docs"),
]

/**
 * A page counts as live only when every declared language has a source file.
 * A redirect landing on an untranslated page would hand a Chinese reader an
 * English page, which is the failure these redirects exist to prevent.
 */
function isLivePage(target: string): boolean {
  const stem = target === "/" ? "index" : target.replace(/^\//, "")
  return contentRoots.some((root) =>
    i18n.languages.every((lang) =>
      existsSync(
        join(root, lang === i18n.defaultLanguage ? `${stem}.mdx` : `${stem}.${lang}.mdx`),
      ),
    ),
  )
}

test("every redirect target is a page that exists in every language", () => {
  for (const [from, to] of Object.entries(LEGACY_TARGETS)) {
    assert.ok(isLivePage(to), `${from} -> ${to} is not a translated page`)
  }
})

test("no retired slug is still published", () => {
  for (const from of Object.keys(LEGACY_TARGETS)) {
    assert.ok(!isLivePage(`/${from}`), `${from} is mapped but is still a live page`)
  }
})

test("legacyTarget resolves with or without surrounding slashes", () => {
  assert.equal(legacyTarget("/api-keys"), "/bootstrap-projects-keys")
  assert.equal(legacyTarget("api-keys"), "/bootstrap-projects-keys")
  assert.equal(legacyTarget("/api-keys/"), "/bootstrap-projects-keys")
})

test("legacyTarget leaves live pages and the root to the router", () => {
  assert.equal(legacyTarget("/configure"), undefined)
  assert.equal(legacyTarget("/public-api"), undefined)
  assert.equal(legacyTarget("/environments-and-files"), undefined)
  assert.equal(legacyTarget("/"), undefined)
  assert.equal(legacyTarget(""), undefined)
})