import assert from 'node:assert/strict'
import test from 'node:test'
import { siteBase } from '../.vitepress/site-base.mts'

test('local builds serve from the root', () => {
  assert.equal(siteBase(undefined), '/')
})

test('Pages URLs support project paths, organization sites and custom domains', () => {
  for (const [url, expected] of [
    ['http://minimax-ai.github.io/OpenAgentCore/', '/OpenAgentCore/'],
    ['https://octocat.github.io/my-repo', '/my-repo/'],
    ['https://octocat.github.io/', '/'],
    ['https://docs.example.com', '/'],
    ['https://docs.example.com/nested/site/', '/nested/site/'],
  ]) assert.equal(siteBase(url), expected, url)
})

test('empty or invalid Pages metadata fails instead of building at the root', () => {
  for (const value of ['', ' ', 'null', '/OpenAgentCore/', 'file:///tmp/site', 'https://user:pass@example.com/', 'https://example.com/?path=docs', 'https://example.com/#docs']) {
    assert.throws(() => siteBase(value), /WEBSITE_URL/, JSON.stringify(value))
  }
})
