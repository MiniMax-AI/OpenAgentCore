import type { ListPage } from "@oac/agents-client";
import { describe, expect, it } from "vitest";

import { appendCollectionPage } from "./collection-pagination";

function page<T extends { id: string }>(data: T[], hasMore: boolean, lastId: string | null = null): ListPage<T> {
  return { object: "list", data, first_id: data[0]?.id ?? null, last_id: lastId, has_more: hasMore };
}

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
