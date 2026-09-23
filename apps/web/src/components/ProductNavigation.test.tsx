import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";

import { ProductNavigation } from "./ProductNavigation";

describe("Product navigation", () => {
  it("exposes only the Core-backed product destinations and current page", () => {
    const html = renderToStaticMarkup(
      <ProductNavigation active="sessions" onSelect={() => undefined} />,
    );

    expect(html).toContain('aria-label="Agents product"');
    expect(html).toContain('class="main-nav product-navigation"');
    expect(html).toContain("Workspace");
    expect(html).toContain("Dashboard");
    expect(html).toContain("Agents");
    expect(html).toContain("Sessions");
    expect(html).not.toContain("Vaults");
    expect(html).not.toContain("Environments");
    expect(html).not.toContain("Templates");
    expect(html).toContain('aria-current="page"');
  });

  it("shows Vaults only after Core capability discovery succeeds", () => {
    const html = renderToStaticMarkup(
      <ProductNavigation active="vaults" showVaults onSelect={() => undefined} />,
    );

    expect(html).toContain("Vaults");
    expect(html).toContain('aria-current="page"');
  });

  it("exposes Templates in managed Environment builds", () => {
    const html = renderToStaticMarkup(
      <ProductNavigation active="templates" showTemplates onSelect={() => undefined} />,
    );
    expect(html).toContain("Templates");
    expect(html).toContain('aria-current="page"');
  });
});
