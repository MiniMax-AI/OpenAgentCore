import { useEffect, useMemo, useState } from "react";

import { fetchOwners, OwnershipUnavailableError, type OwnedResourceType, type OwnerRecord } from "./ownership";

/**
 * Availability is decided once per page load: the first request that reports
 * the capability absent hides the "API key" column everywhere.
 */
let capability: "unknown" | "available" | "unavailable" = "unknown";
const listeners = new Set<() => void>();
const cache = new Map<string, OwnerRecord | null>();

function setCapability(next: typeof capability) {
  if (capability === next) return;
  capability = next;
  for (const listener of listeners) listener();
}

/** Test hook: forget the capability decision and cached owners. */
export function resetOwnershipState() {
  capability = "unknown";
  cache.clear();
}

export interface OwnersState {
  /** false until the console proves the capability exists; the column stays hidden. */
  available: boolean;
  /** undefined: not loaded (or failed); null: Core has no creation record. */
  ownerOf: (id: string) => OwnerRecord | null | undefined;
}

export function useOwners(type: OwnedResourceType, ids: readonly string[], enabled = true): OwnersState {
  const [, setVersion] = useState(0);
  const key = useMemo(() => [...new Set(ids)].sort().join(","), [ids]);

  useEffect(() => {
    const listener = () => setVersion((value) => value + 1);
    listeners.add(listener);
    return () => { listeners.delete(listener); };
  }, []);

  useEffect(() => {
    if (!enabled || !key || capability === "unavailable") return;
    const missing = key.split(",").filter((id) => !cache.has(`${type}:${id}`));
    if (!missing.length) return;
    const controller = new AbortController();
    fetchOwners(type, missing, controller.signal).then((owners) => {
      // An ID missing from the answer (or dropped as malformed) renders like "no record".
      for (const id of missing) cache.set(`${type}:${id}`, owners.get(id) ?? null);
      setCapability("available");
      setVersion((value) => value + 1);
    }, (error: unknown) => {
      if (controller.signal.aborted) return;
      if (error instanceof OwnershipUnavailableError) setCapability("unavailable");
    });
    return () => controller.abort();
  }, [enabled, key, type]);

  return {
    available: enabled && capability === "available",
    ownerOf: (id) => {
      if (!cache.has(`${type}:${id}`)) return undefined;
      return cache.get(`${type}:${id}`) ?? null;
    },
  };
}

/** Drop cached owners for a type so a refresh re-reads them (after creates or deletes). */
export function forgetOwners(type?: OwnedResourceType) {
  for (const key of [...cache.keys()]) if (!type || key.startsWith(`${type}:`)) cache.delete(key);
}
