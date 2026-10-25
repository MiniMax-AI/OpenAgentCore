import { NativeFailure } from "./native_failure.js";
import { ExecutorTurns, type ExecutorEvent } from "./executor_protocol.js";
import { StructuredOutput } from "./structured_output.js";
import { Subagents } from "./subagents.js";
import type { Fact } from "./subagent_history.js";
import { getSessionInfo, query, startup, type HookCallback, type McpServerConfig, type Options, type WarmQuery } from "@anthropic-ai/claude-agent-sdk";
import { Inputs, type InputEvent } from "./inputs.js";
import { executorResultUsage, type NativeUsage } from "./usage.js";
import { spawnNative } from "./native.js";
import { MessageObserver, type MessageEvent } from "./messages.js";
import { createFunctionServer, type FunctionCall } from "./functions.js";
import { FunctionBridge, type FunctionEvent } from "./function_bridge.js";
import { MCPProfile } from "./mcp.js";
import { MCPObserver, type MCPEvent } from "./mcp_observer.js";
import { CommandObserver, type CommandEvent } from "./command_observer.js";
import { SearchObserver, type SearchEvent } from "./search_observer.js";
import { WorkspaceProfile } from "./workspace.js";
import type { ExecutorPrepare } from "./request.js";
import { recoverSession } from "./recovery.js";

export type Event =
  | ExecutorEvent
  | Fact
  | MessageEvent
  | InputEvent
  | FunctionEvent
  | MCPEvent
  | CommandEvent
  | SearchEvent
  | { type: "usage"; session_id: string; result_id: string; usage: NativeUsage }
  | { type: "result"; session_id: string; text: string }
  | { type: "error"; code: "invalid_request" | "history_unavailable" | "execution_failed" | "cancelled"; engine_error_code?: string; session_id?: string; result_id?: string };

export async function execute(request: ExecutorPrepare, ownerEmit: (event: Event) => Promise<void>, abort: AbortController, turns: ExecutorTurns): Promise<void> {
  const emit = (event: Event) => turns.emit(event);
  let functions = new FunctionBridge(emit);
  let inputs = new Inputs();
  const nativeFailure = new NativeFailure();
  const nativeResultFailure = new Error("unsuccessful native result");
  let classifiedFailure: { engine_error_code: string; session_id: string; result_id: string } | undefined;
  const definitions = (request.functions ?? []).map(tool => ({ name: tool.name, description: tool.description, inputSchema: tool.parameters, deferLoading: tool.defer_loading }));
  const names = definitions.map(tool => `mcp__functions__${tool.name}`);
  // Environment none runs only the tools Core selects. WebSearch is a provider-side tool; WebFetch would fetch from the Runtime host.
  const selected = [...(request.tool_search ? ["ToolSearch"] : []), ...(request.web_search === "live" ? ["WebSearch"] : [])];
  const declarations = request.workspace?.mcp ?? request.mcp_http_servers;
  const profile: MCPProfile | undefined = declarations === undefined ? undefined : new MCPProfile(declarations, names, id => subagents?.permitsActor(id) ?? id === undefined);
  const subagents = request.subagents ? new Subagents(request.cwd, request.subagents.max_concurrent, request.resume, profile?.identities, request.reasoning_summary) : undefined;
  const workspace = request.workspace === undefined ? undefined : new WorkspaceProfile(request.cwd, request.workspace, profile, subagents, request.web_search === "disabled");
  let commands = workspace ? new CommandObserver() : undefined;
  const observeSearch = workspace !== undefined || request.web_search === "live";
  let searches = observeSearch ? new SearchObserver() : undefined;
  if (workspace && "mcp_http_servers" in request) throw new Error("invalid_request");
  if (request.require_history && !request.resume) {
    const recovered = await recoverSession(request.cwd);
    if (!recovered) {
      // No Turn is bound yet, so the Executor owner reports missing history.
      await ownerEmit({ type: "error", code: "history_unavailable" });
      return;
    }
    request = { ...request, resume: recovered };
  }
  if (request.resume && !await getSessionInfo(request.resume, { dir: request.cwd })) {
    await ownerEmit({ type: "error", code: "history_unavailable" });
    return;
  }
  subagents?.expectSession(request.resume);
  const mcpServers: Record<string, McpServerConfig> = Object.create(null);
  // Core resolves a Subagent's call through the child's identity and open Turn, so those facts precede it.
  const invoke = async (target: FunctionBridge, call: FunctionCall, signal: AbortSignal) => {
    let child: Awaited<ReturnType<Subagents["child"]>>;
    // A call Core cannot attribute fails the execution instead of reaching the model as a tool error.
    try { child = await subagents?.child(call.id); } catch (error) { failed = true; stream?.close(); throw error; }
    for (const fact of child?.facts ?? []) await emit(fact);
    return target.invoke(child ? { ...call, child: { native_id: child.native_id, turn_id: child.turn_id } } : call, signal);
  };
  if (definitions.length) mcpServers.functions = createFunctionServer(definitions, (call,signal) => { const target=functions; return turns.track(()=>invoke(target,call,signal)); });
  let mcp = profile ? new MCPObserver(profile.identities) : undefined;
  if (profile) Object.assign(mcpServers, profile.servers);
  const children: Promise<number | null>[] = [];
  let result: Extract<Event, { type: "result" }> | undefined;
  let nativeID = "";
  const resultIDs = new Set<string>();
  let failed = false;
  let cancellationFactsFailed = false;
  let structured = request.output_format ? new StructuredOutput() : undefined;
  let messages = new MessageObserver(request.reasoning_summary);
  let stream: ReturnType<typeof query> | undefined;
  let warm: WarmQuery | undefined;
  let nativeAlive = false;
  const closeInputs = () => { inputs.close(); turns.close(); };
  abort.signal.addEventListener("abort", closeInputs, { once: true });
  try {
    if (abort.signal.aborted) throw new Error("cancelled");
    // The model sees only the selected tools of a subset server; the hook below remains the backstop.
    const unselected = profile ? await profile.unselectedTools(abort.signal) : [];
    const beforeTool: HookCallback = async (input, id, context) => {
      if (input.hook_event_name !== "PreToolUse") return {};
      if (subagents?.isCoordination(input.tool_name)) return subagents.beforeTool(input, id, context);
      await subagents?.noteTool(input, id, context);
      return profile ? profile.beforeTool(input, id, context) : {};
    };
    const options: Options = {
        ...(request.native_model_options?.effort !== undefined ? { effort: request.native_model_options.effort } : {}),
        ...(request.native_model_options?.thinking !== undefined ? { thinking: request.native_model_options.thinking } : {}),
        cwd: request.cwd,
        env: workspace?.options.env ?? { ...process.env },
        // Flag settings outrank user and project settings, so no settings file brings back detached work after a
        // Turn or updates of the pinned binary.
        settings: { env: { CLAUDE_CODE_DISABLE_BACKGROUND_TASKS: "1", DISABLE_AUTOUPDATER: "1" } },
        model: request.model,
        ...(request.output_format ? { outputFormat: request.output_format } : {}),
        // On a workspace Claude Code keeps its own prompt and the Agent's instructions are appended; that prompt
        // describes tools environment none does not have.
        systemPrompt: workspace ? { type: "preset", preset: "claude_code", ...(request.system_prompt ? { append: request.system_prompt } : {}) } : request.system_prompt,
        ...(request.resume ? { resume: request.resume } : {}),
        includePartialMessages: true, abortController: abort,
        // Environment none keeps no workspace tools and approves only Core's function and MCP tools. The host's
        // settings, memory files and MCP servers belong to no Session.
        ...(workspace ? {} : {
          tools: [...(subagents ? ["Agent", "SendMessage"] : []), ...selected],
          allowedTools: [...(profile?.allowed ?? names), ...selected],
          settingSources: [], strictMcpConfig: true,
          canUseTool: async () => ({ behavior: "deny" as const, message: "Tools are unavailable in this execution profile." }),
        }),
        ...(workspace?.options ?? {}),
        disallowedTools: [...(workspace?.options.disallowedTools ?? []), ...(profile?.denied ?? []), ...unselected],
        mcpServers,
        ...(subagents ? { forwardSubagentText: true, hooks: {
          Stop: [{ hooks: [subagents.stopped] }], SubagentStart: [{ hooks: [subagents.childStart] }],
          PostToolUseFailure: [{ hooks: [subagents.failedTool] }], PreToolUse: [{ hooks: [beforeTool] }],
        } } : profile ? { hooks: { PreToolUse: [{ hooks: [beforeTool] }] } } : {}),
        spawnClaudeCodeProcess: options => {
          const child = spawnNative(options);
          nativeAlive = true;
          child.once("exit", () => { nativeAlive = false; });
          children.push(new Promise(resolve => child.once("close", code => { nativeAlive = false; resolve(code); })));
          return child;
        },
    };
    // ENABLE_TOOL_SEARCH=true is the official mapping of tool_search; native owns discovery and provider policy.
    if (request.tool_search) options.env = { ...(options.env ?? process.env), ENABLE_TOOL_SEARCH: "true" };
    for(const matchers of Object.values(options.hooks ?? {})) for(const matcher of matchers ?? []) {
      matcher.hooks=matcher.hooks.map(hook=>(...args)=>turns.track(()=>hook(...args)));
    }
    const canUseTool=options.canUseTool;
    if(canUseTool) options.canUseTool=(...args)=>turns.track(()=>canUseTool(...args));
    warm = await startup({ options, initializeTimeoutMs: 15000 });
    stream = warm.query(turns);
    const initialized = await stream.initializationResult();
    const hooksRequested = Object.values(options.hooks ?? {}).some(matchers => matchers?.some(matcher => matcher.hooks.length > 0));
    if ((hooksRequested && initialized.hooks_applied !== true) || children.length !== 1 || !nativeAlive || abort.signal.aborted) {
      throw new Error("preparation unavailable");
    }
    if (declarations?.some(server => "required" in server && server.required)) profile?.verifyRequired(await stream.mcpServerStatus());
    if (abort.signal.aborted) throw new Error("preparation unavailable");
    turns.configure(stream,(input,output)=>{
      inputs=new Inputs(input);
      functions=new FunctionBridge(output);
      messages=new MessageObserver(request.reasoning_summary);
      structured=request.output_format ? new StructuredOutput() : undefined;
      commands=workspace ? new CommandObserver() : undefined;
      searches=observeSearch ? new SearchObserver() : undefined;
      mcp=profile ? new MCPObserver(profile.identities) : undefined;
      subagents?.beginTurn();
      return inputs;
    },async value=>{
      if(value.type==="steer") for(const event of inputs.submit(value)) await emit(event);
      else functions.submit(JSON.stringify(value));
    });
    await ownerEmit({type:"executor_ready",protocol:3});
    for await (const message of stream) {
      // Native command queue telemetry can follow a result. It is not model
      // output or a consumed-input receipt, and has no authority over a Turn.
      if (isCommandLifecycle(message)) continue;
      if(!turns.id && !(message.type==="system" && message.subtype==="init")) throw new Error("unbound native event");
      const errorCode = nativeFailure.observe(message, nativeID, inputs.pendingInputIDs(message));
      subagents?.consume(message);
      structured?.consume(message, nativeID);
      await functions.consume(message, nativeID, turns.cancelled);
      if (mcp) for (const event of mcp.consume(message, nativeID)) await emit(event);
      if (commands) for (const event of commands.consume(message, nativeID, inputs.hasInput)) await emit(event);
      if (searches) for (const event of searches.consume(message, nativeID)) await emit(event);
      for (const event of messages.consume(message)) await emit(event);
      if (message.type === "system" && message.subtype === "init") {
        nativeID = message.session_id;
        if (!nativeID || (request.resume && nativeID !== request.resume)) throw new Error("unexpected native session");
        // Native decides whether the selected tools and StructuredOutput join the inventory; a missing structured call fails the result.
        const tools = message.tools.filter(name => name !== "StructuredOutput" && !selected.includes(name));
        if (profile) profile.verify(tools, await stream.mcpServerStatus(), nativeID, !workspace, subagents ? ["Task", "SendMessage"] : []);
        if (workspace) {
          // Native tools and settings may add servers and tools, but every tool and Skill Core declared must still be present.
          const declared = [...names, ...(workspace.skillNames.length ? ["Skill"] : []), ...(subagents ? ["Task", "SendMessage"] : [])];
          if (declared.some(name => !message.tools.includes(name)) || workspace.skillNames.some(name => !message.skills?.includes(name)) ||
              definitions.length && !message.mcp_servers.some(server => server.name === "functions" && server.status === "connected")) throw new Error("unexpected native configuration");
        } else if (!profile && (tools.length !== names.length + (subagents ? 2 : 0) || tools.some(name => ![...names, ...(subagents ? ["Task", "SendMessage"] : [])].includes(name)) ||
            message.mcp_servers.length !== (definitions.length ? 1 : 0) ||
            message.mcp_servers.some(server => server.name !== "functions" || server.status !== "connected"))) {
          throw new Error("unexpected native configuration");
        }
        turns.identify(nativeID);
        if(turns.id) for (const event of inputs.start(nativeID)) await emit(event);
      } else if (message.type === "result") {
        if (!message.uuid || resultIDs.has(message.uuid) || !nativeID || message.session_id !== nativeID) throw new Error("invalid native result identity");
        resultIDs.add(message.uuid);
        await emit({ type: "usage", session_id: nativeID, result_id: message.uuid, usage: executorResultUsage(message) });
        for (const event of inputs.consume(message)) await emit(event);
        if ((message.subtype !== "success" || message.is_error) && !turns.cancelled) {
          if (errorCode) classifiedFailure = { engine_error_code: errorCode, session_id: nativeID, result_id: message.uuid };
          throw nativeResultFailure;
        }
        if (structured && !turns.cancelled) await emit(structured.complete(message));
        result = { type: "result", session_id: nativeID, text: message.subtype === "success" ? message.result : "" };
        if(turns.cancelled && !inputs.complete) throw new Error("unconfirmed cancelled inputs");
      }
      if (message.type !== "result" && turns.id) for (const event of inputs.consume(message)) await emit(event);
      if(result && inputs.complete && (!subagents || subagents.complete)) {
        if (turns.cancelled) functions.cancelUnanswered();
        if(!await turns.quiescent() || abort.signal.aborted) throw new Error("unconfirmed interrupt");
        functions.assertComplete(); mcp?.assertComplete(); commands?.assertComplete(); searches?.assertComplete();
        if(subagents) for(const event of await subagents.facts()) await emit(event);
        await emit(turns.cancelled ? {type:"error",code:"cancelled"} : result);
        functions.close();
        await turns.settled(true,true,"");
        result=undefined;
      }
    }
    if (subagents && turns.id) for (const event of await subagents.facts()) await emit(event);
    functions.assertComplete();
    mcp?.assertComplete();
    commands?.assertComplete();
    searches?.assertComplete();
  } catch (error) {
    if (error !== nativeResultFailure) classifiedFailure = undefined;
    failed = true;
  } finally {
    profile?.close();
    inputs.close();
    turns.close();
    functions.close();
    stream?.close();
    warm?.close();
    abort.signal.removeEventListener("abort", closeInputs);
    const exits = await Promise.all(children);
    if (!exits.length) classifiedFailure = undefined;
    await turns.drain();
    if (!exits.length || exits.some(code => code !== 0)) failed = true;
    if (subagents && turns.id && abort.signal.aborted && exits.length === 1) {
      try { for (const event of await subagents.facts(Date.now())) await emit(event); }
      catch { cancellationFactsFailed = true; }
    }
    if (mcp) for (const event of mcp.close()) await emit(event);
    if (commands) for (const event of commands.close()) await emit(event);
    if (searches) for (const event of searches.close(nativeID)) await emit(event);
  }
  if(turns.id) {
    await emit({type:"error",code: turns.cancelled && !cancellationFactsFailed ? "cancelled" : "execution_failed", ...(!turns.cancelled && !cancellationFactsFailed ? classifiedFailure : undefined)});
    await turns.settled(false, false, turns.cancelled ? "cancellation_unconfirmed" : "native_execution_unavailable");
  } else if(failed && !abort.signal.aborted) await ownerEmit({type:"error",code:"execution_failed"});
}

function isCommandLifecycle(message: unknown): boolean {
  return typeof message === "object" && message !== null &&
    "type" in message && message.type === "command_lifecycle";
}
