import { sandboxDiagnosticMessage } from "../../lib/sandbox-diagnostic";

export function SandboxDiagnostic({ diagnostic }: { diagnostic?: string }) {
  const message = sandboxDiagnosticMessage(diagnostic);
  if (!message) return null;
  return <div className="sandbox-diagnostic" role="status">
    <strong>{message.label}</strong><small>{message.advice}</small>
  </div>;
}
