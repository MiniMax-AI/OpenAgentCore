import test from 'node:test'
import assert from 'node:assert/strict'
import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import yaml from 'js-yaml'
import { createOpenAPI } from 'fumadocs-openapi/server'
import { schemaToString } from '../node_modules/fumadocs-openapi/dist/utils/schema-to-string.js'
import { appRoot, normalise, surfaces } from './contracts.mjs'

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
