#!/usr/bin/env node
// Compare every rendered operation and schema with the current contract projection,
// and every tag folder's navigation and overview with its operation pages.
import fs from 'node:fs'
import path from 'node:path'
import yaml from 'js-yaml'
import assert from 'node:assert/strict'
import { appRoot, surfaces, normalise, methods, fullPath } from './contracts.mjs'
let count = 0
for (const surface of surfaces) {
  const actual = yaml.load(fs.readFileSync(path.join(appRoot, 'openapi', surface.id + '.yaml'), 'utf8'))
  assert.deepEqual(actual, normalise(surface), surface.id + ': rendered contract differs')
  const listed = []
  const dir = path.join(appRoot, 'content/docs/api-reference') + surface.directory
  // Operation pages live one level down, in a folder per tag; index.mdx files
  // are overviews. Other surfaces' directories (core, machine) are skipped.
  const other = new Set(surfaces.filter(s => s.directory && s !== surface).map(s => s.directory.slice(1)))
  const folders = fs.readdirSync(dir, { withFileTypes: true }).filter(e => e.isDirectory() && !other.has(e.name)).map(e => e.name)
  const routes = new Set(Object.entries(actual.paths).flatMap(([route, item]) => methods.filter(m => item[m]).map(m => `${m.toUpperCase()} ${fullPath(surface, route)}`)))
  for (const folder of folders) {
    const folderDir = path.join(dir, folder)
    const pages = fs.readdirSync(folderDir).filter(n => n.endsWith('.mdx') && n !== 'index.mdx').map(n => n.slice(0, -4))
    // The sidebar shows exactly the folder's pages; a page missing from meta.json is unreachable.
    const meta = JSON.parse(fs.readFileSync(path.join(folderDir, 'meta.json'), 'utf8'))
    assert.deepEqual([...meta.pages].sort(), [...pages].sort(), `${surface.id}/${folder}: meta.json pages differ from its operation pages`)
    // The overview links every page once, with the path a caller sends.
    const overview = fs.readFileSync(path.join(folderDir, 'index.mdx'), 'utf8')
    const rows = [...overview.matchAll(/^\| \[[^\]]*\]\(([^)]+)\) \| `([A-Z]+)` \| `([^`]+)` \|$/gm)]
    assert.deepEqual(rows.map(r => r[1].split('/').pop()).sort(), [...pages].sort(), `${surface.id}/${folder}: overview rows differ from its operation pages`)
    for (const [, , method, route] of rows) assert.ok(routes.has(`${method} ${route}`), `${surface.id}/${folder}: overview lists ${method} ${route}, which the contract does not publish`)
    for (const name of pages) {
      const content = fs.readFileSync(path.join(folderDir, name + '.mdx'), 'utf8')
      assert.ok(content.includes('document={' + JSON.stringify(surface.id) + '}'), 'Wrong reference credential surface: ' + folder + '/' + name)
      const operations = content.match(/operations=\{(\[[\s\S]*?\])\}/)
      assert.ok(operations, 'Missing operations: ' + folder + '/' + name)
      for (const op of JSON.parse(operations[1])) listed.push(op.method + ' ' + op.path)
    }
  }
  const expected = Object.entries(actual.paths).flatMap(([route, item]) => methods.filter(m => item[m]).map(m => m + ' ' + route))
  assert.deepEqual([...new Set(listed)].sort(), expected.sort(), 'Missing or obsolete API page: ' + surface.id)
  assert.equal(listed.length, expected.length, 'An operation has more than one page: ' + surface.id)
  count += expected.length
}
console.log(count + ' operations, schemas, descriptions, navigation, overviews and credential surfaces match current contracts.')
