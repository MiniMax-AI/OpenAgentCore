---
name: OpenAgentCore Console
description: The management console for one self-hosted OpenAgentCore deployment; projects, their assets and keys, health and capacity, with flat page surfaces, clear navigation and compact resource tables.
colors:
  ink: "oklch(0.247 0.006 258.361)"
  ink-muted: "oklch(0.506 0.01 264.477)"
  ink-subtle: "oklch(0.55 0.009 264.505)"
  sidebar-ink: "oklch(0.506 0.01 264.477)"
  surface: "oklch(1 0 0)"
  surface-subtle: "oklch(0.979 0.002 247.839)"
  surface-muted: "oklch(0.961 0.001 286.375)"
  canvas: "oklch(0.961 0.002 247.84)"
  frame-line: "color-mix(in oklch, oklch(0.247 0.006 258.361) 11%, transparent)"
  line: "oklch(0.90 0.003 264.542)"
  line-muted: "oklch(0.966 0.002 264.542)"
  line-strong: "oklch(0.85 0.005 258.326)"
  hover: "oklch(0.97 0.002 247.839)"
  pressed: "oklch(0.933 0.003 247.86)"
  tile: "oklch(0.933 0.003 247.86)"
  accent: "oklch(0.52 0.165 277)"
  accent-emphasis: "oklch(0.47 0.16 277)"
  accent-fg: "#ffffff"
  data: "oklch(0.56 0.14 277)"
  success: "oklch(0.6 0.12 158)"
  warning: "oklch(0.68 0.135 62)"
  danger: "oklch(0.585 0.17 25)"
  status-queued: "oklch(0.55 0.009 264.505)"
  series-1: "oklch(0.56 0.14 277)"
  series-2: "oklch(0.7 0.09 195)"
  series-3: "oklch(0.78 0.11 80)"
  series-4: "oklch(0.66 0.12 20)"
  series-5: "oklch(0.62 0.06 250)"
  series-6: "oklch(0.72 0.08 145)"
  series-other: "oklch(0.82 0.008 264)"
  meter-fill: "color-mix(in srgb, oklch(0.247 0.006 258.361) 62%, transparent)"
typography:
  metric:
    fontFamily: "\"IBM Plex Sans\", -apple-system, BlinkMacSystemFont, \"PingFang SC\", \"Hiragino Sans GB\", \"Segoe UI\", \"Microsoft YaHei\", \"Noto Sans SC\", \"Helvetica Neue\", Helvetica, Arial, sans-serif"
    fontSize: "28px"
    fontWeight: 600
    lineHeight: "36px"
    letterSpacing: "-0.025em"
    fontFeature: "\"tnum\""
  display:
    fontFamily: "\"IBM Plex Sans\", -apple-system, BlinkMacSystemFont, \"PingFang SC\", \"Hiragino Sans GB\", \"Segoe UI\", \"Microsoft YaHei\", \"Noto Sans SC\", \"Helvetica Neue\", Helvetica, Arial, sans-serif"
    fontSize: "20px"
    fontWeight: 500
    lineHeight: "26px"
    letterSpacing: "-0.015em"
    fontFeature: "\"tnum\""
  headline:
    fontFamily: "\"IBM Plex Sans\", -apple-system, BlinkMacSystemFont, \"PingFang SC\", \"Hiragino Sans GB\", \"Segoe UI\", \"Microsoft YaHei\", \"Noto Sans SC\", \"Helvetica Neue\", Helvetica, Arial, sans-serif"
    fontSize: "20px"
    fontWeight: 600
    lineHeight: "28px"
    letterSpacing: "-0.015em"
  title:
    fontFamily: "\"IBM Plex Sans\", -apple-system, BlinkMacSystemFont, \"PingFang SC\", \"Hiragino Sans GB\", \"Segoe UI\", \"Microsoft YaHei\", \"Noto Sans SC\", \"Helvetica Neue\", Helvetica, Arial, sans-serif"
    fontSize: "14px"
    fontWeight: 600
    lineHeight: "20px"
    letterSpacing: "-0.01em"
  body:
    fontFamily: "\"IBM Plex Sans\", -apple-system, BlinkMacSystemFont, \"PingFang SC\", \"Hiragino Sans GB\", \"Segoe UI\", \"Microsoft YaHei\", \"Noto Sans SC\", \"Helvetica Neue\", Helvetica, Arial, sans-serif"
    fontSize: "13px"
    fontWeight: 400
    lineHeight: "18px"
  label:
    fontFamily: "\"IBM Plex Sans\", -apple-system, BlinkMacSystemFont, \"PingFang SC\", \"Hiragino Sans GB\", \"Segoe UI\", \"Microsoft YaHei\", \"Noto Sans SC\", \"Helvetica Neue\", Helvetica, Arial, sans-serif"
    fontSize: "12.5px"
    fontWeight: 500
    lineHeight: "18px"
  mono:
    fontFamily: "\"Geist Mono Variable\", ui-monospace, \"SF Mono\", Menlo, Consolas, \"Liberation Mono\", monospace"
    fontSize: "11.5px"
    fontWeight: 400
rounded:
  hairline: "4px"
  control: "4px"
  control-inner: "3px"
  segment: "8px"
  tooltip: "8px"
  popover: "14px"
  frame: "6px"
  window: "8px"
  pill: "999px"
spacing:
  xs: "4px"
  sm: "8px"
  md: "12px"
  lg: "16px"
  gutter: "24px"
  section: "24px"
components:
  card:
    backgroundColor: "{colors.surface}"
    rounded: "{rounded.frame}"
  button-primary:
    backgroundColor: "{colors.ink}"
    textColor: "{colors.canvas}"
    rounded: "{rounded.control}"
    padding: "0 13px"
    height: "32px"
  button-outline:
    backgroundColor: "{colors.surface}"
    textColor: "{colors.ink}"
    rounded: "{rounded.control}"
    padding: "0 13px"
    height: "32px"
  button-outline-hover:
    backgroundColor: "{colors.surface-subtle}"
  button-danger:
    backgroundColor: "{colors.danger}"
    textColor: "#ffffff"
    rounded: "{rounded.control}"
    padding: "0 13px"
    height: "32px"
  button-ghost:
    textColor: "{colors.ink-muted}"
    rounded: "{rounded.control}"
    padding: "0 13px"
    height: "32px"
  refresh-button:
    textColor: "{colors.ink-muted}"
    rounded: "{rounded.control}"
    size: "32px"
  back-button:
    textColor: "{colors.ink-muted}"
    rounded: "{rounded.control}"
    size: "32px"
  text-action:
    textColor: "{colors.ink-muted}"
    typography: "{typography.label}"
    rounded: "{rounded.control-inner}"
    height: "24px"
  input-field:
    backgroundColor: "{colors.surface-muted}"
    textColor: "{colors.ink}"
    rounded: "{rounded.control}"
    padding: "0 10px"
    height: "32px"
  search-field:
    backgroundColor: "{colors.surface}"
    textColor: "{colors.ink}"
    rounded: "{rounded.control}"
    height: "32px"
  select:
    backgroundColor: "{colors.surface-muted}"
    textColor: "{colors.ink}"
    rounded: "{rounded.control}"
    padding: "0 30px 0 10px"
    height: "32px"
  segmented-track:
    backgroundColor: "{colors.pressed}"
    rounded: "{rounded.segment}"
    padding: "2px"
  segmented-option:
    textColor: "{colors.ink-muted}"
    rounded: "{rounded.control-inner}"
    padding: "0 11px"
    height: "26px"
  segmented-option-active:
    backgroundColor: "{colors.surface}"
    textColor: "{colors.ink}"
  nav-item:
    textColor: "{colors.sidebar-ink}"
    rounded: "{rounded.control}"
    padding: "0 8px"
    height: "36px"
  nav-item-active:
    backgroundColor: "var(--hover-2)"
    textColor: "{colors.ink}"
  metric-tile:
    backgroundColor: "transparent"
    textColor: "{colors.ink}"
    typography: "{typography.metric}"
    padding: "0 20px"
  kpi-cell:
    textColor: "{colors.ink}"
    typography: "{typography.display}"
    padding: "14px 16px 16px"
  table-header:
    backgroundColor: "var(--inset)"
    textColor: "{colors.ink-muted}"
    padding: "0 12px"
    height: "36px"
  table-row:
    textColor: "{colors.ink}"
    typography: "{typography.body}"
    padding: "6px 12px"
    height: "44px"
  empty-state:
    backgroundColor: "{colors.surface}"
    textColor: "{colors.ink-subtle}"
    rounded: "{rounded.frame}"
    padding: "36px 24px"
  help-tip:
    textColor: "{colors.ink-subtle}"
    rounded: "{rounded.pill}"
    size: "18px"
  modal:
    backgroundColor: "{colors.surface}"
    textColor: "{colors.ink}"
    rounded: "{rounded.window}"
  meter:
    backgroundColor: "{colors.surface-muted}"
    rounded: "{rounded.pill}"
    height: "5px"
---

# Design System: OpenAgentCore Console

## Overview

**Creative North Star: "The Operator's Ledger"**

The console is a compact developer and operator workspace. Flat page surfaces, a separated navigation rail and clear typography provide the structure. Resource tables use horizontal rules; independent charts and summaries use small-radius frames. Avoid nested cards. Colour is spent on problems and on the data itself, almost never on decoration. The indigo accent means "you selected this" or "this is a link"; its data shade (`--data`) means "this is the single measured quantity". Primary actions are filled with ink.

Density is deliberately high and calm: 13px body, 44px table rows, 32px controls, tabular figures in every column. The system is bilingual (zh-CN and English) and ships light and dark themes on the same token names; dark swaps values, never structure. It honours reduced motion and treats keyboard focus as a first-class state (2px indigo outline).

The data contract is part of the look. Core reports only what it observes, so the interface shows absence honestly: an em dash, a gap in a line, the word "Unavailable" or "Unknown". Explanations stay one click away behind a circled question mark, so the page stays a ledger rather than a leaflet.

**Key Characteristics:**
- Each page fills the content column without an outer radius or shadow. The sidebar uses a subtle surface and a dividing rule. Page headers and table headers have clear bottom rules.
- One indigo voice for selection, focus, links and single-series data (`--data`); the primary button is ink.
- Meters are neutral ink; green, amber and red appear only when something is wrong or a state needs reporting.
- A six-slot categorical palette for multi-series data, bound to the entity, not its rank.
- One list grammar on every resource page: project filter, search, count, name with compact ID, creator, row actions.
- Tabular numerals everywhere a number can line up.
- Status is always a dot plus a plain-language label.
- Task instructions and empty-state next steps stay visible. Supplementary definitions belong in help tips. Errors, warnings and safety notices stay visible.

## Colors

A restrained neutral ledger with one indigo voice, three signal colours and a separate categorical palette that belongs to multi-series data alone.

### Primary
- **OpenAgentCore Indigo** (accent): keyboard focus outlines and rings, the focus ring of fields, the text caret and the text selection wash. Deepens to **Pressed Indigo** (accent-emphasis) for hovered name links. It is the console's only accent.
- **Data** (`--data`, the same colour as Series 1, a lighter indigo): the one measured series of a chart that has only one, such as Sessions created per hour on Overview, drawn as a tint (62% into the surface) rather than full strength.

### Neutral
- **Ledger Ink** (ink): primary text, figures, table cells, headings.
- **Graphite** (ink-muted): secondary text, column headers, KPI labels, axis ticks, inactive controls, row actions at rest.
- **Pencil** (ink-subtle): help-tip glyphs, crosshairs, untoned dots.
- **Sidebar Ink** (sidebar-ink): navigation text.
- **Canvas** (canvas): the app frame around the page panel, and the sidebar's ground (`oklch(0.231 0.004 264.487)` in dark).
- **Paper** (surface): the page panel, the page header, cards, tables, KPI strips, chart grids, empty states, dialogs and the active navigation chip.
- **Card Edge** (frame-line): the 1px ring of cards, ink at 11%.
- **Margin Gray** (surface-subtle): coverage notes, dialog footers, empty-state icon tiles, and the hover of outline buttons.
- **Well Gray** (surface-muted): the fill of inputs and selects inside cards and dialogs, meter rails, count pills and value pills.
- **Hairline** (line): internal dividers of cards, the page header rule, chart gridlines, dialog rules and the ring of inputs.
- **Faint Rule** (line-muted): row dividers inside tables.
- **Firm Rule** (line-strong): the ring of outline buttons, and of search fields and selects in toolbars and headers.
- **Hover / Pressed / Tile**: opaque cool grays. Hover is the wash of table rows, ghost buttons and text actions; Pressed is the wash of navigation items and icon buttons and the segmented-control track.

### Signal
- **Healthy Green** (success), **Caution Amber** (warning), **Fault Red** (danger): status dots, KPI and tile tone dots, meter fills past their thresholds, error text, error notices, destructive buttons and the hover of destructive row actions.
- **Queued Gray** (status-queued, the same value as Pencil) is the pending KPI tone; idle and neutral status dots use Pencil. A running or pending status dot uses Series 1.

### Data (categorical)
- **Series 1–6** (Indigo, Teal, Ochre, Coral, Slate, Sage) and **Series Other**: lines, stacked bars and legend keys of multi-series charts (requests by model, calls by tool, average against P95 duration, Runtime trends). Dark theme re-tunes each slot under the same name.
- **Meter Fill** (meter-fill): ink at 62%, the healthy fill of every meter.
- Meter rails are Well Gray with an inset hairline.

### Named Rules
**The Colour Only for Problems Rule.** A healthy state is drawn in ink. Meters fill in neutral ink and turn amber or red only past their thresholds; tone dots appear only on figures that report a state.

**The One Voice Rule.** Indigo is for selection, focus, links and the single `--data` series. Multi-series charts draw from `--series-1..6` and `--series-other`, never from the accent.

**The Entity Owns Its Colour Rule.** A categorical colour follows the entity (model, tool), never its rank. An entity keeps its slot while visible; only slots of entities that left the view are reused. Anything beyond six series collapses into Series Other.

## Typography

- **Display Font:** IBM Plex Sans (with the system UI sans as fallback: -apple-system, Segoe UI, and PingFang SC / Microsoft YaHei / Noto Sans SC for Chinese)
- **Body Font:** IBM Plex Sans, the same stack; locally bundled in weights 400, 500, 600 and 700
- **Label/Mono Font:** Geist Mono Variable (with ui-monospace / SF Mono / Menlo) for identifiers and code

**Character:** One quiet sans in several weights, sized for dense reading; hierarchy comes from weight and a tight scale, not from a second typeface. Mono appears only for machine identifiers, key prefixes, models and commands.

### Hierarchy
- **Metric** (600, 24px, 32px, -0.015em, tabular): the four Overview tiles.
- **Display** (500, 20px, 26px, -0.015em, tabular): KPI strip figures. Figures stand out by weight and position, one step above body text.
- **Headline** (600, 20px, 28px, -0.015em): the page title in the page header; one per page. Detail pages put the back button before it.
- **Title** (600, 14px, 20px, -0.01em): section and card headings. Dialog titles are 600 at 15px; empty-state titles 500 at 13.5px.
- **Body** (400, 13px, 18px): table cells, controls, form fields, dialog text. The document base is 14px/20px; help tips run 12px/18px.
- **Label** (500, 12.5px, 18px): KPI labels, column headers, text actions, segmented options and the list count; fact labels 12px/500 Pencil; axis ticks 11px. Status labels are 13px.
- **Mono** (400, 11.5px): IDs in name cells in Graphite; other code in tables in Pencil.

### Named Rules
**The Columns Line Up Rule.** Every figure that can share a column uses tabular numerals: KPI and tile values, numeric table cells (right-aligned), legend totals, axis ticks, tooltip values, counts.

**The Honest Figure Rule.** Missing data renders as "—", a gap in the line, "Unavailable" or "Unknown"; never as 0. Compact numbers keep two decimals only when the integer part is a single digit ("1.04M"), otherwise one ("415.7万"); values under 10,000 print whole. Durations read "850 ms / 12.4 s / 4m 12s / 3h 5m".

**The Plain Vocabulary Rule.** zh-CN copy uses one term per concept: 项目 (project), 沙箱 (sandbox), 运行时 (runtime), 创建者 (creator), 已上报 (reported), 活跃 (active), 提供方 (provider). API terms stay in English (Agent, Session, Turn, Skill, Vault, Credential, API key). Time ranges read "1 小时 / 6 小时 / 24 小时 / 7 天" (English "1h / 6h / 24h / 7d"), always in the one segmented control style.

## Layout

A fixed 224px sidebar sits beside the flat main column, separated by a 1px rule. Below 640px it becomes a 56px icon rail with accessible names and no clipped labels. The header is at least 56px tall, with a 20px/600 title and actions on the right; actions wrap onto a new line when the available width cannot contain them. The scrolling body uses `24px 24px 40px` padding and a 24px section gap; horizontal padding becomes 16px below 640px. Wide tables scroll within their own frame.

The recurring shapes in the body are the KPI strip (auto-fit columns, min 158px; three per row below 1180px), chart grids (two equal columns, single below 1180px), full-width resource tables, and a fact row on detail pages. Overview has its own arrangement: Getting started while a step is to do, a four-column metric strip separated by vertical rules, Session activity beside a compact fleet inventory, then flat attention and project usage tables. The activity and fleet grid has a 24px gap and stacks below 680px of content width; the metric strip uses two columns below 560px. Core and up to four nodes open anchored popovers with facts and links to their pages. When more nodes exist, offline and degraded nodes take priority and a link leads to the full list. Popovers are the overlay card (8px radius, overlay shadow, 16px padding): a 14px title, 12px labels over 13px values, links at a ruled foot. Nodes itself is a plain list with a detail page.

Spacing follows a 4px base: 4, 8, 12, 16, 24 (page gutter and section gap). Controls are 32px tall, segmented options 26px, table rows 44px (32px compact), table headers 36px.

Fleet availability uses labeled status dots. Retained observations keep their stale disclosure and never imply live traffic.

**The One Page Grammar Rule.** Every page uses PageHeader, PageBody and Section from `components/console-ui.tsx`, and every resource list uses the list grammar from `components/list-ui.tsx`. No page invents its own header height, gutter, section rhythm or toolbar.

## Elevation & Depth

Page structure comes from typography, spacing and dividers. Reserve elevation for overlays.

### Shadow Vocabulary
- **Page surface**: flat and full-height, with no outer radius or shadow.
- **Card** (no shadow; a 1px `frame-line` ring at 11% ink): KPI strips, chart grids, Overview cards, the Session transcript and the deployment panel. Cards are flat; no page surface is translucent or blurred.
- **Control ring** (a 1px Firm Rule ring with an extra-small shadow): outline buttons, the active segment, and search fields and selects in toolbars. Inputs inside cards and dialogs carry only a Hairline ring.
- **Overlay** (a 1px Hairline ring with a large soft shadow): anchored popovers, menus, dialogs, help tips and chart tooltips.

### Named Rules
**The One Card Rule.** Figures, charts and tables sit in one card divided by 1px internal rules. A card never contains another bordered, shadowed container; empty states remain unframed, including inside a card.

## Shapes

Use 4px corners on controls, 3px on inner options, 6px on cards and 8px on dialogs. Resource tables and page surfaces have square edges. Keep pills for compact badges and circles for status dots. Borders are 1px.

## Components

### Buttons
Compact and quiet; the primary button is the only filled button in a header.
- **Shape:** 4px corners, 32px tall, 0 13px padding, 13px/500 label, optional 14px Lucide icon. No button is a pill.
- **Primary:** Ledger Ink fill with Canvas text and a faint inner highlight; hover lowers it to 88% opacity. Used for the one affirmative header action (Create project) and for the submit button of non-destructive dialogs (create, rename, issue, continue).
- **Outline:** Paper face with the control ring; hover takes Margin Gray. Used for every action in a card or section header (Issue key, Manage nodes, Session log, Projects and keys), Download on the Skill page, Cancel in dialogs and empty-state actions.
- **Danger:** Fault Red fill, white text: the confirm button of every destructive dialog. On a page, a destructive button such as Delete on a detail page is red text on an outline button that takes a red tint on hover.
- **Ghost:** transparent with Graphite text; hover takes Ink on the Hover wash.
- **Focus / Press:** focus draws a 2px indigo outline 2px outside the button; press scales to 0.96.
- **Text action:** borderless Graphite 12.5px/500, 24px tall with 6px corners, that turns Ink on the Hover wash; used only for per-row actions in tables (Rename, Archive, Delete) and links in a popover's foot, never in a header. A destructive text action turns red on hover.

### Refresh button
A 32px ghost icon button with the refresh glyph. Controls that scope the whole page (project filter, time range) come before it; on detail pages it leads, followed by any outline actions and Delete. It spins while reading; its tooltip carries the last update time instead of a visible timestamp.

### Segmented control
The single style for ranges, order and status filters. A Pressed-gray track (2px padding, 8px corners) holds 26px options in Graphite; the chosen option sits on a Paper thumb with the control ring and Ledger Ink text, and the thumb glides to a new choice (Motion shared layout). Options may carry a tabular count. It is a radiogroup with arrow-key movement.

### Selects and the project filter
Selects and inputs are 32px fields filled Well Gray with a Hairline ring inside cards and dialogs, and Paper with the control ring in toolbars and headers. Focus turns them Paper with a 1px indigo ring and a 4px indigo tint around it. Selects draw their own chevron. The project filter is a select whose first option is **All projects**; archived projects are listed with "· archived".

### List grammar
Every resource list, the Session log and the project list share one grammar:
- **ListToolbar**: on project-scoped lists the project filter first, then the SearchField (280px, search icon, Paper with the control ring), then any further filters (segmented status or order, selects); the count sits on the right in 12.5px Pencil ("12 total", "3 of 12", "40 loaded" when more exist).
- **Project column**: shown only while All projects is selected, right after the name; archived projects are muted.
- **NameCell**: the first column. The name at 500 weight (a link that turns indigo on hover when the row opens a detail page; a muted fallback such as "Untitled" when the resource has no name) with the compact ID underneath in 11.5px mono. The ID's copy button appears on row hover or focus; the full ID lives in its tooltip.
- **Creator column**: the last column before the actions, headed "Creator" with a help tip. It shows the creating key's name (its prefix when unnamed) with a small "Revoked" flag for revoked keys, "Admin copy" in Graphite for an asset Core records as an administrator copy, "Unknown" in Graphite when Core has no record, and "—" while loading or when the lookup failed.
- **RowActions**: text actions right-aligned at the end of the row, 16px apart, ending with Delete (red on hover). A row click opens the detail page; action clicks do not.
- **Partial failure**: when some projects fail to load, one red line names them above the table; the other projects still show.
- **Empty state**: an unframed, centered block with an optional 24px outline icon, a clear title, a visible short explanation and a relevant action. First-use states explain how data arrives; filtered states offer Clear search; failed reads retain their error and retry. Empty Overview activity links to Projects and keys for API onboarding.
- **Load more**: an outline button centred under its table when more rows exist.

### Detail pages
- The page header starts with a **back button** (32px ghost icon button, arrow-left, Graphite) before the title; the actions on the right start with Refresh, continue with outline actions such as Download, and end with Delete (red text on an outline button).
- Under the header, **resource-facts** lays out the facts in one card as an auto-fill grid of label/value pairs, each column at least 176px wide (a 12px/500 Pencil label over a 13px value, 14px by 24px gaps). It starts with the ID (with its copy button) and the Project and includes the Creator.
- Sections follow: usage figures in a KPI strip, then tables in cards.
- A Session's **History** header holds an outline "Jump to the failed Turn" (with the count when several failed) before the view switch while any Turn failed; it shows the conversation (the Turn table when there are no Items), scrolls the page body to the next failed Turn and focuses it.
- An active project's page ends its keys with a **How to call** section (see Dialogs) before its write operations.
- A self-hosted Session's **Executor credentials** section ends with **Connect a host**: a Linux/macOS or PowerShell selector, the one copyable command Core generated for that platform, and a link to the native installation guide. The console shows Core's command as it is and never builds one. The command installs the daemon and its Harnesses, starts it and checks its connection; its authorization expires after 30 minutes. Requirements and reconnection details belong in the title help. Reconnection after credential rotation requires `stop`, replacement of the configured credential file, then `start`; disconnection does not imply process exit. Connection status comes only from Core. When Core has no command, a note replaces it; an archived project shows a note instead.

### Dialogs
Dialogs are 448px Paper cards (960px when wide) with 8px corners, a 52px header and a 56px Margin Gray footer separated by Hairlines, and the overlay shadow. They cannot be closed while a request runs.
- **ConfirmDialog**: the one grammar for destructive actions. The body states what will be deleted and its consequences; the footer holds Cancel (outline) and the confirm button (danger), whose label changes while busy. Core's reason for a rejection, or an uncertain-outcome warning, appears in red inside the dialog. The Skill page's delete dialogs follow the same grammar; deleting a whole Skill also requires typing its name. Archiving a project says in bold that it can't be undone, then how many active keys it revokes (the project read's count, or more when its loaded key list shows more) and that assets and accepted work stay; with active keys it too requires typing the project's name, shown in mono with its inner spaces kept (surrounding spaces are forgiven, Unicode compared in NFC). While the project list is read again Archive waits; if that read failed, a red line says the count may be out of date and Archive stays disabled.
- **Key dialogs**: name fields carry their rules in a help tip and their problem in red underneath. The issued key appears in a read-only field with a copy button, under a notice that it is shown once; only "I've saved this key" dismisses it. Closing the dialog moves the key into a pending notice card on the page.
- **Executor credential dialog** (640px): the shown-once notice, then a prompt to save the JSON privately before Done. Download credential file is primary; Copy credential is secondary. Installation commands are not repeated here. Done forgets the credential; closing preserves it in the pending card. The native installer reads the unchanged JSON file: its absolute path is entered during interactive installation or passed with `--credential-file`; tokens never enter command arguments.
- **Add node**: the sandbox limits first, then the one-time command in a Terminal block (expiry countdown and Copy command in its header), the three progress steps, and, once the installer's minute passes, an amber card with the reason and a copyable system-service log command. Below, the Host requirements Hairline disclosure is open until this browser has shown it once. Installation requires root or sudo, creates the `oac-node` system service, and serves one Core per host because nodes share the service account. For Docker, explain that membership in the docker group is root-equivalent. Do not expose an ordinary-user installation command or user-service prerequisites. The command downloads from the installation's public URL, never the browser's address, so it works as shown on any host. Until the installation is read, a line says it is being checked; a failed read, a public URL other machines can't use (loopback or not HTTPS), or a console without the provider's node files replaces the limits with one line saying why (the failed read with Try again), and the footer offers nothing to generate. Once the node is ready, while Getting started is open, one line under the green status names the next step (set a default model provider, or finish Getting started) with a text action to System or the Overview.
- **Clean up the host**: after a node is removed, a dialog gives the host's uninstall command in the same Terminal block, a Graphite line that it deletes no sandboxes, volumes or images (and, for microsandbox, keeps its image store and data). The command requires root or sudo; there is no user-service alternative. A node enrolled with an earlier Core address adds an "Old Core address gone?" disclosure with the `--force` form. The command, too, downloads from the public URL, which the dialog reads again if it is not at hand: until then one line says it is being checked, a failed read says so with Try again, and a public URL other machines can't use (loopback, or none) gets a line saying the service stays on the host and no command can be given. Done dismisses it and focus returns to the page heading.
- **Use Docker instead of microsandbox?**: choosing Docker in sandbox setup lists what it gives up, each point a 600 Ink lead over a Graphite line: weaker isolation (containers share the host kernel; microsandbox gives each sandbox its own microVM), root-equivalent access (the node's account joins the docker group) and limited use (trusted workloads, or hosts without KVM). The footer holds Use Docker (outline) and Keep microsandbox (primary), which takes focus; closing or Escape keeps microsandbox too.
- **Edit node**: the name, then the sandbox limit with one 12px Graphite line under it once the node's heartbeat has the host's CPUs and memory: the host, each sandbox's size and at most how many fit. The Nodes list and a node's Capacity show "Active / limit" for Docker and microsandbox alike, so a saved limit shows where it was set.
- **How to call**: wherever a new key is shown, a card under it gives three copyable samples, each a Margin Gray block with a Hairline and its label and copy button in a header row: a Shell block exporting `OPENAI_BASE_URL` (the installation's API base URL) and `OPENAI_API_KEY` (the new key) together, then curl and Python (with the pinned SDK), each listing the project's Agents and creating a Session with a first message (`environment`, an inline `agent` with `model: "<model>"`, and `input`). A copy the clipboard refuses selects the sample and says so in red underneath. One Graphite line says to put a model the model provider serves in place of `<model>`, and that running an Agent needs a model provider: in each request, saved on the Agent, or the deployment default. An active project's page shows the same samples as a section without any key: the Shell block exports a quoted placeholder, and a Graphite line above the samples says to use a key issued for this project, shown only once at issuance. When the public address is loopback, a note above the samples says the API is reachable only on the Core machine; without a public address only a note to set one shows. Before the installation is read, a skeleton holds the first sample's place.

### Navigation
Sidebar groups Monitor, Resources and Platform with 12px Pencil group labels; items are 36px rows with an 18px outline icon and primary text. Hover takes the Pressed gray; the active item has a neutral background and an indigo edge. Compact icon-rail rows are 40px tall. The Platform group sits below a hairline. A secondary page (one Session) highlights its parent. The footer holds Show Getting started, then sign-out and the language/theme menu. A detail page's back arrow returns to the page it was opened from (a Skill opened from a template goes back to the template); opened directly, it goes to its list. The arrow is labelled plainly "Back".

### KPI strip and metric tiles
A KPI strip is one card of equal cells separated by inset rules. Each cell: a 12.5px Graphite label with an optional help tip, then the figure at 20px/500 with any unit or limit small beside it, optionally led by an 8px tone dot. Overview uses four separate metric tiles instead: a 13px label with a help tip, the same 20px figure (the service status as a dot and a word), and one 12.5px line of context. Figures ellipsize rather than wrap. Live figures on monitor pages roll their digits to a new value on refresh (NumberFlow) instead of swapping.

### Help tip
An 18px circular button holding a 13px circled "?" in Pencil; hover or open takes Ledger Ink on the Pressed gray. It opens on hover, focus or click (click pins it), closes on Escape, scroll or resize, and renders a dark 12px tooltip (8px corners, overlay shadow, max 288px wide) in a portal. The text also exists in a visually hidden element for assistive technology.

### Status dot
A 6px circle plus a plain 13px label: ok green, warning amber, danger red, pending Series 1 with a soft expanding ring while work is in progress, neutral Pencil. A waiting Session names the result its application must submit under the label in lists, with the caller's responsibility in a help tip. Its detail page shows both in the Waiting for facts. A failed Session's reason, as Core sent it, stays visible under the label in 12px Graphite: in full on the Session page, its line breaks kept; in the Session log on one truncated line, with the full text in its tooltip, that never widens the status column. Never a coloured pill, never colour alone.

### Meter
A 5px pill rail in Well Gray with an inset hairline and a neutral ink fill. The fill turns amber at 90% and red at 100% of its limit by default, and a nonzero ratio shows at least 3% width. An unknown ratio draws an empty rail. A share meter may carry a fixed identity colour and then ignores thresholds.

### Charts
Time-series charts live in chart panels (caption 13px/600, legend with series totals, plot) inside one chart-grid card. Lines are 2px round-joined with a surface-ringed end dot; gridlines are crisp Hairlines with 11px tabular ticks; hovering draws a Pencil crosshair, a hover-wash band and a dark tooltip. Missing buckets are gaps, not zeros. Every chart has a 26px table toggle at its top right that opens its numbers in a dialog, so the chart grid keeps its layout. When a range is first shown, bars rise from the baseline in a short left-to-right wave and lines trace from their first point; refreshes of the same range redraw in place.

### Tables
A card with a sticky 36px Paper header in Graphite 12.5px/500 over a Hairline, 44px rows divided by Faint Rules, hover wash, right-aligned tabular numerics, clickable rows where a detail page exists, and the list grammar above. Agent metrics' By Agent table links a saved Agent's name to its page and a nonzero Failed figure (in its red) to the Session log with its project and Agent filters set to that Agent, every status; both turn indigo on hover. The figure counts failed Turns in the range, as the column's help tip says, so the link's name and tooltip give that count and say it opens the Agent's Sessions. A key count in a section heading reads "3 active · 1 revoked" (revoked left out at zero).

### Notices
On Overview and Session log, failed reads that leave a section unavailable replace its contents with ErrorState and Retry. Partial or stale reads keep useful rows and figures, with a durable ErrorState and Retry beside them explaining that coverage may be incomplete or out of date. A failed read never supplies a zero chart or an all-clear; successfully read zero values stay zero. Session log status counts stay missing until the reads succeed. A failed summary retains its last rows, and a failed project Session read retains only that project's last rows; successful sources update independently. Retention never crosses project scopes.

A failed action whose outcome needs a decision (a sandbox change with no answer, a timeout or a 5xx) opens an error dialog with the reason and the next step as its primary button. Failed refreshes and project reads also raise an error toast; other failed actions, Core's clear refusal of a sandbox change among them, are reported there with the reason. A refusal leaves the page usable as it was. Errors inside a dialog or a form stay beside what they concern. Coverage notes (Margin Gray, Hairline ring, 8px corners, 12.5px Graphite) state bounded aggregation. Standing warnings that need action use an amber-tinted line at the top of the page body. On Nodes, this names nodes still bound to an old Core address; each of those nodes' status reads Old address (amber dot) with "Remove and add again" under it in 12px Graphite. Partial-data chips are amber-tinted pills with a help tip. Safety notices (a key shown once, a destructive consequence) stay visible in body text.

A local-only installation has the same amber notice on Overview, Nodes and System: other machines cannot connect, followed by Core's configuration path and apply command as copyable values. If Core has no configuration snapshot, state that those instructions are unavailable; never fill in a path or command. Add node is disabled with its reason beside the action, and Getting started leaves its first step to do with the address fix visible. A pending or failed installation read cannot complete that step; a failed read shows Unknown and Retry.

### Onboarding
Signing in and the console tour share one frame: a dark stage on the left (always dark, whatever the theme) and the task panel on the right, which follows the theme. The stage is the product's one authored moment: a flickering indigo dot grid under slow light rays (Magic UI's flickering grid and light rays), Core as the OpenAgentCore mark on a tile with a travelling border beam, and two orbits of Agents, Sessions, Skills, Vaults, files, templates and machines around it; the OpenAgentCore mark is itself nodes on a ring. Brand copy sits bottom-left in solid ink; it is a paragraph, not a heading, because the panel's title names the task. Signing in asks for one thing, the deployment's Core key, in a single password field; the default key location and a copyable read command stay visible beneath it, with a reminder to substitute a custom installation directory. The key’s authority stays in a help tip. A refused key, too many attempts or an unavailable console is an error beside the field. Signing in opens the console on the Overview. The optional tour has three chapters — Monitor, Resources, Platform — whose stage shows a real dark screenshot of those pages, tilted towards the panel; it takes the place of the console until its last button, Skip or Escape, and then returns the focus to the control that opened it. Entering the console or the tour, and leaving the tour, happen inside a View Transition: the old page dissolves forward and the new one is revealed in a circle growing from the pressed button. With reduced motion the orbits hold their places, the grid is a still frame and no transition runs.

### Getting started
The first card on the Overview while any step is to do: a card header ("Getting started", "n of 4 done", a help tip, then a ghost Take the tour button and an icon button that hides it) over four rows split by Faint Rules. Each row has a 22px numbered ring (a check on the tile wash when done), a 13px/600 title over one 12.5px Graphite line, a status dot (Done in green, To do in Pencil, Checking pending, Unknown for a failed read) and one outline action while the step is to do: Set up sandboxes, Add node, Open Nodes or Open sandbox backend; Open System; Create project (which continues to the new project's first key) or Issue key; See how to call (the newest active project, preferring one with an active key), or Projects and keys without an active project. Add node, Create project and Issue key open their page with the dialog already open; Open System brings the Default model provider section to the top of the page body and focuses the default harness's Set or Replace; See how to call opens the project and, once its keys, usage and address are read, brings its How to call heading to the top of the page body, focused. Only the page body scrolls; the page header stays. Every step done turns it into one line, "You're set", with Take the tour and Dismiss; it stays, through the tour, until dismissed, and the checklist does not come back on its own. The choice is kept per installation in the browser, also while the deployment cannot be read; Show Getting started, a quiet row above the sidebar's account controls, opens it again at any time.

### Sandbox setup
Setting up hosted sandboxes is a set of pages inside System’s Sandbox configuration secondary page, one decision each: where sandboxes run (own machines or E2B), then the backend or the E2B account, then the size of each sandbox (three presets; E2B skips it, since each sandbox takes the template build's size), then a review. Choices are large cards that advance on a click; short indigo dashes show the progress; pages slide and blur across. The backend page compares microsandbox and Docker behind a help tip; microsandbox comes first, preselected (a saved backend stays selected), with a neutral Recommended pill beside its title. Docker takes a confirmation (see Dialogs) once per visit to setup; a saved Docker deployment has already made it. The review states where sandboxes run, the size, the Runtime (taken from this console's distribution manifest) and the Core address, read-only: it is config.json's `public_url`, and the console never asks for it. A loopback address carries an amber line under it: only the Core machine reaches it. When Core rejects the configuration for it (E2B with a loopback `public_url`), a red-tinted block under the review keeps Core's message and adds the config file and apply command as copyable values. A save attempt clears the transient E2B key. Initial setup then asks for it again, with a link to that step; an update may leave it blank to keep the committed key. Advanced settings, one link away, hold the complete form: resources (not for E2B), the Runtime release and the E2B template. A change keeps the saved size and Runtime while the backend stays the same (a saved size outside the presets is offered as Current). Same-backend editing starts at size or E2B credentials with the provider fixed. It is an online configuration update, including when older sandboxes remain: existing node identities and resource ownership are retained. Changing the backend or E2B team requires reset and then a new setup. E2B updates can omit the key to retain it; every explicitly entered key takes the verified replacement path and advances the target generation on success, including the same value. Rejections remain inline with a safe reason and a deliberate way back to reset; never infer teams from a key, auto-reset or auto-resubmit. Optional explanations sit behind help tips; errors and safety consequences remain visible.

### Configuration generations
A single rollout row opens a details dialog for Core's target generation, previous-generation sandboxes and rollout counts. Poll rapidly only while Core reports preparing, or while the independent reset is active. Settled is preparation state, not proof that all nodes are ready or all older Sessions have ended. Retained old resources alone must not keep rapid polling alive. Render failed, update-required and unknown target states distinctly. Keep offline/live-provider status separate from a node's durable serving-generation pin; the pin alone never means the node is online or ready. Node detail shows the serving generation and target preparation; allocation detail shows the owned configuration generation. Do not calculate rollout completion from these rows or promise immediate placement on the target.

A generation-only update within the same installation/backend lifecycle retains compatible previous node/allocation evidence while refreshing. Failed or pending reads visibly qualify those observations; never replace them with fabricated zeros. Reset, backend and installation lifecycle changes discard incompatible data. The shared deployment query and write ownership below govern navigation, late reads, explicit retries and login isolation.

### Sandbox reset
Sandbox configuration offers an explicit reset through its panels and confirmation dialogs. The confirmation keeps to one concise consequence paragraph, two mode choices, the auto deadline and footer actions. Put cleanup sequencing and preservation details in help tips. Auto clear is selected first, with a one-hour deadline editable from 5 minutes to 24 hours; Force clear and escalation require destructive confirmation. State directly that hosted work is archived, remaining active work may be cancelled, archived Sessions cannot resume and unpersisted workspace contents may be lost. Details about preserved histories, Files/Artifacts and unaffected self-hosted execution live behind the reset impact help tip. Cancelling an active reset stops further clearing but cannot undo completed archives.

A persistent progress panel uses Core's busy, idle and cleanup counts, deadline and named offline-node blockers. Bring blocked nodes online for confirmed cleanup; never offer a browser-side force-release shortcut. Poll the deployment every five seconds only while its reset is non-null. A passed deadline does not establish force or completion; only a Core response does. Completion opens the setup flow, with a new explicit save using the generation read from Core, including zero on a fresh install. Reset and online configuration rollout have independent authoritative progress; neither automatically replays configuration.

Read deployment progress independently of node details. Partial failures retain successful facts with a visible stale/unavailable notice. An uncertain write opens the recovery dialog and requires a new authoritative read before another mutation; refresh reads state and never resubmits the write. The connection's QueryClient owns both the authoritative deployment and pending or uncertain writes across route transitions. Leaving Sandbox configuration cannot cancel or forget a submitted reset, and a cached node snapshot cannot replace a newer reset or completion learned on Overview. Returning to Nodes or Sandbox configuration reads the shared deployment immediately and refreshes node evidence separately. Only a successful authoritative read begun after the write settles can release the mutation block; an earlier or still-pending read cannot. Submitting consumes the reset confirmation even if its outcome is uncertain; recovery uses the separate read-and-review dialog. Observation retries preserve applicable non-secret configuration drafts. A changed installation, owner epoch, backend, mode or generation discards the prior draft and confirmation. Logout clears this connection-scoped state.

### System page
Four sections, each saying where it changes. Installation: the public address, API base URL, installation ID and source commit as a fact card, with an outline action that opens the Domain and HTTPS secondary page. Default model configuration, the one section changed here: one card per harness in an auto-fill grid, its header holding the harness name and outline actions (Set, or Replace and Clear); fact rows give the harness's read-only startup state (a status dot and a Default pill, its source behind a help tip), then the default model ID, provider protocol, base URL, whether a key is configured, token limits when set and the update time, or Not set. Set and Replace open one form dialog. The model ID is required; advanced settings disclose an optional JSON object editor with formatting and inline syntax errors, plus token limits. The harness list supplies supported protocols, native protocols, JSON support and required limits from one adapter declaration. The form uses those fields without harness-specific branches. Nonempty JSON requires a native protocol; the form explains an incompatible selection beside the editor. Help tips explain the scope of native settings. Changing the model ID, provider URL or protocol clears the native JSON so settings cannot follow an unrelated model by accident. Re-entering the required write-only API key alone does not change model identity. The key field is a required password input, never prefilled or shown and forgotten when the form closes. Core's rejection stays in red inside the form; Clear is a ConfirmDialog. Usage details opens Core’s observations in a separate dialog. Sandboxes: one navigation row to the Sandbox configuration secondary page; do not repeat its configuration facts on System. Startup settings: a line naming the config file and the apply command as copyable chips, with when they were last applied, over a table of each setting, its value and the services a change restarts. Sensitive settings show only Configured or Not set; Default and Fixed after install are neutral pills beside the value.

### One place for each task
A configuration or operation has one home. Other pages link to it instead of repeating the same panel. System links to the Sandbox configuration secondary page; Nodes contains node management. Keep the configuration page flat: the resource editor is a dialog, and rollout is one status row with a details action. Put low-frequency counts and generation metadata in that dialog. Explanatory prose belongs in help tips, not rows of small print. Keep actionable errors and unresolved state visible without duplicating the whole workflow.

### Diagnostic observations
Failure reasons belong beside the failed Session or Turn status. Their first read uses a skeleton; an unavailable reason names that state and puts the read retry beside its help tip. Technical classifications are translated through one catalogue, not shown as raw error codes. Trace Timing labels Core receipt times separately from tool-reported duration; explanations of batched delivery, clock differences and historical gaps live in help tips.

The self-hosted connection panel names Core's observed state, the bound key and last heartbeat. The bound-key action uses the rotation confirmation and the one-time credential flow. Stale observations cannot complete Run on host.

### Loading and motion
The console has no spinners and no "Loading…" lines. Reads are cached (TanStack Query) and prefetched on navigation hover, so revisits show data at once and refreshes keep the last data on screen. Only a first read shows a skeleton in the final layout's cards: table rows, a headline strip with chart panels, or a facts card with a table, with a sweep that repeats every 900ms and stops under reduced motion. Work in progress is the Agent's shimmering "Working…" line in the conversation and the breathing pending dot.

Motion reports state and never makes anyone wait: the navigation chip and segmented thumbs glide (Motion, one 320ms spring without bounce), figures roll, charts draw in once per range, new conversation messages settle 6px upward in 260ms, pages fade in 160ms, anchored popovers scale from 98% and dialogs from 96%. Reduced motion makes all of it instant.

## Do's and Don'ts

### Do:
- **Do** keep next steps visible and put supplementary definitions behind a circled "?" help tip; report errors in a dialog or a toast; keep warnings and safety notices (deletion consequences, a key shown once) visible.
- **Do** start every project-scoped toolbar with the project filter, then search, with the count on the right.
- **Do** end every resource table with the Creator column and then the row actions.
- **Do** confirm every deletion in ConfirmDialog.
- **Do** keep meters in neutral ink and let amber and red mean a threshold was crossed.
- **Do** reserve OpenAgentCore Indigo for selection, focus, links and the single `--data` series.
- **Do** place figures, charts and tables in one card divided by 1px internal rules.
- **Do** render missing data as "—", a chart gap, "Unavailable" or "Unknown".
- **Do** show status as a 6px dot plus a plain label.
- **Do** use tabular numerals for every aligned figure and right-align numeric columns.
- **Do** build every page from PageHeader, PageBody and Section.

### Don't:
- **Don't** hide instructions needed to complete a task in a help tip.
- **Don't** colour healthy meters, bars or states; colour is for problems and data.
- **Don't** colour multi-series data with the indigo accent.
- **Don't** nest cards inside cards or draw a dashed empty state.
- **Don't** render an unreported value as 0 or draw a missing interval as a zero line.
- **Don't** use coloured status pills or colour-only status.
- **Don't** reassign a categorical colour by rank when data re-sorts.
- **Don't** show full IDs in list columns; show the compact ID with its copy button.
- **Don't** add uppercase letter-spaced micro-labels or eyebrow lines above headings; a section is named by its title alone.
- **Don't** mix synonyms in zh-CN copy (for example alternating 沙盒 with 沙箱, or API 密钥 with API key).

## Navigation icons and first use

Use Lucide throughout. Navigation icons are 18px with a 1.75px stroke; shared tool icons use the same stroke at their existing compact sizes. Default navigation icons use secondary ink, selected icons use primary ink. The selected row has a neutral background and a narrow indigo marker.

Getting started emphasizes the first confirmed incomplete step with the primary button while leaving every step independently available. Only authoritative, successful zero-Session reads replace the activity chart with first-use guidance. Missing and failed reads retain their existing uncertainty and recovery controls.
