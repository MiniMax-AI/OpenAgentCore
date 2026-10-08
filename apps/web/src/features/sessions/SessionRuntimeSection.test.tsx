import type { AgentSession, RuntimeObservation } from "@oac/agents-client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderToStaticMarkup } from "react-dom/server";
import { afterEach, expect, it } from "vitest";

import i18n from "../../i18n";
import { SessionRuntimeSection } from "./SessionRuntimeSection";
import { sessionObservationQuery } from "./session-queries";

const session = { id: "session", status: "idle" } as AgentSession;
const unsupported: RuntimeObservation = {
  id: session.id, object: "agent.runtime_observation", session_id: session.id,
  environment_id: "environment", mode: "openai_hosted", provider_type: "docker",
  instance: { kind: "managed_allocation", allocation_id: "allocation", connection_generation: null },
  lifecycle_state: "active", status: "unsupported", reason: "native_metrics_not_supported",
  allocation_created_at: null, resolved_at: 30, observed_at: null, started_at: null, cpu: null, memory: null,
};

function render(observation: RuntimeObservation): string {
  const client = new QueryClient({ defaultOptions: { queries: { enabled: false, retry: false } } });
  client.setQueryData(sessionObservationQuery("project", session.id).queryKey, observation);
  const html = renderToStaticMarkup(<QueryClientProvider client={client}>
    <SessionRuntimeSection projectId="project" session={session} active={false} revision={0} refreshToken={0} />
  </QueryClientProvider>);
  client.clear();
  return html;
}

afterEach(async () => { await i18n.changeLanguage("en"); });

it.each(["en", "zh-CN"])("renders a declared unsupported reason without samples and retains known translations in %s", async (language) => {
  await i18n.changeLanguage(language);
  const html = render(unsupported);
  expect(html).toContain("native_metrics_not_supported");
  expect(html).not.toContain("runtime.reason.native_metrics_not_supported");
  const unavailable = render({ ...unsupported, status: "unavailable", reason: "sample_timeout" });
  expect(unavailable).toContain(i18n.t("runtime.reason.sample_timeout", { ns: "sessions" }));
  expect(unavailable).not.toContain("sample_timeout");
});
