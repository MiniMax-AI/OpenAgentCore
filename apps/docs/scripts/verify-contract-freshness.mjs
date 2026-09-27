#!/usr/bin/env node
// Source and output digests catch both stale generation and edited generated prose.
import fs from 'node:fs'
import path from 'node:path'
import crypto from 'node:crypto'
import assert from 'node:assert/strict'
import { appRoot, repoRoot, surfaces } from './contracts.mjs'
const record = JSON.parse(fs.readFileSync(path.join(appRoot, 'openapi/sources.json')))
assert.deepEqual(Object.keys(record.sources).sort(), surfaces.map(s => 'contracts/agents-api/' + s.file).sort())
function filesUnder(directory) {
  return fs.readdirSync(directory, { withFileTypes: true }).flatMap(entry => {
    const file = path.join(directory, entry.name)
    return entry.isDirectory() ? filesUnder(file) : [path.relative(appRoot, file)]
  })
}
const rendered = [...filesUnder(path.join(appRoot, 'openapi')), ...filesUnder(path.join(appRoot, 'content/docs/api-reference'))]
  .filter(file => file !== 'openapi/sources.json')
assert.deepEqual(rendered.sort(), Object.keys(record.outputs).sort(), 'Unexpected or missing generated reference files')
for (const [root, entries] of [[repoRoot, record.sources], [appRoot, record.outputs]]) {
  for (const [relative, digest] of Object.entries(entries)) {
    assert.equal(crypto.createHash('sha256').update(fs.readFileSync(path.join(root, relative))).digest('hex'), digest, 'Regenerate API reference: ' + relative)
  }
}
console.log('All three contract sources and generated references are current.')
