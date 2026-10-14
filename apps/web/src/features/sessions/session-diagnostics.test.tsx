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

function render(kind: "session" | "turn", data: SessionDiagnostics | TurnDiagnostics, error = false, truncate = false) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const key = kind === "session" ? sessionDiagnosticsQuery(scope.projectId, session).queryKey : turnDiagnosticsQuery(scope, turn).queryKey;
  client.setQueryData(key, data);
  if (error) client.getQueryCache().find({ queryKey: key })!.setState({ status: "error", error: new Error("PRIVATE_READ_SENTINEL") });
  const html = renderToStaticMarkup(<QueryClientProvider client={client}><DiagnosticScope.Provider value={scope}>
    {kind === "session" ? <SessionFailure projectId={scope.projectId} session={session} truncate={truncate} /> : <TurnFailure turn={turn} />}
  </DiagnosticScope.Provider></QueryClientProvider>);
  client.clear();
  return html;
}

describe("Core diagnostics in the console", () => {
  it("renders safe classified Session and root Turn failures in both languages", async () => {
    expect(render("session", sessionData)).toContain("Check the API key of the Session&#x27;s model provider.");
    expect(render("turn", turnData)).toContain("Check the API key of the Session&#x27;s model provider.");
    await i18n.changeLanguage("zh-CN");
    expect(render("session", sessionData)).toContain("模型提供商认证失败");
    expect(render("turn", turnData)).not.toContain("PRIVATE_NATIVE_SENTINEL");
  });

  it("withholds old causes after a failed read or mismatched status", () => {
    expect(render("session", sessionData, true)).toContain("Failure details unavailable");
    expect(render("turn", { ...turnData, status: "completed", failure: null })).not.toContain("rejected authentication");
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

  it("gives each classified failure a next step that holds for every Session kind", () => {
    const t = i18n.getFixedT("en", "diagnostics");
    const providerDefault = "Without a model provider set on the Session or its saved Agent, a hosted sandbox Session uses the default in Settings → Models and credentials.";
    expect(diagnosticFailure({ code: "invalid_request", params: {}, failed_at: null }, t)).toBe(`The model provider rejected the request. Check the model and parameters, starting with the model on the Session page. ${providerDefault}`);
    expect(diagnosticFailure({ code: "connection_failed", params: { http_status: 502 }, failed_at: null }, t)).toContain("HTTP 502. Check the Base URL of the Session's model provider.");
    expect(diagnosticFailure({ code: "runtime_unavailable", params: {}, failed_at: null }, t)).toBe("The Runtime is unavailable. Retry later. For a self-hosted Session, check Host connection on the Session page; for a hosted sandbox Session, check Settings → Sandbox and scheduling.");
    expect(diagnosticFailure({ code: "harness_error", params: {}, failed_at: null }, t)).toContain("Core does not show the Harness's own message");
    expect(diagnosticFailure({ code: "invalid_request", params: {}, failed_at: null }, i18n.getFixedT("zh-CN", "diagnostics"))).toBe("模型提供商拒绝了请求。请检查模型和参数，先看 Session 页显示的模型。Session 及其保存的 Agent 都未设置模型提供商时，托管沙箱 Session 使用“设置 → 模型与凭据”中的默认配置。");
    // Lists show the reason alone; the next step stays in the title.
    const row = render("session", { ...sessionData, failure: { ...sessionData.failure!, code: "invalid_request" } }, false, true);
    expect(row).toContain(">The model provider rejected the request.<");
    expect(row).toContain('title="The model provider rejected the request. Check the model and parameters');
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
