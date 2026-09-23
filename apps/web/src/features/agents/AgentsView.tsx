import { RefreshCw, Search } from "lucide-react";
import { useEffect, useRef, useState } from "react";

import type { CoreStartupConfiguration, CreateAgentInput, SavedAgent, UpdateAgentInput } from "@agents-core-web/agents-client";

import { ErrorState } from "../../components/ErrorState";
import { Skeleton } from "../../components/Skeleton";
import type { CoreConnectionState } from "../../lib/connection";
import type { VaultCatalog } from "../vaults/vault-catalog";
import { AgentCatalog } from "./AgentCatalog";
import { AgentDialog } from "./AgentDialog";
import { AgentSetupView } from "./AgentSetupView";
import { createRequestGate, projectSavedTool } from "./agent-form";
import { type AgentTemplate, valuesFromAgentTemplate } from "./agent-templates";
import { sessionAdmissionBlocker } from "./session-admission";

interface AgentsViewProps {
  openAgentId?: string;
  onOpenAgentConsumed?: (id: string) => void;
  agents: SavedAgent[];
  busy: boolean;
  coreBaseUrl?: string;
  coreError: string | null;
  coreState: CoreConnectionState;
  startupConfiguration?: CoreStartupConfiguration | null;
  vaultCatalog?: VaultCatalog | null;
  createRequest?: number;
  onCreateRequestConsumed?: (request: number) => void;
  onCreate: (input: CreateAgentInput) => Promise<SavedAgent | undefined>;
  onDelete?: (agentId: string) => Promise<void>;
  onRefresh: () => void;
  onRetrieve?: (agentId: string) => Promise<SavedAgent | undefined>;
  onStartSession: (agentId: string) => void;
  onUpdate?: (agentId: string, input: UpdateAgentInput) => Promise<SavedAgent | undefined>;
}

type ViewMode = "closed" | "create" | "edit" | "delete";

type ReturnFocusTarget =
  | { kind: "agent"; id: string }
  | { kind: "create" }
  | { kind: "element"; element: HTMLElement }
  | { kind: "template"; id: string };

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : "The Agent Core request failed.";
}

function AgentsLoadingSkeleton() {
  return (
    <div className="agents-loading" aria-busy="true" aria-label="Loading Agents">
      <div className="agents-loading-header" />
      {Array.from({ length: 5 }).map((_, index) => (
        <div className="agents-loading-row" key={index}>
          <Skeleton className="skeleton-status" />
          <Skeleton className="skeleton-tile" />
          <Skeleton className="skeleton-agent-name" />
          <Skeleton />
          <Skeleton className="skeleton-model" />
          <Skeleton className="skeleton-age" />
        </div>
      ))}
    </div>
  );
}

function formatTimestamp(seconds: number): string {
  return new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "medium" }).format(new Date(seconds * 1000));
}

function StructuredValue({ value }: { value: unknown }) {
  return <pre className="agent-structured-value">{JSON.stringify(value, null, 2)}</pre>;
}

function ToolSummary({ tools, vaultCatalog }: { tools: unknown[]; vaultCatalog: VaultCatalog | null }) {
  if (tools.length === 0) return <span className="agent-null-value">None</span>;
  return (
    <ul className="agent-tool-summary">
      {tools.map((rawTool, index) => {
        const projection = projectSavedTool(rawTool, vaultCatalog);
        if (projection.kind === "function") {
          return <li key={index}>Function <code>{projection.name}</code></li>;
        }
        if (projection.kind === "mcp") {
          return <li key={index}>{projection.credentialId ? "Vault bearer" : "Anonymous"} service-origin HTTP MCP <code>{projection.serverLabel}</code></li>;
        }
        return <li key={index}>{projection.label} · read only</li>;
      })}
    </ul>
  );
}

export function AgentDetails({ agent, vaultCatalog = null }: { agent: SavedAgent; vaultCatalog?: VaultCatalog | null }) {
  const blocker = sessionAdmissionBlocker(agent, vaultCatalog);
  return (
    <div className="agent-details">
      <div className="agent-detail-summary">
        <dl>
          <div><dt>ID</dt><dd><code>{agent.id}</code></dd></div>
          <div><dt>Created</dt><dd><time dateTime={new Date(agent.created_at * 1000).toISOString()}>{formatTimestamp(agent.created_at)}</time></dd></div>
          <div><dt>Updated</dt><dd><time dateTime={new Date(agent.updated_at * 1000).toISOString()}>{formatTimestamp(agent.updated_at)}</time></dd></div>
          <div><dt>Model</dt><dd><code>{agent.model}</code></dd></div>
          <div><dt>Name</dt><dd>{agent.name ?? <span className="agent-null-value">Not set</span>}</dd></div>
          <div><dt>Instructions</dt><dd>{agent.instructions ?? <span className="agent-null-value">Not set</span>}</dd></div>
          <div><dt>Metadata</dt><dd><StructuredValue value={agent.metadata} /></dd></div>
        </dl>
      </div>
      <div className="agent-capability-warning" role="note">
        {blocker
          ? `This Agent can be saved, but the known Core Session profile cannot start it: ${blocker}`
          : "Saved advanced configuration is not runtime proof. Model, provider, host, tools, and conditional verbosity still require executor validation."}
      </div>
      <section className="agent-capabilities" aria-labelledby="agent-capabilities-title">
        <h3 id="agent-capabilities-title">Advanced configuration · read only</h3>
        <dl>
          <div><dt>Tools</dt><dd><ToolSummary tools={agent.tools} vaultCatalog={vaultCatalog} /></dd></div>
          <div><dt>Reasoning</dt><dd><StructuredValue value={agent.reasoning} /></dd></div>
          <div><dt>Text</dt><dd><StructuredValue value={agent.text} /></dd></div>
          <div><dt>Service tier</dt><dd><code>{agent.service_tier}</code></dd></div>
          <div><dt>Multi-agent</dt><dd><StructuredValue value={agent.multi_agent} /></dd></div>
        </dl>
      </section>
    </div>
  );
}

export function AgentDeleteConfirmation({ agent }: { agent: SavedAgent }) {
  return (
    <div className="agent-delete-confirmation">
      <p>Delete <strong>{agent.name || "Untitled Agent"}</strong> from Agent Core?</p>
      <p>Exact Agent ID: <code>{agent.id}</code></p>
      <p>This removes the saved Agent only after Core confirms success. Existing Sessions keep their durable Agent snapshots.</p>
    </div>
  );
}

export function AgentsView({
  openAgentId,
  onOpenAgentConsumed,
  agents,
  busy,
  coreBaseUrl = "/v1",
  coreError,
  coreState,
  startupConfiguration = null,
  vaultCatalog = null,
  createRequest = 0,
  onCreateRequestConsumed,
  onCreate,
  onDelete,
  onRefresh,
  onRetrieve,
  onStartSession,
  onUpdate,
}: AgentsViewProps) {
  const [mode, setMode] = useState<ViewMode>("closed");
  const [selectedAgent, setSelectedAgent] = useState<SavedAgent | null>(null);
  const [selectedTemplate, setSelectedTemplate] = useState<AgentTemplate | null>(null);
  const [openingAgentId, setOpeningAgentId] = useState<string | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);
  const [query, setQuery] = useState("");
  const [showAllAgents, setShowAllAgents] = useState(false);
  const [createSetupRevision, setCreateSetupRevision] = useState(0);
  const requestGate = useRef(createRequestGate());
  const returnFocusRef = useRef<ReturnFocusTarget | null>(null);
  const failedOpenFocusRef = useRef<ReturnFocusTarget | null>(null);
  const lastCreateRequestRef = useRef(0);
  const lastOpenAgentRef = useRef<string | undefined>(undefined);
  const knownModels = agents.map((agent) => agent.model);
  const normalizedQuery = query.trim().toLowerCase();
  const filteredAgents = normalizedQuery
    ? agents.filter((agent) => [agent.name, agent.model, agent.instructions, agent.id]
      .some((value) => value?.toLowerCase().includes(normalizedQuery)))
    : agents;

  const restoreCatalogFocus = (target: ReturnFocusTarget | null) => {
    window.requestAnimationFrame(() => {
      if (target?.kind === "element" && target.element.isConnected) {
        target.element.focus();
        return;
      }
      if (target?.kind === "agent") {
        const button = [...document.querySelectorAll<HTMLButtonElement>("button[data-agent-id]")]
          .find((candidate) => candidate.dataset.agentId === target.id);
        if (button) {
          button.focus();
          return;
        }
      }
      if (target?.kind === "template") {
        const button = [...document.querySelectorAll<HTMLButtonElement>("button[data-agent-template-id]")]
          .find((candidate) => candidate.dataset.agentTemplateId === target.id);
        if (button) {
          button.focus();
          return;
        }
      }
      document.querySelector<HTMLButtonElement>('button[data-create-agent-entry="true"]')?.focus();
    });
  };

  const closeSetup = () => {
    if (busy) return;
    const target = returnFocusRef.current;
    returnFocusRef.current = null;
    requestGate.current.invalidate();
    setMode("closed");
    setSelectedAgent(null);
    setSelectedTemplate(null);
    setActionError(null);
    restoreCatalogFocus(target);
  };

  const openCreateSetup = (target: ReturnFocusTarget, template: AgentTemplate | null = null) => {
    requestGate.current.invalidate();
    returnFocusRef.current = target;
    setSelectedAgent(null);
    setSelectedTemplate(template);
    setActionError(null);
    setCreateSetupRevision((current) => current + 1);
    setMode("create");
  };

  useEffect(() => {
    if (!createRequest || createRequest === lastCreateRequestRef.current) return;
    lastCreateRequestRef.current = createRequest;
    const active = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    openCreateSetup(active ? { kind: "element", element: active } : { kind: "create" });
    onCreateRequestConsumed?.(createRequest);
  }, [createRequest, onCreateRequestConsumed]);

  useEffect(() => () => {
    requestGate.current.invalidate();
  }, []);

  useEffect(() => {
    if (openingAgentId !== null || !failedOpenFocusRef.current) return;
    const target = failedOpenFocusRef.current;
    failedOpenFocusRef.current = null;
    restoreCatalogFocus(target);
  }, [openingAgentId]);

  const retrieveForEdit = async (agent: SavedAgent) => {
    const request = requestGate.current.begin();
    returnFocusRef.current = { kind: "agent", id: agent.id };
    setSelectedAgent(agent);
    setActionError(null);
    setOpeningAgentId(agent.id);
    try {
      if (!onRetrieve) throw new Error("Agent retrieval is unavailable for this Agent Core connection.");
      const latest = await onRetrieve(agent.id);
      if (!latest) throw new Error("The Agent request was interrupted by a connection change.");
      if (latest.id !== agent.id) throw new Error("Agent Core returned a different Agent than the one requested.");
      if (!requestGate.current.isCurrent(request)) return;
      setSelectedAgent(latest);
      setMode("edit");
    } catch (error) {
      if (requestGate.current.isCurrent(request)) {
        setActionError(errorMessage(error));
        failedOpenFocusRef.current = { kind: "agent", id: agent.id };
      }
    } finally {
      if (requestGate.current.isCurrent(request)) setOpeningAgentId(null);
    }
  };

  useEffect(() => {
    if (!openAgentId || lastOpenAgentRef.current === openAgentId) return;
    const agent = agents.find((candidate) => candidate.id === openAgentId);
    if (!agent) return;
    lastOpenAgentRef.current = openAgentId;
    onOpenAgentConsumed?.(openAgentId);
    void retrieveForEdit(agent);
  }, [openAgentId, agents, onOpenAgentConsumed]);

  const submitCreate = async (input: CreateAgentInput) => {
    const request = requestGate.current.begin();
    setActionError(null);
    try {
      const created = await onCreate(input);
      return requestGate.current.isCurrent(request) ? created : undefined;
    } catch (error) {
      if (requestGate.current.isCurrent(request)) setActionError(errorMessage(error));
      return undefined;
    }
  };

  const submitUpdate = async (agentId: string, input: UpdateAgentInput): Promise<SavedAgent | undefined> => {
    const request = requestGate.current.begin();
    setActionError(null);
    try {
      if (!onUpdate) throw new Error("Agent updates are unavailable for this Agent Core connection.");
      const updated = await onUpdate(agentId, input);
      if (!updated) throw new Error("The Agent update was interrupted by a connection change.");
      if (updated.id !== agentId) throw new Error("Agent Core returned a different Agent than the one updated.");
      if (!requestGate.current.isCurrent(request)) return undefined;
      setSelectedAgent(updated);
      return updated;
    } catch (error) {
      if (requestGate.current.isCurrent(request)) setActionError(errorMessage(error));
      return undefined;
    }
  };

  const closeDelete = () => {
    if (busy) return;
    setActionError(null);
    setMode("edit");
  };

  const confirmDelete = async () => {
    if (!selectedAgent) return;
    const request = requestGate.current.begin();
    setActionError(null);
    try {
      if (!onDelete) throw new Error("Delete is unavailable for this Agent Core connection.");
      await onDelete(selectedAgent.id);
      if (!requestGate.current.isCurrent(request)) return;
      setMode("closed");
      setSelectedAgent(null);
      setSelectedTemplate(null);
      returnFocusRef.current = null;
      restoreCatalogFocus(null);
    } catch (error) {
      if (requestGate.current.isCurrent(request)) setActionError(errorMessage(error));
    }
  };

  const startSession = (agentId: string) => {
    onStartSession(agentId);
  };

  if (mode === "create" || (mode === "edit" || mode === "delete") && selectedAgent) {
    return (
      <>
        <AgentSetupView
          key={mode === "create" ? `create:${createSetupRevision}` : `edit:${selectedAgent?.id}`}
          actionError={actionError}
          agent={mode !== "create" ? selectedAgent ?? undefined : undefined}
          baseUrl={coreBaseUrl}
          busy={busy}
          defaultHarness={startupConfiguration?.configured.default_harness}
          enabledHarnesses={startupConfiguration?.configured.enabled_harnesses ?? null}
          initialValues={mode === "create" && selectedTemplate ? valuesFromAgentTemplate(selectedTemplate) : undefined}
          knownModels={knownModels}
          vaultCatalog={vaultCatalog}
          onBack={closeSetup}
          onCreate={submitCreate}
          onDeleteRequest={mode !== "create" ? () => {
            requestGate.current.invalidate();
            setActionError(null);
            setMode("delete");
          } : undefined}
          onStartSession={startSession}
          onUpdate={submitUpdate}
        />
        {mode !== "create" ? (
          <AgentDialog
            open={mode === "delete"}
            onClose={closeDelete}
            title="Delete Agent?"
            footer={mode === "delete" ? (
              <>
                <button key="cancel-delete" className="button outline" type="button" onClick={closeDelete} disabled={busy}>Cancel</button>
                <button key="confirm-delete" className="button danger" type="button" onClick={() => void confirmDelete()} disabled={busy} autoFocus>
                  {busy ? "Deleting…" : "Delete Agent"}
                </button>
              </>
            ) : null}
          >
            {actionError ? (
              <div className="agent-action-error" role="alert">
                <strong>Request failed</strong>
                <span>{actionError}</span>
              </div>
            ) : null}
            {mode === "delete" && selectedAgent ? <AgentDeleteConfirmation agent={selectedAgent} /> : null}
          </AgentDialog>
        ) : null}
      </>
    );
  }

  return (
    <section className="page-section agents-page">
      <header className="page-header">
        <h1>Agents</h1>
        <div className="page-actions">
          <label className="search-control">
            <Search size={14} strokeWidth={1.5} aria-hidden="true" />
            <input
              type="search"
              value={query}
              onChange={(event) => {
                setQuery(event.target.value);
                setShowAllAgents(false);
              }}
              placeholder="Search name, model, or ID…"
              aria-label="Search Agents"
              disabled={coreState !== "ready"}
            />
          </label>
          <button className="icon-button outline" type="button" onClick={onRefresh} disabled={coreState === "connecting"} aria-label="Refresh Agents">
            <RefreshCw className={coreState === "connecting" ? "refresh-spinning" : undefined} size={14} strokeWidth={1.5} />
          </button>
        </div>
      </header>

      {coreState === "connecting" && !agents.length ? <AgentsLoadingSkeleton /> : null}

      {coreState === "failed" ? (
        <div className="collection-error">
          <ErrorState
            title={agents.length ? "Couldn’t refresh Agents" : "Couldn’t load Agents"}
            description={agents.length
              ? "The last loaded Agent configurations remain available."
              : "The Web could not read Agent configurations from the connected Agent Core."}
            detail={coreError ?? undefined}
            hint="Check the Agent Core connection in the sidebar, then retry."
            onRetry={onRefresh}
          />
        </div>
      ) : null}

      {mode === "closed" && actionError ? (
        <div className="agent-action-error agent-open-error" role="alert">
          <strong>Couldn’t open the latest Agent</strong>
          <span>{actionError}</span>
          {selectedAgent ? <button className="button outline" type="button" onClick={() => void retrieveForEdit(selectedAgent)}>Retry</button> : null}
        </div>
      ) : null}

      {coreState === "ready" || agents.length ? (
        <AgentCatalog
          agents={filteredAgents}
          busy={busy}
          coreReady={coreState === "ready"}
          hasSavedAgents={agents.length > 0}
          isFiltering={Boolean(normalizedQuery)}
          openingAgentId={openingAgentId}
          expanded={showAllAgents}
          vaultCatalog={vaultCatalog}
          onClearSearch={() => setQuery("")}
          onCreate={() => openCreateSetup({ kind: "create" })}
          onEdit={(agent) => void retrieveForEdit(agent)}
          onExpandedChange={setShowAllAgents}
          onStartSession={startSession}
          onUseTemplate={(template) => openCreateSetup({ kind: "template", id: template.id }, template)}
        />
      ) : null}
    </section>
  );
}
