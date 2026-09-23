import { sandboxDiagnosticMessage } from "../../lib/sandbox-diagnostic";

import { useLocale } from "../../lib/LocaleProvider";

export function SandboxDiagnostic({ diagnostic }: { diagnostic?: string }) {
  const { locale } = useLocale();
  const message = sandboxDiagnosticMessage(diagnostic, locale);
  if (!message) return null;
  return <div className="sandbox-diagnostic" role="status">
    <strong>{message.label}</strong><small>{message.advice}</small>
  </div>;
}
