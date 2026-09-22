import { AlertTriangle, ChevronDown, ChevronRight, Search, TerminalSquare, Wrench } from "lucide-react";
import { useEffect, useState, type ReactNode } from "react";

import type { SessionItem } from "@agents-core-web/agents-client";

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

function toolPresentation(item: SessionItem) {
  if (item.type === "command_execution") return { Icon: TerminalSquare, verb: "Run", target: item.command || "Command" };
  if (item.type === "web_search_call") {
    const action = item.action;
    return { Icon: Search, verb: "Search", target: action?.query || action?.queries?.join(", ") || action?.url || action?.pattern || "Web search" };
  }
  if (item.type === "function_call_output") return { Icon: Wrench, verb: "Return", target: item.name || item.call_id || "Function result" };
  if (item.type === "mcp_call") return { Icon: Wrench, verb: "Use", target: [item.server_label, item.name, firstString(item.arguments)].filter(Boolean).join(" ") || "MCP tool" };
  if (item.type === "function_call") return { Icon: Wrench, verb: "Use", target: [item.name, firstString(item.arguments)].filter(Boolean).join(" ") || "Function" };
  return { Icon: AlertTriangle, verb: "Unsupported", target: `${item.type || "unknown"} Item` };
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
  const [open, setOpen] = useState(false);
  const { Icon, verb, target } = toolPresentation(item);
  const supported = isSupportedWorkItem(item);
  const args = supported ? toolArguments(item) : undefined;
  const result = supported && (item.status !== "in_progress" || item.type === "command_execution")
    ? toolResult(item)
    : undefined;
  const patch = supported && item.type === "function_call" && item.name === "apply_patch" ? parseParsarApplyPatch(item.arguments) : null;
  const expandable = args !== undefined && args !== null || result !== undefined && result !== null;
  const row = <><Icon className="trace-step-icon" size={14} strokeWidth={1.5} aria-hidden="true" /><span className="trace-step-verb">{verb}</span><span className="trace-step-target" title={target}>{target}</span>{item.status != null && item.status !== "completed" ? <StatusIcon status={itemStatusKind(item.status)} title={item.status.replaceAll("_", " ")} /> : null}{item.duration_ms ? <span className="trace-duration">{formatDuration(item.duration_ms)}</span> : null}{expandable ? <ChevronRight className={`trace-step-chevron ${open ? "open" : ""}`} size={14} strokeWidth={1.5} aria-hidden="true" /> : null}</>;
  return <li className="trace-step" data-trace-step={item.id}>{expandable ? <button className="trace-step-row" type="button" aria-expanded={open} onClick={() => setOpen((value) => !value)}>{row}</button> : <div className="trace-step-row">{row}</div>}{expandable ? <TraceCollapse open={open}>{patch ? <ApplyPatchDiffViewer item={item} patch={patch} result={result} /> : <div className="trace-step-details">{args !== undefined && args !== null ? <div><p>Arguments</p><pre>{pretty(args)}</pre></div> : null}{result !== undefined && result !== null ? <div><p>Result</p><pre>{pretty(result)}</pre></div> : null}</div>}</TraceCollapse> : null}</li>;
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
  const steps = mergeFunctionSteps(items);
  const status: StatusKind = steps.some((item) => item.status === "failed") ? "failed" : steps.some((item) => item.status === "in_progress") ? "running" : steps.some((item) => item.status === "incomplete") ? "interrupted" : "completed";
  const running = status === "running";
  const [expanded, setExpanded] = useState(running);
  const duration = steps.reduce((total, item) => total + (item.duration_ms ?? 0), 0);
  const current = running ? [...steps].reverse().find((item) => item.status === "in_progress") : undefined;
  useEffect(() => setExpanded(running), [running]);
  return <section className="work-trace" aria-label="Agent work trace" aria-busy={running} data-work-trace={status}><button className="trace-header" type="button" aria-expanded={expanded} onClick={() => setExpanded((value) => !value)}><StatusIcon status={status} /><span>{status === "running" ? "Running" : status === "failed" ? "Failed" : status === "interrupted" ? "Interrupted" : "Completed"}</span>{duration ? <span className="trace-duration" aria-hidden={running || undefined}>· {formatDuration(duration)}</span> : null}<ChevronDown className={`trace-header-chevron ${expanded ? "" : "closed"}`} size={14} strokeWidth={1.5} aria-hidden="true" /></button><TraceCollapse open={expanded}><ul className="trace-steps">{steps.map((item) => <WorkStep item={item} key={item.id} />)}</ul></TraceCollapse>{!expanded && current ? <ul className="trace-steps" data-work-trace-tail=""><WorkStep item={current} key={`tail:${current.id}`} /></ul> : null}</section>;
}

export function ThreadItems({ items, agentName }: { items: SessionItem[]; agentName: string }) {
  const rendered = [];
  for (let index = 0; index < items.length;) {
    const item = items[index]; if (!item) break;
    if (item.type === "message") {
      const assistant = item.role === "assistant";
      rendered.push(<article className={`message-row ${assistant ? "assistant" : "user"}`} key={item.id}><div className="message-body">{assistant ? <div className="message-byline">{agentName || "Agent"}{item.phase === "commentary" ? " · working note" : ""}</div> : null}{assistant ? <MessageMarkdown content={textOf(item) || "Empty message item"} /> : <div className="message-copy">{textOf(item) || "Empty message item"}</div>}</div></article>);
      index += 1; continue;
    }
    const traceItems = [item]; let cursor = index + 1;
    while (cursor < items.length && items[cursor]?.type !== "message" && items[cursor]?.turn_id === item.turn_id) { const next = items[cursor]; if (next) traceItems.push(next); cursor += 1; }
    rendered.push(<WorkTrace items={traceItems} key={`trace:${item.turn_id}:${item.id}`} />); index = cursor;
  }
  return rendered;
}
