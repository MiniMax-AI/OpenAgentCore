#!/usr/bin/env python3
"""Project the pinned OpenAPI into Core's public contract, Go wire types and TypeScript client types."""
import argparse
import copy
import hashlib
import json
from pathlib import Path
import re
import subprocess

ROOT = Path(__file__).resolve().parents[1]
CONTRACT = ROOT / 'contracts/agents-api'
SHAPES = ROOT / 'services/core/internal/api/official_shapes.gen.go'
CLIENT_TYPES = ROOT / 'packages/agents-client/src/generated/public-api.ts'
# Agent configuration request bodies checked by Core's shape walker.
REQUEST_SHAPES = ('CreateAgentParams', 'UpdateAgentParams', 'SessionAgentConfigParam')
HTTP_METHODS = {'get', 'post', 'put', 'patch', 'delete', 'head', 'options'}


def read_source():
    pin = json.loads((CONTRACT / 'upstream.json').read_text())
    raw = (CONTRACT / 'upstream/openapi.json').read_bytes()
    if hashlib.sha256(raw).hexdigest() != pin['openapi']['sha256']:
        raise ValueError('Official OpenAPI checksum differs from upstream.json')
    return json.loads(raw), pin


def resolve(document, pointer):
    value = document
    for key in pointer.removeprefix('#/').split('/'):
        value = value[key.replace('~1', '/').replace('~0', '~')]
    return value


def dereference(document, schema):
    if '$ref' in schema:
        return {**resolve(document, schema['$ref']), **{k: v for k, v in schema.items() if k != '$ref'}}
    return schema


def variants(document, schema):
    schema = dereference(document, schema)
    for keyword in ('oneOf', 'anyOf'):
        if keyword in schema:
            return [v for part in schema[keyword] for v in variants(document, part)]
    return [schema]


def object_fields(document, sources):
    objects = [v for source in sources for v in variants(document, resolve(document, source))]
    properties = {}
    required = None
    for obj in objects:
        if obj.get('type') == 'null':
            continue
        if 'properties' not in obj:
            raise ValueError(f'Expected object projection: {sources}')
        required = set(obj.get('required', [])) if required is None else required & set(obj.get('required', []))
        for name, schema in obj['properties'].items():
            if name not in properties:
                properties[name] = []
            if schema not in properties[name]:
                properties[name].append(schema)
    return {k: v[0] if len(v) == 1 else {'anyOf': v} for k, v in properties.items()}, required or set()


def nullable(document, schema):
    return any(v.get('type') == 'null' or isinstance(v.get('type'), list) and 'null' in v['type'] for v in variants(document, schema))


def go_name(name):
    initials = {'id': 'ID', 'ids': 'IDs', 'url': 'URL', 'api': 'API', 'mcp': 'MCP', 'npm': 'NPM', 'ms': 'MS', 'oauth': 'OAuth'}
    return ''.join(initials.get(part, part.capitalize()) for part in name.split('_'))


def go_type(document, schema, aliases):
    ref = schema.get('$ref')
    if ref in aliases:
        return aliases[ref]
    values = [v for v in variants(document, schema) if v.get('type') != 'null']
    if len(values) != 1:
        types = {go_type(document, v, aliases) for v in values}
        return types.pop() if len(types) == 1 else 'json.RawMessage'
    value = values[0]
    kind = value.get('type')
    if isinstance(kind, list):
        kinds = set(kind) - {'null'}
        kind = kinds.pop() if len(kinds) == 1 else None
    if kind == 'array':
        return '[]' + go_type(document, value['items'], aliases)
    if kind == 'object':
        extra = value.get('additionalProperties')
        return 'map[string]' + go_type(document, extra, aliases) if isinstance(extra, dict) and not value.get('properties') else 'json.RawMessage'
    return {'string': 'string', 'integer': 'int64', 'number': 'float64', 'boolean': 'bool'}.get(kind, 'json.RawMessage')


def field_type(document, schema, required, aliases):
    result = go_type(document, schema, aliases)
    if (not required or nullable(document, schema)) and not result.startswith(('[]', 'map[', '*')) and result != 'json.RawMessage':
        result = '*' + result
    return result


def enum_values(document, schema):
    values = []
    for variant in variants(document, schema):
        for value in variant.get('enum', []):
            if value is not None and value not in values:
                values.append(value)
    return values


def union_marshaler(document, name, binding, field_types):
    schema = resolve(document, binding['sources'][0])
    discriminator = schema['discriminator']['propertyName']
    selector = go_name(discriminator)
    lines = [f'func (value {name}) MarshalJSON() ([]byte, error) {{', f'switch value.{selector} {{']
    for variant in variants(document, schema):
        tag, = variant['properties'][discriminator]['enum']
        lines += [f'case {json.dumps(tag)}:', 'return json.Marshal(struct {']
        for field in variant['properties']:
            go_field, typ = field_types[field]
            omit = '' if field in variant.get('required', []) else ',omitempty'
            lines.append(f'{go_field} {typ} `json:"{field}{omit}"`')
        lines.append('}{')
        for field in variant['properties']:
            go_field, _ = field_types[field]
            lines.append(f'{go_field}: value.{go_field},')
        lines.append('})')
    lines += ['default:', f'return nil, fmt.Errorf("invalid {name} {discriminator} %q", value.{selector})', '}', '}']
    return lines


def go_types(document, bindings):
    aliases = {}
    for name, binding in sorted(bindings.items()):
        if len(binding['sources']) == 1:
            aliases.setdefault(binding['sources'][0], name)
    lines = ['// Code generated by scripts/generate-public-api.py; DO NOT EDIT.', 'package v1', 'import "encoding/json"', '']
    if any(binding.get('marshal_union') for binding in bindings.values()):
        lines.append('import "fmt"')
    for name, binding in sorted(bindings.items()):
        properties, required = object_fields(document, binding['sources'])
        overrides = binding.get('fields', {})
        unknown = set(overrides) - set(properties) - {'x_agents_core'}
        if unknown:
            raise ValueError(f'{name}: bindings for missing official fields {unknown}')
        lines += [f'// {name} projects ' + ', '.join(s.split('/')[-1] for s in binding['sources']) + '.', f'type {name} struct {{']
        field_types = {}
        embedded = set()
        for embed, fields in binding.get('embed', {}).items():
            lines.append('\t' + embed)
            embedded.update(fields)
        for field in dict.fromkeys(binding.get("order", []) + list(properties)):
            if field in embedded or field in binding.get('exclude', []):
                continue
            if field == "x_agents_core":
                lines.append('\tXAgentsCore ' + overrides[field]['type'] + ' `json:"x_agents_core,omitempty"`')
                continue
            schema = properties[field]
            override = overrides.get(field, {})
            typ = override.get('type', field_type(document, schema, field in required, aliases))
            omit = override.get('omit', field not in required)
            tags = ['json:"' + field + (',omitempty' if omit else '') + '"']
            if field in required:
                tags.append('binding:"required"')
            if nullable(document, schema):
                tags.append('extensions:"x-nullable"')
            enums = enum_values(document, schema)
            if enums:
                tags.append('enums:"' + ','.join(str(v).lower() if isinstance(v, bool) else str(v) for v in enums) + '"')
            # Swag still projects the internal APIs that reference these types.
            if 'json.RawMessage' in typ:
                tags.append('swaggertype:"' + ('array,object' if typ.lstrip('*').startswith('[]') else 'object') + '"')
            field_types[field] = (go_name(field), typ)
            lines.append('\t' + field_types[field][0] + ' ' + typ + ' `' + ' '.join(tags) + '`')
        lines.append('}\n')
        if binding.get('marshal_union'):
            lines.extend(union_marshaler(document, name, binding, field_types))
    return gofmt(lines)


def gofmt(lines):
    return subprocess.run(['gofmt'], input='\n'.join(lines).encode(), stdout=subprocess.PIPE, check=True).stdout


def go_shape(document, schema, required=False):
    """Project one request value onto a shape of services/core/internal/api/configuration_validation.go."""
    value, = [v for v in dereference(document, schema).get('anyOf', [schema]) if v.get('type') != 'null']
    value = dereference(document, value)
    kind = value.get('type')
    if isinstance(kind, list):
        kind, = set(kind) - {'null'}
    if 'oneOf' in value:
        if value['discriminator']['propertyName'] != 'type':
            raise ValueError(f'Unsupported union discriminator: {value["discriminator"]}')
        variants = {}
        for option in value['oneOf']:
            option = dereference(document, option)
            tag, = option['properties']['type']['enum']
            variants[tag] = go_members(document, option, 'type')
        fields = ['kind: unionValue', 'values: []string{' + ', '.join(json.dumps(tag) for tag in variants) + '}',
                  'variants: map[string][]member{\n' + ''.join(f'{json.dumps(tag)}: {members.removeprefix("[]member")},\n' for tag, members in variants.items()) + '}']
    elif 'enum' in value:
        fields = ['kind: enumValue', 'values: []string{' + ', '.join(json.dumps(v) for v in value['enum']) + '}']
    elif kind == 'object' and 'properties' in value:
        fields = ['kind: objectValue', 'members: ' + go_members(document, value)]
    elif kind == 'object':
        values = value['additionalProperties']
        if values and values.get('type') != 'string':
            raise ValueError(f'Unsupported map values: {values}')
        fields = ['kind: mapValue'] + (['items: &shape{kind: stringValue}'] if values else [])
    elif kind == 'array':
        fields = ['kind: arrayValue', 'items: &' + go_shape(document, value['items'])]
    elif kind == 'integer':
        fields = ['kind: integerValue', f'minimum: {value["minimum"]}']
    else:
        fields = [{'string': 'kind: stringValue', 'boolean': 'kind: booleanValue'}[kind]]
    if required:
        fields.append('required: true')
    if nullable(document, schema):
        fields.append('nullable: true')
    return 'shape{' + ', '.join(fields) + '}'


def go_members(document, schema, skip=None):
    # The walker rejects unknown members, so only closed objects project.
    if schema.get('additionalProperties') is not False:
        raise ValueError(f'Unsupported open object: {sorted(schema["properties"])}')
    required = schema.get('required', [])
    return '[]member{\n' + ''.join(f'{{{json.dumps(name)}, {go_shape(document, value, name in required)}}},\n' for name, value in schema['properties'].items() if name != skip) + '}'


def request_shapes(document, owners):
    lines = ['// Code generated by scripts/generate-public-api.py; DO NOT EDIT.', 'package api', '',
             '// Pinned request shapes; x_agents_core is checked by its own parser.', 'var (']
    for name in REQUEST_SHAPES:
        members = go_members(document, resolve(document, '#/components/schemas/' + name))
        if name in owners:
            members = members[:-1] + '{"x_agents_core", shape{}},\n}'
        lines.append(f'{name[0].lower() + name[1:]} = shape{{kind: objectValue, members: {members}}}')
    return gofmt(lines + [')'])


def prune_components(document):
    needed = set()
    def walk(value):
        if isinstance(value, dict):
            ref = value.get('$ref')
            if ref and ref not in needed:
                if not ref.startswith('#/components/'):
                    raise ValueError(f'Non-local reference: {ref}')
                needed.add(ref)
                walk(resolve(document, ref))
            # Discriminator mappings are references too.
            if 'discriminator' in value:
                for ref in value['discriminator'].get('mapping', {}).values():
                    walk({'$ref': ref})
            for child in value.values():
                walk(child)
        elif isinstance(value, list):
            for child in value:
                walk(child)
    walk(document['paths'])
    document['components'] = {section: {k: v for k, v in values.items() if '#/components/' + section + '/' + k in needed} for section, values in document['components'].items()}
    document['components']['securitySchemes'] = {'ProjectKey': {'type': 'http', 'scheme': 'bearer', 'description': 'OpenAgentCore Project API key.'}}


def extension_owners(bindings):
    owners = {}
    for binding in bindings.values():
        extension = binding.get("fields", {}).get("x_agents_core")
        if extension:
            for source in binding["sources"]:
                owners[source.split("/")[-1]] = extension["type"].lstrip("*")
    return owners


def public_document(source, extensions, owners, beta_header):
    doc = copy.deepcopy(source)
    doc['info'] = {'title': 'OpenAgentCore public API', 'version': 'v1', 'description': 'The pinned OpenAI Agents, Files and Skills API with x_agents_core extensions. See the coverage ledger for implementation qualification.'}
    doc['servers'] = [{'url': '/v1'}]
    doc['security'] = [{'ProjectKey': []}]
    doc['paths'] = {p: v for p, v in doc['paths'].items() if p.split('/')[1] in ('agents', 'vaults', 'files', 'skills')}
    for path, item in doc['paths'].items():
        for method, operation in item.items():
            if method not in HTTP_METHODS:
                continue
            operation.pop('security', None)
            if path.split('/')[1] in ('agents', 'vaults'):
                operation['parameters'] = operation.get('parameters', []) + [{'name': 'OpenAI-Beta', 'in': 'header', 'required': True, 'schema': {'type': 'string', 'const': beta_header}}]
    # Extensions are authored in Core's Go types and projected by swag. Official
    # properties are never rewritten to accommodate a narrower implementation.
    def convert(value):
        if isinstance(value, list):
            return [convert(v) for v in value]
        if not isinstance(value, dict):
            return value
        result = {k: convert(v) for k, v in value.items() if k != 'x-nullable'}
        if '$ref' in result:
            result['$ref'] = result['$ref'].replace('#/definitions/', '#/components/schemas/')
        if value.get('x-nullable'):
            return {'anyOf': [result, {'type': 'null'}]}
        return result
    doc['components']['schemas'].update({k: convert(v) for k, v in extensions.items()})
    for name, extension in owners.items():
        doc['components']['schemas'][name]['properties']['x_agents_core'] = {'anyOf': [{'$ref': '#/components/schemas/v1.' + extension}, {'type': 'null'}]}
    prune_components(doc)
    return doc


def ts_name(name):
    return re.sub(r'\W', '', name.split('/')[-1].split('.')[-1])


def ts_const(name, suffix):
    name = ts_name(name)
    return name[0].lower() + name[1:] + suffix


def ts_group(typ):
    return f'({typ})' if ' | ' in typ or ' & ' in typ else typ


def ts_type(schema):
    """Map an OpenAPI 3.1 or Swagger 2.0 schema (x-nullable, any ref prefix) onto a TypeScript type."""
    if schema.get('x-nullable'):
        return ts_type({k: v for k, v in schema.items() if k != 'x-nullable'}) + ' | null'
    if '$ref' in schema:
        return ts_name(schema['$ref'])
    for keyword in ('oneOf', 'anyOf'):
        if keyword in schema:
            return ' | '.join(dict.fromkeys(ts_type(v) for v in schema[keyword]))
    if 'allOf' in schema:
        return ' & '.join(dict.fromkeys(ts_group(ts_type(v)) for v in schema['allOf']))
    kind = schema.get('type')
    if 'enum' in schema:
        values = schema['enum'] + ([None] if isinstance(kind, list) and 'null' in kind and None not in schema['enum'] else [])
        return ' | '.join(json.dumps(v) for v in values)
    if isinstance(kind, list):
        return ' | '.join(ts_type({**schema, 'type': k}) for k in kind)
    if kind == 'array':
        return ts_group(ts_type(schema['items'])) + '[]'
    if 'properties' in schema:
        required = schema.get('required', [])
        return '{ ' + ' '.join(f'{k}{"" if k in required else "?"}: {ts_type(v)};' for k, v in schema['properties'].items()) + ' }'
    if kind == 'object':
        extra = schema.get('additionalProperties')
        return f'Record<string, {ts_type(extra) if isinstance(extra, dict) and extra else "unknown"}>'
    return {'string': 'string', 'integer': 'number', 'number': 'number', 'boolean': 'boolean', 'null': 'null'}.get(kind, 'unknown')


def ts_module(schemas, source):
    """Project parsed schemas (OpenAPI components.schemas or Swagger definitions) onto TypeScript.

    Each enum X gets xValues; each object X gets xFields, plus xRequired when some
    fields are optional; each discriminated union X maps its tags to the variants'
    field names in xFields.
    """
    if len({ts_name(name) for name in schemas}) != len(schemas):
        raise ValueError('TypeScript names collide')
    lines = [f'// Code generated by scripts/generate-public-api.py from {source}; DO NOT EDIT.', '']
    unions = []
    for name, schema in sorted(schemas.items(), key=lambda item: ts_name(item[0])):
        typ = ts_name(name)
        if 'enum' in schema:
            values = ts_const(name, 'Values')
            lines += [f'export const {values} = {json.dumps(schema["enum"])} as const;', f'export type {typ} = (typeof {values})[number];']
        elif 'properties' in schema:
            required = schema.get('required', [])
            lines.append(f'export interface {typ} {{')
            lines += [f'  {k}{"" if k in required else "?"}: {ts_type(v)};' for k, v in schema['properties'].items()]
            lines += ['}', f'export const {ts_const(name, "Fields")} = {json.dumps(list(schema["properties"]))} as const;']
            if set(required) != set(schema['properties']):
                lines.append(f'export const {ts_const(name, "Required")} = {json.dumps(required)} as const;')
        else:
            lines.append(f'export type {typ} = {ts_type(schema)};')
            if 'discriminator' in schema:
                mapping = schema['discriminator']['mapping']
                unions += [f'export const {ts_const(name, "Fields")} = {{'] + [f'  {json.dumps(tag)}: {ts_const(ref, "Fields")},' for tag, ref in mapping.items()] + ['} as const;']
    return ('\n'.join(lines + unions) + '\n').encode()


def write(path, data, check):
    if check:
        if not path.exists() or path.read_bytes() != data:
            raise ValueError(f'{path.relative_to(ROOT)} is stale; run make openapi')
    else:
        path.write_bytes(data)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--check', action='store_true')
    stage = parser.add_mutually_exclusive_group(required=True)
    stage.add_argument('--swag-roots', type=Path, help='Temporary entry points for Go-owned extension types')
    stage.add_argument('--extensions', type=Path, help='JSON definitions projected from Core extension types')
    args = parser.parse_args()
    source, pin = read_source()
    bindings = json.loads((CONTRACT / 'go-bindings.json').read_text())
    owners = extension_owners(bindings)
    if args.swag_roots:
        write(CONTRACT / 'v1/official.gen.go', go_types(source, bindings), args.check)
        write(SHAPES, request_shapes(source, owners), args.check)
        roots = 'package extensions\n\n' + '\n'.join(f'// @Success 200 {{object}} v1.{name}' for name in sorted(set(owners.values()))) + '\nfunc extensions() {}\n'
        args.swag_roots.write_text(roots)
    else:
        doc = public_document(source, json.loads(args.extensions.read_text()), owners, pin["beta_header"])
        # JSON is a YAML subset and keeps generation independent of PyYAML.
        write(CONTRACT / 'openapi.yaml', (json.dumps(doc, indent=2, ensure_ascii=False) + '\n').encode(), args.check)
        write(CLIENT_TYPES, ts_module(doc['components']['schemas'], 'contracts/agents-api/openapi.yaml'), args.check)
        routes = sorted(method.upper() + ' ' + re.sub(r'\{[^}]*\}', '{}', path) for path, item in doc['paths'].items() for method in item if method in HTTP_METHODS)
        write(CONTRACT / 'upstream-routes.json', (json.dumps({'openapi_commit': pin['openapi']['commit'], 'generator': 'scripts/generate-public-api.py', 'routes': routes}, indent=2) + '\n').encode(), args.check)


if __name__ == '__main__':
    main()
