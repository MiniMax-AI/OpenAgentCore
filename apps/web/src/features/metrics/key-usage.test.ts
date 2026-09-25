import { describe, expect, it } from "vitest";


import { summary } from "../overview/test-fixtures";
import { keyUsageRows } from "./key-usage";
import { type KeyRef } from "../../lib/admin-view";

const key = (id: string): KeyRef => ({ id, name: id, prefix: `pc_${id}`, kind: "issued", revoked_at: null });
const sessions = (total: number) => ({ total, idle: total, in_progress: 0, requires_action: 0, failed: 0 });
const usage = (total: number) => ({ input_tokens: total, output_tokens: 0, total_tokens: total, cached_tokens: 0, reasoning_tokens: 0 });

describe("keyUsageRows", () => {
  it("ranks keys by tokens, keeps unreported usage below reported, lists unknown creators last and drops idle keys", () => {
    const rows = keyUsageRows([
      summary("p1", { key: null, assets: null, sessions: sessions(9), usage: usage(900) }),
      summary("p1", { key: key("small"), assets: null, sessions: sessions(2), usage: usage(10) }),
      summary("p2", { key: key("big"), assets: null, sessions: sessions(1), usage: usage(500) }),
      summary("p2", { key: key("silent"), assets: null, sessions: sessions(4), usage: null }),
      summary("p2", { key: key("idle"), assets: null, sessions: sessions(0) }),
    ]);
    expect(rows.map((row) => row.id)).toEqual(["p2:big", "p1:small", "p2:silent", "p1:unknown"]);
  });
});
