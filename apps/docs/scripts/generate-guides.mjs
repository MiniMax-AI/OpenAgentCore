#!/usr/bin/env node
// The site renders current repository guides instead of maintaining a second manual.
import fs from 'node:fs'
import path from 'node:path'
import crypto from 'node:crypto'
import { fileURLToPath } from 'node:url'
const app = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const repo = path.resolve(app, '../..')
const guides = JSON.parse(fs.readFileSync(path.join(app, 'scripts/guides.json'), 'utf8'))
const routes = new Map(guides.map(g => [g.source, g.slug === 'index' ? '/' : '/' + g.slug]))
const sourceRevision = 'e4d5a1d30520a967b3a44a257ed1b6c21e388388'
const sourceURL = relative => `https://github.com/MiniMax-AI/parsar-core/blob/${sourceRevision}/${relative}`
const digest = text => crypto.createHash('sha256').update(text).digest('hex')
function mdx(text) {
  let fence = false
  return text.split('\n').map(line => {
    if (/^\s*(```|~~~)/.test(line)) { fence = !fence; return line }
    if (fence) return line
    return line.split(/(`+[^`]*`+)/g).map((part, i) => i % 2 ? part : part.replaceAll('{', '&#123;').replaceAll('}', '&#125;').replaceAll('<', '&lt;')).join('')
  }).join('\n')
}
const record = { revision: sourceRevision, sources: {}, outputs: {} }
// Only this generated asset directory is cleared; authored diagrams remain separate.
fs.rmSync(path.join(app, 'public/images/source'), { recursive: true, force: true })
for (const guide of guides) {
  const original = fs.readFileSync(path.join(repo, guide.source), 'utf8')
  let body = original.replace(/^# [^\n]*\n/, '').replace(/<!--[\s\S]*?-->/g, '').replace(/^```caddyfile$/gm, '```text')
  body = body.replace(/(!?\[[^\]]*\])\(([^\s)]+)\)/g, (full, label, target) => {
    if (/^(?:[a-z]+:|#|\/)/i.test(target)) return full
    const [file, anchor] = target.split('#')
    const relative = path.posix.normalize(path.posix.join(path.posix.dirname(guide.source), file))
    if (!fs.existsSync(path.join(repo, relative))) throw new Error('Missing guide link: ' + guide.source + ' -> ' + target)
    let url = routes.get(relative) ?? sourceURL(relative)
    if (label.startsWith('!')) {
      const imageSource = guide.images?.[relative] ?? relative
      const asset = 'public/images/source/' + imageSource
      const bytes = fs.readFileSync(path.join(repo, imageSource))
      fs.mkdirSync(path.dirname(path.join(app, asset)), { recursive: true })
      fs.writeFileSync(path.join(app, asset), bytes)
      record.sources[imageSource] = digest(bytes)
      record.outputs[asset] = digest(bytes)
      url = '/' + asset.slice('public/'.length)
    }
    return label + '(' + url + (anchor ? '#' + anchor : '') + ')'
  })
  const en = `---\ntitle: ${JSON.stringify(guide.title)}\ndescription: ${JSON.stringify(guide.description)}\n---\n\n` + (guide.slug === 'execution-model' ? '![Application, administration and machine credential boundaries](/images/architecture.svg)\n\n' : '') + mdx(body.trim()) + `\n\n[Repository source](${sourceURL(guide.source)})\n`
  const relative = `content/docs/${guide.slug}.mdx`
  fs.writeFileSync(path.join(app, relative), en)
  record.outputs[relative] = digest(en)
  record.sources[guide.source] = digest(original)
}
record.sources['apps/docs/scripts/guides.json'] = digest(fs.readFileSync(path.join(app, 'scripts/guides.json')))
fs.writeFileSync(path.join(app, 'content/guide-sources.json'), JSON.stringify(record, null, 2) + '\n')
console.log('Rendered ' + guides.length + ' current English guides.')
