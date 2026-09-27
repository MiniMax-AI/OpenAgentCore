import { APIPage as OpenAPIPage, type ApiPageProps } from "fumadocs-openapi/ui"
import { loadSurface } from "@/lib/openapi"

// Registered as `APIPage` for the MDX components map. The generated pages name
// their API surface by id; the contract itself is loaded through the server
// instance so the rendered schema and the generated prose stay in step.
export async function APIPage({
  document,
  ...props
}: Omit<ApiPageProps, "document"> & { document: string }) {
  // References never collect credentials or dispatch requests from the browser.
  return <OpenAPIPage {...props} document={await loadSurface(document)} disablePlayground={true} />
}
