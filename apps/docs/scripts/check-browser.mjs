#!/usr/bin/env node
// Local documentation acceptance only: never enter credentials or call Core.
const { chromium } = await import(process.env.DOCS_PLAYWRIGHT_MODULE ?? '@playwright/test')
import assert from 'node:assert/strict'
import fs from 'node:fs'
import path from 'node:path'
const origin = process.argv[2] ?? 'http://127.0.0.1:4275'
assert.ok(['127.0.0.1', 'localhost'].includes(new URL(origin).hostname), 'Use a task-owned local documentation server')
const browser = await chromium.launch({ headless: true, channel: 'chromium', ...(process.env.DOCS_BROWSER_EXECUTABLE ? { executablePath: process.env.DOCS_BROWSER_EXECUTABLE } : {}) })
const context = await browser.newContext({ viewport: { width: 1440, height: 1000 } })
const failures = []
const unexpected = []
context.on('request', request => {
  if (!request.url().startsWith(origin + '/') && request.url() !== origin) unexpected.push(request.url())
})
const page = await context.newPage()
page.on('pageerror', error => failures.push(error.message))
const screenshots = process.env.DOCS_SCREENSHOT_DIR
if (screenshots) fs.mkdirSync(screenshots, { recursive: true })
try {
  for (const route of ['/', '/install', '/configure', '/console', '/execution-model', '/api-reference/agents', '/api-reference/core/sandbox-manager', '/api-reference/machine/sandbox-node', '/zh/install']) {
    const response = await page.goto(origin + route, { waitUntil: 'networkidle' })
    assert.equal(response.status(), 200, route)
    assert.ok(await page.locator('h1').count(), 'Missing page title: ' + route)
    assert.ok((await page.locator('body').innerText()).includes('OpenAgentCore Docs'), 'Wrong visible wordmark: ' + route)
    assert.ok((await page.title()).includes('OpenAgentCore Docs'), 'Wrong page metadata: ' + route)
    if (route.includes('/api-reference/')) {
      assert.equal(await page.locator('input, form, textarea').count(), 0, 'Reference exposes request controls: ' + route)
      assert.ok((await page.locator('body').innerText()).includes('Authorization'), 'Missing credential documentation: ' + route)
    }
    if (screenshots && ['/', '/console', '/execution-model', '/api-reference/core/sandbox-manager', '/zh/install'].includes(route)) {
      await page.screenshot({ path: path.join(screenshots, (route.replaceAll('/', '-') || 'home') + '.png'), fullPage: false })
    }
  }
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto(origin + '/install', { waitUntil: 'networkidle' })
  assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth + 1), 'Mobile page overflows viewport')
  assert.deepEqual(failures, [], 'Browser runtime errors')
  assert.deepEqual(unexpected, [], 'Documentation made external requests')
  console.log('Nine desktop routes, both language paths, mobile layout and read-only API controls passed; no external requests.')
} finally {
  await context.close()
  await browser.close()
}
