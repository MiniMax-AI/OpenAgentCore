import { queryOptions } from "@tanstack/react-query";

import { retrieveStartupConfiguration } from "../../lib/admin-view";

/** Core's startup configuration: harnesses, gateways and the managed sandbox. */
export const startupConfigurationQuery = queryOptions({
  queryKey: ["startup-configuration"],
  queryFn: ({ signal }) => retrieveStartupConfiguration(signal),
});
