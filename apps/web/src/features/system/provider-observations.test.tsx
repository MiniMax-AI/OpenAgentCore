import type { HarnessModelConfiguration } from "@oac/agents-client";
import { renderToStaticMarkup } from "react-dom/server";
import { afterEach, describe, expect, it } from "vitest";
import i18n from "../../i18n";
import { activeProviderError, ProviderObservations } from "./ProviderObservations";

const provider: HarnessModelConfiguration = { object: "core.model_configuration", harness: "codex", model: "fixture-model", harness_config: {}, model_provider: { protocol: "responses", base_url: "https://model.example/v1", api_key_configured: true }, updated_at: "2026-09-28T08:00:00Z", last_used_at: null, last_error_at: null, last_error_code: null };
afterEach(() => { void i18n.changeLanguage("en"); });

describe("default provider observations", () => {
  it("keeps missing records unknown and only flags errors newer than success", () => {
    expect(activeProviderError(provider)).toBe(false);
    const error = { ...provider, last_error_code: "authentication_error" as const, last_error_at: "2026-09-28T08:01:00Z" };
    expect(activeProviderError(error)).toBe(true);
    expect(activeProviderError({ ...error, last_used_at: "2026-09-28T08:00:30Z" })).toBe(true);
    expect(activeProviderError({ ...error, last_used_at: error.last_error_at })).toBe(false);
    expect(activeProviderError({ ...error, last_used_at: "2026-09-28T08:02:00Z" })).toBe(false);
  });
  it("keeps low-frequency details out of the card and qualifies failed reads", async () => {
    const html = renderToStaticMarkup(<ProviderObservations provider={provider} name="Codex" stale={false} />);
    expect(html).toContain("Usage details");
    expect(html).not.toContain("Last successful use");
    expect(html).not.toContain("Recent error");
    await i18n.changeLanguage("zh-CN");
    const stale = renderToStaticMarkup(<ProviderObservations provider={{ ...provider, last_error_code: "server_error", last_error_at: provider.updated_at }} name="Codex" stale />);
    expect(stale).toContain("记录尚未确认");
    expect(stale).not.toContain("近期错误");
  });
});
