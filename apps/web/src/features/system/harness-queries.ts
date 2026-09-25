import { queryOptions } from "@tanstack/react-query";

import { admin } from "../../lib/projects";

/**
 * The harnesses and their deployment default model providers
 * (`/core/v1/harnesses`). The cache holds only Core's safe view; an API key
 * being written never enters it.
 */
export const harnessesQuery = queryOptions({
  queryKey: ["harnesses"],
  queryFn: ({ signal }) => admin.listHarnesses({ signal }),
});
