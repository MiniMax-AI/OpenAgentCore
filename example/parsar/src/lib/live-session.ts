import { useEffect, useMemo, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import type { SessionEvent, SessionItem } from "@oac/agents-client";
import { firstTextArrived } from "./session-timing";
import { api, readHistory } from "./api";

// Only append deltas after observing the item's baseline on this connection.
// An event stream is live-only; durable reads recover anything missed on reconnect.
export function applyEvent(
  items: Map<string, SessionItem>,
  event: SessionEvent,
) {
  if (event.item && event.type.startsWith("agent.session.turn.item.")) {
    items.set(event.item.id, event.item);
    return;
  }
  if (!event.item_id) return;
  const item = items.get(event.item_id);
  if (!item) return;
  const index = event.content_index ?? 0;
  const content = [...(item.content || [])];
  if (
    event.type === "agent.session.turn.content_part.added" ||
    event.type === "agent.session.turn.content_part.done"
  ) {
    if (event.part) content[index] = event.part;
  } else if (event.type === "agent.session.turn.output_text.delta") {
    content[index] = {
      type: "output_text",
      text: (content[index]?.text || "") + (event.delta || ""),
    };
  } else if (event.type === "agent.session.turn.output_text.done") {
    content[index] = { type: "output_text", text: event.text || "" };
  } else return;
  items.set(item.id, { ...item, content });
}

export function useLiveSession(id: string, durable: SessionItem[] | undefined) {
  const cache = useQueryClient();
  const [live, setLive] = useState<Map<string, SessionItem>>(new Map());
  const [reconnecting, setReconnecting] = useState(false);
  useEffect(() => {
    const abort = new AbortController();
    let timer: ReturnType<typeof setTimeout>;
    const refresh = () =>
      void cache.invalidateQueries({ queryKey: ["session", id] });
    let recovering = false,
      dirty = false;
    const recoverMissingBaseline = async () => {
      dirty = true;
      if (recovering) return;
      recovering = true;
      try {
        // Without a cursor shared by Items and SSE, appending to a fetched prefix
        // could duplicate text. React to each new delta with an authoritative read.
        // Coalesce overlapping reads; new items still use direct delta rendering.
        while (dirty && !abort.signal.aborted) {
          dirty = false;
          const items = await readHistory(
            (options) => api.listItems(id, options),
            abort.signal,
          );
          cache.setQueryData<{ items: SessionItem[] }>(
            ["session", id],
            (previous) => (previous ? { ...previous, items } : previous),
          );
        }
      } catch {
        /* The next event or history poll retries recovery. */
      } finally {
        recovering = false;
      }
    };
    const connect = async () => {
      const items = new Map<string, SessionItem>();
      const seen = new Set<string>();
      try {
        await api.streamEvents(id, {
          signal: abort.signal,
          onOpen: () => {
            setReconnecting(false);
            setLive(new Map());
            refresh();
          },
          onEvent: (event) => {
            if (seen.has(event.event_id)) return;
            seen.add(event.event_id);
            if (
              event.type === "agent.session.turn.output_text.delta" &&
              event.delta?.length
            )
              firstTextArrived(id, event.turn_id);
            if (seen.size > 2000) seen.delete(seen.values().next().value!);
            if (
              event.type === "agent.session.turn.output_text.delta" &&
              event.item_id &&
              !items.has(event.item_id)
            )
              void recoverMissingBaseline();
            applyEvent(items, event);
            setLive(new Map(items));
            if (
              event.session ||
              event.type.endsWith(".completed") ||
              event.type.endsWith(".cancelled") ||
              event.type.endsWith(".failed")
            )
              refresh();
          },
        });
      } catch {
        /* Durable polling remains available while the stream reconnects. */
      }
      if (!abort.signal.aborted) {
        setReconnecting(true);
        refresh();
        timer = setTimeout(() => void connect(), 1500);
      }
    };
    void connect();
    return () => {
      abort.abort();
      clearTimeout(timer);
    };
  }, [cache, id]);
  const items = useMemo(() => {
    const merged = new Map((durable || []).map((item) => [item.id, item]));
    for (const [key, item] of live) {
      const saved = merged.get(key);
      if (!saved || saved.status === "in_progress") merged.set(key, item);
    }
    return [...merged.values()];
  }, [durable, live]);
  return { items, reconnecting };
}
