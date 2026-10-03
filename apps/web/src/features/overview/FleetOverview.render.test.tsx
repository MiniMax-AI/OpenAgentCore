import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";

import { FleetOverview } from "./FleetOverview";
import { node } from "./test-fixtures";

describe("fleet overview popovers", () => {
  it("opens Core and each node in a popover rather than navigating", () => {
    const noop = () => undefined;
    const html = renderToStaticMarkup(
      <FleetOverview
        nodes={[node("a", { name: "worker-a" }), node("b", { online: false })]}
        coreLabel="Running"
        coreTone="ok"
        stale={false}
        onOpenNode={noop}
        onOpenSandboxMetrics={noop}
        onOpenCoreMetrics={noop}
      />,
    );
    const triggers = html.match(/<button[^>]*aria-haspopup="dialog"[^>]*>/g) ?? [];
    expect(triggers).toHaveLength(3);
    expect(triggers.every((trigger) => trigger.includes('aria-expanded="false"'))).toBe(true);
    expect(html).toContain("worker-a");
    // The glance mounts only once opened.
    expect(html).not.toContain("console-popover-facts");
  });
});
