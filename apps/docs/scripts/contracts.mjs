// The public schema is constrained by the pinned upstream route/field snapshots.
// Core and machine schemas are local generated contracts, with separate authority.
import { upgrade } from '@scalar/openapi-upgrader'
import yaml from 'js-yaml'
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
export const appRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
export const repoRoot = path.resolve(appRoot, '../..')
export const methods = ['get', 'post', 'put', 'patch', 'delete', 'head', 'options', 'trace']
export const surfaces = [
  { id: 'public-api', file: 'openapi.yaml', directory: '', title: 'Application API', prefix: '/v1', credential: 'Project API key', authority: 'Public schema constrained by the pinned OpenAI Agents API baseline; documented x_agents_core fields remain Core extensions.' },
  { id: 'core-api', file: 'core.openapi.yaml', directory: '/core', title: 'Core administration API', prefix: '/core/v1', credential: 'Core key held by Web’s server or an operator script', authority: 'Generated local management contract. This is not part of the public OpenAI API.' },
  { id: 'runtime-api', file: 'runtime.openapi.yaml', directory: '/machine', title: 'Machine connection API', prefix: '/api/v1', credential: 'Route-specific node enrollment, node, daemon, or executor credential', authority: 'Generated local machine contract. These connections reach Core directly, never through Web.' },
]
export function sourcePath(surface) { return path.join(repoRoot, 'contracts/agents-api', surface.file) }
export function normalise(surface, source = yaml.load(fs.readFileSync(sourcePath(surface), 'utf8'))) {
  if (source.swagger !== '2.0' && !/^3\./.test(source.openapi ?? '')) throw new Error('Unsupported contract format: ' + surface.file)
  const base = source.basePath === '/' ? '' : (source.basePath ?? '')
  const document = source.swagger === '2.0' ? upgrade(structuredClone(source), '3.0') : structuredClone(source)
  projectSchemas(document)
  document.servers = [{ url: (surface.id === 'core-api' ? 'https://core-direct.example.com' : 'https://core.example.com') + base, description: surface.id === 'core-api' ? 'Operator-script placeholder: replace with the Core loopback origin, not the public Web entry.' : 'Documentation placeholder; substitute your Core public origin.' }]
  for (const scheme of Object.values(document.components?.securitySchemes ?? {})) {
    if (scheme.type === 'apiKey' && scheme.in === 'header' && scheme.name?.toLowerCase() === 'authorization') {
      delete scheme.in; delete scheme.name; scheme.type = 'http'; scheme.scheme = 'bearer'
    }
  }
  const tags = new Set()
  for (const [route, item] of Object.entries(document.paths ?? {})) {
    for (const method of methods) {
      const operation = item[method]
      if (!operation) continue
      if (!(base + route).startsWith(surface.prefix + '/')) throw new Error('Route crosses credential boundary: ' + surface.id + ' ' + route)
      for (const tag of operation.tags ?? ['Operations']) tags.add(tag)
    }
  }
  document.tags = [...tags].map(name => ({ name, description: `${name}. ${surface.title}: ${surface.credential}. ${surface.authority}` }))
  return document
}

// OpenAPI 3.1 can express nullable references and compositions without relying on
// Swagger extensions that the reference renderer ignores. Visit schema locations
// only: example payloads may themselves contain keys named type or x-nullable.
function projectSchemas(document) {
  const from30 = document.openapi.startsWith('3.0')
  function schema(value) {
    if (!value || typeof value !== 'object') return value
    for (const key of ['properties', 'patternProperties', '$defs', 'dependentSchemas']) {
      for (const [name, child] of Object.entries(value[key] ?? {})) value[key][name] = schema(child)
    }
    for (const key of ['items', 'additionalProperties', 'not', 'contains', 'propertyNames', 'if', 'then', 'else', 'unevaluatedProperties', 'unevaluatedItems']) {
      if (key in value) value[key] = schema(value[key])
    }
    for (const key of ['allOf', 'anyOf', 'oneOf', 'prefixItems']) {
      if (Array.isArray(value[key])) value[key] = value[key].map(schema)
    }
    if (from30) {
      for (const bound of ['Minimum', 'Maximum']) {
        const key = 'exclusive' + bound
        if (value[key] === true) value[key] = value[bound.toLowerCase()]
        else if (value[key] === false) delete value[key]
      }
    }
    const nullable = value['x-nullable'] === true || (from30 && value.nullable === true)
    delete value['x-nullable']
    if (from30) delete value.nullable
    if (!nullable) return value
    // A union also permits null for enum, $ref and allOf schemas; adding only
    // type: [..., 'null'] would leave enum/composition constraints excluding null.
    const result = { anyOf: [value, { type: 'null' }] }
    for (const key of ['description', 'readOnly', 'writeOnly', 'deprecated', 'example', 'examples', 'default']) {
      if (key in value) { result[key] = value[key]; delete value[key] }
    }
    return result
  }
  function visit(value) {
    if (!value || typeof value !== 'object') return
    for (const [key, child] of Object.entries(value)) {
      if (key === 'schema') value[key] = schema(child)
      else if (!['example', 'examples', 'default', 'enum'].includes(key) && !key.startsWith('x-')) visit(child)
    }
  }
  for (const [name, value] of Object.entries(document.components?.schemas ?? {})) {
    document.components.schemas[name] = schema(value)
  }
  // Components schemas were already handled and must not be interpreted as
  // document objects (a property can legally be named "schema").
  const { schemas, ...otherComponents } = document.components ?? {}
  visit({ ...document, components: otherComponents })
  if (from30) document.openapi = '3.1.0'
}
