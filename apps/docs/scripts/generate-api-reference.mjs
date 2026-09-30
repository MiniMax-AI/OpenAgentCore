#!/usr/bin/env node
// Render the actual contracts without copying prose from superseded API surfaces.
//
// One page per operation, in one folder per tag. A page renders a single
// operation: a whole tag on one page (up to 11 operations with their full
// request and response schemas) took 4-10 seconds to render and weighed
// 1.4-1.8 MB, which stalled navigation. Each tag folder keeps an overview page
// at the former tag URL, so /api-reference/sessions still resolves.
import { generateFilesOnly } from 'fumadocs-openapi'
import { createOpenAPI } from 'fumadocs-openapi/server'
import yaml from 'js-yaml'
import fs from 'node:fs'
import path from 'node:path'
import crypto from 'node:crypto'
import { appRoot, repoRoot, surfaces, sourcePath, normalise, methods, splitDescription, detailsBlock, plainText, fullPath } from './contracts.mjs'
const root = path.join(appRoot, 'content/docs/api-reference')
fs.rmSync(root, { recursive: true, force: true })
fs.mkdirSync(root, { recursive: true })
fs.rmSync(path.join(appRoot, 'openapi'), { recursive: true, force: true })
fs.mkdirSync(path.join(appRoot, 'openapi'))
const record = { sources: {}, outputs: {} }
const digest = file => crypto.createHash('sha256').update(fs.readFileSync(file)).digest('hex')
const slug = text => text.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '')
// Record keys are POSIX paths, so a record generated on Linux verifies on Windows.
const key = (from, file) => path.relative(from, file).split(path.sep).join('/')
const write = (file, text) => {
  fs.mkdirSync(path.dirname(file), { recursive: true })
  fs.writeFileSync(file, text)
  record.outputs[key(appRoot, file)] = digest(file)
}
const yamlString = text => JSON.stringify(text)

// The lead goes under the title and into search metadata; the details render in
// the page body above the request.
function withSplitDescription(content) {
  const match = /^---\n([\s\S]*?)\n---\n/.exec(content)
  if (!match) throw new Error('Generated page without frontmatter')
  const frontmatter = yaml.load(match[1])
  const { lead, details } = splitDescription(frontmatter.description)
  frontmatter.description = plainText(lead)
  const body = content.slice(match[0].length)
  const at = body.indexOf('<APIPage')
  if (at < 0) throw new Error('Generated page without APIPage')
  const inserted = detailsBlock(details)
  return `---\n${yaml.dump(frontmatter, { lineWidth: 100 })}---\n${body.slice(0, at)}${inserted}${body.slice(at)}`
}
for (const surface of surfaces) {
  const output = path.join(appRoot, 'openapi', surface.id + '.yaml')
  const document = normalise(surface)
  fs.writeFileSync(output, '# Generated documentation projection; do not edit.\n' + yaml.dump(document, { lineWidth: -1, noRefs: true }))
  // Tags and operations in contract order, so the sidebar follows the contract.
  const tags = new Map()
  for (const [route, item] of Object.entries(document.paths ?? {})) {
    for (const method of methods) {
      const operation = item[method]
      if (!operation) continue
      // The page generator files an untagged operation under its own name and a
      // second tag nowhere, so both must be fixed in the contract.
      if (operation.tags?.length !== 1) throw new Error(`${method.toUpperCase()} ${route} needs exactly one tag, has ${JSON.stringify(operation.tags ?? [])}`)
      const tag = operation.tags[0]
      if (!tags.has(tag)) tags.set(tag, { folder: slug(tag), operations: [] })
      tags.get(tag).operations.push({ route, method, operation })
    }
  }
  const folderOf = new Map()
  for (const [tag, { folder }] of tags) {
    if (!folder) throw new Error(`Tag ${JSON.stringify(tag)} has no page folder name; use ASCII letters or digits.`)
    if (folderOf.has(folder)) throw new Error(`Tags ${JSON.stringify(folderOf.get(folder))} and ${JSON.stringify(tag)} share the folder ${folder}.`)
    if (!surface.directory && surfaces.some(s => s.directory === '/' + folder)) throw new Error(`Tag ${JSON.stringify(tag)} collides with the ${folder} reference.`)
    folderOf.set(folder, tag)
  }
  const pageOf = new Map()
  for (const { folder, operations } of tags.values()) {
    const taken = new Set()
    for (const entry of operations) {
      const name = slug(entry.operation.summary || `${entry.method} ${entry.route}`)
      if (taken.has(name)) throw new Error(`Two ${folder} operations share the page name ${name}; give them distinct summaries.`)
      taken.add(name)
      pageOf.set(`${entry.method} ${entry.route}`, { folder, name })
    }
  }
  const files = await generateFilesOnly({
    input: createOpenAPI({ input: [output] }),
    per: 'operation',
    groupBy: 'tag',
    slugify: slug,
    name: entry => {
      const page = pageOf.get(`${entry.item.method} ${entry.item.path}`)
      if (!page) throw new Error('Unexpected generated entry: ' + JSON.stringify(entry.item))
      return page.name
    },
  })
  const directory = root + surface.directory
  const expected = new Set([...pageOf.values()].map(page => `${page.folder}/${page.name}.mdx`))
  for (const file of files) {
    const relative = file.path.split(path.sep).join('/')
    if (!expected.delete(relative)) throw new Error('Unexpected generated page: ' + relative)
    write(path.join(directory, relative), withSplitDescription(file.content).replace(/document=\{[^}]*\}/, `document={${JSON.stringify(surface.id)}}`))
  }
  if (expected.size) throw new Error('Operations without a generated page: ' + [...expected].join(', '))
  const pages = pageOf.size
  // Each tag folder: an overview at the tag URL and the operations in contract order.
  for (const [tag, { folder, operations }] of tags) {
    const rows = operations.map(({ route, method, operation }) => {
      const page = pageOf.get(`${method} ${route}`)
      return `| [${operation.summary ?? route}](/api-reference${surface.directory}/${folder}/${page.name}) | \`${method.toUpperCase()}\` | \`${fullPath(surface, route)}\` |`
    })
    const description = `${tag}. ${surface.title}: ${surface.credential}.`
    write(path.join(directory, folder, 'index.mdx'), `---\ntitle: ${yamlString(tag)}\ndescription: ${yamlString(description)}\n---\n\n${surface.authority}\n\n| Operation | Method | Path |\n| --- | --- | --- |\n${rows.join('\n')}\n`)
    // Leaving index out of pages makes it the folder's own link: the tag name
    // opens the overview and expands the operations.
    write(path.join(directory, folder, 'meta.json'), JSON.stringify({ title: tag, pages: operations.map(({ route, method }) => pageOf.get(`${method} ${route}`).name) }, null, 2) + '\n')
  }
  const folders = [...tags.values()].map(tag => tag.folder)
  // As in tag folders, the surface overview is the folder's own link.
  const meta = [...folders]
  if (!surface.directory) meta.push('core', 'machine')
  write(path.join(directory, 'meta.json'), JSON.stringify({ title: surface.title, pages: meta }, null, 2) + '\n')
  const body = `---\ntitle: ${surface.title}\ndescription: ${surface.prefix} — ${surface.credential}.\n---\n\n${surface.authority}\n\n**Credential:** ${surface.credential}. Examples use reserved \`example.com\` origins. This reference does not send requests or collect credentials.\n\n${surface.id === 'runtime-api' ? 'This schema covers node configuration, enrollment and identity, and native Runtime installation. Daemon WebSockets and executor connection details are described in the [machine overview](/public-api#machine-connection-api).\n\n' : ''}${surface.id === 'core-api' ? 'Operator scripts use Core’s loopback port. The public entry routes management through Web, which requires its signed-in session and supplies the Core key on the server.\n\n' : ''}[API namespaces and credentials](/public-api) · [Application reference](/api-reference) · [Administration reference](/api-reference/core) · [Machine reference](/api-reference/machine) · [Error codes](/error-codes)\n\n` + [...tags].map(([tag, { folder, operations }]) => `- [${tag}](/api-reference${surface.directory}/${folder}) · ${operations.length} ${operations.length === 1 ? 'operation' : 'operations'}`).join('\n') + '\n'
  write(path.join(directory, 'index.mdx'), body)
  record.sources[key(repoRoot, sourcePath(surface))] = digest(sourcePath(surface))
  record.outputs[key(appRoot, output)] = digest(output)
  console.log(`${surface.id}: ${pages} operation pages in ${tags.size} tags`)
}
fs.writeFileSync(path.join(appRoot, 'openapi/sources.json'), JSON.stringify(record, null, 2) + '\n')
