import { AgentCoreError } from "@oac/agents-client";
import { useCallback, useState } from "react";

export interface DeleteFlow<T> {
  target: T | null;
  busy: boolean;
  error: string | null;
  ask: (target: T) => void;
  cancel: () => void;
  confirm: () => Promise<void>;
}

/**
 * Confirmed deletion through the Web API. A definite rejection keeps the dialog
 * open with Core's reason. An unconfirmed outcome is never retried silently: the
 * dialog stays open with the uncertain message while the view is re-read
 * (`messages.reread`, or `onSettled` where settling only refreshes a list), so a
 * detail page does not navigate away as if the deletion had succeeded.
 */
export function useDeleteFlow<T>(
  run: (target: T) => Promise<unknown>,
  onSettled: () => void,
  messages: { uncertain: string; reread?: () => void },
): DeleteFlow<T> {
  const [target, setTarget] = useState<T | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const confirm = useCallback(async () => {
    if (target === null || busy) return;
    setBusy(true);
    setError(null);
    try {
      await run(target);
      setTarget(null);
      onSettled();
    } catch (caught) {
      if (caught instanceof AgentCoreError && caught.status === 404) {
        // Already gone: the refreshed list shows the truth.
        setTarget(null);
        onSettled();
      } else if (caught instanceof AgentCoreError && caught.status >= 400 && caught.status < 500) {
        setError(caught.message);
      } else {
        setError(messages.uncertain);
        (messages.reread ?? onSettled)();
      }
    } finally {
      setBusy(false);
    }
  }, [busy, messages, onSettled, run, target]);
  return {
    target,
    busy,
    error,
    ask: (next) => { setError(null); setTarget(next); },
    cancel: () => { if (!busy) { setTarget(null); setError(null); } },
    confirm,
  };
}
