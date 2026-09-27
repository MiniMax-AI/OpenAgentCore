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
const sourceRevision = '75bf4484ba957344ae374354858bbdfe104f5c4f'
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
  const zh = `---\ntitle: ${JSON.stringify(guide.title + ' · 中文导读')}\ndescription: "当前版本的中文导读；完整操作步骤见英文正文。"\n---\n\n${guide.zh.replaceAll('<', '&lt;')}\n\n此页为中文导读，完整且与当前源码同步的步骤请阅读[英文正文](/en/${guide.slug === 'index' ? '' : guide.slug})。\n\n[安装](/install) · [配置](/configure) · [API 与凭据](/public-api) · [节点](/hosted-providers) · [应用示例](/quickstart)\n`
  for (const [suffix, text] of [['', en], ['.zh', zh]]) {
    const relative = `content/docs/${guide.slug}${suffix}.mdx`
    fs.writeFileSync(path.join(app, relative), text)
    record.outputs[relative] = digest(text)
  }
  record.sources[guide.source] = digest(original)
}
record.sources['apps/docs/scripts/guides.json'] = digest(fs.readFileSync(path.join(app, 'scripts/guides.json')))
fs.writeFileSync(path.join(app, 'content/guide-sources.json'), JSON.stringify(record, null, 2) + '\n')
console.log('Rendered ' + guides.length + ' current guides and Chinese reading notes.')
