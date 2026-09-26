import { describe, expect, it } from "vitest";

import upstream from "../../../../../contracts/agents-api/upstream.json";
import { callSamples } from "./HowToCall";

describe("how-to-call samples", () => {
  it("install the SDK release Core's contract is pinned to", () => {
    expect(callSamples("https://core.example/v1", "key").python).toContain(`pip install openai==${upstream.sdk_version}\n`);
  });

  it("create a Session in the official shape, and without a key export a quoted placeholder", () => {
    const samples = callSamples("https://core.example/v1", null, "<project API key>");
    expect(samples.shell).toBe('export OPENAI_BASE_URL=https://core.example/v1\nexport OPENAI_API_KEY="<project API key>"');
    const body = samples.curl.match(/-d '([^']*)'$/)?.[1] ?? "";
    expect(JSON.parse(body)).toEqual({ environment: { type: "openai_hosted" }, agent: { model: "<model>" }, input: "Say hello." });
    expect(samples.python).toContain("client.beta.agents.sessions.create(");
  });
});
