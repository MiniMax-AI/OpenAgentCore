/** Omission preserves the credential; every explicit value follows Core's replacement path. */
export function submittedCredential(entries: readonly (readonly [string, string])[]): { credential?: Record<string, string> } {
  const entered = entries.filter(([, value]) => value);
  return entered.length > 0 ? { credential: Object.fromEntries(entered) } : {};
}

/** Clearing a submitted secret must not silently convert replacement into retention. */
export function credentialReady(editing: boolean, replacementRequested: boolean, entered: boolean): boolean {
  return entered || (editing && !replacementRequested);
}
