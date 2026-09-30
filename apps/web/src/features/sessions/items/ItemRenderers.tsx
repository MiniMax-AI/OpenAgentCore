import { AlertTriangle, ChevronDown, ChevronRight, Search, TerminalSquare, Wrench } from "lucide-react";
import { useEffect, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";

import type { SessionItem } from "@oac/agents-client";

import { Monogram } from "../../../components/atoms/EntityChip";
import { Shimmer } from "../../../components/atoms/Shimmer";
import { MessageMarkdown } from "../../../components/MessageMarkdown";
import { StatusIcon, type StatusKind } from "../../../components/StatusIcon";
import { ApplyPatchDiffViewer } from "./ApplyPatchDiffViewer";
import { parseParsarApplyPatch } from "./apply-patch";

function textOf(item: SessionItem): string {
  return (item.content ?? []).map((content) => content.text).filter((value): value is string => Boolean(value)).join("\n");
}

function pretty(value: unknown): string {
  if (typeof value === "string") return value;
  if (value === undefined) return "";
  try { return JSON.stringify(value, null, 2); } catch { return String(value); }
}

const TRACE_COLLAPSE_MS = 220;

function TraceCollapse({ open, children }: { open: boolean; children: ReactNode }) {
  const [mounted, setMounted] = useState(open);
  if (open && !mounted) setMounted(true);
  useEffect(() => {
    if (open || !mounted) return;
    const timer = window.setTimeout(() => setMounted(false), TRACE_COLLAPSE_MS);
    return () => window.clearTimeout(timer);
  }, [mounted, open]);
  return <div className={`trace-collapse ${open ? "open" : ""}`} aria-hidden={!open}><div className="trace-collapse-clip">{mounted ? <div className="trace-collapse-body">{children}</div> : null}</div></div>;
}

function itemStatusKind(status: SessionItem["status"]): StatusKind {
  if (status === "in_progress") return "running";
  if (status === "failed") return "failed";
  if (status === "incomplete") return "interrupted";
  return "completed";
}

function formatDuration(milliseconds: number): string {
  const seconds = Math.floor(Math.max(0, milliseconds) / 1_000);
  if (seconds < 1) return "";
  const minutes = Math.floor(seconds / 60);
  const remainder = seconds % 60;
  return minutes ? `${minutes}m ${String(remainder).padStart(2, "0")}s` : `${seconds}s`;
}

function firstString(value: unknown): string {
  if (!value || typeof value !== "object") return "";
  for (const entry of Object.values(value)) if (typeof entry === "string" && entry.trim()) return entry.trim();
  return "";
}

const supportedWorkItemTypes = new Set<SessionItem["type"]>([
  "command_execution",
  "web_search_call",
  "function_call_output",
  "mcp_call",
  "function_call",
]);

function isSupportedWorkItem(item: SessionItem): boolean {
  return supportedWorkItemTypes.has(item.type);
}

function toolPresentation(item: SessionItem, t: (key: string) => string) {
  if (item.type === "command_execution") return { Icon: TerminalSquare, verb: t("items.verb.run"), target: item.command || t("items.command") };
  if (item.type === "web_search_call") {
    const action = item.action;
    return { Icon: Search, verb: t("items.verb.search"), target: action?.query || action?.queries?.join(", ") || action?.url || action?.pattern || t("items.webSearch") };
  }
  if (item.type === "function_call_output") return { Icon: Wrench, verb: t("items.verb.return"), target: item.name || item.call_id || t("items.functionResult") };
  if (item.type === "mcp_call") return { Icon: Wrench, verb: t("items.verb.use"), target: [item.server_label, item.name, firstString(item.arguments)].filter(Boolean).join(" ") || t("items.mcpTool") };
  if (item.type === "function_call") return { Icon: Wrench, verb: t("items.verb.use"), target: [item.name, firstString(item.arguments)].filter(Boolean).join(" ") || t("items.function") };
  return { Icon: AlertTriangle, verb: t("items.verb.unsupported"), target: `${item.type || t("items.unknown")} ${t("items.item")}` };
}

function toolArguments(item: SessionItem): unknown {
  if (item.type === "command_execution") return item.cwd ? { command: item.command, cwd: item.cwd } : { command: item.command };
  if (item.type === "web_search_call") return item.action;
  if (item.type === "function_call" || item.type === "mcp_call") return item.arguments;
  return undefined;
}

export function toolResult(item: SessionItem): unknown {
  const result: Record<string, unknown> = {};
  if (item.output !== undefined) result.output = item.output;
  if (item.error !== undefined && item.error !== null) result.error = item.error;
  if (item.exit_code !== undefined && item.exit_code !== null) result.exit_code = item.exit_code;
  if (item.duration_ms !== undefined && item.duration_ms !== null) result.duration_ms = item.duration_ms;
  const entries = Object.entries(result);
  if (!entries.length) return undefined;
  return entries.length === 1 && entries[0]?.[0] === "output" ? entries[0][1] : result;
}

function WorkStep({ item }: { item: SessionItem }) {
  const { t } = useTranslation("sessions");
  const [open, setOpen] = useState(false);
  const { Icon, verb, target } = toolPresentation(item, t as (key: string) => string);
  const supported = isSupportedWorkItem(item);
  const args = supported ? toolArguments(item) : undefined;
  const result = supported && (item.status !== "in_progress" || item.type === "command_execution")
    ? toolResult(item)
    : undefined;
  const patch = supported && item.type === "function_call" && item.name === "apply_patch" ? parseParsarApplyPatch(item.arguments) : null;
  const expandable = args !== undefined && args !== null || result !== undefined && result !== null;
  const row = <><Icon className="trace-step-icon" size={14} strokeWidth={1.5} aria-hidden="true" /><span className="trace-step-verb">{verb}</span><span className="trace-step-target" title={target}>{target}</span>{item.status != null && item.status !== "completed" ? <StatusIcon status={itemStatusKind(item.status)} title={t(`status.${item.status}` as never)} /> : null}{item.duration_ms ? <span className="trace-duration">{formatDuration(item.duration_ms)}</span> : null}{expandable ? <ChevronRight className={`trace-step-chevron ${open ? "open" : ""}`} size={14} strokeWidth={1.5} aria-hidden="true" /> : null}</>;
  return <li className="trace-step" data-trace-step={item.id}>{expandable ? <button className="trace-step-row" type="button" aria-expanded={open} onClick={() => setOpen((value) => !value)}>{row}</button> : <div className="trace-step-row">{row}</div>}{expandable ? <TraceCollapse open={open}>{patch ? <ApplyPatchDiffViewer item={item} patch={patch} result={result} /> : <div className="trace-step-details">{args !== undefined && args !== null ? <div><p>{t("items.arguments")}</p><pre>{pretty(args)}</pre></div> : null}{result !== undefined && result !== null ? <div><p>{t("items.result")}</p><pre>{pretty(result)}</pre></div> : null}</div>}</TraceCollapse> : null}</li>;
}

export function mergeFunctionSteps(items: SessionItem[]): SessionItem[] {
  const merged: SessionItem[] = [];
  const calls = new Map<string, number>();
  for (const item of items) {
    if (item.type === "function_call" && item.call_id) { calls.set(item.call_id, merged.length); merged.push(item); continue; }
    if (item.type === "function_call_output" && item.call_id) {
      const index = calls.get(item.call_id);
      const call = index === undefined ? undefined : merged[index];
      if (index !== undefined && call) { merged[index] = { ...call, status: item.status, output: item.output, error: item.error, duration_ms: item.duration_ms ?? call.duration_ms }; continue; }
    }
    merged.push(item);
  }
  return merged;
}

function WorkTrace({ items }: { items: SessionItem[] }) {
  const { t } = useTranslation("sessions");
  const steps = mergeFunctionSteps(items);
  const status: StatusKind = steps.some((item) => item.status === "failed") ? "failed" : steps.some((item) => item.status === "in_progress") ? "running" : steps.some((item) => item.status === "incomplete") ? "interrupted" : "completed";
  const running = status === "running";
  const [expanded, setExpanded] = useState(running);
  const duration = steps.reduce((total, item) => total + (item.duration_ms ?? 0), 0);
  const current = running ? [...steps].reverse().find((item) => item.status === "in_progress") : undefined;
  useEffect(() => setExpanded(running), [running]);
  return <section className="work-trace" aria-label={t("items.agentWorkTrace")} aria-busy={running} data-work-trace={status}><button className="trace-header" type="button" aria-expanded={expanded} onClick={() => setExpanded((value) => !value)}><StatusIcon status={status} /><span>{t("history.steps", { n: steps.length })}</span>{status !== "completed" ? <span>· {t(`status.${status}` as never)}</span> : null}{duration ? <span className="trace-duration" aria-hidden={running || undefined}>· {formatDuration(duration)}</span> : null}<ChevronDown className={`trace-header-chevron ${expanded ? "" : "closed"}`} size={14} strokeWidth={1.5} aria-hidden="true" /></button><TraceCollapse open={expanded}><ul className="trace-steps">{steps.map((item) => <WorkStep item={item} key={item.id} />)}</ul></TraceCollapse>{!expanded && current ? <ul className="trace-steps" data-work-trace-tail=""><WorkStep item={current} key={`tail:${current.id}`} /></ul> : null}</section>;
}

type ChatBlock =
  | { kind: "user"; item: SessionItem }
  | { kind: "assistant"; items: SessionItem[] };

/** Consecutive Agent Items (tool steps, notes, the reply) form one block under one avatar. */
function chatBlocks(items: readonly SessionItem[]): ChatBlock[] {
  const blocks: ChatBlock[] = [];
  for (const item of items) {
    if (item.type === "message" && item.role !== "assistant") {
      blocks.push({ kind: "user", item });
      continue;
    }
    const last = blocks.at(-1);
    if (last?.kind === "assistant") last.items.push(item);
    else blocks.push({ kind: "assistant", items: [item] });
  }
  return blocks;
}

function AgentItems({ items }: { items: SessionItem[] }) {
  const { t } = useTranslation("sessions");
  const rendered = [];
  for (let index = 0; index < items.length;) {
    const item = items[index]; if (!item) break;
    if (item.type === "message") {
      rendered.push(
        <div className={item.phase === "commentary" ? "chat-note" : "chat-reply"} key={item.id}>
          {item.phase === "commentary" ? <span className="chat-note-label">{t("items.workingNote")}</span> : null}
          <MessageMarkdown content={textOf(item) || t("items.emptyMessage")} />
        </div>,
      );
      index += 1; continue;
    }
    const traceItems = [item]; let cursor = index + 1;
    while (cursor < items.length && items[cursor]?.type !== "message" && items[cursor]?.turn_id === item.turn_id) { const next = items[cursor]; if (next) traceItems.push(next); cursor += 1; }
    rendered.push(<WorkTrace items={traceItems} key={`trace:${item.turn_id}:${item.id}`} />);
    index = cursor;
  }
  return rendered;
}

/**
 * One Turn's Items as a conversation: the user's message is a bubble on the
 * right; everything the Agent did (tool steps, working notes, its reply) sits
 * on the left under its avatar, followed by the Turn's error when it failed.
 */
export function ThreadItems({ items, agentName, error, activity }: {
  items: SessionItem[];
  agentName: string;
  error?: string | null;
  /** What the Agent is doing now ("Working…"), shown as a shimmering line at the end. */
  activity?: string | null;
}) {
  const { t } = useTranslation("sessions");
  const name = agentName || t("common.agent");
  const blocks = chatBlocks(items);
  if (error || activity) {
    const last = blocks.at(-1);
    if (last?.kind !== "assistant") blocks.push({ kind: "assistant", items: [] });
  }
  return blocks.map((block, index) => {
    if (block.kind === "user") {
      return (
        <div className="chat-row user" key={block.item.id}>
          <div className="chat-bubble message-copy">{textOf(block.item) || t("items.emptyMessage")}</div>
        </div>
      );
    }
    const isLast = index === blocks.length - 1;
    return (
      <div className="chat-row assistant" key={`agent:${block.items[0]?.id ?? index}`}>
        <span className="chat-avatar" aria-hidden="true"><Monogram color="var(--accent)">{name.trim().charAt(0).toUpperCase()}</Monogram></span>
        <div className="chat-body">
          <span className="chat-author">{name}</span>
          <AgentItems items={block.items} />
          {isLast && error ? <p className="chat-error" role="status">{error}</p> : null}
          {isLast && activity && !error ? <p className="chat-working" role="status"><Shimmer>{activity}</Shimmer></p> : null}
        </div>
      </div>
    );
  });
}
