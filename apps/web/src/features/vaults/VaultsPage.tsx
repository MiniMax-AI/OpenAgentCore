import { useTranslation } from "react-i18next";

import { PageBody, PageHeader } from "../../components/console-ui";

/** Placeholder until this page moves to the Web API. */
export function VaultsPage() {
  const { t } = useTranslation("navigation");
  return (
    <section className="page-section console-page" aria-labelledby="page-heading">
      <PageHeader headingId="page-heading" title={t("views.vaults")} />
      <PageBody><p className="page-status">…</p></PageBody>
    </section>
  );
}
