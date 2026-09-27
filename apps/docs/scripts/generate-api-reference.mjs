#!/usr/bin/env node
// Render the actual contracts without copying prose from superseded API surfaces.
import { generateFilesOnly } from 'fumadocs-openapi'
import { createOpenAPI } from 'fumadocs-openapi/server'
import yaml from 'js-yaml'
import fs from 'node:fs'
import path from 'node:path'
import crypto from 'node:crypto'
import { appRoot, repoRoot, surfaces, sourcePath, normalise } from './contracts.mjs'
const root = path.join(appRoot, 'content/docs/api-reference')
fs.rmSync(root, { recursive: true, force: true })
fs.mkdirSync(root, { recursive: true })
fs.rmSync(path.join(appRoot, 'openapi'), { recursive: true, force: true })
fs.mkdirSync(path.join(appRoot, 'openapi'))
const record = { sources: {}, outputs: {} }
const digest = file => crypto.createHash('sha256').update(fs.readFileSync(file)).digest('hex')
for (const surface of surfaces) {
  const output = path.join(appRoot, 'openapi', surface.id + '.yaml')
  fs.writeFileSync(output, '# Generated documentation projection; do not edit.\n' + yaml.dump(normalise(surface), { lineWidth: -1, noRefs: true }))
  const files = await generateFilesOnly({ input: createOpenAPI({ input: [output] }), per: 'tag' })
  const directory = root + surface.directory
  fs.mkdirSync(directory, { recursive: true })
  for (const file of files) {
    const target = path.join(directory, file.path)
    fs.writeFileSync(target, file.content.replace(/document=\{[^}]*\}/, `document={${JSON.stringify(surface.id)}}`))
    record.outputs[path.relative(appRoot, target)] = digest(target)
  }
  const pages = ['index', ...files.map(file => file.path.replace(/\.mdx$/, ''))]
  if (!surface.directory) pages.push('core', 'machine')
  fs.writeFileSync(path.join(directory, 'meta.json'), JSON.stringify({ title: surface.title, pages }, null, 2) + '\n')
  const body = `---\ntitle: ${surface.title}\ndescription: ${surface.prefix} — ${surface.credential}.\n---\n\n${surface.authority}\n\n**Credential:** ${surface.credential}. Examples use reserved \`example.com\` origins. This reference does not send requests or collect credentials.\n\n${surface.id === 'runtime-api' ? 'This schema covers node configuration, enrollment and identity. Daemon WebSockets and executor connection details are described in the [machine overview](/public-api#machine-connection-api).\n\n' : ''}${surface.id === 'core-api' ? 'Operator scripts use Core’s loopback port. The public entry routes management through Web, which requires its signed-in session and supplies the Core key on the server.\n\n' : ''}[API namespaces and credentials](/public-api) · [Application reference](/api-reference) · [Administration reference](/api-reference/core) · [Machine reference](/api-reference/machine)\n\n` + files.map(file => `- [${file.path.replace(/\.mdx$/, '')}](/api-reference${surface.directory}/${file.path.replace(/\.mdx$/, '')})`).join('\n') + '\n'
  fs.writeFileSync(path.join(directory, 'index.mdx'), body)
  record.sources[path.relative(repoRoot, sourcePath(surface))] = digest(sourcePath(surface))
  record.outputs[path.relative(appRoot, output)] = digest(output)
  for (const name of ['meta.json', 'index.mdx']) record.outputs[path.relative(appRoot, path.join(directory, name))] = digest(path.join(directory, name))
  console.log(surface.id + ': ' + files.length + ' tag pages')
}
fs.writeFileSync(path.join(appRoot, 'openapi/sources.json'), JSON.stringify(record, null, 2) + '\n')
