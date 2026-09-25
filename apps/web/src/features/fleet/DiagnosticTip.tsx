import { useTranslation } from "react-i18next";

import { HelpTip } from "../../components/console-ui";
import { sandboxDiagnosticMessage } from "../../lib/sandbox-diagnostic";

/** The specific reason behind a node's status, in the help tip beside it: what is wrong, then how to fix it. */
export function DiagnosticTip({ code }: { code: string }) {
  const { i18n } = useTranslation();
  const message = sandboxDiagnosticMessage(code, i18n.resolvedLanguage?.startsWith("zh") ? "zh" : "en");
  if (!message) return null;
  return (
    <HelpTip label={message.label}>
      <strong className="help-tip-title">{message.label}</strong> {message.advice}
    </HelpTip>
  );
}
