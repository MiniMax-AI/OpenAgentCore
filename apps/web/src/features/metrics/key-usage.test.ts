import { describe, expect, it } from "vitest";

import { summary } from "../overview/test-fixtures";
import { keyUsageRows } from "./key-usage";

const sessions = (total: number) => ({ total, idle: total, in_progress: 0, requires_action: 0, failed: 0 });
const usage = (total: number) => ({ input_tokens: total, input_tokens_details: { cached_tokens: 0 }, output_tokens: 0, output_tokens_details: { reasoning_tokens: 0 }, total_tokens: total });

describe("keyUsageRows", () => {
  it("ranks keys by tokens, keeps unreported usage below reported, lists unknown creators last and drops idle keys", () => {
    const rows = keyUsageRows([
      summary("p1", { key_id: null, assets: null, sessions: sessions(9), usage: usage(900) }),
      summary("p1", { key_id: "small", assets: null, sessions: sessions(2), usage: usage(10) }),
      summary("p2", { key_id: "big", assets: null, sessions: sessions(1), usage: usage(500) }),
      summary("p2", { key_id: "silent", assets: null, sessions: sessions(4), usage: null }),
      summary("p2", { key_id: "idle", assets: null, sessions: sessions(0) }),
    ]);
    expect(rows.map((row) => row.id)).toEqual(["p2:big", "p1:small", "p2:silent", "p1:unknown"]);
  });
});
