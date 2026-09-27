#!/usr/bin/env node
// Guide source drift requires regeneration and a review of the Chinese reading notes.
import fs from 'node:fs'
import path from 'node:path'
import crypto from 'node:crypto'
import assert from 'node:assert/strict'
import { fileURLToPath } from 'node:url'
const app = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const repo = path.resolve(app, '../..')
const record = JSON.parse(fs.readFileSync(path.join(app, 'content/guide-sources.json')))
for (const [root, entries] of [[repo, record.sources], [app, record.outputs]]) {
  for (const [relative, digest] of Object.entries(entries)) {
    assert.equal(crypto.createHash('sha256').update(fs.readFileSync(path.join(root, relative))).digest('hex'), digest, 'Regenerate guides and review translations: ' + relative)
  }
}
const source = slug => fs.readFileSync(path.join(app, 'content/docs', slug + '.mdx'), 'utf8')
for (const token of ['/v1', '/core/v1', '/api/v1', 'Project API key', 'Core key', 'executor']) assert.ok(source('public-api').includes(token), 'Credential matrix omits ' + token)
for (const token of ['config.json', 'oac apply']) assert.ok(source('configure').includes(token), 'Configuration guide omits ' + token)
for (const token of ['oac-node', '~/.oac/nodes']) assert.ok(source('hosted-providers').includes(token), 'Node guide omits ' + token)
for (const token of ['oac-selfhost', '~/.oac/self-hosted']) assert.ok(source('self-hosted-execution').includes(token), 'Executor guide omits ' + token)
for (const file of fs.readdirSync(path.join(app, 'content/docs')).filter(n => n.endsWith('.mdx'))) {
  const text = fs.readFileSync(path.join(app, 'content/docs', file), 'utf8')
  for (const retired of ['/core/v1/admin', 'sandbox-manager.openapi.yaml']) assert.ok(!text.includes(retired), 'Obsolete claim in ' + file + ': ' + retired)
}
assert.ok(fs.readFileSync(path.join(app, 'FOLLOW-UP.md'), 'utf8').includes('design 50'), 'Missing deferred feature inventory')
assert.deepEqual(fs.readFileSync(path.join(app, 'app/icon.png')), fs.readFileSync(path.join(repo, 'apps/web/public/favicon.png')), 'Docs favicon must match the approved Web asset')
console.log('Guide copies, source authority and namespace/configuration facts are current.')
