import test from 'node:test'
import assert from 'node:assert/strict'
import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import yaml from 'js-yaml'
import { createOpenAPI } from 'fumadocs-openapi/server'
import { schemaToString } from '../node_modules/fumadocs-openapi/dist/utils/schema-to-string.js'
import { appRoot, normalise, surfaces, splitDescription, detailsBlock, mdxText, plainText, fullPath } from './contracts.mjs'

test('overview paths carry the namespace a caller sends', () => {
  const [publicApi, coreApi] = surfaces
  assert.equal(fullPath(publicApi, '/agents/{agent_id}'), '/v1/agents/{agent_id}')
  assert.equal(fullPath(coreApi, '/core/v1/sandbox/deployment'), '/core/v1/sandbox/deployment')
  assert.equal(fullPath(publicApi, '/v1'), '/v1')
})

test('operation descriptions split into a lead and details', () => {
  assert.deepEqual(splitDescription('Saves an Agent.\n\n- Limit one.\n- Limit two.'), { lead: 'Saves an Agent.', details: '- Limit one.\n- Limit two.' })
  // A single paragraph splits after the first sentences that reach 40 characters.
  assert.deepEqual(splitDescription('Core key only. Returns the harnesses Core supports. Keys are never returned.'),
    { lead: 'Core key only. Returns the harnesses Core supports.', details: 'Keys are never returned.' })
  // Abbreviations and paths do not end a sentence; a short description stays whole.
  assert.deepEqual(splitDescription('Returns metadata, e.g. the ID, for /v1.x files.'), { lead: 'Returns metadata, e.g. the ID, for /v1.x files.', details: '' })
  assert.deepEqual(splitDescription(undefined), { lead: '', details: '' })
})

test('a long description folds into paragraphs that keep every sentence', () => {
  const text = Array.from({ length: 12 }, (_, i) => `Fact number ${i + 1} is stated in this description.`).join(' ')
  assert.ok(text.length > 400, 'the fixture must be long enough to fold')
  const block = detailsBlock(text)
  assert.match(block, /^<details className="api-details">\n<summary>Full description<\/summary>\n\n/)
  assert.match(block, /\n\n<\/details>\n\n$/)
  const inner = block.replace(/^<details[^\n]*>\n<summary>[^\n]*<\/summary>\n\n/, '').replace(/\n\n<\/details>\n\n$/, '')
  const paragraphs = inner.split('\n\n')
  assert.ok(paragraphs.length > 1, 'a folded description is regrouped into paragraphs')
  assert.equal(paragraphs.join(' ').replace(/\s+/g, ' '), text.replace(/\s+/g, ' '))
})

test('short details and authored structure stay in place', () => {
  assert.equal(detailsBlock('Keys are never returned.'), 'Keys are never returned.\n\n')
  assert.equal(detailsBlock('Saves an Agent.\n\n- Limit one.\n- Limit two.'), 'Saves an Agent.\n\n- Limit one.\n- Limit two.\n\n')
  assert.equal(detailsBlock(''), '')
})

test('a lead loses inline markdown because page descriptions are plain text', () => {
  assert.equal(plainText('With `stream=true`, see **[Request conventions](/request-conventions)**.'), 'With stream=true, see Request conventions.')
})

test('contract markdown is escaped for MDX outside code spans only', () => {
  assert.equal(mdxText('an empty body is {} and `{}` or <key>'), 'an empty body is &#123;&#125; and `{}` or &lt;key>')
})

test('public rendering preserves operation prose, schemas and project authentication', () => {
  const fixture = { swagger: '2.0', info: { title: 'Example', version: '1' }, basePath: '/v1', paths: {
    '/agents': { get: { tags: ['Agents'], description: 'A constraint that must remain visible.', security: [{ ProjectKey: [] }], responses: { 200: { description: 'Result', schema: { $ref: '#/definitions/Agent' } } } } },
  }, definitions: { Agent: { type: 'object', properties: { id: { type: 'string' } } } }, securityDefinitions: { ProjectKey: { type: 'apiKey', in: 'header', name: 'Authorization' } } }
  const rendered = normalise(surfaces[0], fixture)
  assert.equal(rendered.paths['/agents'].get.description, fixture.paths['/agents'].get.description)
  assert.deepEqual(rendered.paths['/agents'].get.security, [{ ProjectKey: [] }])
  assert.equal(rendered.components.schemas.Agent.properties.id.type, 'string')
  assert.equal(rendered.components.securitySchemes.ProjectKey.scheme, 'bearer')
  assert.equal(rendered.servers[0].url, 'https://core.example.com/v1')
})

test('OpenAPI 3 is accepted without guessing its authority or changing its schemas', () => {
  const fixture = { openapi: '3.0.3', info: { title: 'Example', version: '1' }, paths: {
    '/core/v1/example': { get: { tags: ['Example'], responses: { 200: { description: 'OK' } } } },
  }, components: { schemas: { Payload: { type: 'string', enum: ['exact'] } } } }
  assert.deepEqual(normalise(surfaces[1], fixture).components.schemas, fixture.components.schemas)
  assert.throws(() => normalise(surfaces[0], fixture), /credential boundary/)
  assert.throws(() => normalise(surfaces[1], { ...fixture, openapi: '9.0' }), /Unsupported/)
})

test('actual Fumadocs renderer preserves nullable values, references and compositions', async (t) => {
  const directory = fs.mkdtempSync(path.join(os.tmpdir(), 'oac-docs-nullable-'))
  t.after(() => fs.rmSync(directory, { recursive: true, force: true }))
  const example = { schema: { type: 'string', 'x-nullable': true } }
  const fixture = { swagger: '2.0', info: { title: 'Example', version: '1' }, paths: {}, definitions: {
    Text: { type: 'string' },
    Payload: { type: 'object', properties: {
      text: { type: 'string', 'x-nullable': true, description: 'May be null.' },
      reference: { $ref: '#/definitions/Text', 'x-nullable': true },
      composed: { allOf: [{ $ref: '#/definitions/Text' }], 'x-nullable': true },
      choice: { type: 'string', enum: ['only'], 'x-nullable': true },
      titled: { type: 'string', title: 'Label', 'x-nullable': true },
      requiredText: { type: 'string' },
      sample: { type: 'object', example },
    } },
  } }
  const original = structuredClone(fixture)
  const projected = normalise(surfaces[0], fixture)
  assert.equal(projected.openapi, '3.1.0')
  assert.deepEqual(fixture, original, 'Canonical source must not be mutated')
  assert.deepEqual(projected.components.schemas.Payload.properties.sample.example, example)
  assert.deepEqual(projected.components.schemas.Payload.properties.choice.anyOf, [
    { type: 'string', enum: ['only'] }, { type: 'null' },
  ], 'Null must be allowed outside the enum constraint')
  const file = path.join(directory, 'fixture.yaml')
  fs.writeFileSync(file, yaml.dump(projected))
  const docs = await createOpenAPI({ input: [file], disablePlayground: true }).getSchemas()
  const properties = docs[file].dereferenced.components.schemas.Payload.properties
  for (const [field, expected] of Object.entries({ text: 'string | null', reference: 'Text | null', composed: 'Text | null', choice: 'string | null', titled: 'Label | null', requiredText: 'string' })) {
    assert.equal(schemaToString(properties[field], docs[file]), expected, field)
  }
  assert.equal(properties.text.description, 'May be null.')
})

test('actual generated public reference renders the pinned nullable error fields', async () => {
  const file = path.join(appRoot, 'openapi/public-api.yaml')
  const docs = await createOpenAPI({ input: [file], disablePlayground: true }).getSchemas()
  const schemas = docs[file].dereferenced.components.schemas
  for (const [name, field] of [['v1.Session', 'error'], ['v1.APIError', 'code'], ['v1.APIError', 'param']]) {
    assert.equal(schemaToString(schemas[name].properties[field], docs[file]), 'string | null', `${name}.${field}`)
  }
})

test('OpenAPI 3.0 schema conversion preserves bounds and binary annotations', () => {
  const fixture = { openapi: '3.0.3', info: { title: 'Example', version: '1' }, paths: {}, components: { schemas: {
    Number: { type: 'number', minimum: 1, exclusiveMinimum: true, maximum: 10, exclusiveMaximum: false, nullable: true },
    File: { type: 'string', format: 'binary' },
  } } }
  const schemas = normalise(surfaces[0], fixture).components.schemas
  assert.deepEqual(schemas.Number.anyOf, [
    { type: 'number', minimum: 1, exclusiveMinimum: 1, maximum: 10 }, { type: 'null' },
  ])
  assert.deepEqual(schemas.File, fixture.components.schemas.File)
})
