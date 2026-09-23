import { Settings2 } from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";

import { AgentCoreError } from "@agents-core-web/agents-client";
import type {
  AgentCore,
  AgentSession,
  AgentTurn,
  CoreStartupConfiguration,
  CreateAgentInput,
  CreateEnvironmentTemplateInput,
  FunctionResultInput,
  SavedAgent,
  SessionEvent,
  SessionItem,
  UpdateAgentInput,
} from "@agents-core-web/agents-client";

import { SandboxManagerView } from "./features/sandbox/SandboxManagerView";
import { SandboxProvider } from "./features/sandbox/SandboxContext";
import { SystemNavigation } from "./components/SystemNavigation";
import { ConnectionModal } from "./components/ConnectionModal";
import { CreateMenu } from "./components/CreateMenu";
import { ProductNavigation, type ProductView } from "./components/ProductNavigation";
import { StatusIcon } from "./components/StatusIcon";
import { ThemeMenu } from "./components/ThemeMenu";
import { useToast } from "./components/Toast";
import { AgentsView } from "./features/agents/AgentsView";
import {
  sessionEnvironmentAdmissionBlocker,
} from "./features/agents/session-admission";
import {
  removeSavedAgent,
  replaceSavedAgent,
  requestAgentDelete,
  requestAgentDetail,
  requestAgentUpdate,
} from "./features/agents/agent-actions";
import { DashboardView } from "./features/dashboard/DashboardView";
import { loadRuntimeDurableSnapshot } from "./features/dashboard/runtime-history";
import {
  loadRuntimeDashboardSnapshot,
  RUNTIME_SNAPSHOT_REFRESH_MS,
  RUNTIME_SNAPSHOT_TIMEOUT_MS,
  type RuntimeDashboardSnapshot,
} from "./features/dashboard/runtime-snapshot";
import { EnvironmentTemplatesView } from "./features/environment-templates/EnvironmentTemplatesView";
import {
  SessionsView,
  type SessionDetailState,
  type StreamState,
} from "./features/sessions/SessionsView";
import type { SessionStartInput } from "./features/sessions/create/SessionStartDialog";
import { sessionInitialInputError } from "./features/sessions/create/session-initial-input";
import { sessionCreateRequestPayload } from "./features/sessions/create/session-create-attempt";
import { normalizeSessionEnvironmentInput } from "./features/sessions/create/session-environment";
import { validateSessionAgentSubmission } from "./features/sessions/create/session-start-draft";
import {
  removeSession,
  reconcileUnknownSessionDelete,
  replaceSessionMetadata,
  requestSessionDelete,
  requestSessionDetail,
  requestSessionUpdate,
  selectionAfterSessionDelete,
  SessionActionError,
  SessionMetadataConflictError,
} from "./features/sessions/actions/session-actions";
import {
  environmentObservationFromResource,
  environmentIdentitiesMatch,
  environmentReadIsCurrent,
  mergeDurableEnvironmentObservation,
  matchingSessionSnapshot,
  reduceEnvironmentObservation,
  supportedEnvironmentIdentity,
  type EnvironmentIdentity,
  type EnvironmentObservation,
  type ScopedEnvironmentObservation,
  unavailableEnvironmentObservation,
  visibleEnvironmentObservation,
} from "./features/sessions/environment/environment-state";
import {
  loadEnvironmentTemplateCatalog,
  type EnvironmentTemplateCatalog,
} from "./features/sessions/environment/environment-templates";
import {
  listAllTurns,
  matchingTurnSnapshot,
  mergeDurableAndLiveTurns,
  turnReadIsCurrent,
  upsertTurn,
} from "./features/sessions/turns/turn-state";
import { SystemView } from "./features/system/SystemView";
import type { SourceFilesOperations } from "./features/system/SourceFilesPanel";
import { VaultsView, type VaultOperations } from "./features/vaults/VaultsView";
import { deriveSessionVaultPlan, loadVaultCatalog, type VaultCatalog } from "./features/vaults/vault-catalog";
import { requestVaultCreate } from "./features/vaults/vault-operations";
import {
  createCore,
  loadConnection,
  saveConnection,
  type CoreConnection,
  type CoreConnectionState,
} from "./lib/connection";
import { settleCollection } from "./lib/collection-load";
import { listStableCollectionPages } from "./lib/collection-pagination";
import { BACKEND_NOT_READY_NOTICE, backendFailureStatus } from "./lib/core-readiness";
import {
  beginPendingFunctionResult,
  failPendingFunctionResult,
  functionResultActionKey,
  type FailedPendingFunctionResult,
} from "./lib/pending-function-result";
import {
  beginPendingSend,
  failPendingSend,
  type FailedPendingSend,
} from "./lib/pending-send";
import {
  appendCommandOutputDelta,
  mergeDurableAndLiveItems,
  updateLiveSessionItems,
  upsertSessionItem,
} from "./lib/session-items";
import {
  createDurableRefreshCoordinator,
  createStreamRecoveryBuffer,
  type DurableRefreshCoordinator,
} from "./lib/session-recovery";
import {
  beginStreamReconciliation,
  requestCurrentStreamRetry,
  shouldRetryStreamError,
  streamConnectionWasStable,
  streamReconnectDelay,
  waitForStreamReconnect,
} from "./lib/stream-reconnect";

type View = ProductView | "system" | "sandbox";

function viewFromLocation(): View {
  if (typeof window === "undefined") return "dashboard";
  const candidate = window.location.hash.slice(1);
  if (candidate === "templates" && __AGENTS_CORE_WEB_OPENAI_HOSTED_SESSIONS__) return "templates";
  return candidate === "agents" || candidate === "sessions" || candidate === "vaults" || candidate === "system" || candidate === "sandbox"
    ? candidate
    : "dashboard";
}

interface StreamConnection {
  sessionId: string | null;
  state: StreamState;
  error: string | null;
}

interface SelectedSessionLoad {
  sessionId: string | null;
  state: SessionDetailState;
  error: string | null;
}

interface SessionCreateRequest {
  agentId: string | null;
  requestId: number;
}

interface CreationStreamOwner {
  controller: AbortController;
  coreGeneration: number;
  sessionId: string | null;
  streamEpoch: number | null;
  startReconciliation?: () => void;
}

interface LiveSessionEventContext {
  sessionId: string;
  streamEpoch: number;
  isCurrent: () => boolean;
  refreshCoordinator: DurableRefreshCoordinator | null;
}

const COLLECTION_RECONCILIATION_ATTEMPTS = 3;

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : "The Agent core request failed.";
}

function isAbort(error: unknown): boolean {
  return error instanceof DOMException && error.name === "AbortError";
}

async function listAllItems(
  core: AgentCore,
  sessionId: string,
  signal?: AbortSignal,
): Promise<SessionItem[]> {
  const items: SessionItem[] = [];
  let after: string | undefined;

  while (true) {
    const page = await core.listItems(sessionId, { after, limit: 100, order: "asc", signal });
    items.push(...page.data);
    if (!page.has_more) return items;

    const nextAfter = page.last_id ?? page.data[page.data.length - 1]?.id;
    if (!nextAfter || nextAfter === after) {
      throw new Error("The Agent core returned an invalid Items pagination cursor.");
    }
    after = nextAfter;
  }
}

function projectTextEvent(current: SessionItem[], event: SessionEvent): SessionItem[] {
  const eventType = typeof event.type === "string" ? event.type : "";
  if (!eventType.includes(".output_text.")) return current;

  const partText = event.part?.type === "output_text" ? event.part.text : undefined;
  const replacement = event.text ?? partText ?? undefined;
  const addition = event.delta;
  if (replacement === undefined && addition === undefined) return current;

  const streamId = `stream:${event.turn_id ?? "turn"}:${event.output_index ?? 0}:${event.content_index ?? 0}`;
  const existingIndex = current.findIndex((item) => item.id === event.item_id || item.id === streamId);
  const existing = existingIndex >= 0 ? current[existingIndex] : undefined;
  const previousText = existing?.type === "message"
    ? (existing.content ?? []).map((content) => content.text ?? "").join("")
    : "";
  const text = replacement ?? `${previousText}${addition ?? ""}`;
  const projected: SessionItem = {
    id: existing?.id ?? event.item_id ?? streamId,
    turn_id: event.turn_id ?? existing?.turn_id ?? "",
    type: "message",
    status: eventType.endsWith(".done") || eventType.endsWith(".completed") ? "completed" : "in_progress",
    role: "assistant",
    phase: "final_answer",
    content: [{ type: "output_text", text }],
  };

  return upsertSessionItem(current, projected);
}

export function App() {
  const { show: showToast } = useToast();
  const [view, setView] = useState<View>(viewFromLocation);
  const [connection, setConnection] = useState<CoreConnection>(() => loadConnection());
  const [connectionOpen, setConnectionOpen] = useState(false);

  useEffect(() => {
    const hash = view === "dashboard" ? "" : `#${view}`;
    const next = `${window.location.pathname}${window.location.search}${hash}`;
    const current = `${window.location.pathname}${window.location.search}${window.location.hash}`;
    if (current !== next) window.history.replaceState(window.history.state, "", next);
  }, [view]);

  useEffect(() => {
    const onHashChange = () => setView(viewFromLocation());
    window.addEventListener("hashchange", onHashChange);
    return () => window.removeEventListener("hashchange", onHashChange);
  }, []);
  const [agents, setAgents] = useState<SavedAgent[]>([]);
  const [vaultCatalog, setVaultCatalog] = useState<VaultCatalog | null>(null);
  const [vaultCollectionState, setVaultCollectionState] = useState<CoreConnectionState>("connecting");
  const [vaultCollectionError, setVaultCollectionError] = useState<string | null>(null);
  const [vaultSupported, setVaultSupported] = useState<boolean | null>(null);
  const [startupConfiguration, setStartupConfiguration] = useState<CoreStartupConfiguration | null>(null);
  const [startupConfigurationState, setStartupConfigurationState] = useState<CoreConnectionState>("connecting");
  const [startupConfigurationSupported, setStartupConfigurationSupported] = useState<boolean | null>(null);
  const [environmentTemplates, setEnvironmentTemplates] = useState<EnvironmentTemplateCatalog | null>(null);
  const [sessions, setSessions] = useState<AgentSession[]>([]);
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [items, setItems] = useState<SessionItem[]>([]);
  const [itemsSessionId, setItemsSessionId] = useState<string | null>(null);
  const [turns, setTurns] = useState<AgentTurn[]>([]);
  const [turnsSessionId, setTurnsSessionId] = useState<string | null>(null);
  const [turnCollectionLoad, setTurnCollectionLoad] = useState<SelectedSessionLoad>({
    sessionId: null,
    state: "idle",
    error: null,
  });
  const [environmentObservations, setEnvironmentObservations] = useState<Map<string, ScopedEnvironmentObservation>>(
    () => new Map(),
  );
  const [agentCollectionState, setAgentCollectionState] = useState<CoreConnectionState>("connecting");
  const [agentCollectionError, setAgentCollectionError] = useState<string | null>(null);
  const [agentCollectionHasSnapshot, setAgentCollectionHasSnapshot] = useState(false);
  const [sessionCollectionState, setSessionCollectionState] = useState<CoreConnectionState>("connecting");
  const [sessionCollectionError, setSessionCollectionError] = useState<string | null>(null);
  const [sessionCollectionHasSnapshot, setSessionCollectionHasSnapshot] = useState(false);
  const [runtimeSnapshot, setRuntimeSnapshot] = useState<RuntimeDashboardSnapshot | null>(null);
  const [runtimeCollectionState, setRuntimeCollectionState] = useState<CoreConnectionState>("connecting");
  const [runtimeCollectionError, setRuntimeCollectionError] = useState<string | null>(null);
  const [runtimeCollectionHasSnapshot, setRuntimeCollectionHasSnapshot] = useState(false);
  const [sessionAgentFilter, setSessionAgentFilter] = useState<string | null>(null);
  const [filteredSessions, setFilteredSessions] = useState<AgentSession[]>([]);
  const [filteredSessionCollectionState, setFilteredSessionCollectionState] = useState<CoreConnectionState>("connecting");
  const [filteredSessionCollectionError, setFilteredSessionCollectionError] = useState<string | null>(null);
  const [selectedSessionLoad, setSelectedSessionLoad] = useState<SelectedSessionLoad>({
    sessionId: null,
    state: "idle",
    error: null,
  });
  const [streamConnection, setStreamConnection] = useState<StreamConnection>({
    sessionId: null,
    state: "idle",
    error: null,
  });
  const [streamRetryRevision, setStreamRetryRevision] = useState(0);
  const [sessionSendFailures, setSessionSendFailures] = useState<Map<string, FailedPendingSend>>(
    () => new Map(),
  );
  const cancelFailureRef = useRef<FailedPendingSend | undefined>(undefined);
  const functionResultFailuresRef = useRef(new Map<string, FailedPendingFunctionResult>());
  const [busy, setBusy] = useState(false);
  const [agentCreateRequest, setAgentCreateRequest] = useState<number | null>(null);
  const [sessionCreateRequest, setSessionCreateRequest] = useState<SessionCreateRequest | null>(null);
  const agentCreateSequenceRef = useRef(0);
  const sessionCreateSequenceRef = useRef(0);
  const selectedIdRef = useRef<string | null>(selectedId);
  const sessionsRef = useRef<AgentSession[]>(sessions);
  const filteredSessionsRef = useRef<AgentSession[]>(filteredSessions);
  const itemsSessionIdRef = useRef<string | null>(itemsSessionId);
  const turnsSessionIdRef = useRef<string | null>(turnsSessionId);
  const connectionGenerationRef = useRef(0);
  const agentCollectionRequestRef = useRef(0);
  const sessionCollectionRequestRef = useRef(0);
  const agentCollectionAbortRef = useRef<AbortController | null>(null);
  const sessionCollectionAbortRef = useRef<AbortController | null>(null);
  const runtimeCollectionAbortRef = useRef<AbortController | null>(null);
  const runtimeCollectionRequestRef = useRef(0);
  const runtimeCollectionHasSnapshotRef = useRef(false);
  const dashboardRefreshInFlightRef = useRef(false);
  const filteredSessionCollectionAbortRef = useRef<AbortController | null>(null);
  const filteredSessionCollectionRequestRef = useRef(0);
  const sessionAgentFilterRef = useRef<string | null>(sessionAgentFilter);
  const vaultCollectionAbortRef = useRef<AbortController | null>(null);
  const startupConfigurationAbortRef = useRef<AbortController | null>(null);
  const environmentTemplateAbortRef = useRef<AbortController | null>(null);
  const vaultCollectionRequestRef = useRef(0);
  const startupConfigurationRequestRef = useRef(0);
  const agentCollectionRevisionRef = useRef(0);
  const sessionCollectionRevisionRef = useRef(0);
  const sessionRequestRef = useRef(new Map<string, number>());
  const sessionEventRevisionRef = useRef(new Map<string, number>());
  const itemEventRevisionRef = useRef(new Map<string, number>());
  const turnEventRevisionRef = useRef(new Map<string, number>());
  const environmentEventRevisionRef = useRef(new Map<string, number>());
  const environmentRequestRef = useRef(new Map<string, number>());
  const sessionEnvironmentIdentityRef = useRef(new Map<string, EnvironmentIdentity | null>());
  const operationRequestRef = useRef(0);
  const streamEpochRef = useRef(0);
  const selectedSessionReadAbortRef = useRef<AbortController | null>(null);
  const selectedStreamAbortRef = useRef<AbortController | null>(null);
  const creationStreamOwnerRef = useRef<CreationStreamOwner | null>(null);
  selectedIdRef.current = selectedId;
  sessionsRef.current = sessions;
  filteredSessionsRef.current = filteredSessions;
  sessionAgentFilterRef.current = sessionAgentFilter;

  const core = useMemo(() => createCore(connection), [connection]);
  const sourceFilesOperations = useMemo<SourceFilesOperations>(() => ({
    uploadSourceFile: (input, options) => core.uploadSourceFile(input, options),
    retrieveSourceFile: (fileId, options) => core.retrieveSourceFile(fileId, options),
    deleteSourceFile: (fileId, options) => core.deleteSourceFile(fileId, options),
    retrieveEnvironment: (environmentId, options) => core.retrieveEnvironment(environmentId, options),
    createEnvironmentFile: (environmentId, input, options) => core.createEnvironmentFile(environmentId, input, options),
    listEnvironmentFiles: (environmentId, options) => core.listEnvironmentFiles(environmentId, options),
  }), [core]);
  const coreGeneration = connectionGenerationRef.current;
  const coreState: CoreConnectionState = agentCollectionState === "ready" || sessionCollectionState === "ready"
    ? "ready"
    : agentCollectionState === "failed" && sessionCollectionState === "failed"
      ? "failed"
      : "connecting";
  const sessionVaultCatalog = vaultCollectionState === "ready" ? vaultCatalog : null;
  const sessionBrowserSessions = sessionAgentFilter ? filteredSessions : sessions;
  const sessionBrowserState = sessionAgentFilter ? filteredSessionCollectionState : sessionCollectionState;
  const sessionBrowserError = sessionAgentFilter ? filteredSessionCollectionError : sessionCollectionError;
  const selected = selectedId
    ? sessionBrowserSessions.find((session) => session.id === selectedId) ?? null
    : null;
  const detailState: SessionDetailState = !selectedId
    ? "idle"
    : selectedSessionLoad.sessionId === selectedId
      ? selectedSessionLoad.state
      : "loading";
  const detailError = selectedSessionLoad.sessionId === selectedId ? selectedSessionLoad.error : null;
  const turnState: SessionDetailState = !selectedId
    ? "idle"
    : turnCollectionLoad.sessionId === selectedId
      ? turnCollectionLoad.state
      : "loading";
  const turnError = turnCollectionLoad.sessionId === selectedId ? turnCollectionLoad.error : null;
  const streamState: StreamState = !selectedId
    ? "idle"
    : streamConnection.sessionId !== selectedId
      ? "connecting"
      : streamConnection.state;
  const streamError = selectedId && streamConnection.sessionId === selectedId
    ? streamConnection.error
    : null;
  const sendError = selectedId ? sessionSendFailures.get(selectedId) ?? null : null;
  const environmentObservation: EnvironmentObservation | null = visibleEnvironmentObservation(
    selectedId ? environmentObservations.get(selectedId) : undefined,
    selectedId,
    streamConnection.sessionId,
    streamEpochRef.current,
  );
  const streamReady = streamState === "listening";
  const notify = useCallback((message: string, tone: "success" | "error") => {
    showToast(message, { tone, key: `${tone}:${message}` });
  }, [showToast]);

  const refreshAgents = useCallback(async () => {
    if (coreGeneration !== connectionGenerationRef.current) return false;
    agentCollectionAbortRef.current?.abort();
    const controller = new AbortController();
    agentCollectionAbortRef.current = controller;
    setAgentCollectionState("connecting");
    setAgentCollectionError(null);
    try {
      const request = agentCollectionRequestRef.current + 1;
      agentCollectionRequestRef.current = request;
      const result = await settleCollection(() => listStableCollectionPages(
        (options) => core.listAgents(options),
        () => agentCollectionRevisionRef.current,
        controller.signal,
        COLLECTION_RECONCILIATION_ATTEMPTS,
      ));
      if (
        coreGeneration !== connectionGenerationRef.current ||
        request !== agentCollectionRequestRef.current
      ) return false;
      if (result.status === "rejected") {
        if (isAbort(result.reason)) return false;
        const message = errorMessage(result.reason);
        setAgentCollectionState("failed");
        setAgentCollectionError(message);
        notify(backendFailureStatus(result.reason) ? BACKEND_NOT_READY_NOTICE : message, "error");
        return false;
      }
      if (result.value === null) {
        const message = "Agent data changed while the collection was loading. Refresh again to reconcile all loaded pages.";
        setAgentCollectionState("failed");
        setAgentCollectionError(message);
        notify(message, "error");
        return false;
      }
      setAgents(result.value);
      setAgentCollectionHasSnapshot(true);
      setAgentCollectionState("ready");
      return true;
    } finally {
      if (agentCollectionAbortRef.current === controller) agentCollectionAbortRef.current = null;
    }
  }, [core, coreGeneration, notify]);

  const refreshSessions = useCallback(async () => {
    if (coreGeneration !== connectionGenerationRef.current) return false;
    sessionCollectionAbortRef.current?.abort();
    const controller = new AbortController();
    sessionCollectionAbortRef.current = controller;
    setSessionCollectionState("connecting");
    setSessionCollectionError(null);
    try {
      const request = sessionCollectionRequestRef.current + 1;
      sessionCollectionRequestRef.current = request;
      const result = await settleCollection(() => listStableCollectionPages(
        (options) => core.listSessions(options),
        () => sessionCollectionRevisionRef.current,
        controller.signal,
        COLLECTION_RECONCILIATION_ATTEMPTS,
      ));
      if (
        coreGeneration !== connectionGenerationRef.current ||
        request !== sessionCollectionRequestRef.current
      ) return false;
      if (result.status === "rejected") {
        if (isAbort(result.reason)) return false;
        const message = errorMessage(result.reason);
        setSessionCollectionState("failed");
        setSessionCollectionError(message);
        notify(backendFailureStatus(result.reason) ? BACKEND_NOT_READY_NOTICE : message, "error");
        return false;
      }
      if (result.value === null) {
        const message = "Session data changed while the collection was loading. Refresh again to reconcile all loaded pages.";
        setSessionCollectionState("failed");
        setSessionCollectionError(message);
        notify(message, "error");
        return false;
      }
      const stableSessions = result.value;

      const nextEnvironmentIdentities = new Map(
        stableSessions.map((session) => [session.id, supportedEnvironmentIdentity(session.environment)]),
      );
      const changedEnvironmentSessions = new Set<string>();
      for (const [sessionId, identity] of nextEnvironmentIdentities) {
        if (!environmentIdentitiesMatch(sessionEnvironmentIdentityRef.current.get(sessionId), identity)) {
          changedEnvironmentSessions.add(sessionId);
        }
      }
      for (const sessionId of sessionEnvironmentIdentityRef.current.keys()) {
        if (!nextEnvironmentIdentities.has(sessionId)) changedEnvironmentSessions.add(sessionId);
      }
      for (const sessionId of changedEnvironmentSessions) {
        environmentRequestRef.current.set(
          sessionId,
          (environmentRequestRef.current.get(sessionId) ?? 0) + 1,
        );
        environmentEventRevisionRef.current.set(
          sessionId,
          (environmentEventRevisionRef.current.get(sessionId) ?? 0) + 1,
        );
      }
      sessionEnvironmentIdentityRef.current = nextEnvironmentIdentities;
      if (changedEnvironmentSessions.size) {
        setEnvironmentObservations((current) => {
          if (![...changedEnvironmentSessions].some((sessionId) => current.has(sessionId))) return current;
          const next = new Map(current);
          for (const sessionId of changedEnvironmentSessions) next.delete(sessionId);
          return next;
        });
      }
      sessionsRef.current = stableSessions;
      setSessions(stableSessions);
      setSessionCollectionHasSnapshot(true);
      if (!sessionAgentFilterRef.current) {
        setSelectedId((current) => {
          const next = current && stableSessions.some((session) => session.id === current)
            ? current
            : stableSessions[0]?.id ?? null;
          selectedIdRef.current = next;
          return next;
        });
      }
      setSessionCollectionState("ready");
      return true;
    } finally {
      if (sessionCollectionAbortRef.current === controller) sessionCollectionAbortRef.current = null;
    }
  }, [core, coreGeneration, notify]);

  const refreshRuntimeSnapshot = useCallback(async () => {
    if (coreGeneration !== connectionGenerationRef.current) return false;
    runtimeCollectionAbortRef.current?.abort();
    const controller = new AbortController();
    runtimeCollectionAbortRef.current = controller;
    const request = runtimeCollectionRequestRef.current + 1;
    runtimeCollectionRequestRef.current = request;
    let timedOut = false;
    const timeout = window.setTimeout(() => {
      timedOut = true;
      controller.abort();
    }, RUNTIME_SNAPSHOT_TIMEOUT_MS);
    if (!runtimeCollectionHasSnapshotRef.current) setRuntimeCollectionState("connecting");
    setRuntimeCollectionError(null);
    try {
      const result = await settleCollection(() => loadRuntimeDashboardSnapshot(
        core,
        controller.signal,
      ));
      if (
        coreGeneration !== connectionGenerationRef.current ||
        request !== runtimeCollectionRequestRef.current
      ) return false;
      if (result.status === "rejected") {
        if (isAbort(result.reason) && !timedOut) return false;
        const message = timedOut
          ? "Runtime snapshot exceeded the 15 second Web refresh budget."
          : errorMessage(result.reason);
        setRuntimeCollectionState("failed");
        setRuntimeCollectionError(message);
        return false;
      }
      if (result.value === null) {
        setRuntimeCollectionState("failed");
        setRuntimeCollectionError("Session data changed while Runtime observations were loading. The previous complete snapshot was retained.");
        return false;
      }
      setRuntimeSnapshot(result.value);
      runtimeCollectionHasSnapshotRef.current = true;
      setRuntimeCollectionHasSnapshot(true);
      setRuntimeCollectionState("ready");
      return true;
    } finally {
      window.clearTimeout(timeout);
      if (runtimeCollectionAbortRef.current === controller) runtimeCollectionAbortRef.current = null;
    }
  }, [core, coreGeneration]);

  const refreshFilteredSessions = useCallback(async (agentId: string) => {
    if (
      coreGeneration !== connectionGenerationRef.current ||
      sessionAgentFilterRef.current !== agentId
    ) return false;
    filteredSessionCollectionAbortRef.current?.abort();
    const controller = new AbortController();
    filteredSessionCollectionAbortRef.current = controller;
    const request = filteredSessionCollectionRequestRef.current + 1;
    filteredSessionCollectionRequestRef.current = request;
    setFilteredSessionCollectionState("connecting");
    setFilteredSessionCollectionError(null);
    try {
      const result = await settleCollection(() => listStableCollectionPages(
        (options) => core.listSessions({ ...options, agentId }),
        () => sessionCollectionRevisionRef.current,
        controller.signal,
        COLLECTION_RECONCILIATION_ATTEMPTS,
      ));
      if (
        coreGeneration !== connectionGenerationRef.current ||
        request !== filteredSessionCollectionRequestRef.current ||
        sessionAgentFilterRef.current !== agentId
      ) return false;
      if (result.status === "rejected") {
        if (isAbort(result.reason)) return false;
        const message = errorMessage(result.reason);
        setFilteredSessionCollectionState("failed");
        setFilteredSessionCollectionError(message);
        notify(backendFailureStatus(result.reason) ? BACKEND_NOT_READY_NOTICE : message, "error");
        return false;
      }
      if (result.value === null) {
        const message = "Filtered Session data changed while the collection was loading. Refresh again to reconcile all loaded pages.";
        setFilteredSessionCollectionState("failed");
        setFilteredSessionCollectionError(message);
        notify(message, "error");
        return false;
      }
      const stableFilteredSessions = result.value;
      if (stableFilteredSessions.some((session) => session.agent.id !== agentId)) {
        const message = "Agent Core returned a Session outside the requested Agent filter.";
        setFilteredSessionCollectionState("failed");
        setFilteredSessionCollectionError(message);
        notify(message, "error");
        return false;
      }
      filteredSessionsRef.current = stableFilteredSessions;
      setFilteredSessions(stableFilteredSessions);
      setSelectedId((current) => {
        const next = current && stableFilteredSessions.some((session) => session.id === current)
          ? current
          : stableFilteredSessions[0]?.id ?? null;
        selectedIdRef.current = next;
        return next;
      });
      setFilteredSessionCollectionState("ready");
      return true;
    } finally {
      if (filteredSessionCollectionAbortRef.current === controller) {
        filteredSessionCollectionAbortRef.current = null;
      }
    }
  }, [core, coreGeneration, notify]);

  const refreshVaults = useCallback(async () => {
    if (coreGeneration !== connectionGenerationRef.current) return false;
    vaultCollectionAbortRef.current?.abort();
    const controller = new AbortController();
    vaultCollectionAbortRef.current = controller;
    const request = vaultCollectionRequestRef.current + 1;
    vaultCollectionRequestRef.current = request;
    setVaultCollectionState("connecting");
    setVaultCollectionError(null);
    try {
      const catalog = await loadVaultCatalog(core, controller.signal);
      if (
        coreGeneration !== connectionGenerationRef.current ||
        request !== vaultCollectionRequestRef.current
      ) return false;
      setVaultCatalog(catalog);
      setVaultSupported(true);
      setVaultCollectionState("ready");
      return true;
    } catch (error) {
      if (
        coreGeneration !== connectionGenerationRef.current ||
        request !== vaultCollectionRequestRef.current ||
        isAbort(error)
      ) return false;
      if (error instanceof AgentCoreError && (error.status === 404 || error.status === 405)) {
        setVaultCatalog(null);
        setVaultSupported(false);
        setVaultCollectionState("ready");
        setVaultCollectionError(null);
        return false;
      }
      setVaultCollectionState("failed");
      setVaultCollectionError(errorMessage(error));
      return false;
    } finally {
      if (vaultCollectionAbortRef.current === controller) vaultCollectionAbortRef.current = null;
    }
  }, [core, coreGeneration]);

  const refreshStartupConfiguration = useCallback(async () => {
    if (coreGeneration !== connectionGenerationRef.current) return false;
    startupConfigurationAbortRef.current?.abort();
    const controller = new AbortController();
    startupConfigurationAbortRef.current = controller;
    const request = startupConfigurationRequestRef.current + 1;
    startupConfigurationRequestRef.current = request;
    setStartupConfigurationState("connecting");
    try {
      const configuration = await core.retrieveStartupConfiguration({ signal: controller.signal });
      if (
        coreGeneration !== connectionGenerationRef.current ||
        request !== startupConfigurationRequestRef.current
      ) return false;
      setStartupConfiguration(configuration);
      setStartupConfigurationSupported(true);
      setStartupConfigurationState("ready");
      return true;
    } catch (error) {
      if (
        coreGeneration !== connectionGenerationRef.current ||
        request !== startupConfigurationRequestRef.current ||
        isAbort(error)
      ) return false;
      setStartupConfiguration(null);
      if (error instanceof AgentCoreError && (error.status === 404 || error.status === 405)) {
        setStartupConfigurationSupported(false);
        setStartupConfigurationState("ready");
        return false;
      }
      setStartupConfigurationSupported(null);
      setStartupConfigurationState("failed");
      return false;
    } finally {
      if (startupConfigurationAbortRef.current === controller) startupConfigurationAbortRef.current = null;
    }
  }, [core, coreGeneration]);

  const refreshEnvironmentTemplates = useCallback(async () => {
    // Managed Environment configuration is only ever read for the Web build that
    // can request it. An unread catalog stays null, never an implied capability.
    if (!__AGENTS_CORE_WEB_OPENAI_HOSTED_SESSIONS__) return;
    if (coreGeneration !== connectionGenerationRef.current) return;
    environmentTemplateAbortRef.current?.abort();
    const controller = new AbortController();
    environmentTemplateAbortRef.current = controller;
    try {
      const catalog = await loadEnvironmentTemplateCatalog(core, controller.signal);
      if (
        coreGeneration !== connectionGenerationRef.current ||
        environmentTemplateAbortRef.current !== controller
      ) return;
      setEnvironmentTemplates(catalog);
    } catch (error) {
      if (
        coreGeneration !== connectionGenerationRef.current ||
        environmentTemplateAbortRef.current !== controller ||
        isAbort(error)
      ) return;
      setEnvironmentTemplates({ state: "failed", message: errorMessage(error) });
    } finally {
      if (environmentTemplateAbortRef.current === controller) environmentTemplateAbortRef.current = null;
    }
  }, [core, coreGeneration]);

  const refreshSession = useCallback(
    async (sessionId: string, signal?: AbortSignal): Promise<boolean> => {
      if (coreGeneration !== connectionGenerationRef.current) return false;
      const sessionScopeAgentId = sessionAgentFilterRef.current;
      const request = (sessionRequestRef.current.get(sessionId) ?? 0) + 1;
      const sessionRevision = sessionEventRevisionRef.current.get(sessionId) ?? 0;
      const itemRevision = itemEventRevisionRef.current.get(sessionId) ?? 0;
      const turnRevision = turnEventRevisionRef.current.get(sessionId) ?? 0;
      const environmentRevision = environmentEventRevisionRef.current.get(sessionId) ?? 0;
      sessionRequestRef.current.set(sessionId, request);
      const turnRead = { coreGeneration, request, sessionId };
      void listAllTurns(core, sessionId, signal).then((sessionTurns) => {
        const currentTurnRead = {
          coreGeneration: connectionGenerationRef.current,
          request: sessionRequestRef.current.get(sessionId) ?? 0,
          sessionId,
          selectedSessionId: selectedIdRef.current,
        };
        if (!turnReadIsCurrent(turnRead, currentTurnRead)) return;
        const liveRevisionChanged = turnRevision !== (turnEventRevisionRef.current.get(sessionId) ?? 0);
        const currentTurnsSessionId = turnsSessionIdRef.current;
        turnsSessionIdRef.current = sessionId;
        setTurns((current) => (
          liveRevisionChanged
            ? mergeDurableAndLiveTurns(
              sessionTurns,
              currentTurnsSessionId === sessionId ? current : [],
            )
            : sessionTurns
        ));
        setTurnsSessionId(sessionId);
        setTurnCollectionLoad({ sessionId, state: "ready", error: null });
      }).catch((error: unknown) => {
        const currentTurnRead = {
          coreGeneration: connectionGenerationRef.current,
          request: sessionRequestRef.current.get(sessionId) ?? 0,
          sessionId,
          selectedSessionId: selectedIdRef.current,
        };
        if (!turnReadIsCurrent(turnRead, currentTurnRead) || isAbort(error)) return;
        setTurnCollectionLoad({
          sessionId,
          state: "failed",
          error: errorMessage(error),
        });
      });
      try {
        const [session, sessionItems] = await Promise.all([
          core.retrieveSession(sessionId, { signal }),
          listAllItems(core, sessionId, signal),
        ]);
        if (
          coreGeneration !== connectionGenerationRef.current ||
          request !== sessionRequestRef.current.get(sessionId)
        ) return false;
        if (sessionScopeAgentId && session.agent.id !== sessionScopeAgentId) {
          throw new Error("Agent Core returned Session details outside the active Agent filter.");
        }
        const currentSessionRevision = sessionEventRevisionRef.current.get(sessionId) ?? 0;
        const sessionIsCurrent = sessionRevision === currentSessionRevision;
        const environmentIdentity = supportedEnvironmentIdentity(session.environment);
        if (sessionIsCurrent) {
          sessionEnvironmentIdentityRef.current.set(sessionId, environmentIdentity);
          sessionCollectionRevisionRef.current += 1;
          setSessions((current) => {
            const found = current.some((value) => value.id === session.id);
            const next = found
              ? current.map((value) => (value.id === session.id ? session : value))
              : sessionScopeAgentId ? current : [session, ...current];
            sessionsRef.current = next;
            return next;
          });
          if (
            sessionScopeAgentId &&
            sessionAgentFilterRef.current === sessionScopeAgentId
          ) {
            setFilteredSessions((current) => {
              if (!current.some((value) => value.id === session.id)) return current;
              const next = current.map((value) => (value.id === session.id ? session : value));
              filteredSessionsRef.current = next;
              return next;
            });
          }
        }
        if (selectedIdRef.current === sessionId) {
          const liveRevisionChanged = itemRevision !== (itemEventRevisionRef.current.get(sessionId) ?? 0);
          const currentItemsSessionId = itemsSessionIdRef.current;
          itemsSessionIdRef.current = sessionId;
          setItems((current) => (
            liveRevisionChanged
              ? mergeDurableAndLiveItems(
                sessionItems,
                currentItemsSessionId === sessionId ? current : [],
              )
              : sessionItems
          ));
          setItemsSessionId(sessionId);
        }
        if (selectedIdRef.current === sessionId) {
          setSelectedSessionLoad({ sessionId, state: "ready", error: null });
        }

        if (!sessionIsCurrent) return true;
        if (!environmentIdentity) {
          if (environmentRevision === (environmentEventRevisionRef.current.get(sessionId) ?? 0)) {
            setEnvironmentObservations((current) => {
              if (!current.has(sessionId)) return current;
              const next = new Map(current);
              next.delete(sessionId);
              return next;
            });
          }
          return true;
        }
        if (signal?.aborted || selectedIdRef.current !== sessionId) return false;

        const environmentStreamEpoch = streamEpochRef.current;
        const environmentRequest = (environmentRequestRef.current.get(sessionId) ?? 0) + 1;
        environmentRequestRef.current.set(sessionId, environmentRequest);
        const environmentRead = {
          coreGeneration,
          sessionId,
          ...environmentIdentity,
          sessionRequest: request,
          environmentRequest,
          streamEpoch: environmentStreamEpoch,
          sessionRevision,
          environmentRevision,
        };
        let observation: EnvironmentObservation;
        try {
          const resource = await core.retrieveEnvironment(environmentIdentity.environmentId, { signal });
          observation = environmentObservationFromResource(resource, environmentIdentity)
            ?? unavailableEnvironmentObservation(environmentIdentity);
        } catch (error) {
          if (isAbort(error)) return false;
          observation = unavailableEnvironmentObservation(environmentIdentity);
        }
        const currentEnvironmentIdentity = sessionEnvironmentIdentityRef.current.get(sessionId);
        if (signal?.aborted || !environmentReadIsCurrent(environmentRead, {
          coreGeneration: connectionGenerationRef.current,
          sessionId,
          environmentId: currentEnvironmentIdentity?.environmentId ?? "",
          environmentType: currentEnvironmentIdentity?.environmentType ?? environmentIdentity.environmentType,
          sessionRequest: sessionRequestRef.current.get(sessionId) ?? 0,
          environmentRequest: environmentRequestRef.current.get(sessionId) ?? 0,
          streamEpoch: streamEpochRef.current,
          sessionRevision: sessionEventRevisionRef.current.get(sessionId) ?? 0,
          environmentRevision: environmentEventRevisionRef.current.get(sessionId) ?? 0,
          selectedSessionId: selectedIdRef.current,
        })) return false;
        setEnvironmentObservations((current) => {
          const next = new Map(current);
          const existing = current.get(sessionId);
          const merged = existing?.streamEpoch === environmentStreamEpoch
            ? mergeDurableEnvironmentObservation(existing.observation, observation)
            : observation;
          next.set(sessionId, { observation: merged, sessionId, streamEpoch: environmentStreamEpoch });
          return next;
        });
        return true;
      } catch (error) {
        if (
          coreGeneration === connectionGenerationRef.current &&
          request === sessionRequestRef.current.get(sessionId) &&
          selectedIdRef.current === sessionId
        ) {
          if (isAbort(error)) return false;
          const message = errorMessage(error);
          setSelectedSessionLoad({ sessionId, state: "failed", error: message });
          notify(message, "error");
        }
        return false;
      }
    },
    [core, coreGeneration, notify],
  );

  const refreshSelectedSession = useCallback((sessionId: string): Promise<boolean> => {
    if (selectedIdRef.current !== sessionId) return Promise.resolve(false);
    selectedSessionReadAbortRef.current?.abort();
    const controller = new AbortController();
    // Turn pagination can outlive refreshSession's Session/Item result, so retain this
    // controller until the next selected read, selection change, deletion, or Core change.
    selectedSessionReadAbortRef.current = controller;
    return refreshSession(sessionId, controller.signal);
  }, [refreshSession]);

  const recoverSessionWorkspace = useCallback(() => {
    void (async () => {
      const generation = coreGeneration;
      void refreshAgents();
      const refreshed = await refreshSessions();
      if (!refreshed || generation !== connectionGenerationRef.current) return;
      const filter = sessionAgentFilterRef.current;
      if (filter) await refreshFilteredSessions(filter);
      if (generation !== connectionGenerationRef.current) return;
      const sessionId = selectedIdRef.current;
      if (sessionId) await refreshSelectedSession(sessionId);
    })();
  }, [coreGeneration, refreshAgents, refreshFilteredSessions, refreshSelectedSession, refreshSessions]);

  const refreshDashboard = useCallback(() => {
    if (dashboardRefreshInFlightRef.current) return;
    dashboardRefreshInFlightRef.current = true;
    void refreshAgents();
    void refreshVaults();
    void refreshEnvironmentTemplates();
    void (async () => {
      try {
        if (await refreshSessions()) await refreshRuntimeSnapshot();
      } finally {
        dashboardRefreshInFlightRef.current = false;
      }
    })();
    const filter = sessionAgentFilterRef.current;
    if (filter) void refreshFilteredSessions(filter);
  }, [refreshAgents, refreshEnvironmentTemplates, refreshFilteredSessions, refreshRuntimeSnapshot, refreshSessions, refreshVaults]);

  const loadDashboardRuntimeHistory = useCallback((
    snapshot: RuntimeDashboardSnapshot,
    range: Parameters<typeof loadRuntimeDurableSnapshot>[2],
    signal: AbortSignal,
  ) => loadRuntimeDurableSnapshot(core, snapshot, range, signal), [core]);

  const refreshSystem = useCallback(() => {
    void refreshStartupConfiguration();
  }, [refreshStartupConfiguration]);

  const changeSessionAgentFilter = useCallback((agentId: string | null) => {
    if (sessionAgentFilterRef.current === agentId) return;
    filteredSessionCollectionAbortRef.current?.abort();
    filteredSessionCollectionAbortRef.current = null;
    filteredSessionCollectionRequestRef.current += 1;
    sessionAgentFilterRef.current = agentId;
    filteredSessionsRef.current = [];
    setFilteredSessions([]);
    setFilteredSessionCollectionError(null);
    setFilteredSessionCollectionState(agentId ? "connecting" : "ready");
    const current = selectedIdRef.current;
    const nextSelectedId = agentId
      ? null
      : current && sessionsRef.current.some((session) => session.id === current)
        ? current
        : sessionsRef.current[0]?.id ?? null;
    selectedIdRef.current = nextSelectedId;
    setSelectedId(nextSelectedId);
    setSessionAgentFilter(agentId);
  }, []);

  useEffect(() => {
    cancelFailureRef.current = undefined;
    functionResultFailuresRef.current.clear();
  }, [coreGeneration, selectedId]);

  useEffect(() => {
    setAgents([]);
    setVaultCatalog(null);
    setVaultCollectionState("connecting");
    setVaultCollectionError(null);
    setVaultSupported(null);
    setStartupConfiguration(null);
    setStartupConfigurationState("connecting");
    setStartupConfigurationSupported(null);
    setSessions([]);
    setAgentCollectionHasSnapshot(false);
    setSessionCollectionHasSnapshot(false);
    setRuntimeSnapshot(null);
    runtimeCollectionHasSnapshotRef.current = false;
    setRuntimeCollectionState("connecting");
    setRuntimeCollectionError(null);
    setRuntimeCollectionHasSnapshot(false);
    setItems([]);
    setTurns([]);
    setEnvironmentObservations(new Map());
    itemsSessionIdRef.current = null;
    setItemsSessionId(null);
    turnsSessionIdRef.current = null;
    setTurnsSessionId(null);
    setTurnCollectionLoad({ sessionId: null, state: "idle", error: null });
    setSelectedId(null);
    setEnvironmentTemplates(null);
    void refreshAgents();
    void refreshSessions();
    void refreshVaults();
    void refreshEnvironmentTemplates();
    void refreshStartupConfiguration();
  }, [refreshAgents, refreshEnvironmentTemplates, refreshSessions, refreshStartupConfiguration, refreshVaults]);

  useEffect(() => {
    if (view !== "dashboard") return;
    let timer: number | null = null;
    void (async () => {
      if (
        sessionCollectionState === "ready" &&
        !runtimeCollectionHasSnapshotRef.current &&
        !dashboardRefreshInFlightRef.current
      ) {
        await refreshRuntimeSnapshot();
      }
    })();
    const schedule = () => {
      const jitter = Math.floor(Math.random() * 5_000);
      timer = window.setTimeout(() => {
        if (!document.hidden && !dashboardRefreshInFlightRef.current) {
          void refreshRuntimeSnapshot();
        }
        schedule();
      }, RUNTIME_SNAPSHOT_REFRESH_MS + jitter);
    };
    const onVisibilityChange = () => {
      if (!document.hidden && !dashboardRefreshInFlightRef.current) {
        void refreshRuntimeSnapshot();
      }
    };
    document.addEventListener("visibilitychange", onVisibilityChange);
    schedule();
    return () => {
      if (timer !== null) window.clearTimeout(timer);
      document.removeEventListener("visibilitychange", onVisibilityChange);
    };
  }, [refreshRuntimeSnapshot, sessionCollectionState, view]);

  useEffect(() => {
    filteredSessionCollectionAbortRef.current?.abort();
    filteredSessionCollectionAbortRef.current = null;
    filteredSessionCollectionRequestRef.current += 1;
    filteredSessionsRef.current = [];
    setFilteredSessions([]);
    setFilteredSessionCollectionError(null);
    if (!sessionAgentFilter) {
      setFilteredSessionCollectionState("ready");
      return;
    }
    setFilteredSessionCollectionState("connecting");
    void refreshFilteredSessions(sessionAgentFilter);
    return () => {
      filteredSessionCollectionAbortRef.current?.abort();
      filteredSessionCollectionAbortRef.current = null;
    };
  }, [refreshFilteredSessions, sessionAgentFilter]);

  useEffect(() => {
    if (vaultSupported === false && view === "vaults") setView("dashboard");
  }, [vaultSupported, view]);

  useEffect(() => {
    selectedSessionReadAbortRef.current?.abort();
    if (!selectedId) {
      selectedSessionReadAbortRef.current = null;
      setItems([]);
      setTurns([]);
      itemsSessionIdRef.current = null;
      setItemsSessionId(null);
      turnsSessionIdRef.current = null;
      setTurnsSessionId(null);
      setTurnCollectionLoad({ sessionId: null, state: "idle", error: null });
      setSelectedSessionLoad({ sessionId: null, state: "idle", error: null });
      setStreamConnection({ sessionId: null, state: "idle", error: null });
      return;
    }
    setItems([]);
    setTurns([]);
    itemsSessionIdRef.current = selectedId;
    setItemsSessionId(selectedId);
    turnsSessionIdRef.current = selectedId;
    setTurnsSessionId(selectedId);
    setTurnCollectionLoad({ sessionId: selectedId, state: "loading", error: null });
    setSelectedSessionLoad({ sessionId: selectedId, state: "loading", error: null });
    environmentEventRevisionRef.current.set(
      selectedId,
      (environmentEventRevisionRef.current.get(selectedId) ?? 0) + 1,
    );
    environmentRequestRef.current.set(
      selectedId,
      (environmentRequestRef.current.get(selectedId) ?? 0) + 1,
    );
    setEnvironmentObservations((current) => {
      if (!current.has(selectedId)) return current;
      const next = new Map(current);
      next.delete(selectedId);
      return next;
    });
    const creationOwner = creationStreamOwnerRef.current;
    if (
      creationOwner &&
      creationOwner.coreGeneration === coreGeneration &&
      creationOwner.sessionId === selectedId &&
      !creationOwner.controller.signal.aborted
    ) creationOwner.startReconciliation?.();
    else void refreshSelectedSession(selectedId);
    return () => {
      selectedSessionReadAbortRef.current?.abort();
      selectedSessionReadAbortRef.current = null;
    };
  }, [coreGeneration, refreshSelectedSession, selectedId]);

  const applyLiveSessionEvent = useCallback((
    event: SessionEvent,
    context: LiveSessionEventContext,
  ) => {
    const { sessionId, streamEpoch, isCurrent, refreshCoordinator } = context;
    if (!isCurrent()) return;
    if (typeof event.session_id === "string" && event.session_id && event.session_id !== sessionId) return;
    const eventType = typeof event.type === "string" ? event.type : "";
    const eventSession = matchingSessionSnapshot(event, sessionId);
    if (eventSession) {
      const nextEnvironmentIdentity = supportedEnvironmentIdentity(eventSession.environment);
      const previousEnvironmentIdentity = sessionEnvironmentIdentityRef.current.get(sessionId);
      if (!environmentIdentitiesMatch(previousEnvironmentIdentity, nextEnvironmentIdentity)) {
        sessionEnvironmentIdentityRef.current.set(sessionId, nextEnvironmentIdentity);
        environmentRequestRef.current.set(
          sessionId,
          (environmentRequestRef.current.get(sessionId) ?? 0) + 1,
        );
        environmentEventRevisionRef.current.set(
          sessionId,
          (environmentEventRevisionRef.current.get(sessionId) ?? 0) + 1,
        );
        setEnvironmentObservations((current) => {
          if (!current.has(sessionId)) return current;
          const next = new Map(current);
          next.delete(sessionId);
          return next;
        });
      }
    }
    const isEnvironmentEvent = eventType.startsWith("agent.session.environment.");
    if (isEnvironmentEvent) {
      environmentEventRevisionRef.current.set(
        sessionId,
        (environmentEventRevisionRef.current.get(sessionId) ?? 0) + 1,
      );
      setEnvironmentObservations((current) => {
        const next = new Map(current);
        const existing = current.get(sessionId);
        const previous = existing?.streamEpoch === streamEpoch ? existing.observation : null;
        const reduced = reduceEnvironmentObservation(
          previous,
          event,
          sessionId,
          sessionEnvironmentIdentityRef.current.get(sessionId) ?? null,
        );
        if (reduced) next.set(sessionId, { observation: reduced, sessionId, streamEpoch });
        else next.delete(sessionId);
        return next;
      });
    }
    if (eventSession) {
      sessionEventRevisionRef.current.set(
        sessionId,
        (sessionEventRevisionRef.current.get(sessionId) ?? 0) + 1,
      );
      sessionCollectionRevisionRef.current += 1;
      setSessions((current) => {
        const next = current.map((session) => (session.id === eventSession.id ? eventSession : session));
        sessionsRef.current = next;
        return next;
      });
      setFilteredSessions((current) => {
        if (!current.some((session) => session.id === eventSession.id)) return current;
        const next = current.map((session) => (session.id === eventSession.id ? eventSession : session));
        filteredSessionsRef.current = next;
        return next;
      });
    }
    const eventTurn = matchingTurnSnapshot(event, sessionId);
    if (eventTurn) {
      turnEventRevisionRef.current.set(
        sessionId,
        (turnEventRevisionRef.current.get(sessionId) ?? 0) + 1,
      );
      if (selectedIdRef.current === sessionId) {
        const currentTurnsSessionId = turnsSessionIdRef.current;
        turnsSessionIdRef.current = sessionId;
        setTurnsSessionId(sessionId);
        setTurns((current) => upsertTurn(
          currentTurnsSessionId === sessionId ? current : [],
          eventTurn,
        ));
      }
    }
    const isCommandOutputDelta = eventType === "agent.output.command_execution_output.delta";
    if (event.item || eventType.includes(".output_text.") || isCommandOutputDelta) {
      itemEventRevisionRef.current.set(
        sessionId,
        (itemEventRevisionRef.current.get(sessionId) ?? 0) + 1,
      );
    }
    if (event.item && selectedIdRef.current === sessionId) {
      const currentItemsSessionId = itemsSessionIdRef.current;
      itemsSessionIdRef.current = sessionId;
      setItemsSessionId(sessionId);
      setItems((current) => {
        const sessionItems = updateLiveSessionItems(
          current,
          currentItemsSessionId,
          sessionId,
          (value) => value,
        );
        const withoutTemporary = sessionItems.filter((item) => (
          event.item?.type !== "message" || event.item.role !== "assistant" || !item.id.startsWith(`stream:${event.item.turn_id}:`)
        ));
        return upsertSessionItem(withoutTemporary, event.item as SessionItem);
      });
    }
    if (eventType.includes(".output_text.") && selectedIdRef.current === sessionId) {
      const currentItemsSessionId = itemsSessionIdRef.current;
      itemsSessionIdRef.current = sessionId;
      setItemsSessionId(sessionId);
      setItems((current) => updateLiveSessionItems(
        current,
        currentItemsSessionId,
        sessionId,
        (sessionItems) => projectTextEvent(sessionItems, event),
      ));
    }
    if (isCommandOutputDelta && selectedIdRef.current === sessionId) {
      const currentItemsSessionId = itemsSessionIdRef.current;
      itemsSessionIdRef.current = sessionId;
      setItemsSessionId(sessionId);
      setItems((current) => updateLiveSessionItems(
        current,
        currentItemsSessionId,
        sessionId,
        (sessionItems) => appendCommandOutputDelta(sessionItems, event),
      ));
    }
    refreshCoordinator?.accept(event);
  }, []);

  useEffect(() => {
    selectedStreamAbortRef.current?.abort();
    const creationOwner = creationStreamOwnerRef.current;
    if (!selectedId) {
      creationOwner?.controller.abort();
      if (creationStreamOwnerRef.current === creationOwner) creationStreamOwnerRef.current = null;
      return;
    }
    if (
      creationOwner &&
      !creationOwner.controller.signal.aborted &&
      creationOwner.coreGeneration === coreGeneration &&
      creationOwner.sessionId === selectedId
    ) return;
    if (creationOwner?.sessionId && creationOwner.sessionId !== selectedId) {
      creationOwner.controller.abort();
      if (creationStreamOwnerRef.current === creationOwner) creationStreamOwnerRef.current = null;
    }

    const sessionId = selectedId;
    const controller = new AbortController();
    selectedStreamAbortRef.current = controller;
    const streamEpoch = streamEpochRef.current + 1;
    streamEpochRef.current = streamEpoch;
    setStreamConnection({ sessionId, state: "connecting", error: null });
    let reconnectAttempt = 0;
    const recovery = createStreamRecoveryBuffer();
    let refreshCoordinator: DurableRefreshCoordinator | null = null;
    let recoveryPromise: Promise<unknown> | null = null;

    const isCurrentStream = () => (
      !controller.signal.aborted &&
      coreGeneration === connectionGenerationRef.current &&
      streamEpoch === streamEpochRef.current &&
      selectedIdRef.current === sessionId
    );

    const setCurrentStreamState = (state: StreamState, error: string | null = null) => {
      if (!isCurrentStream()) return;
      setStreamConnection((current) => (
        current.sessionId === sessionId ? { ...current, state, error } : current
      ));
    };

    const applyEvent = (event: SessionEvent) => applyLiveSessionEvent(event, {
      sessionId,
      streamEpoch,
      isCurrent: isCurrentStream,
      refreshCoordinator,
    });

    refreshCoordinator = createDurableRefreshCoordinator(
      () => refreshSession(sessionId, controller.signal),
    );

    const listen = async () => {
      while (isCurrentStream()) {
        let openedAt: number | null = null;
        let receivedEvent = false;
        try {
          await core.streamEvents(sessionId, {
            signal: controller.signal,
            onOpen: () => {
              if (!isCurrentStream()) return;
              environmentEventRevisionRef.current.set(
                sessionId,
                (environmentEventRevisionRef.current.get(sessionId) ?? 0) + 1,
              );
              environmentRequestRef.current.set(
                sessionId,
                (environmentRequestRef.current.get(sessionId) ?? 0) + 1,
              );
              setEnvironmentObservations((current) => {
                if (!current.has(sessionId)) return current;
                const next = new Map(current);
                next.delete(sessionId);
                return next;
              });
              openedAt = beginStreamReconciliation(
                isCurrentStream,
                () => setCurrentStreamState("listening"),
                () => {
                  const token = recovery.begin();
                  recoveryPromise = refreshSession(sessionId, controller.signal)
                    .finally(() => recovery.finish(token, isCurrentStream, applyEvent));
                },
              );
            },
            onEvent: (event) => {
              if (!isCurrentStream()) return;
              receivedEvent = true;
              recovery.accept(event, applyEvent);
            },
          });
        } catch (error) {
          if (!isCurrentStream() || isAbort(error)) return;
          if (!shouldRetryStreamError(error)) {
            setCurrentStreamState("failed", errorMessage(error));
            const status = error instanceof AgentCoreError ? ` (${error.status})` : "";
            notify(
              `Live event stream was rejected by Agent Core${status}. Check the Core connection settings.`,
              "error",
            );
            return;
          }
        }

        if (!isCurrentStream()) return;
        await recoveryPromise;
        recoveryPromise = null;
        if (!isCurrentStream()) return;
        if (streamConnectionWasStable(openedAt, receivedEvent)) reconnectAttempt = 0;
        setCurrentStreamState("recovering");

        const delay = streamReconnectDelay(reconnectAttempt);
        reconnectAttempt += 1;
        if (!(await waitForStreamReconnect(delay, controller.signal))) return;
      }
    };

    void listen();

    return () => {
      refreshCoordinator?.dispose();
      recovery.invalidate();
      controller.abort();
      if (selectedStreamAbortRef.current === controller) selectedStreamAbortRef.current = null;
    };
  }, [applyLiveSessionEvent, core, coreGeneration, notify, refreshSession, selectedId, streamRetryRevision]);

  const retryCurrentStream = useCallback(() => {
    requestCurrentStreamRetry(selectedIdRef.current, (sessionId) => {
      setStreamConnection({ sessionId, state: "connecting", error: null });
      setStreamRetryRevision((current) => current + 1);
    });
  }, []);

  const run = async <T,>(operation: () => Promise<T>, success?: string): Promise<T | undefined> => {
    if (coreGeneration !== connectionGenerationRef.current) return undefined;
    const operationRequest = operationRequestRef.current + 1;
    operationRequestRef.current = operationRequest;
    setBusy(true);
    try {
      const result = await operation();
      if (coreGeneration !== connectionGenerationRef.current) return undefined;
      if (success) notify(success, "success");
      return result;
    } catch (error) {
      if (coreGeneration !== connectionGenerationRef.current) return undefined;
      notify(errorMessage(error), "error");
      throw error;
    } finally {
      if (
        coreGeneration === connectionGenerationRef.current &&
        operationRequest === operationRequestRef.current
      ) setBusy(false);
    }
  };

  const createAgent = async (input: CreateAgentInput) => {
    const agent = await run(() => core.createAgent(input), "Agent created.");
    if (!agent || coreGeneration !== connectionGenerationRef.current) return undefined;
    agentCollectionRevisionRef.current += 1;
    setAgents((current) => [agent, ...current]);
    return agent;
  };

  const retrieveAgent = async (agentId: string) => {
    return run(() => requestAgentDetail(core, agentId));
  };

  const updateAgent = async (agentId: string, input: UpdateAgentInput) => {
    const agent = await run(() => requestAgentUpdate(core, agentId, input), "Agent updated.");
    if (!agent || coreGeneration !== connectionGenerationRef.current) return undefined;
    agentCollectionRevisionRef.current += 1;
    setAgents((current) => replaceSavedAgent(current, agent));
    return agent;
  };

  const deleteAgent = async (agentId: string) => {
    const deleted = await run(() => requestAgentDelete(core, agentId), "Agent deleted.");
    if (!deleted || coreGeneration !== connectionGenerationRef.current) return;
    agentCollectionRevisionRef.current += 1;
    setAgents((current) => removeSavedAgent(current, agentId));
  };

  const runVaultMutation = async <T,>(operation: () => Promise<T>): Promise<T> => {
    if (coreGeneration !== connectionGenerationRef.current) {
      throw new Error("The Core connection changed before the Vault operation started.");
    }
    const operationRequest = operationRequestRef.current + 1;
    operationRequestRef.current = operationRequest;
    setBusy(true);
    try {
      const result = await operation();
      if (coreGeneration !== connectionGenerationRef.current) {
        throw new Error("The Core connection changed before the Vault operation was confirmed.");
      }
      return result;
    } finally {
      if (
        coreGeneration === connectionGenerationRef.current &&
        operationRequest === operationRequestRef.current
      ) setBusy(false);
    }
  };

  const refreshAfterVaultMutation = async () => {
    if (coreGeneration === connectionGenerationRef.current) await refreshVaults();
  };

  const vaultOperations: VaultOperations = {
    async createVault(name, metadata) {
      try {
        await runVaultMutation(() => requestVaultCreate(core, name, metadata));
        notify("Vault created.", "success");
      } catch (error) {
        await refreshAfterVaultMutation();
        throw error;
      }
      await refreshAfterVaultMutation();
    },
    async createCredential(vaultId, name, serverURL, token) {
      try {
        const created = await runVaultMutation(() => core.createVaultCredential(vaultId, {
          name,
          auth: { type: "static_bearer", mcp_server_url: serverURL, token },
        }));
        if (created.vault_id !== vaultId || created.name !== name || created.auth.mcp_server_url !== serverURL) {
          throw new Error("Agent Core returned mismatched Credential metadata.");
        }
        notify("Credential created. Token remains hidden.", "success");
      } catch (error) {
        await refreshAfterVaultMutation();
        throw error;
      }
      await refreshAfterVaultMutation();
    },
    async replaceCredential(vaultId, credentialId, token) {
      const baseline = vaultCatalog?.credentials.find((credential) => (
        credential.vault_id === vaultId && credential.id === credentialId
      ));
      if (!baseline || vaultCollectionState !== "ready") {
        throw new Error("The latest Credential metadata is unavailable. Refresh before replacing its token.");
      }
      try {
        const updated = await runVaultMutation(() => core.replaceVaultCredentialToken(vaultId, credentialId, {
          auth: { type: "static_bearer", token },
        }));
        if (
          updated.id !== baseline.id || updated.vault_id !== baseline.vault_id ||
          updated.name !== baseline.name || updated.auth.mcp_server_url !== baseline.auth.mcp_server_url ||
          updated.created_at !== baseline.created_at || updated.updated_at < baseline.updated_at
        ) throw new Error("Agent Core returned mismatched Credential metadata after replacement.");
        notify("Credential token replaced. Running work may still hold the previous token.", "success");
      } catch (error) {
        await refreshAfterVaultMutation();
        throw error;
      }
      await refreshAfterVaultMutation();
    },
    async deleteCredential(vaultId, credentialId) {
      try {
        await runVaultMutation(() => core.deleteVaultCredential(vaultId, credentialId));
        notify("Credential deleted. Provider-side token was not revoked.", "success");
      } catch (error) {
        let confirmedDeleted = false;
        try {
          await core.retrieveVaultCredential(vaultId, credentialId);
        } catch (readError) {
          confirmedDeleted = readError instanceof AgentCoreError && readError.status === 404;
        }
        await refreshAfterVaultMutation();
        if (confirmedDeleted) {
          notify("Credential deletion reconciled from Core.", "success");
          return;
        }
        throw error;
      }
      await refreshAfterVaultMutation();
    },
    async deleteVault(vaultId) {
      try {
        await runVaultMutation(() => core.deleteVault(vaultId));
        notify("Vault and its Credentials deleted. Provider-side tokens were not revoked.", "success");
      } catch (error) {
        let confirmedDeleted = false;
        try {
          await core.retrieveVault(vaultId);
        } catch (readError) {
          confirmedDeleted = readError instanceof AgentCoreError && readError.status === 404;
        }
        await refreshAfterVaultMutation();
        if (confirmedDeleted) {
          notify("Vault deletion reconciled from Core.", "success");
          return;
        }
        throw error;
      }
      await refreshAfterVaultMutation();
    },
    refresh() {
      void refreshVaults();
    },
  };

  const createEnvironmentTemplate = async (input: CreateEnvironmentTemplateInput) => {
    if (!__AGENTS_CORE_WEB_OPENAI_HOSTED_SESSIONS__) {
      throw new Error("Managed Environment configuration is not enabled for this Web build.");
    }
    const template = await core.createEnvironmentTemplate(input);
    // Re-read the durable collection so selection uses Core's own listing.
    await refreshEnvironmentTemplates();
    notify("Environment Template saved. No Runtime was allocated.", "success");
    return template;
  };

  const createSession = async (input: SessionStartInput) => {
    const submittedAgent = validateSessionAgentSubmission(
      input.agentMode,
      input.agentId,
      input.agent,
      agents,
      sessionVaultCatalog,
    );
    if (submittedAgent.error || !submittedAgent.effectiveAgent || !submittedAgent.requestAgent && input.agentMode === "inline") {
      const error = new Error(`Session was not created. ${submittedAgent.error ?? "The Agent request is invalid."}`);
      notify(error.message, "error");
      throw error;
    }
    const effectiveAgent = submittedAgent.effectiveAgent;
    const vaultPlan = deriveSessionVaultPlan(
      effectiveAgent,
      sessionVaultCatalog,
      input.manualVaultIds,
    );
    const expectedVaultIds = [...input.vaultIds].sort();
    if (
      vaultPlan.blocker ||
      vaultPlan.vaultIds.length !== expectedVaultIds.length ||
      vaultPlan.vaultIds.some((vaultId, index) => vaultId !== expectedVaultIds[index])
    ) {
      const error = new Error(`Session was not created. ${vaultPlan.blocker ?? "The derived Vault attachments changed before submission."}`);
      notify(error.message, "error");
      throw error;
    }
    if (input.environment.type === "self_hosted" && !__AGENTS_CORE_WEB_SELF_HOSTED_SESSIONS__) {
      const error = new Error("Session was not created. Self-hosted Sessions are not enabled for this Web build.");
      notify(error.message, "error");
      throw error;
    }
    if (input.environment.type === "openai_hosted" && !__AGENTS_CORE_WEB_OPENAI_HOSTED_SESSIONS__) {
      const error = new Error("Session was not created. Managed hosted Sessions are not enabled for this Web build.");
      notify(error.message, "error");
      throw error;
    }
    const normalizedEnvironment = normalizeSessionEnvironmentInput(input.environment);
    const environmentInput = normalizedEnvironment.input;
    if (!environmentInput) {
      const error = new Error(`Session was not created. ${normalizedEnvironment.error ?? "The Environment input is invalid."}`);
      notify(error.message, "error");
      throw error;
    }
    const environmentAdmissionBlocker = sessionEnvironmentAdmissionBlocker(effectiveAgent, environmentInput.type);
    if (environmentAdmissionBlocker) {
      const error = new Error(`Session was not created. ${environmentAdmissionBlocker}`);
      notify(error.message, "error");
      throw error;
    }
    const inputError = sessionInitialInputError(input.input, environmentInput.type);
    if (inputError) {
      const error = new Error(`Session was not created. ${inputError}`);
      notify(error.message, "error");
      throw error;
    }
    const request = sessionCreateRequestPayload({
      ...(input.agentMode === "saved" ? { agentId: input.agentId } : {}),
      ...(submittedAgent.requestAgent ? { agent: submittedAgent.requestAgent } : {}),
      environment: environmentInput,
      ...(input.input !== undefined ? { input: input.input } : {}),
      metadata: input.metadata,
      stream: input.stream,
      vaultIds: vaultPlan.vaultIds,
      sandboxNodeId: input.sandboxNodeId,
    });

    const openSession = (session: AgentSession) => {
      sessionCollectionRevisionRef.current += 1;
      sessionEnvironmentIdentityRef.current.set(session.id, supportedEnvironmentIdentity(session.environment));
      setSessions((current) => {
        const next = [session, ...current.filter((value) => value.id !== session.id)];
        sessionsRef.current = next;
        return next;
      });
      changeSessionAgentFilter(null);
      selectedIdRef.current = session.id;
      setSelectedId(session.id);
      setItems([]);
      itemsSessionIdRef.current = session.id;
      setItemsSessionId(session.id);
      setTurns([]);
      turnsSessionIdRef.current = session.id;
      setTurnsSessionId(session.id);
      setView("sessions");
    };

    if (!input.stream) {
      const session = await run(() => core.createSession(
        { ...request, stream: false },
        input.idempotencyKey,
      ));
      if (!session || coreGeneration !== connectionGenerationRef.current) {
        throw new Error("The Session creation outcome could not be confirmed.");
      }
      openSession(session);
      notify(input.input === undefined ? "Idle Session created. Opening live events…" : "Session opened. Connecting live events…", "success");
      return;
    }

    const generation = coreGeneration;
    const operationRequest = operationRequestRef.current + 1;
    operationRequestRef.current = operationRequest;
    setBusy(true);
    creationStreamOwnerRef.current?.controller.abort();
    const owner: CreationStreamOwner = {
      controller: new AbortController(),
      coreGeneration: generation,
      sessionId: null,
      streamEpoch: null,
    };
    creationStreamOwnerRef.current = owner;
    const recovery = createStreamRecoveryBuffer();
    let refreshCoordinator: DurableRefreshCoordinator | null = null;
    let recoveryPromise: Promise<unknown> | null = null;
    let resolveCreated: (session: AgentSession) => void = () => undefined;
    let rejectCreated: (error: unknown) => void = () => undefined;
    const created = new Promise<AgentSession>((resolve, reject) => {
      resolveCreated = resolve;
      rejectCreated = reject;
    });
    const isCurrentCreationStream = () => (
      creationStreamOwnerRef.current === owner &&
      !owner.controller.signal.aborted &&
      generation === connectionGenerationRef.current &&
      owner.sessionId !== null &&
      owner.streamEpoch !== null &&
      streamEpochRef.current === owner.streamEpoch &&
      selectedIdRef.current === owner.sessionId
    );
    const applyCreationEvent = (event: SessionEvent) => {
      if (!owner.sessionId || owner.streamEpoch === null) return;
      applyLiveSessionEvent(event, {
        sessionId: owner.sessionId,
        streamEpoch: owner.streamEpoch,
        isCurrent: isCurrentCreationStream,
        refreshCoordinator,
      });
    };
    const handoffToGetStream = async () => {
      const sessionId = owner.sessionId;
      if (!sessionId) return;
      owner.startReconciliation?.();
      await recoveryPromise;
      if (!isCurrentCreationStream()) return;
      setStreamConnection({ sessionId, state: "recovering", error: null });
      await refreshSession(sessionId, owner.controller.signal);
      if (!isCurrentCreationStream()) return;
      refreshCoordinator?.dispose();
      recovery.invalidate();
      if (creationStreamOwnerRef.current === owner) creationStreamOwnerRef.current = null;
      owner.controller.abort();
      setStreamRetryRevision((current) => current + 1);
    };

    void core.createSessionStream(request, input.idempotencyKey, {
      signal: owner.controller.signal,
      onSession: (session) => {
        if (
          creationStreamOwnerRef.current !== owner ||
          owner.controller.signal.aborted ||
          generation !== connectionGenerationRef.current
        ) return;
        owner.sessionId = session.id;
        owner.streamEpoch = streamEpochRef.current + 1;
        streamEpochRef.current = owner.streamEpoch;
        const token = recovery.begin();
        refreshCoordinator = createDurableRefreshCoordinator(
          () => refreshSession(session.id, owner.controller.signal),
        );
        owner.startReconciliation = () => {
          if (recoveryPromise || !isCurrentCreationStream()) return;
          recoveryPromise = refreshSession(session.id, owner.controller.signal)
            .finally(() => recovery.finish(token, isCurrentCreationStream, applyCreationEvent));
        };
        openSession(session);
        setStreamConnection({ sessionId: session.id, state: "listening", error: null });
        owner.startReconciliation();
        resolveCreated(session);
      },
      onOpen: () => undefined,
      onEvent: (event) => {
        if (event.type === "agent.session.created") return;
        recovery.accept(event, applyCreationEvent);
      },
    }).then(
      () => handoffToGetStream(),
      (error: unknown) => {
        if (!owner.sessionId) {
          recovery.invalidate();
          refreshCoordinator?.dispose();
          if (creationStreamOwnerRef.current === owner) creationStreamOwnerRef.current = null;
          owner.controller.abort();
          rejectCreated(error);
          return;
        }
        return handoffToGetStream();
      },
    ).catch((error: unknown) => {
      if (
        generation === connectionGenerationRef.current &&
        owner.sessionId &&
        selectedIdRef.current === owner.sessionId
      ) {
        setStreamConnection({ sessionId: owner.sessionId, state: "failed", error: errorMessage(error) });
      }
    });

    try {
      await created;
      if (generation !== connectionGenerationRef.current) {
        throw new Error("The Session creation outcome could not be confirmed after the Core connection changed.");
      }
      notify(
        input.input === undefined
          ? input.environment.type === "openai_hosted"
            ? "Managed hosted Session created. Live creation events are connected."
            : "Idle Session created. Live creation events are connected."
          : "Session created with initial input. Live creation events are connected.",
        "success",
      );
    } catch (error) {
      if (generation === connectionGenerationRef.current) notify(errorMessage(error), "error");
      throw error;
    } finally {
      if (
        generation === connectionGenerationRef.current &&
        operationRequest === operationRequestRef.current
      ) setBusy(false);
    }
  };

  const retrieveSessionForAction = useCallback(async (sessionId: string) => {
    const generation = coreGeneration;
    try {
      const session = await requestSessionDetail(core, sessionId);
      return generation === connectionGenerationRef.current ? session : undefined;
    } catch (error) {
      if (generation !== connectionGenerationRef.current) return undefined;
      throw error;
    }
  }, [core, coreGeneration]);

  const updateSessionMetadata = async (
    sessionId: string,
    baselineMetadata: Record<string, string>,
    draftMetadata: Record<string, string>,
  ): Promise<AgentSession | undefined> => {
    const generation = coreGeneration;
    let updated: AgentSession;
    try {
      updated = await requestSessionUpdate(core, sessionId, baselineMetadata, draftMetadata);
    } catch (error) {
      if (generation !== connectionGenerationRef.current) return undefined;
      if (error instanceof SessionMetadataConflictError && error.latestSession) {
        sessionCollectionRevisionRef.current += 1;
        sessionEventRevisionRef.current.set(
          sessionId,
          (sessionEventRevisionRef.current.get(sessionId) ?? 0) + 1,
        );
        setSessions((current) => {
          const next = replaceSessionMetadata(current, error.latestSession as AgentSession);
          sessionsRef.current = next;
          return next;
        });
        setFilteredSessions((current) => {
          const next = replaceSessionMetadata(current, error.latestSession as AgentSession);
          filteredSessionsRef.current = next;
          return next;
        });
      }
      throw error;
    }
    if (generation !== connectionGenerationRef.current) {
      notify("The previous Core returned a Session update after the connection changed. The current Core view was not modified.", "error");
      return undefined;
    }
    sessionCollectionRevisionRef.current += 1;
    sessionEventRevisionRef.current.set(
      sessionId,
      (sessionEventRevisionRef.current.get(sessionId) ?? 0) + 1,
    );
    setSessions((current) => {
      const next = replaceSessionMetadata(current, updated);
      sessionsRef.current = next;
      return next;
    });
    setFilteredSessions((current) => {
      const next = replaceSessionMetadata(current, updated);
      filteredSessionsRef.current = next;
      return next;
    });
    notify("Session metadata updated.", "success");
    return updated;
  };

  const removeSessionFromWorkspace = (sessionId: string, message: string): boolean => {
    const selectedAtCompletion = selectedIdRef.current;
    const deletingSelected = selectedAtCompletion === sessionId;
    const nextSelectedId = selectionAfterSessionDelete(
      sessionAgentFilterRef.current ? filteredSessionsRef.current : sessionsRef.current,
      selectedAtCompletion,
      sessionId,
    );
    const increment = (revisions: Map<string, number>) => {
      revisions.set(sessionId, (revisions.get(sessionId) ?? 0) + 1);
    };
    sessionCollectionRevisionRef.current += 1;
    increment(sessionRequestRef.current);
    increment(sessionEventRevisionRef.current);
    increment(itemEventRevisionRef.current);
    increment(turnEventRevisionRef.current);
    increment(environmentEventRevisionRef.current);
    increment(environmentRequestRef.current);
    sessionEnvironmentIdentityRef.current.delete(sessionId);

    setSessions((current) => {
      const next = removeSession(current, sessionId);
      sessionsRef.current = next;
      return next;
    });
    setFilteredSessions((current) => {
      const next = removeSession(current, sessionId);
      filteredSessionsRef.current = next;
      return next;
    });
    setSessionSendFailures((current) => {
      if (!current.has(sessionId)) return current;
      const next = new Map(current);
      next.delete(sessionId);
      return next;
    });
    setEnvironmentObservations((current) => {
      if (!current.has(sessionId)) return current;
      const next = new Map(current);
      next.delete(sessionId);
      return next;
    });

    if (deletingSelected) {
      selectedIdRef.current = nextSelectedId;
      streamEpochRef.current += 1;
      selectedSessionReadAbortRef.current?.abort();
      selectedSessionReadAbortRef.current = null;
      selectedStreamAbortRef.current?.abort();
      selectedStreamAbortRef.current = null;
      if (creationStreamOwnerRef.current?.sessionId === sessionId) {
        creationStreamOwnerRef.current.controller.abort();
        creationStreamOwnerRef.current = null;
      }
      itemsSessionIdRef.current = null;
      turnsSessionIdRef.current = null;
      setItems([]);
      setItemsSessionId(null);
      setTurns([]);
      setTurnsSessionId(null);
      setSelectedSessionLoad({ sessionId: null, state: "idle", error: null });
      setTurnCollectionLoad({ sessionId: null, state: "idle", error: null });
      setStreamConnection({ sessionId: null, state: "idle", error: null });
      setSelectedId(nextSelectedId);
    }
    notify(message, "success");
    return true;
  };

  const deleteSessionFromCore = async (sessionId: string): Promise<boolean> => {
    const generation = coreGeneration;
    try {
      await requestSessionDelete(core, sessionId);
    } catch (error) {
      if (generation !== connectionGenerationRef.current) return false;
      if (!(error instanceof SessionActionError) || error.kind !== "unknown_write") throw error;

      const reconciliation = await reconcileUnknownSessionDelete(core, sessionId);
      if (generation !== connectionGenerationRef.current) return false;
      if (reconciliation.state === "missing") {
        return removeSessionFromWorkspace(
          sessionId,
          "Session is absent from Agent Core after reconciling the unknown deletion result.",
        );
      }
      if (reconciliation.state === "unknown") {
        throw new SessionActionError(
          `${error.message} The follow-up durable Session refresh also failed, so the result remains unknown.`,
          "unknown_write",
          { cause: error },
        );
      }
      throw new SessionActionError(
        `${error.message} A follow-up durable Session refresh confirmed that the Session is still present.`,
        "request_failed",
        { cause: error },
      );
    }
    if (generation !== connectionGenerationRef.current) {
      notify("The previous Core confirmed Session deletion after the connection changed. The current Core view was not modified.", "error");
      return false;
    }
    return removeSessionFromWorkspace(sessionId, "Session deleted from Agent Core.");
  };

  const sendMessage = async (text: string) => {
    const sessionId = selectedId;
    if (!sessionId) return;
    if (!streamReady) {
      const message = "Wait for the live event stream to connect before sending.";
      notify(message, "error");
      throw new Error(message);
    }
    const previousFailure = sessionSendFailures.get(sessionId);
    const pending = beginPendingSend(sessionId, text, previousFailure);
    setSessionSendFailures((current) => {
      if (!current.has(sessionId)) return current;
      const next = new Map(current);
      next.delete(sessionId);
      return next;
    });
    try {
      await run(() => core.sendMessage(sessionId, text, pending.idempotencyKey));
    } catch (error) {
      if (coreGeneration === connectionGenerationRef.current) {
        setSessionSendFailures((current) => {
          const next = new Map(current);
          next.set(sessionId, failPendingSend(pending, error, errorMessage(error)));
          return next;
        });
      }
      throw error;
    }
    if (coreGeneration !== connectionGenerationRef.current || selectedIdRef.current !== sessionId) return;
    setSessionSendFailures((current) => {
      if (!current.has(sessionId)) return current;
      const next = new Map(current);
      next.delete(sessionId);
      return next;
    });
    await refreshSelectedSession(sessionId);
  };

  const cancel = async () => {
    const sessionId = selectedId;
    if (!sessionId) return;
    const pending = beginPendingSend(
      sessionId,
      "agent.session.input.cancel",
      cancelFailureRef.current,
    );
    cancelFailureRef.current = undefined;
    try {
      await run(
        () => core.cancelTurn(sessionId, pending.idempotencyKey),
        "Cancellation requested.",
      );
    } catch (error) {
      if (
        coreGeneration === connectionGenerationRef.current &&
        selectedIdRef.current === sessionId
      ) cancelFailureRef.current = failPendingSend(pending, error, errorMessage(error));
      throw error;
    }
    if (coreGeneration !== connectionGenerationRef.current || selectedIdRef.current !== sessionId) return;
    cancelFailureRef.current = undefined;
    await refreshSelectedSession(sessionId);
  };

  const submitFunctionResult = async (input: FunctionResultInput) => {
    const sessionId = selectedId;
    if (!sessionId) return;
    const actionKey = functionResultActionKey(sessionId, input);
    const pending = beginPendingFunctionResult(
      sessionId,
      input,
      functionResultFailuresRef.current.get(actionKey),
    );
    functionResultFailuresRef.current.delete(actionKey);
    try {
      await run(
        () => core.submitFunctionResult(sessionId, input, pending.idempotencyKey),
        "Function result submitted.",
      );
    } catch (error) {
      if (
        coreGeneration === connectionGenerationRef.current &&
        selectedIdRef.current === sessionId
      ) {
        functionResultFailuresRef.current.set(
          actionKey,
          failPendingFunctionResult(pending, error, errorMessage(error)),
        );
      }
      throw error;
    }
    if (coreGeneration !== connectionGenerationRef.current || selectedIdRef.current !== sessionId) return;
    functionResultFailuresRef.current.delete(actionKey);
    await refreshSelectedSession(sessionId);
  };

  const listEnvironmentFiles = useCallback((
    environmentId: string,
    options: Parameters<AgentCore["listEnvironmentFiles"]>[1],
  ) => core.listEnvironmentFiles(environmentId, options), [core]);

  const createEnvironmentFile = useCallback<AgentCore["createEnvironmentFile"]>((
    environmentId,
    input,
    options,
  ) => core.createEnvironmentFile(environmentId, input, options), [core]);

  const applyConnection = (next: CoreConnection) => {
    const normalized = { ...next, baseUrl: next.baseUrl.trim() || "/v1", token: next.token.trim() };
    connectionGenerationRef.current += 1;
    agentCollectionRequestRef.current += 1;
    sessionCollectionRequestRef.current += 1;
    runtimeCollectionRequestRef.current += 1;
    filteredSessionCollectionRequestRef.current += 1;
    vaultCollectionRequestRef.current += 1;
    startupConfigurationRequestRef.current += 1;
    agentCollectionRevisionRef.current = 0;
    sessionCollectionRevisionRef.current = 0;
    sessionRequestRef.current.clear();
    sessionEventRevisionRef.current.clear();
    itemEventRevisionRef.current.clear();
    turnEventRevisionRef.current.clear();
    environmentEventRevisionRef.current.clear();
    environmentRequestRef.current.clear();
    sessionEnvironmentIdentityRef.current.clear();
    operationRequestRef.current += 1;
    streamEpochRef.current += 1;
    selectedSessionReadAbortRef.current?.abort();
    selectedSessionReadAbortRef.current = null;
    selectedStreamAbortRef.current?.abort();
    selectedStreamAbortRef.current = null;
    creationStreamOwnerRef.current?.controller.abort();
    creationStreamOwnerRef.current = null;
    agentCollectionAbortRef.current?.abort();
    agentCollectionAbortRef.current = null;
    sessionCollectionAbortRef.current?.abort();
    sessionCollectionAbortRef.current = null;
    runtimeCollectionAbortRef.current?.abort();
    runtimeCollectionAbortRef.current = null;
    filteredSessionCollectionAbortRef.current?.abort();
    filteredSessionCollectionAbortRef.current = null;
    vaultCollectionAbortRef.current?.abort();
    vaultCollectionAbortRef.current = null;
    startupConfigurationAbortRef.current?.abort();
    startupConfigurationAbortRef.current = null;
    environmentTemplateAbortRef.current?.abort();
    environmentTemplateAbortRef.current = null;
    setEnvironmentTemplates(null);
    saveConnection(normalized);
    setBusy(false);
    setAgentCollectionState("connecting");
    setAgentCollectionError(null);
    setAgentCollectionHasSnapshot(false);
    setSessionCollectionState("connecting");
    setSessionCollectionError(null);
    setSessionCollectionHasSnapshot(false);
    setRuntimeSnapshot(null);
    runtimeCollectionHasSnapshotRef.current = false;
    setRuntimeCollectionState("connecting");
    setRuntimeCollectionError(null);
    setRuntimeCollectionHasSnapshot(false);
    sessionAgentFilterRef.current = null;
    filteredSessionsRef.current = [];
    setSessionAgentFilter(null);
    setFilteredSessions([]);
    setFilteredSessionCollectionState("connecting");
    setFilteredSessionCollectionError(null);
    setVaultCollectionState("connecting");
    setVaultCollectionError(null);
    setVaultSupported(null);
    setVaultCatalog(null);
    setStartupConfiguration(null);
    setStartupConfigurationState("connecting");
    setStartupConfigurationSupported(null);
    setAgents([]);
    setSessions([]);
    setItems([]);
    setSelectedId(null);
    setSelectedSessionLoad({ sessionId: null, state: "idle", error: null });
    setTurnCollectionLoad({ sessionId: null, state: "idle", error: null });
    setStreamConnection({ sessionId: null, state: "idle", error: null });
    setSessionSendFailures(new Map());
    cancelFailureRef.current = undefined;
    functionResultFailuresRef.current.clear();
    setEnvironmentObservations(new Map());
    itemsSessionIdRef.current = null;
    setItemsSessionId(null);
    turnsSessionIdRef.current = null;
    setTurnsSessionId(null);
    setTurns([]);
    setConnection(normalized);
    setConnectionOpen(false);
  };

  const openAgentSetup = () => {
    setView("agents");
    agentCreateSequenceRef.current += 1;
    setAgentCreateRequest(agentCreateSequenceRef.current);
  };

  const openSessionSetup = (agentId?: string) => {
    if (agentId && !agents.some((candidate) => candidate.id === agentId)) {
      notify("Session setup could not open because the selected saved Agent is not loaded.", "error");
      return;
    }
    setView("sessions");
    sessionCreateSequenceRef.current += 1;
    setSessionCreateRequest({ agentId: agentId ?? null, requestId: sessionCreateSequenceRef.current });
  };

  const consumeAgentCreateRequest = useCallback((request: number) => {
    setAgentCreateRequest((current) => current === request ? null : current);
  }, []);

  const consumeSessionCreateRequest = useCallback((request: number) => {
    setSessionCreateRequest((current) => current?.requestId === request ? null : current);
  }, []);

  return (
    <SandboxProvider connection={connection}><div className="app-shell">
      <a className="skip-link" href="#main-content">Skip to main content</a>
      <aside className="app-sidebar">
        <div className="brand-lockup">
          <span className="brand-mark-frame">
            <img className="brand-mark brand-mark-light" src="/parsar-mark-light.png" width="18" height="18" alt="" aria-hidden="true" />
            <img className="brand-mark brand-mark-dark" src="/parsar-mark-dark.png" width="18" height="18" alt="" aria-hidden="true" />
          </span>
          <span className="brand-name">Agents Core Web</span>
        </div>

        <ProductNavigation
          active={view === "system" || view === "sandbox" ? null : view}
          onSelect={(nextView) => setView(nextView)}
          showVaults={vaultSupported === true}
          showTemplates={__AGENTS_CORE_WEB_OPENAI_HOSTED_SESSIONS__}
        />

        <SystemNavigation active={view === "system" || view === "sandbox" ? view : null} onSelect={setView} />

        <div className="sidebar-footer">
          <button
            className="core-switcher"
            type="button"
            onClick={() => setConnectionOpen(true)}
            aria-label="Configure Agent Core connection"
          >
            <StatusIcon
              status={coreState === "ready" ? "completed" : coreState === "failed" ? "failed" : "running"}
              title={`Core API ${coreState}`}
            />
            <span>
              <strong>Agent Core</strong>
              <small>{coreState === "connecting" ? "Connecting…" : coreState === "ready" ? "API ready" : coreState}</small>
            </span>
            <Settings2 size={14} strokeWidth={1.5} />
          </button>
          <ThemeMenu />
        </div>
      </aside>

      <main className="app-main" id="main-content" tabIndex={-1}>
        <header className="product-header">
          <CreateMenu
            canCreateAgent={agentCollectionState === "ready" && !busy}
            canStartSession={sessionCollectionState === "ready" && !busy}
            onCreateAgent={openAgentSetup}
            onStartSession={() => openSessionSetup()}
          />
        </header>
        <div className="page-transition" key={view}>
          {view === "templates" ? (
            <EnvironmentTemplatesView
              key={`templates:${coreGeneration}`}
              catalog={environmentTemplates}
              operations={core}
              onRefresh={refreshEnvironmentTemplates}
              onConfigureConnection={() => setConnectionOpen(true)}
            />
          ) : null}
          {view === "dashboard" ? (
            <DashboardView
              agents={agents}
              sessions={sessions}
              agentCollectionState={agentCollectionState}
              agentCollectionError={agentCollectionError}
              agentCollectionHasSnapshot={agentCollectionHasSnapshot}
              sessionCollectionState={sessionCollectionState}
              sessionCollectionError={sessionCollectionError}
              sessionCollectionHasSnapshot={sessionCollectionHasSnapshot}
              runtimeSnapshot={runtimeSnapshot}
              runtimeCollectionState={runtimeCollectionState}
              runtimeCollectionError={runtimeCollectionError}
              runtimeCollectionHasSnapshot={runtimeCollectionHasSnapshot}
              loadRuntimeHistory={loadDashboardRuntimeHistory}
              onRefresh={refreshDashboard}
              onCreateAgent={openAgentSetup}
              onStartSession={() => openSessionSetup()}
              onViewAgents={() => setView("agents")}
              onViewSessions={() => setView("sessions")}
              onConfigureConnection={() => setConnectionOpen(true)}
              onOpenSession={(sessionId) => {
                changeSessionAgentFilter(null);
                selectedIdRef.current = sessionId;
                setSelectedId(sessionId);
                setView("sessions");
              }}
            />
          ) : null}
          {view === "sessions" ? (
            <SessionsView
              key={`sessions:${coreGeneration}`}
              agents={agents}
              agentFilter={sessionAgentFilter}
              sessions={sessionBrowserSessions}
              selected={selected}
              items={itemsSessionId === selectedId ? items : []}
              turns={turnsSessionId === selectedId ? turns : []}
              busy={busy}
              coreError={sessionBrowserError}
              coreState={sessionBrowserState}
              createRequest={sessionCreateRequest}
              onCreateRequestConsumed={consumeSessionCreateRequest}
              detailError={detailError}
              detailState={detailState}
              turnError={turnError}
              turnState={turnState}
              environmentObservation={environmentObservation}
              sendError={sendError}
              streamError={streamError}
              streamState={streamState}
              selfHostedEnabled={__AGENTS_CORE_WEB_SELF_HOSTED_SESSIONS__}
              openAIHostedEnabled={__AGENTS_CORE_WEB_OPENAI_HOSTED_SESSIONS__}
              environmentTemplates={environmentTemplates}
              onCreateEnvironmentTemplate={createEnvironmentTemplate}
              vaultCatalog={sessionVaultCatalog}
              onCancel={cancel}
              onAgentFilterChange={changeSessionAgentFilter}
              onCreateSession={createSession}
              onDeleteSession={deleteSessionFromCore}
              onFunctionResult={submitFunctionResult}
              onListEnvironmentFiles={listEnvironmentFiles}
              onCreateEnvironmentFile={createEnvironmentFile}
              onRefresh={recoverSessionWorkspace}
              onRetrySession={() => {
                if (selectedId) void refreshSelectedSession(selectedId);
              }}
              onRetryStream={retryCurrentStream}
              onRetrieveSession={retrieveSessionForAction}
              onSelect={setSelectedId}
              onSend={sendMessage}
              onUpdateSession={updateSessionMetadata}
            />
          ) : null}
          {view === "agents" ? (
            <AgentsView
              key={`agents:${coreGeneration}`}
              agents={agents}
              busy={busy}
              coreBaseUrl={connection.baseUrl}
              coreError={agentCollectionError}
              coreState={agentCollectionState}
              startupConfiguration={startupConfigurationState === "ready" && startupConfigurationSupported === true ? startupConfiguration : null}
              vaultCatalog={sessionVaultCatalog}
              createRequest={agentCreateRequest ?? 0}
              onCreateRequestConsumed={consumeAgentCreateRequest}
              onCreate={createAgent}
              onDelete={deleteAgent}
              onRefresh={() => void refreshAgents()}
              onRetrieve={retrieveAgent}
              onStartSession={openSessionSetup}
              onUpdate={updateAgent}
            />
          ) : null}
          {view === "vaults" && vaultSupported === true ? (
            <VaultsView
              key={`vaults:${coreGeneration}`}
              busy={busy}
              catalog={vaultCatalog}
              coreError={vaultCollectionError}
              coreState={vaultCollectionState}
              operations={vaultOperations}
            />
          ) : null}
          {view === "sandbox" ? <SandboxManagerView key={`sandbox:${coreGeneration}`} coreBaseUrl={connection.baseUrl} /> : null}
          {view === "system" ? (
            <SystemView
              key={`system:${coreGeneration}`}
              startupConfiguration={startupConfiguration}
              startupConfigurationState={startupConfigurationState}
              startupConfigurationSupported={startupConfigurationSupported}
              selfHostedWebEnabled={__AGENTS_CORE_WEB_SELF_HOSTED_SESSIONS__}
              managedWebEnabled={__AGENTS_CORE_WEB_OPENAI_HOSTED_SESSIONS__}
              refreshing={startupConfigurationState === "connecting"}
              onRefresh={refreshSystem}
            />
          ) : null}
        </div>
      </main>

      <ConnectionModal
        connection={connection}
        open={connectionOpen}
        onClose={() => setConnectionOpen(false)}
        onSave={applyConnection}
      />
    </div></SandboxProvider>
  );
}
