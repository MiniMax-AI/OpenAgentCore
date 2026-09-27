import assert from "node:assert/strict"
import test from "node:test"
import { i18n } from "./i18n.ts"
import { prefixLocale } from "./locale-link.ts"

// The rule this file locks down: a *translated* page must never hand the reader
// back to the default language. `[Configure](/configure)` written in
// `configure.zh.mdx` has to render as `/zh/configure`.
test("a translated page keeps prefix-free internal links in its own language", () => {
  assert.equal(prefixLocale("/configure", "zh", i18n), "/zh/configure")
  assert.equal(prefixLocale("/configure/", "zh", i18n), "/zh/configure/")
  assert.equal(prefixLocale("/", "zh", i18n), "/zh")
  assert.equal(prefixLocale("/#section", "zh", i18n), "/zh#section")
  assert.equal(prefixLocale("/public-api?tab=2", "zh", i18n), "/zh/public-api?tab=2")
})

test("the default language stays prefix-free", () => {
  assert.equal(prefixLocale("/configure", "en", i18n), "/configure")
  assert.equal(prefixLocale("/", "en", i18n), "/")
})

test("a link that already carries a locale is left alone", () => {
  // Hand-written prefixed links must not become `/zh/zh/...`.
  assert.equal(prefixLocale("/zh/configure", "zh", i18n), "/zh/configure")
  // A link into another language is deliberate; do not rewrite it.
  assert.equal(prefixLocale("/en/configure", "zh", i18n), "/en/configure")
  assert.equal(prefixLocale("/zh", "zh", i18n), "/zh")
})

test("non-page links are never prefixed", () => {
  const untouched = [
    "https://github.com/MiniMax-AI/parsar-core",
    "http://127.0.0.1:18092/core/v1/admin/summary",
    "mailto:docs@example.com",
    "tel:+1234567890",
    "//example.com/path",
    "#local-anchor",
    "./sibling",
    "../parent",
    "relative/page",
    "",
  ]

  for (const href of untouched) {
    assert.equal(prefixLocale(href, "zh", i18n), href, `${href} must not be rewritten`)
  }
})

test("a path that merely starts with a locale name is still prefixed", () => {
  // `/environment` must not be mistaken for an `/en`-prefixed link, and
  // `/zh-tw` is not a locale this site declares.
  assert.equal(prefixLocale("/environment", "zh", i18n), "/zh/environment")
  assert.equal(prefixLocale("/zh-tw/guide", "zh", i18n), "/zh/zh-tw/guide")
})

test("every declared language is recognised as a prefix", () => {
  for (const lang of i18n.languages) {
    assert.equal(prefixLocale(`/${lang}/configure`, "zh", i18n), `/${lang}/configure`)
  }
})

test("translating the default language into itself is a no-op", () => {
  // Guards the `lang === defaultLanguage` short circuit: if it ever stops
  // firing, every English internal link gains an `/en` prefix the router then
  // redirects away from.
  for (const href of ["/configure", "/", "/a/b", "#x", "https://example.com"]) {
    assert.equal(prefixLocale(href, i18n.defaultLanguage, i18n), href)
  }
})