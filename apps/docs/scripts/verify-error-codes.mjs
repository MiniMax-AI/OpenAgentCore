#!/usr/bin/env node
// Keep client-side error codes tied to the registry: codes the TypeScript
// client creates itself and codes the client and Web compare against.
//
// This is half of the registry check. The other half, every status and code the
// Go services write, is services/agents-api/internal/api/contract_conformance_test.go,
// which runs in `make check-agents-api`, not in `pnpm verify`. Both run in CI.
import fs from 'node:fs'
import path from 'node:path'
import assert from 'node:assert/strict'
import { repoRoot } from './contracts.mjs'

const registry = fs.readFileSync(path.join(repoRoot, 'contracts/agents-api/error-codes.md'), 'utf8')

// Codes by registry section, from the code column of each table row.
const sections = new Map()
let heading = null
for (const line of registry.split('\n')) {
  if (line.startsWith('## ')) { heading = line.slice(3).trim(); sections.set(heading, new Set()); continue }
  const cells = line.split('|').map(cell => cell.trim())
  if (!heading || cells.length < 3) continue
  for (const cell of cells.slice(1, 3)) {
    const match = /^`([a-z0-9_]+)`$/.exec(cell)
    if (match) { sections.get(heading).add(match[1]); break }
  }
}
const known = new Set([...sections.values()].flatMap(set => [...set]))

// Session diagnostic categories are not HTTP codes; core-errors.md owns them.
const coreErrors = fs.readFileSync(path.join(repoRoot, 'contracts/agents-api/core-errors.md'), 'utf8')
const diagnostics = /^## Diagnostic failure categories\n([\s\S]*?)(?=^## |(?![\s\S]))/m.exec(coreErrors)
assert.ok(diagnostics, 'core-errors.md has no Diagnostic failure categories section')
for (const match of diagnostics[1].matchAll(/^\| `([a-z0-9_]+)` \|/gm)) known.add(match[1])

// Codes Web still compares against although nothing emits them, with the owner's
// follow-up. An entry here is a known defect, not an accepted code.
const retired = new Map()

function sources(directory) {
  return fs.readdirSync(directory, { withFileTypes: true }).flatMap(entry => {
    const file = path.join(directory, entry.name)
    if (entry.isDirectory()) return entry.name === 'node_modules' ? [] : sources(file)
    return /\.(ts|tsx)$/.test(entry.name) && !/\.test\.(ts|tsx)$/.test(entry.name) ? [file] : []
  })
}

// Top-level arguments of the call whose "(" is at index open. Strings,
// template literals and nested brackets are skipped; this is enough for the
// client sources, which a type checker already keeps well formed.
function callArguments(text, open) {
  const args = []
  let depth = 0, start = open + 1, quote = null
  for (let i = open; i < text.length; i++) {
    const c = text[i]
    if (quote) {
      if (c === '\\') i++
      else if (c === quote) quote = null
      continue
    }
    if (c === '"' || c === "'" || c === '`') quote = c
    else if ('([{'.includes(c)) depth++
    else if (')]}'.includes(c)) {
      depth--
      if (depth === 0) { args.push(text.slice(start, i).trim()); return Object.assign(args.filter(a => a !== ''), { end: i }) }
    } else if (c === ',' && depth === 1) { args.push(text.slice(start, i).trim()); start = i + 1 }
  }
  return args
}
const literal = arg => /^"([a-z0-9_]+)"$/.exec(arg ?? '')?.[1]
const calls = (text, name) => [...text.matchAll(new RegExp(`(?<!function\\s)(?<![\\w.])${name}\\s*(?:<[^<>()]*>)?\\s*\\(`, 'g'))]
  .map(match => callArguments(text, match.index + match[0].length - 1))

const files = ['packages/agents-client/src', 'apps/web/src'].flatMap(root => sources(path.join(repoRoot, root)))
  .map(file => ({ file, text: fs.readFileSync(file, 'utf8') }))

// A forwarder is a function that passes one of its parameters on as an
// AgentCoreError code, directly or through another forwarder. Codes given to a
// forwarder as literals are created codes as well.
const forwarders = new Map([['AgentCoreError', 2]]) // new AgentCoreError(message, status, code, param?)
const definitions = files.flatMap(({ text }) => [...text.matchAll(/\bfunction\s+(\w+)\s*(?:<[^()]*>)?\s*\(/g)].map(match => {
  const open = match.index + match[0].length - 1
  const list = callArguments(text, open)
  const params = list.map(param => /^(?:\.\.\.)?(\w+)/.exec(param)?.[1])
  // A return type may itself be an object type, so the body is taken up to the
  // function's closing brace at column 0 rather than from the first "{".
  const start = list.end ?? open
  const close = text.indexOf('\n}', start)
  return { name: match[1], params, body: text.slice(start, close < 0 ? text.length : close + 2) }
}))
for (let changed = true; changed;) {
  changed = false
  for (const { name, params, body } of definitions) {
    if (forwarders.has(name)) continue
    for (const [target, index] of forwarders) {
      const forwarded = calls(body, target).map(args => params.indexOf(args[index])).find(position => position >= 0)
      if (forwarded !== undefined) { forwarders.set(name, forwarded); changed = true; break }
    }
  }
}

const created = new Map()
const compared = new Map()
const note = (map, code, file) => map.set(code, [...(map.get(code) ?? []), path.relative(repoRoot, file)])
for (const { file, text } of files) {
  for (const [name, index] of forwarders) {
    for (const args of calls(text, name)) {
      const code = literal(args[index])
      if (code) note(created, code, file)
    }
  }
  for (const match of text.matchAll(/(typeof\s+[\w.?]*)?\bcode\s*[!=]==?\s*"([a-z0-9_]+)"|"([a-z0-9_]+)"\s*[!=]==?\s*[\w.?]*\bcode\b/g)) {
    if (match[1]) continue // typeof value.code === "string" checks a type, not a code
    note(compared, match[2] ?? match[3], file)
  }
}

const client = sections.get('Client-generated codes')
assert.ok(client, 'error-codes.md has no Client-generated codes section')
const server = new Set([...sections].filter(([name]) => name !== 'Client-generated codes').flatMap(([, codes]) => [...codes]))
const problems = []
for (const [code, sites] of created) {
  // The client may rebuild a Core error with the same code, dropping unsafe fields.
  if (!client.has(code) && !server.has(code)) problems.push(`client creates ${code} (${[...new Set(sites)].join(', ')}) but the Client-generated codes table does not list it`)
}
for (const code of client) {
  if (!created.has(code)) problems.push(`Client-generated codes lists ${code}, which no client source creates`)
  if (server.has(code)) problems.push(`${code} is listed both as a Core code and as client-generated`)
}
for (const [code, files] of compared) {
  if (!known.has(code) && !retired.has(code)) problems.push(`${[...new Set(files)].join(', ')} compares against ${code}, which error-codes.md does not list`)
}
for (const [code, reason] of retired) {
  if (known.has(code)) problems.push(`${code} is listed in error-codes.md; remove it from the retired list`)
  if (!compared.has(code)) problems.push(`${code} is no longer compared; remove it from the retired list`)
  else console.warn('Known defect: ' + reason)
}
assert.deepEqual(problems, [], 'Error code registry is out of date:\n' + problems.join('\n'))
console.log(`${created.size} client-created codes (through ${forwarders.size - 1} forwarding helpers) and ${compared.size} compared codes match the registry.`)
