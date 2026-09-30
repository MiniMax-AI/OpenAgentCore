import type { AgentSession, AgentTurn, DiagnosticFailure, SessionDiagnostics, TurnDiagnostics } from "@oac/agents-client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderToStaticMarkup } from "react-dom/server";
import { afterEach, describe, expect, it } from "vitest";

import i18n from "../../i18n";
import { diagnosticFailure } from "../../lib/diagnostic-failure";
import { DiagnosticScope, SessionFailure, TurnFailure, sessionDiagnosticsQuery, turnDiagnosticsQuery } from "./session-diagnostics";
import { ItemReceiptTiming } from "./trace/ItemReceiptTiming";

const scope = { projectId: "project", sessionId: "session" };
const session = { id: "session", status: "failed", last_active_at: 20 } as AgentSession;
const turn = { id: "turn", session_id: "session", status: "failed", completed_at: 20, error: { code: "internal_error", message: "PRIVATE_NATIVE_SENTINEL" } } as AgentTurn;
const failure: DiagnosticFailure = { code: "authentication_error", params: {}, failed_at: null };
const sessionData: SessionDiagnostics = { object: "core.session_diagnostics", session_id: "session", status: "failed", failure: { ...failure, source: "turn", turn_id: "turn" } };
const turnData: TurnDiagnostics = { object: "core.turn_diagnostics", session_id: "session", turn_id: "turn", status: "failed", failure, items: [], items_truncated: false };

afterEach(() => { void i18n.changeLanguage("en"); });

function render(kind: "session" | "turn", data: SessionDiagnostics | TurnDiagnostics, error = false) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const key = kind === "session" ? sessionDiagnosticsQuery(scope.projectId, session).queryKey : turnDiagnosticsQuery(scope, turn).queryKey;
  client.setQueryData(key, data);
  if (error) client.getQueryCache().find({ queryKey: key })!.setState({ status: "error", error: new Error("PRIVATE_READ_SENTINEL") });
  const html = renderToStaticMarkup(<QueryClientProvider client={client}><DiagnosticScope.Provider value={scope}>
    {kind === "session" ? <SessionFailure projectId={scope.projectId} session={session} truncate={false} /> : <TurnFailure turn={turn} />}
  </DiagnosticScope.Provider></QueryClientProvider>);
  client.clear();
  return html;
}

describe("Core diagnostics in the console", () => {
  it("renders safe classified Session and root Turn failures in both languages", async () => {
    expect(render("session", sessionData)).toContain("Check its API key");
    expect(render("turn", turnData)).toContain("Check its API key");
    await i18n.changeLanguage("zh-CN");
    expect(render("session", sessionData)).toContain("模型服务认证失败");
    expect(render("turn", turnData)).not.toContain("PRIVATE_NATIVE_SENTINEL");
  });

  it("withholds old causes after a failed read or mismatched status", () => {
    expect(render("session", sessionData, true)).toContain("Failure details unavailable");
    expect(render("turn", { ...turnData, status: "completed", failure: null })).not.toContain("Check its API key");
    expect(render("turn", turnData, true)).not.toContain("PRIVATE_READ_SENTINEL");
  });

  it("does not read healthy Sessions, and separates projects, status changes and Turns", () => {
    expect(sessionDiagnosticsQuery("project", { ...session, status: "idle" }).enabled).toBe(false);
    expect(sessionDiagnosticsQuery("other", session).queryKey).not.toEqual(sessionDiagnosticsQuery("project", session).queryKey);
    expect(turnDiagnosticsQuery(scope, { ...turn, status: "in_progress" }).refetchInterval).toBe(5000);
    expect(turnDiagnosticsQuery(scope, turn).refetchInterval).toBe(false);
    expect(turnDiagnosticsQuery(scope, turn).retry).toBe(false);
  });

  it("renders only typed provisioning metadata and does not guess a timeout", () => {
    const t = i18n.getFixedT("en", "diagnostics");
    const text = diagnosticFailure({ code: "environment_provisioning_failed", params: { step: "setup", index: 0, exit_code: 7 }, failed_at: null }, t);
    expect(text).toContain("Setup command 1");
    expect(text).toContain("Exit code 7");
    expect(diagnosticFailure({ code: "harness_error", params: {}, failed_at: null }, t)).not.toContain("timed out");
    expect(diagnosticFailure({ code: "connection_failed", params: { http_status: 429 }, failed_at: null }, t)).toContain("HTTP 429");
  });

  it("keeps call/result receipt intervals distinct and historical settlement missing", () => {
    const client = new QueryClient();
    client.setQueryData(turnDiagnosticsQuery(scope, turn).queryKey, { ...turnData, items: [
      { item_id: "CALL", started_at: "2026-09-28T00:00:00Z", completed_at: null, observed_duration_ms: null },
      { item_id: "RESULT", started_at: "2026-09-28T00:00:02Z", completed_at: "2026-09-28T00:00:01Z", observed_duration_ms: -1000 },
    ], items_truncated: true });
    const html = renderToStaticMarkup(<QueryClientProvider client={client}><DiagnosticScope.Provider value={scope}>
      <ItemReceiptTiming turn={turn} items={[{ id: "call" }, { id: "result" }] as never} />
    </DiagnosticScope.Provider></QueryClientProvider>);
    expect(html).toContain("Core receipt times");
    expect(html).toContain("first 1,000 Items");
    expect(html).toContain("clock moved backwards");
    expect(html).not.toContain("0 ms");
    expect(html).toContain("—");
    client.clear();
  });
});
