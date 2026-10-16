import "./lib/plugin-init";
import { useEffect, useState, type ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import { product } from "./lib/product";
import type { Capability } from "./Capabilities";
import { slotRegistry } from "./lib/plugin-slots";
import { ErrorNotice } from "./components/shared";
import { SingleSlot } from "./components/plugin/SlotRenderer";
let chain = Promise.resolve();
let generation = 0;
export function PluginHost({ children }: { children: ReactNode }) {
  const [error, setError] = useState<Error | null>(null);
  const query = useQuery({
    queryKey: ["capabilities"],
    queryFn: () => product<Capability[]>("capabilities"),
  });
  const signature = query.data
    ?.filter(
      (row) =>
        !row.deprecated &&
        row.versions.find((version) => version.version === row.default_version)
          ?.has_client_bundle,
    )
    .map((row) => `${row.id}:${row.revision}`)
    .join(",");
  useEffect(() => {
    if (signature === undefined) return;
    const current = ++generation;
    chain = chain
      .catch(() => {})
      .then(async () => {
        if (current !== generation) return;
        slotRegistry.clear();
        setError(null);
        for (const entry of signature.split(",").filter(Boolean)) {
          const [id, revision] = entry.split(":");
          try {
            await import(
              /* @vite-ignore */ `/app/capabilities/${id}/client.js?revision=${revision}&load=${current}`
            );
          } catch {
            setError(new Error("A plugin client could not be loaded."));
          }
        }
      });
  }, [signature]);
  return (
    <>
      <ErrorNotice error={error} />
      <SingleSlot slotId="workspace.main" fallback={children} />
    </>
  );
}
