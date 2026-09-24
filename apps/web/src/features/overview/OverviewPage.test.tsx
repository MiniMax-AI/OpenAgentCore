import type { ReactElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";

import { ProjectsProvider } from "../../lib/projects";
import { AgentMetricsPage } from "../metrics/AgentMetricsPage";
import { SandboxMetricsPage } from "../metrics/SandboxMetricsPage";
import { OverviewPage } from "./OverviewPage";

const render = (page: ReactElement) => renderToStaticMarkup(<ProjectsProvider>{page}</ProjectsProvider>);

describe("Monitor pages before data arrives", () => {
  it("shows missing figures instead of zero and draws Core before the nodes load", () => {
    const html = render(<OverviewPage />);
    expect(html).toContain("Overview");
    expect(html).not.toMatch(/metric-tile-value kpi-value">0</);
    expect(html).toContain('metric-tile-value kpi-value">—<');
    expect(html).toContain(">Core<");
    expect(html).toContain("Checking");
    expect(html).not.toContain("fleet-node");
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
