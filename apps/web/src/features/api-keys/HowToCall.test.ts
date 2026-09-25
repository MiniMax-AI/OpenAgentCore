import { describe, expect, it } from "vitest";

import upstream from "../../../../../contracts/agents-api/upstream.json";
import { callSamples } from "./HowToCall";

describe("how-to-call samples", () => {
  it("install the SDK release Core's contract is pinned to", () => {
    expect(callSamples("https://core.example/v1", "key").python).toContain(`pip install openai==${upstream.sdk_version}\n`);
  });
});
