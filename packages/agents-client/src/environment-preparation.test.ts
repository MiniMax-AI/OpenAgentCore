import { expect, it } from "vitest";
import type { CreateSessionInput, EnvironmentPreparationInput } from "./types";
import { OpenAIAgentsClient } from "./client";

it("sends the complete shared preparation input unchanged", async () => {
  const preparation: EnvironmentPreparationInput = {
    environment_template_id: "template",
    env: { EXPLICIT: "value" },
    files: [{ type: "inline", path: "/workspace/input", data: "eA==" }],
    packages: { npm: ["is-number@7.0.0"], python: null },
    setup_commands: [{ command: "printf ready", cwd: "/workspace" }],
    skills: [{ type: "skill_reference", skill_id: "skill", version: null }],
    plugins: [{ type: "inline", name: "plugin", description: "proof", source: { type: "base64", media_type: "application/zip", data: "eA==" } }],
    capability_directories: ["/home/user/capabilities"],
  };
  for (const environment of [{ type: "self_hosted", workspace_directory: "/home/user/work" }, { type: "openai_hosted" }] as const) {
    let submitted: unknown;
    const client = new OpenAIAgentsClient({ fetch: async (_url, options) => {
      submitted = JSON.parse(String(options?.body));
      return new Response(JSON.stringify({ error: { message: "test admission", type: "invalid_request_error", code: "invalid_request_error", param: null } }), { status: 400, headers: { "Content-Type": "application/json" } });
    } });
    const input: CreateSessionInput = { agent_id: "agent", environment, x_agents_core: { environment: preparation } };
    await expect(client.createSession(input)).rejects.toThrow();
    expect(submitted).toEqual(input);
  }
});
