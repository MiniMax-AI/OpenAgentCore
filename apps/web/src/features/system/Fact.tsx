import type { ReactNode } from "react";

import { HelpTip } from "../../components/console-ui";

/** One label/value row of a Settings section; an action beside the value changes only that value. */
export function Fact({ label, help, action, children }: { label: string; help?: ReactNode; action?: ReactNode; children: ReactNode }) {
  return (
    <dl className="settings-row">
      <dt>
        <span>{label}</span>
        {help ? <HelpTip>{help}</HelpTip> : null}
      </dt>
      <dd>{action ? <><div className="settings-row-content">{children}</div><div className="settings-row-action">{action}</div></> : children}</dd>
    </dl>
  );
}
