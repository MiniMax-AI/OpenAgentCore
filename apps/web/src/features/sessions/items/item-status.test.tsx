import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import type { SessionItem } from "@agents-core-web/agents-client";
import { ThreadItems } from "./ItemRenderers";

describe("nullable protocol Item status", () => {
  it("renders an open trace containing a statusless message and unknown reasoning status", () => {
    const items: SessionItem[] = [
      { id: "command", turn_id: "turn", type: "command_execution", status: "in_progress", command: "pwd" },
      { id: "message", turn_id: "turn", type: "agent_message", sender_agent_id: "child", recipient_agent_id: "root", content: [] },
      { id: "reasoning", turn_id: "turn", type: "reasoning", status: null, summary: [] },
    ];
    const html = renderToStaticMarkup(<ThreadItems items={items} agentName="Agent" />);
    expect(html).toContain('data-trace-step="message"');
    expect(html).toContain('data-trace-step="reasoning"');
    expect(html).not.toContain('title="null"');
    expect(html).not.toContain('title="undefined"');
  });
});
