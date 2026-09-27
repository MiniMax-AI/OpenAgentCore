import test from 'node:test'
import assert from 'node:assert/strict'
import { normalise, surfaces } from './contracts.mjs'

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
