---
version: 1
slug: "src-app-tsx"
primary_target: "src/App.tsx"
related_targets: ["src/ConsoleApp.tsx"]
---

# Administrator console (operate)

Scope: the signed-in console shell and every page behind it, including Getting started on the Overview and the optional console tour. Visitor mode: Operate.
Audience: the administrator of one OpenAgentCore deployment. Task: judge health, capacity, usage and failures; inspect and delete or copy project assets; manage projects, keys, nodes and each harness's default model.
Constraints: Web API only (`/core/v1`); missing data stays visibly missing; no small print, explanations live in help tips; API terms stay English in Chinese copy; zh-CN and English, light and dark.

Information architecture: Monitor (Overview, Agent metrics, Sandbox metrics, Session log) · Resources (Agents, Environment templates, Skills, Files, Vaults) · Platform (Projects and keys, Nodes, System).

Unresolved: deployment configuration wizard waits for backend fields.

## Direction contract

THESIS: One calm instrument panel for a whole deployment; every screen speaks one component language so the administrator reads state, not layout. Refuses the assembled dashboard of mismatched widgets and loading spinners.
OWN-WORLD: Beautiful UI's foundation: cool near-white canvas, white cards drawn by a hairline ring and smooth layered shadow, neutral ink ramp, pill buttons (ink primary), Inter with CJK system fallback, tabular numerals, semantic tints as condiment; Parsar indigo as the only accent, for selection, links and data.
STORY: The administrator lands on health, sees what needs attention, drills into a project, Session or node, and acts (delete, copy, issue, revoke) without waiting on a spinner.
FIRST VIEWPORT: Page header with title, filters and refresh on one line; KPI strip; the page's primary card (chart grid, table or topology); nothing above the fold is a loader.
FORM: User-pinned world (Beautiful UI + Parsar indigo, 2026-09-24); concept roll skipped because a user-pinned direction beats the roll.
FINISH: unreviewed and undocumented is unfinished; this build ends with the finish review, the verdict, DESIGN.md, and every shipping raster carrying its provenance
