import { describe, expect, it } from "vitest";

import { appendCollectionPage, listAllCollectionPages, listStableCollectionPages } from "./collection-pagination";

describe("top-level collection pagination", () => {
  it("loads every page in stable descending order and forwards cancellation", async () => {
    const controller = new AbortController();
    const calls: Array<{ after?: string; signal?: AbortSignal }> = [];
    const result = await listAllCollectionPages(async (options) => {
      calls.push({ after: options.after, signal: options.signal });
      return options.after
        ? { data: [{ id: "agent-1" }], has_more: false }
        : { data: [{ id: "agent-3" }, { id: "agent-2" }], has_more: true, last_id: "agent-2" };
    }, controller.signal);

    expect(result.map((value) => value.id)).toEqual(["agent-3", "agent-2", "agent-1"]);
    expect(calls).toEqual([
      { after: undefined, signal: controller.signal },
      { after: "agent-2", signal: controller.signal },
    ]);
  });

  it("fails closed for duplicate identities, empty continuation pages, and cyclic cursors", async () => {
    await expect(listAllCollectionPages(async (options) => options.after
      ? { data: [{ id: "duplicate" }], has_more: false }
      : { data: [{ id: "duplicate" }], has_more: true, last_id: "next" }))
      .rejects.toThrow("duplicate or invalid collection identities");

    await expect(listAllCollectionPages(async () => ({ data: [], has_more: true })))
      .rejects.toThrow("invalid collection pagination cursor");

    await expect(listAllCollectionPages(async (options) => ({
      data: [{ id: options.after ? "second" : "first" }],
      has_more: true,
      last_id: "same-cursor",
    }))).rejects.toThrow("invalid collection pagination cursor");
  });

  it("stops before another page read when cancellation is requested", async () => {
    const controller = new AbortController();
    let calls = 0;

    await expect(listAllCollectionPages(async () => {
      calls += 1;
      controller.abort();
      return { data: [{ id: "first" }], has_more: true, last_id: "first" };
    }, controller.signal)).rejects.toMatchObject({ name: "AbortError" });

    expect(calls).toBe(1);
  });

  it("accepts a complete hundred-page collection and rejects a required page 101", async () => {
    let completeCalls = 0;
    const complete = await listAllCollectionPages(async () => {
      completeCalls += 1;
      return {
        data: [{ id: `complete-${completeCalls}` }],
        has_more: completeCalls < 100,
        last_id: `complete-${completeCalls}`,
      };
    });

    expect(complete).toHaveLength(100);
    expect(completeCalls).toBe(100);

    let incompleteCalls = 0;
    await expect(listAllCollectionPages(async () => {
      incompleteCalls += 1;
      return {
        data: [{ id: `incomplete-${incompleteCalls}` }],
        has_more: true,
        last_id: `incomplete-${incompleteCalls}`,
      };
    })).rejects.toThrow("pagination exceeded the Web safety limit");
    expect(incompleteCalls).toBe(100);
  });

  it("rereads an invalidated collection until one complete revision is stable", async () => {
    let revision = 0;
    let reads = 0;
    const result = await listStableCollectionPages(async () => {
      reads += 1;
      if (reads === 1) revision += 1;
      return { data: [{ id: `snapshot-${reads}` }], has_more: false };
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
      return { data: [{ id: `unstable-${reads}` }], has_more: false };
    }, () => revision);

    expect(result).toBeNull();
    expect(reads).toBe(3);
  });
});

describe("load-more collection pages", () => {
  it("appends a page and returns the next cursor until Core reports the end", () => {
    const first = appendCollectionPage([], { data: [{ id: "skill_3" }, { id: "skill_2" }], has_more: true, last_id: "skill_2" });
    expect(first).toEqual({ values: [{ id: "skill_3" }, { id: "skill_2" }], nextAfter: "skill_2" });
    const second = appendCollectionPage(first.values, { data: [{ id: "skill_1" }], has_more: false }, "skill_2");
    expect(second).toEqual({ values: [{ id: "skill_3" }, { id: "skill_2" }, { id: "skill_1" }], nextAfter: null });
  });

  it("falls back to the last entry and rejects repeated identities or cursors that do not advance", () => {
    expect(appendCollectionPage([], { data: [{ id: "a" }], has_more: true }).nextAfter).toBe("a");
    expect(() => appendCollectionPage([{ id: "a" }], { data: [{ id: "a" }], has_more: false }))
      .toThrow("duplicate or invalid collection identities");
    expect(() => appendCollectionPage([], { data: [], has_more: true })).toThrow("invalid collection pagination cursor");
    expect(() => appendCollectionPage([{ id: "a" }], { data: [{ id: "b" }], has_more: true, last_id: "a" }, "a"))
      .toThrow("invalid collection pagination cursor");
  });
});
