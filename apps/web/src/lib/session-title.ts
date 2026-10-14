import type { AgentSession } from "@oac/agents-client";

/**
 * A Session's name wherever Sessions are listed: its metadata title, else its
 * Agent's name. Null means untitled; callers show `common:untitledSession`.
 */
export function sessionTitle(session: AgentSession | null): string | null {
  const title = session?.metadata?.title;
  if (typeof title === "string" && title.trim()) return title;
  return session?.agent?.name?.trim() ? session.agent.name : null;
}
