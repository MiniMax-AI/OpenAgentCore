import type { ListPage } from "@oac/agents-client";
import { describe, expect, it } from "vitest";

import { appendCollectionPage, listAllCollectionPages, listStableCollectionPages } from "./collection-pagination";

function page<T extends { id: string }>(data: T[], hasMore: boolean, lastId: string | null = null): ListPage<T> {
  return { object: "list", data, first_id: data[0]?.id ?? null, last_id: lastId, has_more: hasMore };
}

describe("top-level collection pagination", () => {
  it("loads every page in stable descending order and forwards cancellation", async () => {
    const controller = new AbortController();
    const calls: Array<{ after?: string; signal?: AbortSignal }> = [];
    const result = await listAllCollectionPages(async (options) => {
      calls.push({ after: options.after, signal: options.signal });
      return options.after
        ? page([{ id: "agent-1" }], false)
        : page([{ id: "agent-3" }, { id: "agent-2" }], true, "agent-2");
    }, controller.signal);

    expect(result.map((value) => value.id)).toEqual(["agent-3", "agent-2", "agent-1"]);
    expect(calls).toEqual([
      { after: undefined, signal: controller.signal },
      { after: "agent-2", signal: controller.signal },
    ]);
  });

  it("fails closed for duplicate identities, empty continuation pages, and cyclic cursors", async () => {
    await expect(listAllCollectionPages(async (options) => options.after
      ? page([{ id: "duplicate" }], false)
      : page([{ id: "duplicate" }], true, "next")))
      .rejects.toThrow("duplicate or invalid collection identities");

    await expect(listAllCollectionPages(async () => (page([], true))))
      .rejects.toThrow("invalid collection pagination cursor");

    await expect(listAllCollectionPages(async (options) => page([{ id: options.after ? "second" : "first" }], true, "same-cursor"))).rejects.toThrow("invalid collection pagination cursor");
  });

  it("stops before another page read when cancellation is requested", async () => {
    const controller = new AbortController();
    let calls = 0;

    await expect(listAllCollectionPages(async () => {
      calls += 1;
      controller.abort();
      return page([{ id: "first" }], true, "first");
    }, controller.signal)).rejects.toMatchObject({ name: "AbortError" });

    expect(calls).toBe(1);
  });

  it("accepts a complete hundred-page collection and rejects a required page 101", async () => {
    let completeCalls = 0;
    const complete = await listAllCollectionPages(async () => {
      completeCalls += 1;
      return page([{ id: `complete-${completeCalls}` }], completeCalls < 100, `complete-${completeCalls}`);
    });

    expect(complete).toHaveLength(100);
    expect(completeCalls).toBe(100);

    let incompleteCalls = 0;
    await expect(listAllCollectionPages(async () => {
      incompleteCalls += 1;
      return page([{ id: `incomplete-${incompleteCalls}` }], true, `incomplete-${incompleteCalls}`);
    })).rejects.toThrow("pagination exceeded the Web safety limit");
    expect(incompleteCalls).toBe(100);
  });

  it("rereads an invalidated collection until one complete revision is stable", async () => {
    let revision = 0;
    let reads = 0;
    const result = await listStableCollectionPages(async () => {
      reads += 1;
      if (reads === 1) revision += 1;
      return page([{ id: `snapshot-${reads}` }], false);
    }, () => revision);

    expect(result).toEqual([{ id: "snapshot-2" }]);
    expect(reads).toBe(2);
  });

  it("returns no publishable snapshot after every allowed read is invalidated", async () => {
    let revision = 0;
    let reads = 0;
    const result = await listStableCollectionPages(async () => {
      reads += 1;
      revision += 1;
      return page([{ id: `unstable-${reads}` }], false);
    }, () => revision);

    expect(result).toBeNull();
    expect(reads).toBe(3);
  });
});

describe("load-more collection pages", () => {
  it("appends a page and returns the next cursor until Core reports the end", () => {
    const first = appendCollectionPage([], page([{ id: "skill_3" }, { id: "skill_2" }], true, "skill_2"));
    expect(first).toEqual({ values: [{ id: "skill_3" }, { id: "skill_2" }], nextAfter: "skill_2" });
    const second = appendCollectionPage(first.values, page([{ id: "skill_1" }], false), "skill_2");
    expect(second).toEqual({ values: [{ id: "skill_3" }, { id: "skill_2" }, { id: "skill_1" }], nextAfter: null });
  });

  it("falls back to the last entry and rejects repeated identities or cursors that do not advance", () => {
    expect(appendCollectionPage([], page([{ id: "a" }], true)).nextAfter).toBe("a");
    expect(() => appendCollectionPage([{ id: "a" }], page([{ id: "a" }], false)))
      .toThrow("duplicate or invalid collection identities");
    expect(() => appendCollectionPage([], page([], true))).toThrow("invalid collection pagination cursor");
    expect(() => appendCollectionPage([{ id: "a" }], page([{ id: "b" }], true, "a"), "a"))
      .toThrow("invalid collection pagination cursor");
  });
});
