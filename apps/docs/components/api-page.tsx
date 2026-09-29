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
  // Response schemas are shown in full; generating a TypeScript copy of every
  // response for every status compiled the same schemas again and was about
  // half of each page's render time.
  return (
    <OpenAPIPage
      {...props}
      document={await loadSurface(document)}
      disablePlayground={true}
      generateTypeScriptSchema={false}
    />
  )
}
