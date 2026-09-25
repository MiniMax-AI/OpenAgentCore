import { QueryClient } from "@tanstack/react-query";
import { describe, expect, it } from "vitest";

import { projectsQuery } from "../../lib/queries";
import { invalidateProjects, loadedFrom, projectActivityQuery, projectKeysQuery, projectSummaryQuery, writeOperationsQuery } from "./project-queries";

describe("loaded state from the cache", () => {
  it("keeps cached data on screen while a refresh runs or fails", () => {
    expect(loadedFrom({ data: undefined, isFetching: true, isError: false })).toEqual({ status: "loading", value: null });
    expect(loadedFrom({ data: ["a"], isFetching: true, isError: false })).toEqual({ status: "loading", value: ["a"] });
    expect(loadedFrom({ data: ["a"], isFetching: false, isError: true })).toEqual({ status: "failed", value: ["a"] });
    expect(loadedFrom({ data: undefined, isFetching: false, isError: true })).toEqual({ status: "failed", value: null });
    expect(loadedFrom({ data: ["a"], isFetching: false, isError: false })).toEqual({ status: "ready", value: ["a"] });
    // A query that has not started yet (no project chosen) is not "ready" with nothing.
    expect(loadedFrom({ data: undefined, isFetching: false, isError: false })).toEqual({ status: "loading", value: null });
  });
});

describe("project invalidation", () => {
  const seeded = () => {
    const client = new QueryClient();
    client.setQueryData(projectsQuery.queryKey, []);
    client.setQueryData(projectActivityQuery.queryKey, []);
    client.setQueryData(projectKeysQuery("proj_a").queryKey, []);
    client.setQueryData(projectSummaryQuery("proj_a").queryKey, { project: null, byKey: null });
    client.setQueryData(writeOperationsQuery("proj_a", { keyId: "", type: "" }).queryKey, { pages: [], pageParams: [] });
    client.setQueryData(projectKeysQuery("proj_b").queryKey, []);
    return client;
  };
  const invalidated = (client: QueryClient) => client.getQueryCache().getAll().filter((query) => query.state.isInvalidated).map((query) => JSON.stringify(query.queryKey)).sort();

  it("re-reads a project's keys, usage and write operations together with the project list", async () => {
    const client = seeded();
    await invalidateProjects(client, { projectId: "proj_a" });
    expect(invalidated(client)).toEqual([
      '["project","proj_a","keys"]',
      '["project","proj_a","summary"]',
      '["project","proj_a","write-operations",{"keyId":"","type":""}]',
      '["projects"]',
    ]);
  });

  it("re-reads only the list (and its activity when asked) for project-level writes", async () => {
    const client = seeded();
    await invalidateProjects(client, { activity: true });
    expect(invalidated(client)).toEqual(['["project-activity"]', '["projects"]']);
  });
});
