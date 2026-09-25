import { describe, expect, it } from "vitest";

import { node } from "../overview/test-fixtures";
import { nodeLogCommand } from "./enrollment-command";
import { enrolledNode, enrollmentProgress, formatCountdown, NODE_READY_WAIT_MS, progressSteps } from "./node-enrollment";

describe("node enrollment", () => {
  it("counts down to the expiry, reading 0:00 only once expired", () => {
    expect(formatCountdown(600_000)).toBe("10:00");
    expect(formatCountdown(59_001)).toBe("1:00");
    expect(formatCountdown(1)).toBe("0:01");
    expect(formatCountdown(-5)).toBe("0:00");
    expect(formatCountdown(3_723_000)).toBe("1:02:03");
  });

  it("follows the newest new node from registered to ready, and reports it once the installer's wait has passed", () => {
    const known = new Set(["old"]);
    const fresh = node("new", { online: false, provider_ready: false, created_at: "2026-09-25T00:00:02Z" });
    expect(enrolledNode([node("old"), node("earlier", { created_at: "2026-09-25T00:00:01Z" }), fresh], known)?.id).toBe("new");
    expect(enrollmentProgress(null, undefined, 0)).toEqual({ stage: "waiting", problem: "" });
    expect(enrollmentProgress(fresh, 0, NODE_READY_WAIT_MS - 1)).toEqual({ stage: "registered", problem: "" });
    expect(enrollmentProgress(fresh, 0, NODE_READY_WAIT_MS)).toEqual({ stage: "registered", problem: "not_connected" });
    const connected = { ...fresh, online: true };
    expect(enrollmentProgress(connected, 0, 1_000)).toEqual({ stage: "connected", problem: "" });
    expect(enrollmentProgress(connected, 0, NODE_READY_WAIT_MS)).toEqual({ stage: "connected", problem: "provider_unavailable" });
    expect(enrollmentProgress({ ...connected, diagnostic: "kvm_unavailable" }, 0, 1_000)).toEqual({ stage: "connected", problem: "kvm_unavailable" });
    expect(enrollmentProgress({ ...connected, provider_ready: true }, 0, NODE_READY_WAIT_MS * 2)).toEqual({ stage: "ready", problem: "" });
  });

  it("marks the passed steps done and waits on the next one", () => {
    expect(progressSteps("waiting")).toEqual(["current", "future", "future"]);
    expect(progressSteps("registered")).toEqual(["done", "current", "future"]);
    expect(progressSteps("connected")).toEqual(["done", "done", "current"]);
  });

  it("points at the installer's systemd user unit", () => {
    expect(nodeLogCommand("7f3c2a90-fixture")).toBe("journalctl --user -u parsar-node-7f3c2a90-fixture.service");
    expect(nodeLogCommand("a b")).toBe("journalctl --user -u 'parsar-node-a b.service'");
  });
});
