# OpenAgentCore website

The website is the landing page plus the published documentation, built with [VitePress](https://vitepress.dev) and deployed to GitHub Pages. It reads the repository's existing `docs/`, `contracts/` and `docs.json` in place: no page is copied, moved or rewritten.

## Run it

```sh
pnpm --dir website install --frozen-lockfile
pnpm --dir website dev       # http://127.0.0.1:4180
pnpm --dir website build     # website/.vitepress/dist
pnpm --dir website test      # after build: navigation and output checks
pnpm --dir website preview   # serve the build at http://127.0.0.1:4181
```

## How documentation reaches the site

- The content root is the repository root. `docs/**` and `contracts/**` keep their paths as URLs, so `docs/architecture.md` is `/docs/architecture`. `website/index.md` and `website/zh/index.md` are mapped onto `/` and `/zh/`. Chinese sources in `docs/zh/` and `contracts/agents-api/zh/` map to `/zh/docs/` and `/zh/contracts/agents-api/`; the language switch opens the matching page.
- `docs.json` owns the page list and order. The sidebar and `/llms.txt` are generated from it at build time; each page's frontmatter `title` is its label. Every Markdown page under `docs/` and `contracts/` is built; listing it in `docs.json` adds it to the sidebar and `llms.txt`.
- Each page's source Markdown is published next to its HTML (`/docs/architecture.md`), and the `README` paths declared in `docs.json` redirect to their section index.
- Relative links from a page to a file that is not a published page, such as `CONTRIBUTING.md` or `openapi.yaml`, are rewritten to GitHub at build time, so the Markdown works unchanged on GitHub and on the site.

The visual system combines warm paper surfaces, green accents, ruled sections and ASCII artwork. Landing headings use self-hosted Space Grotesk; body text uses Inter and code uses Geist Mono. Chinese text uses the system Chinese sans-serif stack. Both light and dark themes share the same hierarchy.

Site appearance and metadata are configured in `.vitepress/config.mts` and `.vitepress/theme/`.

The landing page lives in `.vitepress/theme/`. `landing-content.ts` holds its English and Chinese copy; every claim there must be backed by a page in `docs/` or `contracts/`, and its harness protocols follow [Model execution](../contracts/agents-api/model-execution.md).

The landing page has five numbered sections: the interactive combination example, an architecture image, execution lifecycle, console screenshots and installation. Four documentation shortcuts link to the detailed guides. `components/SessionFlow.vue` illustrates native execution, Core orchestration and multiple Session environments through a three-stage hand-drawn animation. It autoplays while visible and offers previous/next buttons, a stage slider and pause/play. Reduced-motion preferences disable autoplay. The diagram uses the locally bundled [Virgil font](https://github.com/excalidraw/virgil); its OFL license is stored beside the font in `.vitepress/theme/assets/fonts/`. Section headings share the hero’s display font with white text and green italic accents.

### Ecosystem logo wall

`components/LogoWall.vue` places two full-width white logo rows between the documentation shortcuts and the closing installation section. The wall is transparent and borderless, allowing the landing page's background and grid to continue through it. Harness and model brands occupy the first row; cloud and compute brands occupy the second. They scroll in opposite directions, pause on hover or row keyboard focus, and have an explicit pause control. Reduced-motion preferences disable animation and leave both rows manually scrollable. Labels and controls use the landing page's English and Chinese copy.

`.vitepress/theme/ecosystem-logos.ts` owns the brand list. These are ecosystem illustrations; supported Harness combinations remain defined by [Model execution](../contracts/agents-api/model-execution.md), and host requirements by [Installation](../docs/getting-started/install.md). Brand artwork does not establish deployment qualification or a partnership.

SVG files are bundled under `.vitepress/theme/assets/logos/` and rendered in white with CSS, preserving their proportions. Their sources are:

- AI brands, AWS, Azure, Google Cloud, Alibaba Cloud, Tencent Cloud, Huawei Cloud, Volcengine, Baidu AI Cloud and DigitalOcean: [Lobe Icons](https://github.com/lobehub/lobe-icons), from `@lobehub/icons-static-svg` version `1.95.1`, using the matching filename in its `icons/` directory. MIT license: `LICENSE-lobe-icons.txt` beside the assets.
- Docker: [SVGL's Docker SVG](https://github.com/pheralb/svgl/blob/main/static/library/docker.svg). MIT license: `LICENSE-svgl.txt` beside the assets.
- Hetzner: [Simple Icons' Hetzner SVG](https://github.com/simple-icons/simple-icons/blob/develop/icons/hetzner.svg). CC0 notice: `LICENSE-simple-icons.txt` beside the assets.
- E2B: the medium white symbol from [E2B's official brand assets](https://changelog.e2b.dev/brand), file `e2b-symbol-white-m.svg`. Preserve its proportions and clear space according to that source's usage guidance.

Brand names and marks belong to their respective owners. Keep provenance and license notices with any added or replaced artwork.

## Publish

`make check-website` builds and tests the site. `make check-docs` checks repository Markdown links, including explicit heading IDs used by translations. core-check runs it for pull requests that change the website, published documentation or relevant Node dependencies; `.github/workflows/website.yml` builds and deploys `main` to GitHub Pages. A repository administrator enables Pages once: **Settings → Pages → Source: GitHub Actions**.

The publishing step reads `html_url` directly from the GitHub Pages API and passes it to the build as `WEBSITE_URL`. VitePress derives the base path from that URL, supporting both `https://<owner>.github.io/<repository>/` and a custom domain set under **Settings → Pages → Custom domain**. An empty or invalid URL stops the build. Local builds omit `WEBSITE_URL` to serve from `/`.

Pull request checks build under the published `/OpenAgentCore/` path. Output tests verify that generated navigation, assets, redirects and `llms.txt` links use the configured path and that local HTML links resolve to generated files. To check a deployment path locally, run `WEBSITE_URL=https://example.com/OpenAgentCore/ make check-website`.

## Maintain bilingual documentation

English documentation owns technical facts. Translate each authored page in `docs/` and `contracts/agents-api/` into its mirrored `zh/` path in the same change. Keep code blocks, API identifiers and exact Web action names unchanged; translate explanatory prose and preserve the English heading ID with an explicit `{#id}` anchor. Resolve relative links from the Chinese file to the corresponding Chinese document or shared original asset. The Harness catalog generator produces both language editions. Extend other generators before translating their output; preserve generated regions in otherwise authored pages byte for byte, with a Chinese explanation before the region.

Each Chinese frontmatter records `source` as the repository-relative English path and `source_hash` as its SHA-256 digest. Update that digest only after reconciling the complete translation with the source. `pnpm --dir website check-translations` checks coverage and freshness before the build. `docs.json` remains the single navigation order; titles and summaries are read from the selected language's source.
