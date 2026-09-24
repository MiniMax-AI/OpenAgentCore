import { describe, expect, it } from "vitest";

import { buildWorkbenchRequest, emptyWorkbenchForm, workbenchCurl, WORKBENCH_METADATA } from "./workbench-requests";

const KEY = "3f1c9a52-8a8e-4a57-9d51-0f2c7c9a1b11";

describe("API workbench requests", () => {
  it("tags created Agents and reports a missing model", () => {
    const form = emptyWorkbenchForm("gpt-test");
    const request = buildWorkbenchRequest("agent.create", form, KEY);
    expect(request).toMatchObject({ method: "POST", path: "/agents", beta: true, bodyFile: "agent.json", problem: null });
    expect(request.body).toMatchObject({ model: "gpt-test", metadata: WORKBENCH_METADATA });
    expect(request.idempotencyKey).toBeUndefined();
    expect(buildWorkbenchRequest("agent.create", { ...form, agent: { ...form.agent, model: " " } }, KEY).problem).toBe("model");
  });

  it("requires a first input for a Session without an environment", () => {
    const form = emptyWorkbenchForm("gpt-test");
    const withAgent = { ...form, session: { ...form.session, agentId: "agent_1" } };
    expect(buildWorkbenchRequest("session.create", form, KEY).problem).toBe("agent");
    expect(buildWorkbenchRequest("session.create", { ...withAgent, session: { ...withAgent.session, input: "" } }, KEY).problem).toBe("input");
    const hosted = buildWorkbenchRequest("session.create", { ...withAgent, session: { ...withAgent.session, environment: "openai_hosted", templateId: "envtpl_1", input: "" } }, KEY);
    expect(hosted.problem).toBeNull();
    expect(hosted.body).toMatchObject({ agent_id: "agent_1", environment: { type: "openai_hosted", environment_template_id: "envtpl_1" }, metadata: WORKBENCH_METADATA });
    expect(hosted.idempotencyKey).toBe(KEY);
  });

  it("sends a message as one Session input event", () => {
    const form = emptyWorkbenchForm("gpt-test");
    const request = buildWorkbenchRequest("session.message", { ...form, message: { sessionId: "sess/1", text: "Hi" } }, KEY);
    expect(request.path).toBe("/agents/sessions/sess%2F1/events");
    expect(request.body).toEqual({ events: [{ type: "agent.session.input.message", input: [{ role: "user", content: [{ type: "input_text", text: "Hi" }] }] }] });
    expect(buildWorkbenchRequest("session.message", form, KEY)).toMatchObject({ path: "/agents/sessions/{session_id}/events", problem: "session" });
  });

  it("looks objects up by ID, without the Beta header for files", () => {
    const form = emptyWorkbenchForm("gpt-test");
    expect(buildWorkbenchRequest("object.retrieve", { ...form, lookup: { kind: "vault", id: "vlt_1" } }, KEY)).toMatchObject({ method: "GET", path: "/vaults/vlt_1", beta: true, problem: null });
    expect(buildWorkbenchRequest("object.retrieve", { ...form, lookup: { kind: "file", id: "file_1" } }, KEY)).toMatchObject({ path: "/files/file_1", beta: false });
    expect(buildWorkbenchRequest("object.retrieve", form, KEY).problem).toBe("id");
  });

  it("renders curl with credential placeholders only", () => {
    const form = emptyWorkbenchForm("gpt-test");
    const session = buildWorkbenchRequest("session.create", { ...form, session: { ...form.session, agentId: "agent_1" } }, KEY);
    const proxied = workbenchCurl(session, "/v1");
    expect(proxied).toContain('curl --request POST "${AGENTS_CORE_BASE_URL}/agents/sessions"');
    expect(proxied).toContain('--header "Authorization: Bearer ${AGENTS_CORE_API_KEY}"');
    expect(proxied).toContain(`--header "Idempotency-Key: ${KEY}"`);
    expect(proxied).toContain("--data @session.json");
    expect(workbenchCurl(session, "https://core.example/v1/")).toContain("'https://core.example/v1/agents/sessions'");
    // A base URL with credentials never reaches the preview.
    expect(workbenchCurl(session, "https://user:secret@core.example/v1")).not.toContain("secret");
    const file = workbenchCurl(buildWorkbenchRequest("object.retrieve", { ...form, lookup: { kind: "file", id: "file_1" } }, KEY), "/v1");
    expect(file).not.toContain("OpenAI-Beta");
    expect(file).not.toContain("Content-Type");
  });
});
