import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";

import { FleetOverview } from "./FleetOverview";
import { node } from "./test-fixtures";

describe("fleet overview", () => {
  it("lists Core and each node as a row that opens its page", () => {
    const noop = () => undefined;
    const html = renderToStaticMarkup(
      <FleetOverview
        nodes={[node("a", { name: "worker-a" }), node("b", { online: false })]}
        coreLabel="Running"
        coreTone="ok"
        onOpenNode={noop}
        onOpenCloud={noop}
        onOpenCore={noop}
      />,
    );
    expect(html.match(/<button[^>]*class="fleet-row/g)).toHaveLength(3);
    expect(html).not.toContain("aria-haspopup");
    expect(html).toContain("worker-a");
  });
});
