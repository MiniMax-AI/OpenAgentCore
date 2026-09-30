import { type CoreHarnessKind } from "@oac/agents-client";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useCallback, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";

import { ConfirmDialog } from "../../components/ConfirmDialog";
import { EmptyState, revealInPageBody, Section } from "../../components/console-ui";
import { ErrorState } from "../../components/ErrorState";
import { TableSkeleton } from "../../components/Skeleton";
import { failedLast, useFailureToast, useToast } from "../../components/Toast";
import { useDeleteFlow } from "../../lib/delete-flow";
import { useConsoleIntent } from "../../lib/console-navigation";
import { harnessNames } from "../../lib/harness-labels";
import { admin } from "../../lib/projects";
import { ModelProviderDialog } from "./ModelProviderDialog";
import { HarnessCard } from "./HarnessCard";
import { harnessesQuery } from "./harness-queries";

function message(error: unknown): string {
  return error instanceof Error ? error.message : String(error ?? "");
}

/**
 * System owns each harness's deployment default model configuration.
 * New Core-hosted Sessions and Sessions without an environment inherit it
 * when no explicit Session or Agent model configuration takes precedence.
 * Self-hosted Sessions bring their own configuration. Whether a harness is enabled, and
 * which is the default, is Core's startup configuration and only shown here.
 * A write replaces the whole model configuration and needs the API key every time; the
 * key lives only in the open form's state, never in the query cache, storage
 * or the URL. Writes are never retried automatically; after each one the
 * harnesses are read again.
 */
export function DefaultModelsSection() {
  const { t } = useTranslation("system");
  const toast = useToast();
  const queryClient = useQueryClient();
  const query = useQuery(harnessesQuery);
  const harnesses = query.data?.data ?? null;
  const reread = useCallback(() => { void queryClient.invalidateQueries({ queryKey: harnessesQuery.queryKey }); }, [queryClient]);
  useFailureToast(harnesses && failedLast(query) ? message(query.error) : null, t("models.refreshFailed"), "harnesses-read");
  // From Getting started: bring the default harness's Set or Replace into view and focus.
  useConsoleIntent("default-model", harnesses ? "ready" : query.isError ? "unavailable" : "wait", () => {
    window.requestAnimationFrame(() => {
      const section = document.getElementById("system-models-heading")?.closest("section");
      const action = section?.querySelector<HTMLButtonElement>("article[data-default] .system-model-actions button") ?? section?.querySelector<HTMLButtonElement>(".system-model-actions button");
      revealInPageBody(section ?? null, action ?? null);
    });
  });

  // The harness being edited, read from the latest list so a reread updates the open form's title.
  const [editing, setEditing] = useState<CoreHarnessKind | null>(null);
  const editingHarness = (editing && harnesses?.find((harness) => harness.id === editing)) || null;
  const clear = useDeleteFlow<CoreHarnessKind>(
    async (harness) => {
      await admin.deleteHarnessModelConfiguration(harness, { signal: AbortSignal.timeout(30_000) });
      toast.show(t("models.cleared", { harness: harnessNames[harness] }), { tone: "success" });
    },
    reread,
    { uncertain: t("models.clearDialog.uncertain") },
  );

  let body: ReactNode;
  if (!harnesses) {
    body = query.isError && !query.isFetching
      ? <ErrorState title={t("models.loadFailed")} description={message(query.error)} onRetry={() => { void query.refetch(); }} />
      : <TableSkeleton rows={3} columns={3} />;
  } else if (!harnesses.length) {
    body = <EmptyState title={t("models.none")} />;
  } else {
    body = (
      <div className="system-models">
        {harnesses.map((harness) => (
          <HarnessCard key={harness.id} harness={harness} busy={clear.busy} stale={failedLast(query)} onEdit={() => setEditing(harness.id)} onClear={() => clear.ask(harness.id)} />
        ))}
      </div>
    );
  }

  return (
    <Section headingId="system-models-heading" title={t("models.title")} help={t("models.help")}>
      {body}
      <ModelProviderDialog
        // A new form for every opening: closing it drops whatever was typed, the key included.
        key={editing ?? "closed"}
        harness={editingHarness}
        onClose={() => setEditing(null)}
        onReread={reread}
        onSaved={(harness) => {
          setEditing(null);
          reread();
          toast.show(t("models.saved", { harness: harnessNames[harness] }), { tone: "success" });
        }}
      />
      <ConfirmDialog
        open={clear.target !== null}
        title={t("models.clearDialog.title")}
        confirmLabel={t("models.clearDialog.confirm")}
        busyLabel={t("models.clearDialog.busy")}
        busy={clear.busy}
        error={clear.error}
        onConfirm={() => void clear.confirm()}
        onClose={clear.cancel}
      >
        {clear.target ? (
          <>
            <p>{t("models.clearDialog.prompt", { harness: harnessNames[clear.target] })}</p>
            <p>{t("models.clearDialog.consequence")}</p>
          </>
        ) : null}
      </ConfirmDialog>
    </Section>
  );
}
