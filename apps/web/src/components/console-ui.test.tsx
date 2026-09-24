import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";

import type { AgentSession, RuntimeObservation } from "@agents-core-web/agents-client";

import { HelpTip, Kpi, KpiStrip, PageHeader } from "./console-ui";

describe("console grammar", () => {
  it("keeps help text in the document and links it to the help button", () => {
    const html = renderToStaticMarkup(<HelpTip>Saving does not start a Runtime.</HelpTip>);
    const describedBy = html.match(/aria-describedby="([^"]+)"/)?.[1];
    expect(describedBy).toBeTruthy();
    expect(html).toContain('aria-label="Help"');
    expect(html).toContain(`id="${describedBy}" class="visually-hidden">Saving does not start a Runtime.</span>`);
    expect(html).not.toContain('role="tooltip"');
  });

  it("renders KPI labels with values and hides details behind help", () => {
    const html = renderToStaticMarkup(
      <KpiStrip label="Health">
        <Kpi label="Needs attention" value="5" tone="danger" help="Failed 3 · Waiting 2" />
      </KpiStrip>,
    );
    expect(html).toContain('<dl class="kpi-strip" aria-label="Health">');
    expect(html).toContain("kpi kpi-danger");
    expect(html).toContain('<span class="kpi-value">5</span>');
    expect(html).not.toContain("<small>");
  });

  it("renders page headers without description paragraphs", () => {
    const html = renderToStaticMarkup(<PageHeader title="Overview" help="Health and capacity." headingId="h" />);
    expect(html).toContain('<h1 id="h">Overview</h1>');
    expect(html).not.toContain("<p>");
  });
});
