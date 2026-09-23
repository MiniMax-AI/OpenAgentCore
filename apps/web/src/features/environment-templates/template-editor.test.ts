import { describe, expect, it, vi } from "vitest";
import type { EnvironmentTemplate } from "@agents-core-web/agents-client";
import { createTemplateWriteScope, templatePatch } from "./template-editor";

const template = {
  id: "template-id", name: "Original", network: { access: "enabled", allowed_domains: [] },
} as unknown as EnvironmentTemplate;

describe("Template updates", () => {
  it("omits unchanged fields and preserves network configuration on a rename", () => {
    expect(templatePatch(template, { name: " Renamed ", access: "enabled" })).toEqual({ name: "Renamed" });
    expect(templatePatch(template, { name: "Original", access: "enabled" })).toEqual({});
    expect(templatePatch(template, { name: " ", access: "enabled" })).toEqual({ name: null });
  });

  it("only updates network access for an empty allowed-domain policy", () => {
    expect(templatePatch(template, { name: "Original", access: "disabled" })).toEqual({ network: { access: "disabled" } });
    const restricted = { ...template, network: { access: "enabled", allowed_domains: ["private.example"] } } as unknown as EnvironmentTemplate;
    expect(templatePatch(restricted, { name: "Renamed", access: "enabled" })).toEqual({ name: "Renamed" });
    expect(() => templatePatch(restricted, { name: "Original", access: "disabled" })).toThrow("allowed domains must be preserved");
  });

  it.each(["  Existing name  ", "   "])("preserves the untouched name %j", (name) => {
    const original = { ...template, name };
    expect(templatePatch(original, { name, access: "enabled" })).toEqual({});
    expect(templatePatch(original, { name, access: "disabled" })).toEqual({ network: { access: "disabled" } });
  });

  it("counts Unicode characters rather than UTF-16 code units", () => {
    expect(templatePatch(template, { name: "😀".repeat(256), access: "enabled" }).name).toHaveLength(512);
    expect(() => templatePatch(template, { name: "😀".repeat(257), access: "enabled" })).toThrow("256");
  });
});

describe("Template write lifecycle", () => {
  it("prevents duplicate writes and refreshes only after the receipt", async () => {
    let resolve!: () => void;
    const write = vi.fn(() => new Promise<void>((done) => { resolve = done; }));
    const refresh = vi.fn(async () => undefined);
    const scope = createTemplateWriteScope();
    const result = scope.run(write, refresh);
    await expect(scope.run(write, refresh)).resolves.toEqual({ kind: "ignored" });
    expect(write).toHaveBeenCalledOnce();
    expect(refresh).not.toHaveBeenCalled();
    resolve();
    await expect(result).resolves.toEqual({ kind: "saved" });
    expect(refresh).toHaveBeenCalledOnce();
  });

  it("aborts on disposal and never refreshes after a late mutation receipt", async () => {
    let resolve!: () => void;
    let signal!: AbortSignal;
    const refresh = vi.fn(async () => undefined);
    const scope = createTemplateWriteScope();
    const result = scope.run((value) => { signal = value; return new Promise<void>((done) => { resolve = done; }); }, refresh);
    scope.dispose();
    expect(signal.aborted).toBe(true);
    resolve();
    await expect(result).resolves.toEqual({ kind: "ignored" });
    expect(refresh).not.toHaveBeenCalled();
  });

  it("discards a refresh result after unmount", async () => {
    let resolve!: () => void;
    const scope = createTemplateWriteScope();
    const refresh = vi.fn(() => new Promise<void>((done) => { resolve = done; }));
    const result = scope.run(async () => undefined, refresh);
    await Promise.resolve();
    expect(refresh).toHaveBeenCalledOnce();
    scope.dispose();
    resolve();
    await expect(result).resolves.toEqual({ kind: "ignored" });
  });

  it("separates a saved write from a failed refresh without replaying it", async () => {
    const write = vi.fn(async () => undefined);
    const scope = createTemplateWriteScope();
    await expect(scope.run(write, async () => { throw new Error("private"); })).resolves.toEqual({ kind: "saved-refresh-failed" });
    expect(write).toHaveBeenCalledOnce();
  });

  it("does not replay uncertain writes or expose raw failures", async () => {
    const write = vi.fn(async () => { throw new Error("secret-token-and-hostname"); });
    const refresh = vi.fn(async () => undefined);
    const result = await createTemplateWriteScope().run(write, refresh);
    expect(result.kind).toBe("failed");
    expect(JSON.stringify(result)).not.toContain("secret-token");
    expect(write).toHaveBeenCalledOnce();
    expect(refresh).not.toHaveBeenCalled();
  });
});
