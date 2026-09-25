import { queryOptions } from "@tanstack/react-query";

import { admin } from "./admin-view";

/**
 * This installation's public address, its `/v1` base URL for applications and
 * its config.json startup settings (`/core/v1/installation`). Readable before
 * any sandbox deployment exists; the console never writes it.
 */
export const installationQuery = queryOptions({
  queryKey: ["installation"],
  queryFn: ({ signal }) => admin.retrieveInstallation({ signal }),
});
