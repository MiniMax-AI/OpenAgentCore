#!/usr/bin/env node
// Compare every rendered operation and schema with the current contract projection.
import fs from 'node:fs'
import path from 'node:path'
import yaml from 'js-yaml'
import assert from 'node:assert/strict'
import { appRoot, surfaces, normalise, methods } from './contracts.mjs'
let count = 0
for (const surface of surfaces) {
  const actual = yaml.load(fs.readFileSync(path.join(appRoot, 'openapi', surface.id + '.yaml'), 'utf8'))
  assert.deepEqual(actual, normalise(surface), surface.id + ': rendered contract differs')
  const listed = []
  const dir = path.join(appRoot, 'content/docs/api-reference') + surface.directory
  for (const name of fs.readdirSync(dir).filter(n => n.endsWith('.mdx') && n !== 'index.mdx')) {
    const content = fs.readFileSync(path.join(dir, name), 'utf8')
    assert.ok(content.includes('document={' + JSON.stringify(surface.id) + '}'), 'Wrong reference credential surface: ' + name)
    const operations = content.match(/operations=\{(\[[\s\S]*?\])\}/)
    assert.ok(operations, 'Missing operations: ' + name)
    for (const op of JSON.parse(operations[1])) listed.push(op.method + ' ' + op.path)
  }
  const expected = Object.entries(actual.paths).flatMap(([route, item]) => methods.filter(m => item[m]).map(m => m + ' ' + route))
  assert.deepEqual([...new Set(listed)].sort(), expected.sort(), 'Missing or obsolete API page: ' + surface.id)
  count += expected.length
}
console.log(count + ' operations, schemas, descriptions and credential surfaces match current contracts.')
