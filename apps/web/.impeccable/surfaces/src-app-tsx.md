---
version: 1
slug: "src-app-tsx"
primary_target: "src/App.tsx"
related_targets: []
---

# Administrator console (operate)

Scope: the signed-in console shell and every page behind it. Visitor mode: Operate.
Audience: the operator of one Parsar Core deployment. Task: judge health, capacity,
usage and failures; inspect resources; manage nodes, keys and configuration.
Constraints: existing public client and console routes only; browser aggregation is
bounded and labelled; missing data stays visibly missing; Parsar identity retained.

Information architecture: Monitor (Overview, Agent metrics, Sandbox metrics,
Sessions log) · Resources (Agents, Environment templates, Vaults) · Infrastructure
(Nodes) · Settings (API keys, System) · Playground (Session console, Agent builder,
Getting started).

## Direction contract

THESIS: The home is the fleet, not a builder: one health strip, the deployment's nodes and sandboxes on the left, evidence for the selected target on the right. Refuses the card-grid dashboard with create shortcuts.
OWN-WORLD: Parsar neutrals on white, one indigo accent for selection and primary actions only, hairline rules instead of cards, tabular numerals, status dots, dense rows, system sans.
STORY: The operator sees in one glance whether Core, nodes and agents are healthy, drills from a node to its sandboxes and failing Sessions, and opens deeper metrics pages.
FIRST VIEWPORT: Page header; full-width KPI strip; fleet list at one third; selected-target metrics and allocations at two thirds; attention table beneath.
FORM: Fleet split-pane, sixth of seven grounded structures; seed e20a0bc1.
FINISH: unreviewed and undocumented is unfinished; this build ends with the finish review, the verdict, DESIGN.md, and every shipping raster carrying its provenance
