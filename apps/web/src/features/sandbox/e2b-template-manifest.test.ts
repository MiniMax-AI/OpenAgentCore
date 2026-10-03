import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import { MAX_MANIFEST_BYTES, parseTemplateManifest, validEndpoint, validTemplate } from "./e2b-template-manifest";

const manifests = JSON.parse(readFileSync(new URL("../../../../../services/core/deploy/e2b/testdata/template-manifests.json", import.meta.url), "utf8")) as Array<{ name: string; value: unknown; valid: boolean }>;
const selectors = JSON.parse(readFileSync(new URL("../../../../../services/core/internal/sandbox/e2b/testdata/configuration-selectors.json", import.meta.url), "utf8")) as {
  endpoints: Array<{ name: string; api_url: string | null; domain: string | null; valid: boolean }>;
  templates: Array<{ name: string; value: string; valid: boolean }>;
};

describe("template build handoff", () => {
  for (const test of manifests) it(test.name, () => {
    const parse = () => parseTemplateManifest(JSON.stringify(test.value));
    if (test.valid) expect(parse()).toEqual(test.value);
    else expect(parse).toThrow();
  });
  it("rejects oversized, malformed and non-object files", () => {
    for (const text of [" ".repeat(MAX_MANIFEST_BYTES + 1), "{", "null", "[]", "42"]) {
      expect(() => parseTemplateManifest(text)).toThrow();
    }
  });
  for (const test of selectors.endpoints) it(`endpoint: ${test.name}`, () => {
    expect(validEndpoint(test.api_url ?? "", test.domain ?? "")).toBe(test.valid);
  });
  for (const test of selectors.templates) it(`template: ${test.name}`, () => {
    expect(validTemplate(test.value)).toBe(test.valid);
  });
});
