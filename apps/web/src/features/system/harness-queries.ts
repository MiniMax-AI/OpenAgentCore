import type { CoreHarness } from "@oac/agents-client";
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

/**
 * Whether a new Core-hosted Session can run on this harness without bringing
 * its own model provider: the harness is enabled and has a deployment default.
 */
export function harnessReady(harness: CoreHarness): boolean {
  return harness.enabled && harness.model_configuration !== null;
}
