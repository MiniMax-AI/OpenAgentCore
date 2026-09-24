import { describe, expect, it } from "vitest";

import { hostedObservation, node } from "../overview/test-fixtures";
import { sandboxAttention } from "./sandbox-attention";

describe("sandbox attention", () => {
  it("lists offline and degraded nodes first, then cleanup and allocation diagnostics", () => {
    const items = sandboxAttention(
      [node("a", { cleanup_pending: 2 }), node("b", { online: false }), node("c", { provider_ready: false })],
      [{ id: "x", node_id: "a", diagnostic: "resource_missing" } as never],
      [],
    );
    expect(items.map((item) => item.kind)).toEqual(["offline", "degraded", "cleanup", "allocation"]);
  });

  it("counts failed Runtime samples but not waiting or stopped Runtimes", () => {
    const unavailable = (id: string, reason: string) => hostedObservation(id, "p", { status: "unavailable", reason } as never);
    const items = sandboxAttention([], [], [
      unavailable("s1", "sample_timeout"),
      unavailable("s2", "allocation_pending"),
      unavailable("s3", "runtime_not_running"),
      hostedObservation("s4", "p"),
    ]);
    expect(items).toEqual([{ kind: "unobservable", tone: "warning", count: 1 }]);
  });
});
