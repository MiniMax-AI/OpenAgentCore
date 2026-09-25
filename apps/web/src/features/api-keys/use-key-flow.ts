import { useCallback, useEffect, useReducer, useRef } from "react";

import { flowError, idleFlow, isAbort, keyFlowReducer, normalizeName, type KeyFlow, type KeyFlowEvent } from "./key-flows";
import { issueKey, type Project } from "../../lib/admin-view";

export interface KeyFlowControls {
  flow: KeyFlow;
  dispatch: (event: KeyFlowEvent) => void;
  /** Issues the named key. */
  submit: () => Promise<void>;
}

/**
 * Runs the requests of `keyFlowReducer`. `onChanged` runs after Core may have
 * changed a project or its keys. Writes are never retried automatically: an
 * uncertain result is reported for the operator to check.
 */
export function useKeyFlow(onChanged: (project: Project) => void = () => undefined): KeyFlowControls {
  const [flow, dispatch] = useReducer(keyFlowReducer, idleFlow);
  const flowRef = useRef(flow);
  flowRef.current = flow;
  const changed = useRef(onChanged);
  changed.current = onChanged;
  const mounted = useRef(true);
  useEffect(() => {
    mounted.current = true;
    return () => { mounted.current = false; };
  }, []);
  const send = useCallback((event: KeyFlowEvent) => { if (mounted.current) dispatch(event); }, []);

  // A reload or a closed tab would lose a key that is shown only once.
  useEffect(() => {
    if (flow.step !== "issued") return;
    const guard = (event: BeforeUnloadEvent) => { event.preventDefault(); };
    window.addEventListener("beforeunload", guard);
    return () => window.removeEventListener("beforeunload", guard);
  }, [flow.step]);

  const submit = useCallback(async () => {
    const current = flowRef.current;
    if (current.step !== "issue" || current.busy) return;
    const keyName = normalizeName(current.name);
    const project = current.project;
    send({ type: "started" });
    try {
      const key = await issueKey(project.id, keyName);
      send({ type: "issued", projectId: project.id, key });
    } catch (error) {
      if (!isAbort(error)) send({ type: "failed", error: flowError(error) });
    } finally {
      changed.current(project);
    }
  }, [send]);

  return { flow, dispatch: send, submit };
}
