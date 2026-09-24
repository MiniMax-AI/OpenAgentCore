import type { ReactElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";

import { ProjectsProvider } from "../../lib/projects";
import { AgentMetricsPage } from "../metrics/AgentMetricsPage";
import { SandboxMetricsPage } from "../metrics/SandboxMetricsPage";
import { OverviewPage } from "./OverviewPage";

const render = (page: ReactElement) => renderToStaticMarkup(<ProjectsProvider>{page}</ProjectsProvider>);

describe("Monitor pages before data arrives", () => {
  it("shows missing figures instead of zero and lists Core beside the hosts", () => {
    const html = render(<OverviewPage />);
    expect(html).toContain("Overview");
    expect(html).not.toMatch(/metric-tile-value kpi-value">0</);
    expect(html).toContain('metric-tile-value kpi-value">—<');
    // Core has no slots and reports no CPU or memory yet: say so instead of inventing figures.
    expect(html).toContain(">Core<");
    expect(html.match(/Not reported/g)).toHaveLength(2);
  });

  it("puts the project filter before the time range on Agent metrics", () => {
    const html = render(<AgentMetricsPage />);
    const filter = html.indexOf("All projects");
    const range = html.indexOf('role="radiogroup"');
    expect(filter).toBeGreaterThan(-1);
    expect(filter).toBeLessThan(range);
  });

  it("keeps node figures missing while the fleet loads", () => {
    const html = render(<SandboxMetricsPage />);
    expect(html).toContain("Sandbox metrics");
    expect(html).not.toMatch(/kpi-value">0</);
    expect(html).toContain("Loading Runtime observations…");
  });
});
