// Registers the OpenAPI documents the reference pages render.
//
// Generated MDX names its surface by id rather than by file path so the pages
// stay portable across checkout locations. `scripts/generate-api-reference.mjs`
// owns the ids and the files; add an entry here when it gains a surface.
//
// `getSchemas()` keys its result by the very string passed as `input`, so the
// lookup below goes through the same `absolute` map that built the input.

import { createOpenAPI } from "fumadocs-openapi/server"
import path from "node:path"

export const SURFACES = {
  "public-api": "openapi/public-api.yaml",
  "core-api": "openapi/core-api.yaml",
  "runtime-api": "openapi/runtime-api.yaml",
} as const

export type SurfaceId = keyof typeof SURFACES

const absolute = Object.fromEntries(
  Object.entries(SURFACES).map(([id, relative]) => [
    id,
    path.join(process.cwd(), relative),
  ]),
) as Record<SurfaceId, string>

export const openapi = createOpenAPI({ input: Object.values(absolute), disablePlayground: true })

/** Reads one surface, by the id the generated MDX refers to. */
export async function loadSurface(id: string) {
  const file = absolute[id as SurfaceId]
  if (!file) {
    throw new Error(
      `Unknown API surface "${id}". Known surfaces: ` +
        `${Object.keys(SURFACES).join(", ")}.`,
    )
  }
  const schemas = await openapi.getSchemas()
  const document = schemas[file]
  if (!document) {
    throw new Error(`API surface "${id}" was not processed from ${file}.`)
  }
  return document
}
