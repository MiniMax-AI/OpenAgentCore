#!/usr/bin/env node
// Guide copies must match their canonical English sources.
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
    assert.equal(crypto.createHash('sha256').update(fs.readFileSync(path.join(root, relative))).digest('hex'), digest, 'Regenerate guides: ' + relative)
  }
}
const source = slug => fs.readFileSync(path.join(app, 'content/docs', slug + '.mdx'), 'utf8')
for (const token of ['/v1', '/core/v1', '/api/v1', 'Project API key', 'Core key', 'executor']) assert.ok(source('public-api').includes(token), 'Credential matrix omits ' + token)
for (const token of ['config.json', 'oac apply']) assert.ok(source('configure').includes(token), 'Configuration guide omits ' + token)
for (const token of ['oac-node', '/var/lib/oac-node/.oac/nodes', 'Node installation and removal require root.']) assert.ok(source('hosted-providers').includes(token), 'Node guide omits ' + token)
for (const token of ['oac-daemon install', 'OAC_RUNTIME_HOME', 'Linux, macOS and Windows']) assert.ok(source('self-hosted-execution').includes(token), 'Executor guide omits ' + token)
// Keep the installation policy visible in the operator guides.
for (const [slug, tokens] of [
  ['troubleshooting', ['In-place version upgrades, downgrades and historical conversions are not supported.', '.oac.lock']],
  ['hosted-providers', ['Node program version updates are not supported.', '`--update` refuses']],
]) for (const token of tokens) assert.ok(source(slug).includes(token), 'Installation policy drift in ' + slug + ': ' + token)
for (const file of fs.readdirSync(path.join(app, 'content/docs')).filter(n => n.endsWith('.mdx'))) {
  const text = fs.readFileSync(path.join(app, 'content/docs', file), 'utf8')
  for (const retired of ['/core/v1/admin', 'sandbox-manager.openapi.yaml']) assert.ok(!text.includes(retired), 'Obsolete claim in ' + file + ': ' + retired)
}
assert.deepEqual(fs.readFileSync(path.join(app, 'app/icon.png')), fs.readFileSync(path.join(repo, 'apps/web/public/favicon.png')), 'Docs favicon must match the approved Web asset')
console.log('Guide copies, source authority and namespace/configuration facts are current.')
