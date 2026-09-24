import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";

import type { AgentTurn, SessionItem } from "@agents-core-web/agents-client";

import { SessionTurnsTable } from "./SessionTurnsTable";

function turn(id: string, overrides: Partial<AgentTurn> = {}): AgentTurn {
  return { id, agent_id: "agent", session_id: "session", object: "agent.session.turn", status: "completed", created_at: 1, started_at: 10, completed_at: 14, error: null, usage: null, ...overrides };
}

const item = (id: string, turnId: string) => ({ id, turn_id: turnId, type: "message", status: "completed", role: "user", content: [] }) as SessionItem;

describe("Session Turns table", () => {
  it("lists Turns in order with timing, Item counts and usage, missing usage as —", () => {
    const html = renderToStaticMarkup(
      <SessionTurnsTable
        turns={[
          turn("turn-one", { usage: { input_tokens: 1200, output_tokens: 300, total_tokens: 1500, input_tokens_details: { cached_tokens: 0 }, output_tokens_details: { reasoning_tokens: 0 } } }),
          turn("turn-two", { status: "failed", error: { code: "internal_error", message: "The execution could not complete." } }),
        ]}
        items={[item("a", "turn-one"), item("b", "turn-one"), item("c", "elsewhere")]}
      />,
    );
    expect(html).toContain("Turn 1");
    expect(html).toContain("Turn 2");
    expect(html).toContain("4.0 s");
    expect(html).toContain("1,500");
    expect(html).toContain('title="2 linked Items"');
    expect(html).toContain("The execution could not complete.");
    // The failed Turn reported no usage: its token cells stay missing, never 0.
    expect(html.match(/<td class="numeric">—<\/td>/g)).toHaveLength(3);
    expect(html).toContain("1 Item is not associated with an observed Turn yet.");
  });

  it("says so when no Turn was reported", () => {
    expect(renderToStaticMarkup(<SessionTurnsTable turns={[]} items={[]} />)).toContain("No Turns yet");
  });
});
