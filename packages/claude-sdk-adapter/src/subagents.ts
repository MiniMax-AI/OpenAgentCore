import type { HookCallback, SDKMessage } from "@anthropic-ai/claude-agent-sdk";
import { readHistory, type Fact } from "./subagent_history.js";
import type { ToolIdentity } from "./mcp.js";

const deny = (reason: string) => ({ hookSpecificOutput: { hookEventName: "PreToolUse" as const, permissionDecision: "deny" as const, permissionDecisionReason: reason } });
export class Subagents {
  readonly known = new Set<string>();
  private readonly observedCalls = new Set<string>();
  private readonly pending = new Map<string, string>();
  private readonly names = new Map<string, string>();
  private readonly pendingNames = new Map<string, string>();
  private readonly running = new Map<string, string>();
  private readonly admitted = new Map<string, string>();
  private readonly actors = new Map<string, string>();
  private session = "";
  private transcript = "";
  private hydrated = false;
  constructor(private readonly cwd: string, private readonly limit: number, private resume: string | undefined, private readonly tools: ReadonlyMap<string, ToolIdentity> = new Map(), private readonly reasoning = false) {
    if (!Number.isSafeInteger(limit) || limit < 1) throw new Error("invalid_request");
  }
  get complete(): boolean { return this.pending.size === 0 && this.running.size === 0; }
  beginTurn(): void {
    if (!this.complete) throw new Error("native child work is unsettled");
    this.observedCalls.clear(); this.admitted.clear(); this.actors.clear();
  }
  expectSession(id: string | undefined): void { this.resume = id; }
  permitsActor(id?: string): boolean { return id === undefined || this.known.has(id); }
  isCoordination(name: string): boolean { return ["Agent", "Task", "SendMessage"].includes(name); }
  readonly beforeTool: HookCallback = async (input, _id, { signal }) => {
    if (input.hook_event_name !== "PreToolUse" || !this.isCoordination(input.tool_name)) return {};
    if (input.session_id === this.session && input.agent_id === undefined) {
      this.transcript = input.transcript_path;
      if (this.resume && !this.hydrated) {
        for (const id of (await readHistory(this.session, this.transcript, this.cwd)).ids) this.known.add(id);
        this.hydrated = true;
      }
    }
    if (signal.aborted || input.session_id !== this.session || !this.permitsActor(input.agent_id)) return deny("Subagent identity is unavailable.");
    const value = input.tool_input as Record<string, unknown>;
    if (!value || typeof value !== "object") return deny("Invalid subagent call.");
    const spawn = input.tool_name !== "SendMessage";
    // Native decides which agent types, models and modes exist; only a remote run, which needs an account Core does not hold, is refused.
    const to = spawn ? "" : typeof value.to === "string" ? this.recipient(value.to) : "";
    if (spawn ? typeof value.prompt !== "string" || !value.prompt.trim() || value.isolation === "remote"
      : !this.known.has(to) || (value.type !== undefined && value.type !== "message") ||
        typeof value.message !== "string" || !value.message.trim()) return deny("Unsupported subagent operation.");
    if (this.pending.has(input.tool_use_id)) return {};
    if (!spawn && (this.running.has(to) || [...this.pending.values()].includes(to))) return deny("The subagent is still running.");
    {
      if (this.pending.size + this.running.size >= this.limit) return deny("The concurrent subagent limit has been reached.");
      this.pending.set(input.tool_use_id, to);
      if (spawn && typeof value.name === "string") this.pendingNames.set(input.tool_use_id, value.name);
    }
    return { hookSpecificOutput: { hookEventName: "PreToolUse", permissionDecision: "allow" } };
  };
  // A native MCP request names its tool call but not the agent that made it.
  readonly noteTool: HookCallback = async input => {
    if (input.hook_event_name === "PreToolUse" && input.agent_id !== undefined) this.actors.set(input.tool_use_id, input.agent_id);
    return {};
  };
  // The child of a call, with the facts Core needs before it can resolve the call: the child's identity and its open Turn.
  async child(call: string): Promise<{ native_id: string; turn_id: string; facts: Fact[] } | undefined> {
    const id = this.actors.get(call);
    if (id === undefined) return;
    if (!this.transcript || !this.known.has(id)) throw new Error("native child history is missing");
    const { facts } = await readHistory(this.session, this.transcript, this.cwd, { child: id, live: this.admitted, tools: this.tools, reasoning: this.reasoning });
    return { native_id: id, turn_id: facts[facts.length - 1].fact.turn_id as string, facts };
  }
  readonly stopped: HookCallback = async input => {
    if (input.hook_event_name === "Stop" && input.session_id === this.session && input.agent_id === undefined) this.transcript = input.transcript_path;
    return {};
  };
  readonly childStart: HookCallback = async input => {
    if (input.hook_event_name !== "SubagentStart" || input.session_id !== this.session || !this.running.has(input.agent_id)) throw new Error("invalid native child start");
    this.known.add(input.agent_id);
    return {};
  };
  readonly failedTool: HookCallback = async input => {
    if (input.hook_event_name === "PostToolUseFailure") { this.pending.delete(input.tool_use_id); this.pendingNames.delete(input.tool_use_id); }
    return {};
  };
  consume(message: SDKMessage): void {
    if (message.type === "assistant" && message.parent_tool_use_id === null) {
      for (const block of message.message.content) if (block.type === "tool_use") this.observedCalls.add(block.id);
    }
    if (message.type === "system" && message.subtype === "init") {
      if (!message.session_id || this.session && this.session !== message.session_id || this.resume && this.resume !== message.session_id) throw new Error("unexpected subagent root");
      this.session = message.session_id;
    }
    if (message.type === "system" && message.subtype === "task_started" && message.task_type === "local_agent") {
      if (message.session_id !== this.session || !message.tool_use_id || !/^[A-Za-z0-9_-]{1,160}$/.test(message.task_id)) throw new Error("unbound native child task");
      const recipient = this.pending.get(message.tool_use_id);
      if (recipient && recipient !== message.task_id) throw new Error("mismatched native continuation target");
      if (!this.pending.delete(message.tool_use_id) && !this.running.has(message.task_id)) throw new Error("unadmitted native child task");
      const name = this.pendingNames.get(message.tool_use_id);
      if (name !== undefined) { this.pendingNames.delete(message.tool_use_id); this.names.set(name, message.task_id); }
      this.known.add(message.task_id);
      this.running.set(message.task_id, message.tool_use_id);
      this.admitted.set(message.task_id, message.tool_use_id);
    } else if (message.type === "system" && message.subtype === "task_notification" && this.known.has(message.task_id)) {
      if (this.running.get(message.task_id) !== message.tool_use_id) throw new Error("unmatched native child completion");
      this.running.delete(message.task_id);
    } else if (message.type === "user" && Array.isArray(message.message.content)) {
      for (const block of message.message.content) if (block.type === "tool_result") { this.pending.delete(block.tool_use_id); this.pendingNames.delete(block.tool_use_id); }
    }
  }
  async facts(cancelledAt?: number): Promise<Fact[]> {
    if (!this.session || !this.transcript) { if (!this.known.size) return []; throw new Error("native child history is missing"); }
    if (cancelledAt === undefined && (this.pending.size || this.running.size)) throw new Error("native child work is unsettled");
    const history = await readHistory(this.session, this.transcript, this.cwd, { tools: this.tools, reasoning: this.reasoning, ...(cancelledAt === undefined ? {} : { cancelled: { at: cancelledAt, admitted: this.admitted } }) });
    if ([...this.known].some(id => !history.ids.includes(id))) throw new Error("native child history is missing");
    return history.facts.filter(event => event.type !== "subagent_coordination" || this.observedCalls.has(event.fact.id as string));
  }

  // Native resolves an exact name first, then the name in its normalized form.
  private recipient(to: string): string {
    const exact = this.names.get(to);
    if (exact !== undefined) return exact;
    const name = nativeName(to);
    for (const [key, id] of this.names) if (nativeName(key) === name) return id;
    return to;
  }
}

const nativeName = (name: string) => name.normalize("NFKC").replace(/[\p{Cc}\p{Cf}]/gu, c => /\s/.test(c) ? c : "").trim().toLowerCase().replace(/\s+/g, "-");
