import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";

import { Modal } from "../../components/Modal";
import { installationQuery } from "../../lib/installation";
import { nodeSourceUrl } from "./core-origin";
import { nodeUninstallCommand } from "./enrollment-command";
import { CommandBlock } from "./node-commands";

/** A node Core has just removed, with what builds its host's uninstall command. */
export interface NodeCleanup {
  name: string;
  installationId: string;
  scriptDigest: string;
  provider: string;
  /** The Core address the node enrolled with, when it is no longer the deployment's; else null. */
  oldAddress: string | null;
}

/**
 * After Remove: the command that removes the node's service and files from its
 * host (deploy/install/node_install.py `uninstall_system`).
 * The installer first confirms with Core, at the node's own address, that the
 * node is removed, which holds from the removal on. The command uses sudo unless the shell is already root. A node enrolled with an earlier address may find it gone; then
 * `--force` skips only that confirmation. Nothing deletes sandboxes, volumes or
 * images. Like Add node's, the command downloads from the installation's public
 * URL, which the dialog reads (again, if it is not at hand): until it is read, if
 * the read fails (with Try again), or while other machines can't use it, the
 * dialog says so in place of the command. It never opens empty.
 */
export function NodeCleanupDialog({ cleanup, open, onClose }: { cleanup: NodeCleanup | null; open: boolean; onClose: () => void }) {
  const { t, i18n } = useTranslation("sandbox");
  // Sentences run on with a space in English and without one in Chinese.
  const join = (...sentences: string[]) => sentences.join(i18n.resolvedLanguage?.startsWith("zh") ? "" : " ");
  const installation = useQuery({ ...installationQuery, enabled: cleanup !== null });
  const sourceUrl = installation.data ? nodeSourceUrl(installation.data) : null;
  const command = (force = false) => cleanup && sourceUrl
    ? nodeUninstallCommand({ sourceUrl, installationId: cleanup.installationId, scriptDigest: cleanup.scriptDigest, force }) : "";
  const stays = cleanup ? t("{{name}} is removed from Core, but its service and files stay on the host.", { name: cleanup.name }) : "";
  return <Modal open={open} title={t("Clean up the host")} onClose={onClose} footer={<button className="button primary" type="button" onClick={onClose}>{t("Done")}</button>}>
    {cleanup && !installation.data ? <div className="sandbox-add-node form-stack">
      {installation.isError
        ? <p role="alert">{join(stays, t("The installation couldn't be read, so no command can be issued."))} <button className="text-action" type="button" disabled={installation.isFetching} onClick={() => void installation.refetch()}>{t("Try again")}</button></p>
        : <p role="status">{t("Checking this installation's public URL…")}</p>}
    </div> : cleanup && !sourceUrl ? <div className="sandbox-add-node form-stack">
      <p>{join(stays, installation.data?.local_only && installation.data.public_url
        ? t("Other machines can't reach this installation's public URL, {{url}}, so no uninstall command can be given.", { url: installation.data.public_url })
        : t("An uninstall command needs an HTTPS public URL that other machines can reach, and this installation has none."))}</p>
    </div> : cleanup ? <div className="sandbox-add-node form-stack">
      <p>{t("{{name}} is removed from Core. To remove its service and files from the host, run:", { name: cleanup.name })}</p>
      <CommandBlock key={command()} value={command()} label={t("Uninstall command")} autoFocus />
      <p className="sandbox-cleanup-note">{join(t("It never deletes sandboxes, volumes or images."),
        ...(cleanup.provider === "microsandbox" ? [t("It keeps microsandbox's image store and sandbox data, and prints how to remove them by hand.")] : []))}</p>
      {cleanup.oldAddress !== null ? <details className="sandbox-host-requirements">
        <summary>{t("Old Core address gone?")}</summary>
        <div className="sandbox-cleanup-details">
          <p>{join(t("{{name}} still points at the old Core address {{address}}.", { name: cleanup.name, address: cleanup.oldAddress }),
            t("If this node's old Core address no longer responds, first remove it on the Nodes page, then add --force to the uninstall command."))}</p>
          <CommandBlock key={command(true)} value={command(true)} label={t("Uninstall command with --force")} copyName={t("Copy command with --force")} />
        </div>
      </details> : null}
    </div> : null}
  </Modal>;
}
