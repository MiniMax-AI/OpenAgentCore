import { useTranslation } from "react-i18next";

import { Modal } from "../../components/Modal";
import { nodeUninstallCommand } from "./enrollment-command";
import { CommandBlock } from "./node-commands";

/** A node Core has just removed, with what builds its host's uninstall command. */
export interface NodeCleanup {
  name: string;
  sourceUrl: string;
  installationId: string;
  scriptDigest: string;
}

/**
 * After Remove: the command that removes the node's service and files from its
 * host. The installer uninstalls only once Core refuses the node's credential,
 * which it does from the removal on. The sudo form escalates by itself, so a node
 * installed without sudo gets its own command, run as that node's user.
 */
export function NodeCleanupDialog({ cleanup, open, onClose }: { cleanup: NodeCleanup | null; open: boolean; onClose: () => void }) {
  const { t } = useTranslation("sandbox");
  const command = (mode: "sudo" | "user") => cleanup ? nodeUninstallCommand({ sourceUrl: cleanup.sourceUrl, installationId: cleanup.installationId, scriptDigest: cleanup.scriptDigest, mode }) : "";
  return <Modal open={open} title={t("Clean up the host")} onClose={onClose} footer={<button className="button primary" type="button" onClick={onClose}>{t("Done")}</button>}>
    {cleanup ? <div className="sandbox-add-node form-stack">
      <p>{t("{{name}} is removed from Core. To remove its service and files from the host, run:", { name: cleanup.name })}</p>
      <CommandBlock key={command("sudo")} value={command("sudo")} label={t("Uninstall command")} copyLabel={t("Copy uninstall command")} autoFocus />
      <details className="sandbox-host-requirements sandbox-no-sudo">
        <summary>{t("Installed without sudo?")}</summary>
        <div className="sandbox-no-sudo-body">
          <p>{t("Run this as that user instead:")}</p>
          <CommandBlock key={command("user")} value={command("user")} label={t("Uninstall command without sudo")} copyLabel={t("Copy the uninstall command without sudo")} />
        </div>
      </details>
    </div> : null}
  </Modal>;
}
