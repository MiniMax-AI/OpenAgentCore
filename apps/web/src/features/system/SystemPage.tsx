import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";

import { PageBody, PageHeader, RefreshButton, Section } from "../../components/console-ui";
import { ErrorState } from "../../components/ErrorState";
import { CopyableId } from "../../components/list-ui";
import { TableSkeleton } from "../../components/Skeleton";
import { useConsoleNavigation } from "../../lib/console-navigation";
import { InstallationNotice } from "../../components/InstallationNotice";
import { installationQuery } from "../../lib/installation";
import { DefaultModelsSection } from "./DefaultModelsSection";
import { Fact } from "./Fact";
import { harnessesQuery } from "./harness-queries";
import { StartupSettings } from "./StartupSettings";
import "./system.css";

/** A facts card before its first read; the page announces its loading once. */
function FactsSkeleton({ facts }: { facts: number }) {
  return (
    <dl className="system-facts" aria-hidden="true">
      {Array.from({ length: facts }, (_, index) => (
        <div key={index} className="system-fact">
          <dt><span className="skeleton-bar" style={{ width: `${40 + (index * 13) % 24}%` }} /></dt>
          <dd><span className="skeleton-bar" style={{ width: `${30 + (index * 17) % 30}%` }} /></dd>
        </div>
      ))}
    </dl>
  );
}

/** System owns installation settings and the entry to sandbox configuration. */
export function SystemPage() {
  const { t } = useTranslation("system");
  const { t: tSandboxNav } = useTranslation("sandboxNavigation");
  const { t: tNav } = useTranslation("navigation");
  const { navigate } = useConsoleNavigation();
  const installation = useQuery(installationQuery);
  const harnesses = useQuery(harnessesQuery);
  const refresh = () => { void installation.refetch(); void harnesses.refetch(); };

  const about = installation.data;
  const facts = about ? (
    <Section headingId="system-installation-heading" title={t("installation.title")}>
      <dl className="system-facts">
        <Fact label={t("installation.publicUrl")} help={t("installation.publicUrlHelp")}>{about.public_url ? <code className="system-code">{about.public_url}</code> : <span className="system-muted">{t("installation.notSet")}</span>}</Fact>
        <Fact label={t("installation.apiBaseUrl")} help={t("installation.apiBaseUrlHelp")}>
          {about.api_base_url ? <CopyableId id={about.api_base_url} label={t("installation.copyApiBaseUrl")} /> : <span className="system-muted">{t("installation.notSet")}</span>}
        </Fact>
        <Fact label={t("installation.id")}>{about.installation_id ? <CopyableId id={about.installation_id} /> : <span className="system-muted">{t("installation.unknown")}</span>}</Fact>
        <Fact label={t("installation.sourceCommit")}>{about.source_commit ? <code className="system-code" title={about.source_commit}>{about.source_commit.slice(0, 12)}</code> : <span className="system-muted">{t("installation.unknown")}</span>}</Fact>
      </dl>
    </Section>
  ) : installation.isError && !installation.isFetching
    ? <ErrorState title={t("installation.failed")} onRetry={() => void installation.refetch()} />
    : <FactsSkeleton facts={4} />;
  // One announcement while either first read is still out; each slot shows only its own placeholder.
  const reading = (!about && !(installation.isError && !installation.isFetching)) ||
    (!harnesses.data && !(harnesses.isError && !harnesses.isFetching));

  return (
    <section className="page-section console-page system-page" aria-labelledby="system-heading">
      <PageHeader
        headingId="system-heading"
        title={tNav("views.system")}
        help={t("help")}
        actions={<RefreshButton onClick={refresh} refreshing={installation.isFetching || harnesses.isFetching} label={t("refresh")} />}
      />
      <PageBody>
        <InstallationNotice installation={about} />
        {reading ? <p className="visually-hidden" role="status">{t("loading")}</p> : null}
        {facts}
        <DefaultModelsSection />
        <Section headingId="system-sandbox-heading" title={tSandboxNav("title")} help={tSandboxNav("help")}>
          <button className="button outline" type="button" onClick={() => navigate("system", { id: "sandbox" })}>{tSandboxNav("open")}</button>
        </Section>
        {about ? <StartupSettings configuration={about.configuration} /> : installation.isError ? null : <TableSkeleton rows={4} columns={3} />}
      </PageBody>
    </section>
  );
}
