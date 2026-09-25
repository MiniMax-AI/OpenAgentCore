import type { ReactNode } from "react";

import { HelpTip } from "../../components/console-ui";

/** One label/value row of a System page ledger. */
export function Fact({ label, help, children }: { label: string; help?: string; children: ReactNode }) {
  return (
    <div className="system-fact">
      <dt>
        <span>{label}</span>
        {help ? <HelpTip>{help}</HelpTip> : null}
      </dt>
      <dd>{children}</dd>
    </div>
  );
}
