import { describe, expect, it } from "vitest";

import type { SavedAgent } from "@agents-core-web/agents-client";

import {
  applySessionAgentOverrides,
  optionalInitialSessionInput,
  sessionStartDetailsFromAgent,
  validateInlineSessionAgent,
  validateSessionAgentOverrides,
  validateSessionAgentSubmission,
  validateSessionStartDetails,
} from "./session-start-draft";

function agent(overrides: Partial<SavedAgent> = {}): SavedAgent {
  return {
    id: "agent_1",
    object: "agent",
    model: "provider/model",
    name: "Builder",
    instructions: null,
    metadata: {},
    multi_agent: { enabled: false, max_concurrent_subagents: null },
    reasoning: {},
    service_tier: "auto",
    text: { format: { type: "text" }, verbosity: "medium" },
    tools: [],
    created_at: 1,
    updated_at: 1,
    ...overrides,
  };
}

describe("Session start details", () => {
  it("maps title into bounded metadata and omits unrelated Agent overrides", () => {
    const source = agent();
    const values = {
      ...sessionStartDetailsFromAgent(source),
      title: "  Release review  ",
      metadata: JSON.stringify({ team: "web" }),
    };

    expect(validateSessionStartDetails(values, "saved", source, null, "openai_hosted")).toEqual({
      effectiveAgent: source,
      request: {
        metadata: { team: "web", title: "Release review" },
        stream: false,
      },
    });
  });

  it.each(["openai_hosted", "self_hosted"])("keeps empty %s creation unstreamed", (environmentType) => {
    const source = agent();
    expect(validateSessionStartDetails(sessionStartDetailsFromAgent(source), "saved", source, null, environmentType).request)
      .toEqual({ metadata: {}, stream: false });
  });

  it("enforces the Session metadata limit including title", () => {
    const source = agent();
    const metadata = Object.fromEntries(
      Array.from({ length: 16 }, (_, index) => [`key-${index}`, `value-${index}`]),
    );
    const result = validateSessionStartDetails({
      ...sessionStartDetailsFromAgent(source),
      title: "One pair too many",
      metadata: JSON.stringify(metadata),
    }, "saved", source);

    expect(result.request).toBeUndefined();
    expect(result.metadataError).toContain("at most 16 pairs");
  });

  it("requires nonblank none input, preserves exact text, and streams initial input", () => {
    const source = agent();
    const blank = validateSessionStartDetails({
      ...sessionStartDetailsFromAgent(source),
      initialInput: { mode: "text", text: " \t\n\u0085 ", messages: [] },
    }, "saved", source);
    const exact = "  Keep these edges.\n";
    const nonblank = validateSessionStartDetails({
      ...sessionStartDetailsFromAgent(source),
      initialInput: { mode: "text", text: exact, messages: [] },
    }, "saved", source);

    expect(optionalInitialSessionInput("\u0085")).toBeUndefined();
    expect(blank.request).toBeUndefined();
    expect(blank.inputError).toContain("first message is required");
    expect(nonblank.request).toEqual({ metadata: {}, input: exact, stream: true });
  });

  it("preserves ordered user-message grouping and exact text", () => {
    const source = agent();
    const result = validateSessionStartDetails({
      ...sessionStartDetailsFromAgent(source),
      initialInput: {
        mode: "messages",
        text: "inactive",
        messages: [
          { id: "a", parts: [{ id: "a1", text: " first\n" }, { id: "a2", text: "second" }] },
          { id: "b", parts: [{ id: "b1", text: "third" }] },
        ],
      },
    }, "saved", source);

    expect(result.request?.input).toEqual([
      {
        type: "message",
        role: "user",
        content: [
          { type: "input_text", text: " first\n" },
          { type: "input_text", text: "second" },
        ],
      },
      {
        type: "message",
        role: "user",
        content: [{ type: "input_text", text: "third" }],
      },
    ]);
    expect(result.request?.stream).toBe(true);
  });

  it("creates a standalone inline Agent and rejects an empty model", () => {
    const values = {
      ...sessionStartDetailsFromAgent(),
      inlineModel: "  provider/inline  ",
      inlineInstructions: "  Work carefully.  ",
    };
    expect(validateInlineSessionAgent(values, null).agent).toEqual({
      model: "provider/inline",
      instructions: "Work carefully.",
    });
    expect(validateSessionStartDetails(values, "inline", undefined, null, "self_hosted").request).toEqual({
      agent: { model: "provider/inline", instructions: "Work carefully." },
      metadata: {},
      stream: false,
    });
    expect(validateInlineSessionAgent({
      ...values,
      inlineModel: " \u0085 ",
    }, null).overrideError).toContain("model ID");
  });

  it("projects every supported saved-Agent field as a whole-field override", () => {
    const source = agent({
      instructions: "Inherited",
      multi_agent: { enabled: true, max_concurrent_subagents: 7 },
      reasoning: { effort: "high", summary: "detailed" },
      service_tier: "priority",
      text: { format: { type: "json_schema", schema: { type: "object" } }, verbosity: "low" },
      tools: [{ type: "tool_search" }],
    });
    const values = {
      ...sessionStartDetailsFromAgent(source),
      overridesEnabled: true,
      overrideModelEnabled: true,
      overrideModel: " provider/other ",
      overrideInstructionsEnabled: true,
      overrideInstructions: "   ",
      overrideTextEnabled: true,
      overrideTextVerbosity: "high" as const,
      resetMultiAgent: true,
      resetReasoning: true,
      resetServiceTier: true,
      toolsMode: "clear" as const,
    };
    const result = validateSessionAgentOverrides(values, source);

    expect(result.overrideError).toBeUndefined();
    expect(result.agent).toEqual({
      model: "provider/other",
      instructions: null,
      text: { format: { type: "text" }, verbosity: "high" },
      multi_agent: null,
      reasoning: null,
      service_tier: null,
      tools: [],
    });
    expect(result.effectiveAgent).toMatchObject({
      model: "provider/other",
      instructions: null,
      multi_agent: { enabled: false, max_concurrent_subagents: null },
      reasoning: {},
      service_tier: "auto",
      text: { format: { type: "text" }, verbosity: "high" },
      tools: [],
    });
    expect(source).toMatchObject({
      model: "provider/model",
      instructions: "Inherited",
      service_tier: "priority",
      tools: [{ type: "tool_search" }],
    });
  });

  it("serializes executable replacement tools and rejects read-only saved variants", () => {
    const source = agent();
    const valid = validateSessionAgentOverrides({
      ...sessionStartDetailsFromAgent(source),
      overridesEnabled: true,
      toolsMode: "replace",
      overrideTools: [{
        kind: "function",
        name: "lookup",
        description: "Look up",
        parameters: "{\"type\":\"object\"}",
      }],
    }, source);
    expect(valid.agent?.tools).toEqual([{
      type: "function",
      name: "lookup",
      description: "Look up",
      parameters: { type: "object" },
      defer_loading: false,
    }]);

    const invalid = validateSessionAgentOverrides({
      ...sessionStartDetailsFromAgent(source),
      overridesEnabled: true,
      toolsMode: "replace",
      overrideTools: [{ kind: "read-only", value: { type: "web_search" }, label: "unsupported" }],
    }, source);
    expect(invalid.overrideError).toContain("read-only");
  });

  it("strictly revalidates inline identity and saved override profiles at the App boundary", () => {
    const source = agent();
    expect(validateSessionAgentSubmission(
      "inline",
      undefined,
      { model: "provider/inline" },
      [source],
      null,
    ).effectiveAgent?.model).toBe("provider/inline");
    expect(validateSessionAgentSubmission(
      "inline",
      source.id,
      { model: "provider/inline" },
      [source],
      null,
    ).error).toContain("must not send agent_id");
    expect(validateSessionAgentSubmission(
      "inline",
      undefined,
      { model: "provider/inline", name: "saved-only" },
      [source],
      null,
    ).error).toContain("outside");
    expect(validateSessionAgentSubmission(
      "saved",
      source.id,
      { reasoning: { effort: "high" } },
      [source],
      null,
    ).error).toContain("outside");

    const applied = applySessionAgentOverrides(source, { instructions: null, tools: [] });
    expect(applied.instructions).toBeNull();
    expect(applied.tools).toEqual([]);
  });
});
