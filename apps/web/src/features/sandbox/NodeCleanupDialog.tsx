import { TriangleAlert } from "lucide-react";
import { useTranslation } from "react-i18next";

import { Modal } from "../../components/Modal";
import { sandboxSetupOrigin } from "./core-origin";
import { nodeUninstallCommand, type NodeInstallMode } from "./enrollment-command";
import { CommandBlock } from "./node-commands";

/** A node Core has just removed, with what builds its host's uninstall command. */
export interface NodeCleanup {
  name: string;
  /** This console's address, which the command downloads the installer from. */
  sourceUrl: string;
  installationId: string;
  scriptDigest: string;
  provider: string;
  /** The Core address the node enrolled with, when it is no longer the deployment's; else null. */
  oldAddress: string | null;
}

/**
 * After Remove: the command that removes the node's service and files from its
 * host (deploy/install/node_install.py `uninstall_system` and `uninstall_user`).
 * The installer first confirms with Core, at the node's own address, that the
 * node is removed, which holds from the removal on. The sudo form escalates by
 * itself, so a node installed without sudo gets its own command, run as that
 * node's user. A node enrolled with an earlier address may find it gone; then
 * `--force` skips only that confirmation. Nothing deletes sandboxes, volumes or
 * images.
 */
export function NodeCleanupDialog({ cleanup, open, onClose }: { cleanup: NodeCleanup | null; open: boolean; onClose: () => void }) {
  const { t, i18n } = useTranslation("sandbox");
  // Sentences run on with a space in English and without one in Chinese.
  const join = (...sentences: string[]) => sentences.join(i18n.resolvedLanguage?.startsWith("zh") ? "" : " ");
  const command = (mode: NodeInstallMode, force = false) => cleanup
    ? nodeUninstallCommand({ sourceUrl: cleanup.sourceUrl, installationId: cleanup.installationId, scriptDigest: cleanup.scriptDigest, mode, force }) : "";
  return <Modal open={open} title={t("Clean up the host")} onClose={onClose} footer={<button className="button primary" type="button" onClick={onClose}>{t("Done")}</button>}>
    {cleanup ? <div className="sandbox-add-node form-stack">
      <p>{t("{{name}} is removed from Core. To remove its service and files from the host, run:", { name: cleanup.name })}</p>
      {/* Like Add node's command, this one downloads from the console's own address. */}
      {sandboxSetupOrigin(cleanup.sourceUrl) === null ? <p className="sandbox-add-node-warning" role="note"><TriangleAlert size={14} aria-hidden="true" /><span>{t("This console is open at {{origin}}, which other machines can't reach. On another machine, replace it in the command with the console's HTTPS address.", { origin: cleanup.sourceUrl })}</span></p> : null}
      <CommandBlock key={command("sudo")} value={command("sudo")} label={t("Uninstall command")} autoFocus />
      <p className="sandbox-cleanup-note">{join(t("It never deletes sandboxes, volumes or images."),
        ...(cleanup.provider === "microsandbox" ? [t("It keeps microsandbox's image store and sandbox data, and prints how to remove them by hand.")] : []))}</p>
      <details className="sandbox-host-requirements sandbox-no-sudo">
        <summary>{t("Installed without sudo?")}</summary>
        <div className="sandbox-no-sudo-body">
          <p>{t("Run this as that user instead:")}</p>
          <CommandBlock key={command("user")} value={command("user")} label={t("Uninstall command without sudo")} copyName={t("Copy command without sudo")} />
        </div>
      </details>
      {cleanup.oldAddress !== null ? <details className="sandbox-host-requirements sandbox-no-sudo">
        <summary>{t("Old Core address gone?")}</summary>
        <div className="sandbox-no-sudo-body">
          <p>{join(t("{{name}} still points at the old Core address {{address}}.", { name: cleanup.name, address: cleanup.oldAddress }),
            t("If this node's old Core address no longer responds, first remove it on the Nodes page, then add --force to the uninstall command."))}</p>
          <CommandBlock key={command("sudo", true)} value={command("sudo", true)} label={t("Uninstall command with --force")} copyName={t("Copy command with --force")} />
        </div>
      </details> : null}
    </div> : null}
  </Modal>;
}
