import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";

import { PageBody, PageHeader, RefreshButton, Section } from "../../components/console-ui";
import { ErrorState } from "../../components/ErrorState";
import { CopyableId } from "../../components/list-ui";
import { TableSkeleton } from "../../components/Skeleton";

import { InstallationNotice } from "../../components/InstallationNotice";
import { installationQuery } from "../../lib/installation";
import { SettingsTabs } from "../settings/SettingsTabs";
import { Fact } from "./Fact";

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

/** Service settings display installation facts and the effective process configuration. */
export function SystemPage() {
  const { t } = useTranslation("system");
  const { t: tNav } = useTranslation("navigation");
  const installation = useQuery(installationQuery);
  const refresh = () => { void installation.refetch();  };

  const about = installation.data;
  const facts = about ? (
    <Section headingId="system-installation-heading" title={t("installation.title")}>
      <dl className="system-facts">
        <Fact label={t("installation.publicUrl")} help={t("installation.publicUrlHelp")}><code className="system-code">{about.public_url}</code></Fact>
        <Fact label={t("installation.apiBaseUrl")} help={t("installation.apiBaseUrlHelp")}>
          <CopyableId id={about.api_base_url} label={t("installation.copyApiBaseUrl")} />
        </Fact>
        <Fact label={t("installation.id")}><CopyableId id={about.installation_id} /></Fact>
        <Fact label={t("installation.sourceCommit")}>{about.source_commit ? <code className="system-code" title={about.source_commit}>{about.source_commit.slice(0, 12)}</code> : <span className="system-muted">{t("installation.unknown")}</span>}</Fact>
      </dl>
    </Section>
  ) : installation.isError && !installation.isFetching
    ? <ErrorState title={t("installation.failed")} onRetry={() => void installation.refetch()} />
    : <FactsSkeleton facts={4} />;
  // Announce the initial installation read once.
  const reading = (!about && !(installation.isError && !installation.isFetching));

  return (
    <section className="page-section console-page system-page" aria-labelledby="system-heading">
      <PageHeader
        headingId="system-heading"
        title={tNav("settings.service")}
        help={t("help")}
        actions={<RefreshButton onClick={refresh} refreshing={installation.isFetching} label={t("refresh")} />}
      />
      <SettingsTabs active="service" />
      <PageBody>
        <InstallationNotice installation={about} />
        {reading ? <p className="visually-hidden" role="status">{t("loading")}</p> : null}
        {facts}
        {about ? <StartupSettings configuration={about.configuration} /> : installation.isError ? null : <TableSkeleton rows={4} columns={3} />}
      </PageBody>
    </section>
  );
}
