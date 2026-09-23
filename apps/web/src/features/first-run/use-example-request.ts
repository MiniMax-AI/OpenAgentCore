import { useEffect, useRef, useState } from "react";
import type { AgentCore, CreateAgentInput, SavedAgent } from "@agents-core-web/agents-client";
import { createExampleRequest, loadExampleIdentity, saveExampleIdentity, type ExampleState } from "./example-request";

export function useExampleRequest(core: AgentCore, scope: string, onCreated: (agent: SavedAgent) => void) {
  const [identity] = useState(() => loadExampleIdentity(scope));
  const [state, setState] = useState<ExampleState>({ phase: identity.submitted ? "waiting" : "idle", agent: null, error: null });
  const request = useRef<ReturnType<typeof createExampleRequest> | null>(null);
  const created = useRef(onCreated);
  created.current = onCreated;
  useEffect(() => {
    saveExampleIdentity(scope, identity);
    let notified = false;
    const current = createExampleRequest(core, identity, (next) => {
      setState(next);
      if (next.agent && !notified) { notified = true; created.current(next.agent); }
    }, (submitted) => {
      identity.submitted = submitted;
      saveExampleIdentity(scope, identity);
    });
    request.current = current;
    setState(current.getState());
    const refresh = () => { if (document.visibilityState === "visible") void current.reconcile(); };
    refresh();
    const timer = window.setInterval(refresh, 3000);
    document.addEventListener("visibilitychange", refresh);
    return () => {
      current.dispose();
      if (request.current === current) request.current = null;
      window.clearInterval(timer);
      document.removeEventListener("visibilitychange", refresh);
    };
  }, [core, identity, scope]);
  return {
    marker: identity.marker,
    state,
    run: (input: CreateAgentInput) => request.current?.run(input),
    observeExternal: () => request.current?.observeExternal(),
    reconcile: () => request.current?.reconcile(),
  };
}
