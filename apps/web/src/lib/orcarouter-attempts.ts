/**
 * One console-side state machine for `Connect with OrcaRouter`.
 *
 * A connect attempt is a browser redirect plus a paste, so a late answer must
 * never overwrite a newer attempt, and leaving the page must drop the busy flag
 * and the hint synchronously — the console keeps no server-side login lock, so
 * there is nothing else to release, but the in-flight exchange is aborted so it
 * cannot land after the page is gone.
 *
 * The controller is plain and dependency-free so its ordering rules can be
 * tested without a browser.
 */
export interface CredentialAttempt {
  /** Increases with every attempt; a result from an older one is ignored. */
  readonly generation: number;
  readonly signal: AbortSignal;
}

export interface CredentialAttemptView {
  attempt: number;
  busy: boolean;
  hint: string | null;
}

export function createCredentialAttempts(onChange?: (view: CredentialAttemptView) => void) {
  let generation = 0;
  let busy = false;
  let hint: string | null = null;
  let controller: AbortController | null = null;

  const report = () => onChange?.({ attempt: generation, busy, hint });

  /** Starts one attempt: a new generation and a signal that dies with the page. */
  function begin(): CredentialAttempt {
    controller?.abort();
    controller = new AbortController();
    generation += 1;
    busy = true;
    hint = null;
    report();
    return { generation, signal: controller.signal };
  }

  /** Records the outcome of an attempt. A stale generation changes nothing. */
  function settle(attempt: CredentialAttempt, outcome: { ok: true } | { ok: false; message: string }): boolean {
    if (attempt.generation !== generation) return false;
    busy = false;
    hint = outcome.ok ? null : outcome.message;
    report();
    return true;
  }

  /** Releases the lock because the operator moved on, without reporting a hint. */
  function abandon(): void {
    controller?.abort();
    controller = null;
    generation += 1;
    busy = false;
    hint = null;
    report();
  }

  /**
   * Leaving the page: drop the busy flag and the hint now, before the unload
   * completes, and abort the exchange. A second login after this is a fresh
   * attempt and does not need a remount.
   */
  function pagehide(): void {
    abandon();
  }

  function snapshot(): CredentialAttemptView {
    return { attempt: generation, busy, hint };
  }

  return { begin, settle, abandon, pagehide, snapshot };
}
