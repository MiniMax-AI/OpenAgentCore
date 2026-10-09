import type { Query, SDKUserMessage } from "@anthropic-ai/claude-agent-sdk";
import { Inputs } from "./inputs.js";
import { parseMessageInput, type MessageInput } from "./message_input.js";

export type ExecutorEvent =
  | { type: "executor_ready"; protocol: 4 }
  | { type: "turn_started"; turn_id: string }
  | { type: "mcp_stop"; turn_id: string; servers: string[] }
  | { type: "turn_settled"; turn_id: string; confirmed: boolean; reusable: boolean; reason: string };
type WireEvent = { type: string; [key: string]: unknown };
type Turn = { id: string; inputs: Inputs; cancelled: boolean; interrupt?: Promise<boolean>; callbacks: Set<Promise<unknown>>; stoppedMCP?: (confirmed: boolean) => void };

// The SDK sees one iterator for the Executor lifetime. Each yielded batch belongs
// to a fresh receipt ledger; finishing a Turn never closes this outer iterator.
export class ExecutorTurns implements AsyncIterable<SDKUserMessage> {
  private active?: Turn;
  private readonly used = new Set<string>();
  private readonly queue: Inputs[] = [];
  private wake?: () => void;
  private ended = false;
  private stream?: Query;
  private begin?: (input: MessageInput, emit: (event: WireEvent) => Promise<void>) => Inputs;
  private submitInput?: (value: Record<string,unknown>) => Promise<void>;
  private cancelCalls?: () => void;
  private nativeID = "";
  constructor(private readonly output: (event: WireEvent) => Promise<void>, private readonly abort: AbortController) {}

  configure(stream: Query, begin: NonNullable<ExecutorTurns["begin"]>, submit: (value: Record<string,unknown>)=>Promise<void>, cancelCalls: () => void = () => {}): void { this.stream = stream; this.begin = begin; this.submitInput=submit; this.cancelCalls=cancelCalls; }
  async submit(value: Record<string,unknown>): Promise<void> {
    const payload=this.assertCurrent(value);
    if(!this.submitInput || (payload.type!=="steer" && payload.type!=="function_result")) throw new Error("invalid turn input");
    await this.track(()=>this.submitInput!(payload));
  }
  identify(id: string): void {
    if (!id || this.nativeID && this.nativeID !== id) throw new Error("invalid native identity");
    this.nativeID = id;
  }
  get id(): string | undefined { return this.active?.id; }
  get cancelled(): boolean { return this.active?.cancelled ?? false; }
  get inputs(): Inputs { if (!this.active) throw new Error("no active turn"); return this.active.inputs; }

  async start(value: Record<string, unknown>): Promise<void> {
    const id = value.turn_id;
    if (this.ended || this.active || !this.stream || !this.begin || typeof id !== "string" || !id.trim() || id.length > 256 ||
        this.used.has(id) || Object.keys(value).some(key => !["type", "turn_id", "input"].includes(key))) throw new Error("invalid turn start");
    const input = parseMessageInput(value.input);
    const output = async (event: WireEvent) => {
      if (this.active?.id !== id) throw new Error("late turn event");
      await this.output({ ...event, turn_id: id });
    };
    const inputs = this.begin(input, output);
    this.active = { id, inputs, cancelled: false, callbacks: new Set() };
    this.used.add(id);
    await this.output({ type: "turn_started", turn_id: id });
    if (this.nativeID) for (const event of inputs.start(this.nativeID)) await output(event);
    this.queue.push(inputs);
    this.wake?.();
  }

  async cancel(value: Record<string, unknown>): Promise<void> {
    if (typeof value.turn_id !== "string" || Object.keys(value).some(key => !["type", "turn_id"].includes(key))) throw new Error("invalid cancellation");
    const turn = this.active;
    // A retired identity has no authority over its successor.
    if (!turn || value.turn_id !== turn.id) {
      if (this.used.has(value.turn_id)) return;
      throw new Error("unknown turn");
    }
    if (turn.interrupt) return;
    turn.cancelled = true;
    this.cancelCalls?.();
    const discarded=turn.inputs.cancelQueued();
    turn.interrupt = this.stream!.interrupt().then(receipt => {
      const confirmed = !discarded && receipt !== undefined && Array.isArray(receipt.still_queued) && receipt.still_queued.length === 0;
      if (!confirmed) this.abort.abort();
      return confirmed;
    }, () => { this.abort.abort(); return false; });
    await turn.interrupt;
  }

  async stopMCP(servers: string[]): Promise<void> {
    const turn = this.active;
    if (!turn?.cancelled || turn.stoppedMCP || this.abort.signal.aborted) throw new Error("invalid MCP stop");
    let confirm!: (confirmed: boolean) => void;
    const receipt = new Promise<boolean>(resolve => { confirm = resolve; });
    turn.stoppedMCP = confirm;
    const abort = () => confirm(false);
    this.abort.signal.addEventListener("abort", abort, { once: true });
    try {
      await this.emit({ type: "mcp_stop", servers });
      if (!await receipt) throw new Error("MCP stop unconfirmed");
    } finally { this.abort.signal.removeEventListener("abort", abort); }
  }

  mcpStopped(value: Record<string, unknown>): void {
    const turn = this.active;
    if (!turn?.cancelled || turn.id !== value.turn_id || !turn.stoppedMCP || typeof value.confirmed !== "boolean" ||
        Object.keys(value).some(key => !["type", "turn_id", "confirmed"].includes(key))) throw new Error("invalid MCP stop receipt");
    const complete = turn.stoppedMCP;
    turn.stoppedMCP = undefined;
    complete(value.confirmed);
  }

  assertCurrent(value: Record<string, unknown>): Record<string, unknown> {
    if (!this.active || this.active.cancelled || value.turn_id !== this.active.id) throw new Error("inactive turn input");
    const { turn_id: _id, ...payload } = value;
    return payload;
  }
  async emit(event: WireEvent): Promise<void> {
    if (!this.active) throw new Error("unbound native event");
    await this.output({ ...event, turn_id: this.active.id });
  }
  track<T>(operation: () => Promise<T>): Promise<T> {
    const turn = this.active;
    if (!turn) return Promise.reject(new Error("unbound native callback"));
    const promise = Promise.resolve().then(operation);
    turn.callbacks.add(promise);
    void promise.then(() => turn.callbacks.delete(promise), () => turn.callbacks.delete(promise));
    return promise;
  }
  async quiescent(): Promise<boolean> {
    const turn = this.active;
    if (!turn) return false;
    if (this.abort.signal.aborted) return false;
    let stop: () => void = () => {};
    const aborted = new Promise<false>(resolve => {
      stop = () => resolve(false);
      this.abort.signal.addEventListener("abort", stop, { once: true });
    });
    try {
      return await Promise.race([
        this.drain().then(async () => !turn.cancelled || await turn.interrupt === true),
        aborted,
      ]);
    } finally { this.abort.signal.removeEventListener("abort", stop); }
  }
  async drain(): Promise<void> {
    const turn = this.active;
    while (turn?.callbacks.size) await Promise.allSettled([...turn.callbacks]);
  }
  async settled(confirmed: boolean, reusable: boolean, reason: string): Promise<void> {
    const turn = this.active;
    if (!turn) return;
    turn.inputs.close();
    this.active = undefined;
    await this.output({ type: "turn_settled", turn_id: turn.id, confirmed, reusable, reason });
  }
  close(): void { this.ended = true; this.active?.inputs.close(); this.wake?.(); }
  async *[Symbol.asyncIterator](): AsyncIterator<SDKUserMessage> {
    while (!this.ended) {
      const inputs = this.queue.shift();
      if (inputs) yield* inputs;
      else await new Promise<void>(resolve => { this.wake = resolve; });
    }
  }
}
