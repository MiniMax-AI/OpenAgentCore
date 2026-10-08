import { describe, expect, it, vi } from "vitest";

import { createCredentialAttempts } from "./orcarouter-attempts";

describe("connect attempts", () => {
  it("reports busy while an attempt is open and clears it when it settles", () => {
    const seen: Array<{ attempt: number; busy: boolean; hint: string | null }> = [];
    const attempts = createCredentialAttempts((view) => seen.push(view));
    const attempt = attempts.begin();
    expect(attempts.snapshot()).toEqual({ attempt: 1, busy: true, hint: null });
    attempts.settle(attempt, { ok: true });
    expect(attempts.snapshot()).toEqual({ attempt: 1, busy: false, hint: null });
    expect(seen.at(-1)).toEqual({ attempt: 1, busy: false, hint: null });
  });

  it("ignores a late answer from an older attempt", () => {
    const attempts = createCredentialAttempts();
    const first = attempts.begin();
    const second = attempts.begin();
    expect(attempts.settle(first, { ok: true })).toBe(false);
    expect(attempts.snapshot().busy).toBe(true);
    expect(attempts.settle(second, { ok: false, message: "code-expired" })).toBe(true);
    expect(attempts.snapshot()).toEqual({ attempt: 2, busy: false, hint: "code-expired" });
  });

  it("aborts the in-flight exchange when the operator switches or cancels", () => {
    const attempts = createCredentialAttempts();
    const attempt = attempts.begin();
    expect(attempt.signal.aborted).toBe(false);
    attempts.abandon();
    expect(attempt.signal.aborted).toBe(true);
    expect(attempts.snapshot()).toEqual({ attempt: 2, busy: false, hint: null });
  });

  it("clears busy and the hint synchronously on pagehide", () => {
    const attempts = createCredentialAttempts();
    const attempt = attempts.begin();
    attempts.settle(attempt, { ok: false, message: "unreachable" });
    expect(attempts.snapshot().hint).toBe("unreachable");
    const second = attempts.begin();
    attempts.pagehide();
    expect(attempts.snapshot()).toEqual({ attempt: 3, busy: false, hint: null });
    expect(second.signal.aborted).toBe(true);
  });

  it("starts a second login without a remount after pagehide", () => {
    const attempts = createCredentialAttempts();
    const first = attempts.begin();
    attempts.pagehide();
    const second = attempts.begin();
    expect(second.generation).toBeGreaterThan(first.generation);
    expect(second.signal.aborted).toBe(false);
    expect(attempts.settle(second, { ok: true })).toBe(true);
    expect(attempts.snapshot()).toEqual({ attempt: second.generation, busy: false, hint: null });
  });

  it("never lets a stale answer restore a busy flag", () => {
    const onChange = vi.fn();
    const attempts = createCredentialAttempts(onChange);
    const stale = attempts.begin();
    attempts.abandon();
    attempts.settle(stale, { ok: true });
    expect(onChange).toHaveBeenCalledTimes(2);
    expect(attempts.snapshot().busy).toBe(false);
  });
});
