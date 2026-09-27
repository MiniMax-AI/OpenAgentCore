---
name: OpenAgentCore Console
description: The management console for one self-hosted OpenAgentCore deployment; projects, their assets and keys, health and capacity, on raised cards over a quiet canvas.
colors:
  ink: "#37352f"
  ink-muted: "#787774"
  ink-subtle: "#9b9a97"
  sidebar-ink: "#5f5e5a"
  surface: "#ffffff"
  surface-subtle: "#fafafa"
  surface-muted: "#f1f1f0"
  canvas: "#f5f5f6"
  card-border: "rgb(20 20 30 / 11%)"
  line: "#e9e9ec"
  line-muted: "#efeff1"
  line-strong: "#d6d7dc"
  hover: "rgb(55 53 47 / 3%)"
  pressed: "rgb(55 53 47 / 6%)"
  tile: "rgb(55 53 47 / 7%)"
  accent: "#4f46e5"
  accent-emphasis: "#4338ca"
  accent-fg: "#ffffff"
  data: "#4f46e5"
  success: "#16a34a"
  warning: "#d97706"
  danger: "#dc2626"
  status-queued: "#9a9ca4"
  status-idle: "#b4b4b9"
  series-1: "#2a78d6"
  series-2: "#eb6834"
  series-3: "#1baf7a"
  series-4: "#eda100"
  series-5: "#e87ba4"
  series-6: "#008300"
  series-other: "#a3a3a8"
  meter-fill: "color-mix(in srgb, #37352f 62%, transparent)"
  meter-track: "rgb(55 53 47 / 8%)"
typography:
  metric:
    fontFamily: "-apple-system, BlinkMacSystemFont, \"PingFang SC\", \"Hiragino Sans GB\", \"Segoe UI\", \"Microsoft YaHei\", \"Noto Sans SC\", \"Helvetica Neue\", Helvetica, Arial, sans-serif"
    fontSize: "30px"
    fontWeight: 600
    lineHeight: "36px"
    letterSpacing: "-0.025em"
    fontFeature: "\"tnum\""
  display:
    fontFamily: "-apple-system, BlinkMacSystemFont, \"PingFang SC\", \"Hiragino Sans GB\", \"Segoe UI\", \"Microsoft YaHei\", \"Noto Sans SC\", \"Helvetica Neue\", Helvetica, Arial, sans-serif"
    fontSize: "24px"
    fontWeight: 500
    lineHeight: "28px"
    letterSpacing: "-0.01em"
    fontFeature: "\"tnum\""
  headline:
    fontFamily: "-apple-system, BlinkMacSystemFont, \"PingFang SC\", \"Hiragino Sans GB\", \"Segoe UI\", \"Microsoft YaHei\", \"Noto Sans SC\", \"Helvetica Neue\", Helvetica, Arial, sans-serif"
    fontSize: "20px"
    fontWeight: 600
    lineHeight: "26px"
    letterSpacing: "-0.02em"
  title:
    fontFamily: "-apple-system, BlinkMacSystemFont, \"PingFang SC\", \"Hiragino Sans GB\", \"Segoe UI\", \"Microsoft YaHei\", \"Noto Sans SC\", \"Helvetica Neue\", Helvetica, Arial, sans-serif"
    fontSize: "15px"
    fontWeight: 600
    lineHeight: "22px"
    letterSpacing: "-0.01em"
  body:
    fontFamily: "-apple-system, BlinkMacSystemFont, \"PingFang SC\", \"Hiragino Sans GB\", \"Segoe UI\", \"Microsoft YaHei\", \"Noto Sans SC\", \"Helvetica Neue\", Helvetica, Arial, sans-serif"
    fontSize: "13px"
    fontWeight: 400
    lineHeight: "18px"
  label:
    fontFamily: "-apple-system, BlinkMacSystemFont, \"PingFang SC\", \"Hiragino Sans GB\", \"Segoe UI\", \"Microsoft YaHei\", \"Noto Sans SC\", \"Helvetica Neue\", Helvetica, Arial, sans-serif"
    fontSize: "12.5px"
    fontWeight: 500
    lineHeight: "18px"
  mono:
    fontFamily: "ui-monospace, \"SF Mono\", Menlo, Consolas, \"Liberation Mono\", \"Noto Sans Mono\", monospace"
    fontSize: "11.5px"
    fontWeight: 400
rounded:
  hairline: "4px"
  control: "6px"
  control-inner: "5px"
  segment: "7px"
  popover: "8px"
  frame: "12px"
  pill: "999px"
spacing:
  xs: "4px"
  sm: "8px"
  md: "12px"
  lg: "16px"
  gutter: "28px"
  section: "28px"
components:
  card:
    backgroundColor: "{colors.surface}"
    rounded: "{rounded.frame}"
  button-primary:
    backgroundColor: "{colors.accent}"
    textColor: "{colors.accent-fg}"
    rounded: "{rounded.control}"
    padding: "0 10px"
    height: "28px"
    typography: "{typography.label}"
  button-primary-hover:
    backgroundColor: "{colors.accent-emphasis}"
  button-outline:
    backgroundColor: "{colors.surface}"
    textColor: "{colors.ink}"
    rounded: "{rounded.control}"
    padding: "0 10px"
    height: "28px"
  button-outline-hover:
    backgroundColor: "{colors.hover}"
  button-danger:
    backgroundColor: "{colors.danger}"
    textColor: "#ffffff"
    rounded: "{rounded.control}"
    padding: "0 10px"
    height: "28px"
  button-ghost:
    textColor: "{colors.ink-muted}"
    rounded: "{rounded.control}"
    padding: "0 10px"
    height: "28px"
  refresh-button:
    textColor: "{colors.ink-muted}"
    rounded: "{rounded.segment}"
    size: "32px"
  back-button:
    textColor: "{colors.ink-muted}"
    rounded: "{rounded.control}"
    size: "28px"
  text-action:
    textColor: "{colors.ink-muted}"
    typography: "{typography.label}"
  input-field:
    backgroundColor: "{colors.surface}"
    textColor: "{colors.ink}"
    rounded: "{rounded.control}"
    padding: "4px 8px"
    height: "28px"
  search-field:
    backgroundColor: "{colors.surface}"
    textColor: "{colors.ink}"
    rounded: "{rounded.control}"
    height: "28px"
  select:
    backgroundColor: "{colors.surface}"
    textColor: "{colors.ink}"
    rounded: "{rounded.control}"
    padding: "0 28px 0 9px"
    height: "28px"
  segmented-track:
    backgroundColor: "{colors.surface-muted}"
    rounded: "{rounded.segment}"
    padding: "2px"
  segmented-option:
    textColor: "{colors.ink-muted}"
    rounded: "{rounded.control-inner}"
    padding: "0 10px"
    height: "24px"
  segmented-option-active:
    backgroundColor: "{colors.surface}"
    textColor: "{colors.ink}"
  nav-item:
    textColor: "{colors.sidebar-ink}"
    rounded: "{rounded.control}"
    padding: "0 8px"
    height: "30px"
  nav-item-active:
    backgroundColor: "{colors.pressed}"
    textColor: "{colors.ink}"
  metric-tile:
    backgroundColor: "{colors.surface}"
    textColor: "{colors.ink}"
    typography: "{typography.metric}"
    rounded: "{rounded.frame}"
    padding: "16px 18px 18px"
  kpi-cell:
    textColor: "{colors.ink}"
    typography: "{typography.display}"
    padding: "14px 16px 16px"
  table-header:
    backgroundColor: "{colors.surface-subtle}"
    textColor: "{colors.ink-muted}"
    padding: "0 12px"
    height: "34px"
  table-row:
    textColor: "{colors.ink}"
    typography: "{typography.body}"
    padding: "6px 12px"
    height: "44px"
  empty-state:
    backgroundColor: "{colors.surface}"
    textColor: "{colors.ink-muted}"
    rounded: "{rounded.frame}"
    padding: "40px 24px"
  help-tip:
    textColor: "{colors.ink-subtle}"
    rounded: "{rounded.pill}"
    size: "18px"
  modal:
    backgroundColor: "{colors.surface}"
    textColor: "{colors.ink}"
    rounded: "{rounded.popover}"
  meter:
    backgroundColor: "{colors.meter-track}"
    rounded: "{rounded.pill}"
    height: "6px"
---

# Design System: OpenAgentCore Console

## Overview

**Creative North Star: "The Operator's Ledger"**

The console is a management tool, not a developer showroom. Every screen reads like
a ledger page laid on a desk: a quiet canvas, one header, then raised white cards
holding the evidence (figures, charts, tables). Structure comes from the card edge,
1px internal rules and whitespace; there are no cards inside cards. Colour is spent
on problems and on the data itself, almost never on decoration. The indigo accent
means "you selected this", "this is the primary action" or, as `--data`, "this is
the single measured quantity".

Density is deliberately high and calm: 13px body, 44px table rows, 28px controls,
tabular figures in every column. The system is bilingual (zh-CN and English) and
ships light and dark themes on the same token names; dark swaps values, never
structure. It honours reduced motion and treats keyboard focus as a first-class
state (2px indigo outline).

The data contract is part of the look. Core reports only what it observes, so the
interface shows absence honestly: an em dash, a gap in a line, the word
"Unavailable" or "Unknown". Explanations stay one click away behind a circled
question mark, so the page stays a ledger rather than a leaflet.

**Key Characteristics:**
- White cards with a faint border and shadow on a light-gray canvas, beside a white
  sidebar; the page header is solid canvas.
- One indigo voice for selection, primary actions and single-series data (`--data`).
- Meters are neutral ink; green, amber and red appear only when something is wrong
  or a state needs reporting.
- A six-slot categorical palette for multi-series data, bound to the entity, not its
  rank.
- One list grammar on every resource page: project filter, search, count, name with
  compact ID, creator, row actions.
- Tabular numerals everywhere a number can line up.
- Status is always a dot plus a plain-language label.
- Explanations live behind "?" help tips; errors, warnings and safety notices stay
  visible. No small print: an empty state's explanation is a help tip beside its
  title, a field's rules a help tip beside its label, and filler lines are cut.

## Colors

A restrained neutral ledger with one indigo voice, three signal colours and a
separate categorical palette that belongs to multi-series data alone.

### Primary
- **OpenAgentCore Indigo** (accent): keyboard focus outlines and rings, primary buttons,
  hover on name links and text actions, text selection wash. Deepens to **Pressed
  Indigo** (accent-emphasis) on primary hover. It is the brand colour shared with
  the public OpenAgentCore landing.
- **Data** (`--data`, an alias of accent): the one measured series of a chart that
  has only one, such as Sessions created per hour on Overview, drawn as a tint
  (62% into the surface) rather than full strength.

### Neutral
- **Ledger Ink** (ink): primary text, figures, table cells, headings.
- **Graphite** (ink-muted): secondary text, column headers, KPI labels, axis ticks,
  inactive controls, row actions at rest.
- **Pencil** (ink-subtle): help-tip glyphs, crosshairs, untoned dots.
- **Sidebar Ink** (sidebar-ink): navigation text.
- **Canvas** (canvas): the ground of the main column (`#121212` in dark).
- **Paper** (surface): cards, tables, KPI strips, chart grids, empty states,
  dialogs, popovers, the sidebar.
- **Card Edge** (card-border): the 1px border of raised cards.
- **Margin Gray** (surface-subtle): table header band, coverage notes.
- **Well Gray** (surface-muted): segmented-control track, secondary buttons.
- **Hairline** (line): internal dividers of cards, chart gridlines, dialog rules.
- **Faint Rule** (line-muted): row dividers inside tables.
- **Firm Rule** (line-strong): control borders (inputs, selects, search, outline
  buttons) and the pending-key notice.
- **Hover / Pressed / Tile washes**: translucent ink at 3% / 6% / 7% for hover, the
  active navigation item, and count pills.

### Signal
- **Healthy Green** (success), **Caution Amber** (warning), **Fault Red** (danger):
  status dots, KPI and tile tone dots, meter fills past their thresholds, error text,
  error notices, destructive buttons and the hover of destructive row actions.
- **Queued Gray** (status-queued) is the pending KPI tone; **Idle Gray**
  (status-idle) marks idle and neutral status dots. A running or pending status dot
  uses Series 1.

### Data (categorical)
- **Series 1–6** (Cobalt, Persimmon, Jade, Saffron, Rose, Forest) and **Series
  Other**: lines, stacked bars and legend keys of multi-series charts (requests by
  model, calls by tool, average against P95 duration, Runtime trends). Dark theme
  re-tunes each slot under the same name.
- **Meter Fill** (meter-fill): ink at 62%, the healthy fill of every meter.
- **Meter Track** (meter-track): the empty rail under meters.

### Named Rules
**The Colour Only for Problems Rule.** A healthy state is drawn in ink. Meters fill
in neutral ink and turn amber or red only past their thresholds; tone dots appear
only on figures that report a state.

**The One Voice Rule.** Indigo is for selection, focus, primary actions and the
single `--data` series. Multi-series charts draw from `--series-1..6` and
`--series-other`, never from the accent.

**The Entity Owns Its Colour Rule.** A categorical colour follows the entity (model,
tool), never its rank. An entity keeps its slot while visible; only slots of
entities that left the view are reused. Anything beyond six series collapses into
Series Other.

## Typography

**Display Font:** system UI sans (-apple-system, Segoe UI, with PingFang SC /
Microsoft YaHei / Noto Sans SC for Chinese)
**Body Font:** the same system stack
**Label/Mono Font:** ui-monospace / SF Mono / Menlo for identifiers and code

**Character:** One quiet system sans in several weights, sized for dense reading;
hierarchy comes from weight and a tight scale, not from a second typeface. Mono
appears only for machine identifiers, key prefixes, models and commands.

### Hierarchy
- **Metric** (600, 30px, 36px, -0.025em, tabular): the four Overview tiles; the
  largest type in the console.
- **Display** (500, 24px, 28px, -0.01em, tabular): KPI strip figures.
- **Headline** (600, 20px, 26px, -0.02em): the page title in the 64px page header;
  one per page. Detail pages put the back button before it.
- **Title** (600, 15px, 22px, -0.01em): section headings. Card headings and
  empty-state titles step down to 600 at 14px.
- **Body** (400, 13px, 18px): table cells, controls, form fields, dialog text. The
  document base is 14px/20px; help popovers run 12.5px/19px.
- **Label** (500, 12.5px, 18px): KPI labels (400), status labels, text actions,
  segmented options; column headers 500 at 12px; fact labels 12px Graphite; axis
  ticks 11px; the list count 12px.
- **Mono** (400, 11.5px): IDs and code in tables and name cells, in Graphite.

### Named Rules
**The Columns Line Up Rule.** Every figure that can share a column uses tabular
numerals: KPI and tile values, numeric table cells (right-aligned), legend totals,
axis ticks, tooltip values, counts.

**The Honest Figure Rule.** Missing data renders as "—", a gap in the line,
"Unavailable" or "Unknown"; never as 0. Compact numbers keep two decimals only when
the integer part is a single digit ("1.04M"), otherwise one ("415.7万"); values under
10,000 print whole. Durations read "850 ms / 12.4 s / 4m 12s / 3h 5m".

**The Plain Vocabulary Rule.** zh-CN copy uses one term per concept: 项目
(project), 沙箱 (sandbox), 运行时 (runtime), 创建者 (creator), 已上报 (reported),
活跃 (active), 提供方 (provider). API terms stay in English (Agent, Session, Turn,
Skill, Vault, Credential, API key). Time ranges read "1 小时 / 6 小时 / 24 小时 /
7 天" (English "1h / 6h / 24h / 7d"), always in the one segmented control style.

## Layout

A fixed 232px white sidebar beside a full-height main column on the canvas; below
640px the sidebar collapses to a 52px icon rail. The desktop minimum is 960px.
Every page uses the same frame: a 64px header (title, optional help tip, actions
on the right) in solid canvas, then a scrolling body
padded `20px 28px 48px` with sections stacked 28px apart. Inside a section the
heading row sits 12px above its content.

The recurring shapes in the body are the KPI strip (auto-fit columns, min 158px;
three per row below 1180px), chart grids (two equal columns, single below 1180px),
full-width table cards, and a fact row on detail pages. Overview has its own
arrangement: Getting started while a step is to do, four metric tiles, Session
activity beside the fleet topology (Core in the middle, nodes left and right,
solid lines online and dashed offline; Core and each node open an anchored popover with a two-column glance and links to
their pages), then the attention table and usage by project, each on its own
card with a 16px gap. Popovers are the overlay card (14px radius, overlay shadow,
16px padding): a 14px title, 12px labels over 13px values, links at a ruled foot. Nodes itself is a plain list with a detail page.

Spacing follows a 4px base: 4, 8, 12, 16, 28 (page gutter and section gap).
Controls are 28px tall, segmented options 24px, table rows 44px (32px compact),
table headers 34px.

**The One Page Grammar Rule.** Every page uses PageHeader, PageBody and Section
from `components/console-ui.tsx`, and every resource list uses the list grammar
from `components/list-ui.tsx`. No page invents its own header height, gutter,
section rhythm or toolbar.

## Elevation & Depth

Depth comes from the canvas-to-card step, not from stacked shadows.

### Shadow Vocabulary
- **Card** (no shadow; a 1px `card-border` edge at 11% ink): KPI strips, table
  frames, chart grids, Overview cards, the Session transcript and the deployment
  panel. Cards are flat; no page surface is translucent or blurred.
- **Control lift** (`0 1px 2px rgb(0 0 0 / 6%)`): primary and outline buttons,
  inputs, selects, the search field, the active segment.
- **Floating** (`0 1px 2px rgb(24 24 27 / 4%), 0 8px 24px -12px rgb(24 24 27 /
  18%)`): help-tip popovers, chart tooltips, menus and dialogs.

### Named Rules
**The One Card Rule.** Figures, charts and tables sit in one card divided by 1px
internal rules. A card never contains another bordered, shadowed container; an
empty list is itself one card.

## Shapes

12px corners on cards, tables, KPI strips, chart grids, empty states, coverage
notes and the pending-key notice; 8px on dialogs and help popovers; 7px on the
segmented track, the refresh button and chart tooltips; 6px on buttons, inputs,
selects and the search field; 5px on inner segments; 4px on small inline marks and
flags; full pills for meters and count badges; circles for status dots (7px), KPI
tone dots (8px) and tile dots (10px). Legend keys are 9px squares with 2px corners,
or 12×2px strokes for line series. Borders are always 1px.

## Components

### Buttons
Compact and quiet; the primary button is the only filled accent in a header.
- **Shape:** 6px corners, 28px tall, 0 10px padding, 13px/500 label, optional 14px
  Lucide icon.
- **Primary:** OpenAgentCore Indigo fill, white text, control lift; deepens on hover. Used
  for the one affirmative header action (Create project) and for the submit button
  of non-destructive dialogs (create, rename, issue, continue).
- **Outline:** Paper face, Firm Rule border, control lift; hover takes the ink wash.
  Used for every action in a card or section header (Issue key, Manage nodes,
  Session log, Projects and keys), Download on the Skill page, Cancel in dialogs
  and empty-state actions.
- **Danger:** Fault Red fill, white text. Used for Delete on detail pages and for
  the confirm button of every destructive dialog.
- **Ghost:** transparent with Graphite text; darkens on hover.
- **Focus / Press:** focus draws an indigo border plus 1px indigo ring; press scales
  to 0.97.
- **Text action:** borderless Graphite 12.5px/500 that takes the hover wash; used
  only for per-row actions in tables (Rename, Archive, Delete) and links in a
  popover's foot, never in a header. A destructive text action turns red on hover.

### Refresh button
A 32px ghost icon button with the refresh glyph. Controls that scope the whole page
(project filter, time range) come before it; on detail pages it leads, followed by
any outline actions and Delete. It spins while reading; its tooltip carries the last update time
instead of a visible timestamp.

### Segmented control
The single style for ranges, order and status filters. A Well Gray track (2px
padding, 8px corners) holds 26px options in Graphite; the chosen option sits on a
Paper thumb with control lift and Ledger Ink text, and the thumb glides to a new
choice (Motion shared layout). Options may carry a tabular count. It is a
radiogroup with arrow-key movement.

### Selects and the project filter
Selects are 28px Paper fields with a Firm Rule border, control lift and a drawn
chevron; focus swaps the border to indigo with a 1px ring. The project filter is a
select whose first option is **All projects**; archived projects are listed with
"· archived".

### List grammar
Every resource list, the Session log and the project list share one grammar:
- **ListToolbar**: on project-scoped lists the project filter first, then the
  SearchField (280px, search icon, Paper, Firm Rule border), then any further
  filters (segmented status or order, selects); the count sits on the right in 12px
  Graphite ("12 total", "3 of 12", "40 loaded" when more exist).
- **Project column**: shown only while All projects is selected, right after the
  name; archived projects are muted.
- **NameCell**: the first column. The name at 500 weight (a link that turns indigo
  on hover when the row opens a detail page; a muted fallback such as "Untitled"
  when the resource has no name) with the compact ID underneath in 11.5px mono. The
  ID's copy button appears on row hover or focus; the full ID lives in its tooltip.
- **Creator column**: the last column before the actions, headed "Creator" with a
  help tip. It shows the creating key's name (its prefix when unnamed) with a small
  "Revoked" flag for revoked keys, "Admin copy" in Graphite for an asset an
  administrator copied in an earlier release, "Unknown" in Graphite when Core has no
  record, and "—" while loading or when the lookup failed.
- **RowActions**: text actions right-aligned at the end of the row, 16px apart,
  ending with Delete (red on hover). A row click opens the detail page; action
  clicks do not.
- **Partial failure**: when some projects fail to load, one red line names them
  above the table; the other projects still show.
- **Empty state**: a solid card (Paper, 1px Hairline, 12px, 40px 24px padding) with
  an optional 20px outline icon, a 14px/600 title, an optional one-line description
  and an optional action. "No matches" offers Clear search.
- **Load more**: an outline button centred under its table when more rows exist.

### Detail pages
- The page header starts with a **back button** (28px ghost icon button, arrow-left,
  Graphite) before the title; the actions on the right start with Refresh, continue
  with outline actions such as Download, and end with Delete (danger).
- Under the header, **resource-facts** lays out the facts as a grid of up to four
  label/value pairs per row (12px Graphite label over a 13px value, 14px by 40px
  gaps, two columns below 900px). It starts with the ID (with its copy button) and
  the Project and includes the Creator.
- Sections follow: usage figures in a KPI strip, then tables in cards.
- A Session's **History** header holds an outline "Jump to the failed Turn" (with
  the count when several failed) before the view switch while any Turn failed;
  it shows the conversation (the Turn table when there are no Items), scrolls the
  page body to the next failed Turn and focuses it.
- An active project's page ends its keys with a **How to call** section (see
  Dialogs) before its write operations.
- A self-hosted Session's **Executor credentials** section ends with a **Connect
  a host** card when the console serves the self-hosted installer: a 13px/600
  title with a help tip (what revoke and rotate do to the host, how to reconnect,
  how to remove the Runtime), one Graphite line with the steps (run the command,
  paste a credential at its hidden prompt, safe to rerun), the command in a
  Margin Gray Terminal block with an icon copy button, and the host requirements
  on one dot-separated line. The command wraps rather than scrolls. Without a
  public address, with a loopback one, or with a Session address that is not
  `wss://`, one Graphite note takes the command's place; an archived project
  keeps the command and says the host still needs a credential.

### Dialogs
Dialogs are 448px Paper cards with 8px corners, a 48px header and a 52px footer
separated by Hairlines, and the floating shadow. They cannot be closed while a
request runs.
- **ConfirmDialog**: the one grammar for destructive actions. The body states what
  will be deleted and its consequences; the footer holds Cancel (outline) and the
  confirm button (danger), whose label changes while busy. Core's reason for a
  rejection, or an uncertain-outcome warning, appears in red inside the dialog.
  The Skill page's delete dialogs follow the same grammar; deleting a whole Skill
  also requires typing its name. Archiving a project says in bold that it can't be
  undone, then how many active keys it revokes (the project read's count, or more
  when its loaded key list shows more) and that assets and accepted work stay;
  with active keys it too requires typing the project's name, shown in mono with
  its inner spaces kept (surrounding spaces are forgiven, Unicode compared in NFC).
  While the project list is read again Archive waits; if that read failed, a red
  line says the count may be out of date and Archive stays disabled.
- **Key dialogs**: name fields carry their rules in a help tip and their problem in
  red underneath. The issued key appears in a read-only field with a copy button,
  under a notice that it is shown once; only "I've saved this key" dismisses it.
  Closing the dialog moves the key into a pending notice card on the page.
- **Executor credential dialog** (640px): the shown-once notice, then one line
  saying what to do in order. With Connect a host available, the install
  command's Terminal block comes first, so it is copied and run before the
  credential is pasted and Done pressed. Then the credential as one line of JSON
  (wrapped, never pretty-printed), Copy credential (primary: it is pasted at the
  installer's hidden prompt) and Download credential file (outline), with a
  Graphite hint for automation (`chmod 600`, `--credential-file`). Done is
  outline and forgets the credential; closing the dialog keeps it in a pending
  card, which points to the Connect a host command below.
- **Add node**: the sandbox limits first, then the one-time command in a Terminal
  block (expiry countdown and Copy command in its header), the three progress
  steps, and, once the installer's minute passes, an amber card with the reason
  and a copyable log command. Below, two folded Hairline disclosures: Host
  requirements for the default command, which uses sudo (open until this browser
  has shown it once, with notes that it creates the `oac-node` system service
  and, for Docker, that the docker group is root-equivalent), and "No sudo on this
  host?", with what the node's own user needs and the command without sudo.
  Sudo mode allows one Core per host because nodes share the service account.
  For microsandbox without sudo, the home path must be at most 28 bytes after
  filesystem encoding, so `~/.oac/m/<12 hex>` fits the 48-byte Runtime home limit.
  The log command follows the command last copied; after the no-sudo one it adds the
  system service's, for a root shell. The command downloads from the
  installation's public URL, never the browser's address, so it works as shown on
  any host. Until the installation is read, a line says it is being checked; a
  failed read, a public URL other machines can't use (loopback or not HTTPS), or a
  console without the provider's node files replaces the limits with one line
  saying why (the failed read with Try again), and the footer offers nothing to
  generate. Once the node is ready, while Getting started is open, one line under
  the green status names the next step (set a default model provider, or finish Getting
  started) with a text action to System or the Overview.
- **Clean up the host**: after a node is removed, a dialog gives the host's
  uninstall command in the same Terminal block, a Graphite line that it deletes no
  sandboxes, volumes or images (and, for microsandbox, keeps its image store and
  data), and the no-sudo form behind an "Installed without sudo?" disclosure. A
  node enrolled with an earlier Core address adds an "Old Core address gone?"
  disclosure with the `--force` form. The command, too, downloads from the public
  URL, which the dialog reads again if it is not at hand: until then one line says
  it is being checked, a failed read says so with Try again, and a public URL other
  machines can't use (loopback, or none) gets a line saying the service stays on the
  host and no command can be given. Done dismisses it and focus returns to the page
  heading.
- **Use Docker instead of microsandbox?**: choosing Docker in sandbox setup lists
  what it gives up, each point a 600 Ink lead over a Graphite line: weaker
  isolation (containers share the host kernel; microsandbox gives each sandbox
  its own microVM), root-equivalent access (the node's account joins the docker
  group) and limited use (trusted workloads, or hosts without KVM). The footer
  holds Use Docker (outline) and Keep microsandbox (primary), which takes focus;
  closing or Escape keeps microsandbox too.
- **Edit node**: the name, then the sandbox limit with one 12px Graphite line under
  it once the node's heartbeat has the host's CPUs and memory: the host, each
  sandbox's size and at most how many fit. The Nodes list and a node's Capacity
  show "Active / limit" for Docker and microsandbox alike, so a saved limit shows
  where it was set.
- **How to call**: wherever a new key is shown, a card under it gives three
  copyable samples, each a Margin Gray block with a Hairline and its label and copy
  button in a header row: a Shell block exporting `OPENAI_BASE_URL` (the
  installation's API base URL) and `OPENAI_API_KEY` (the new key) together, then
  curl and Python (with the pinned SDK), each listing the project's Agents and
  creating a Session with a first message (`environment`, an inline `agent` with
  `model: "<model>"`, and `input`). A copy the clipboard refuses
  selects the sample and says so in red underneath. One Graphite line says to put
  a model the model provider serves in place of `<model>`, and that running an
  Agent needs a model provider: in each request, saved on the Agent, or the
  deployment default. An active project's page shows the same samples as a
  section without any key: the Shell block exports a quoted placeholder, and a
  Graphite line above the samples says to use a key issued for this project,
  shown only once at issuance. When the public address is loopback, a note above the
  samples says the API is reachable only on the Core machine; without a public
  address only a note to set one shows. Before the installation is read, a
  skeleton holds the first sample's place.

### Navigation
Sidebar groups Monitor, Resources and Platform with 12px Graphite group labels;
items are 30px rows with a 15px outline icon and Sidebar Ink text. Hover takes the
ink wash; the active item sits on a white chip (the page panel's surface, ringed)
with Ledger Ink at 500, and the chip glides to the next item on navigation. The
Platform group sits below a hairline. A secondary page (one Session) highlights its
parent. The footer holds Show Getting started, then sign-out and the language/theme menu. A detail page's back arrow returns to the page it was opened
from (a Skill opened from a template goes back to the template); opened directly,
it goes to its list. The arrow is labelled plainly "Back".

### KPI strip and metric tiles
A KPI strip is one card of equal cells separated by inset rules. Each cell: a
12.5px Graphite label with an optional help tip, then the figure at 20px/500 with
any unit or limit small beside it, optionally led by an 8px tone dot. Overview uses
four separate metric tiles instead: a 13px label with a help tip, the same 20px
figure (the service status as a dot and a word), and one 12.5px line of context.
Figures ellipsize rather than wrap. Live figures on monitor pages roll their digits
to a new value on refresh (NumberFlow) instead of swapping.

### Help tip
An 18px circular button holding a 13px circled "?" in Pencil; hover or open takes
Ledger Ink on the ink wash. It opens on hover, focus or click (click pins it),
closes on Escape, scroll or resize, and renders a 12.5px popover (Paper, Hairline,
8px, floating shadow, max 288px) in a portal. The text also exists in a visually
hidden element for assistive technology.

### Status dot
A 7px circle plus a plain label at 12.5px: ok green, warning amber, danger red,
pending Series 1 with a soft expanding ring while work is in progress, neutral
Idle Gray. A waiting Session names the result its application must submit under
the label in lists, with the caller's responsibility in a help tip. Its detail
page shows both in the Waiting for facts.
A failed Session's reason, as Core sent it, stays visible under the label
in 12px Graphite: in full on the Session page, its line breaks kept; in the
Session log on one truncated line, with the full text in its tooltip, that never
widens the status column. Never a coloured pill, never colour alone.

### Meter
A 6px pill rail in Meter Track with a neutral ink fill. The fill turns amber at 90%
and red at 100% of its limit by default, and a nonzero ratio shows at least 3%
width. An unknown ratio draws an empty rail. A share meter may carry a fixed
identity colour and then ignores thresholds.

### Charts
Time-series charts live in chart panels (caption 13px/600, legend with series
totals, plot) inside one chart-grid card. Lines are 2px round-joined with a
surface-ringed end dot; gridlines are crisp Hairlines with 11px tabular ticks;
hovering draws a Pencil crosshair, a hover-wash band and a floating tooltip. Missing
buckets are gaps, not zeros. Every chart has a 26px table toggle at its top right
that reveals the numbers in a 220px scrolling table. When a range is first shown,
bars rise from the baseline in a short left-to-right wave and lines trace from their
first point; refreshes of the same range redraw in place.

### Tables
A card with a sticky 34px Margin Gray header in Graphite 12px/500, 44px rows divided
by Faint Rules, hover wash, right-aligned tabular numerics, clickable rows where a
detail page exists, and the list grammar above. Agent metrics' By Agent table
links a saved Agent's name to its page and a nonzero Failed figure (in its red) to
the Session log with its project and Agent filters set to that Agent, every
status; both turn indigo on hover. The figure counts failed Turns in the range, as
the column's help tip says, so the link's name and tooltip give that count and say
it opens the Agent's Sessions. A
key count in a section heading reads "3 active · 1 revoked" (revoked left out
at zero).

### Notices
On Overview and Session log, failed reads that leave a section unavailable replace
its contents with ErrorState and Retry. Partial or stale reads keep useful rows
and figures, with a durable ErrorState and Retry beside them explaining that
coverage may be incomplete or out of date. A failed read never supplies a zero
chart or an all-clear; successfully read zero values stay zero. Session log status
counts stay missing until the reads succeed. A failed summary retains its last
rows, and a failed project Session read retains only that project's last rows;
successful sources update independently. Retention never crosses project scopes.

A failed action whose outcome needs a decision (a sandbox change with no answer,
a timeout or a 5xx) opens an error dialog with the reason and the next step as its
primary button. Failed refreshes and project reads also raise an error toast;
other failed actions, Core's clear refusal of a sandbox change among them, are
reported there with the reason. A refusal leaves the page usable as it was.
Errors inside a dialog or a form stay beside what they concern. Coverage notes
(Margin Gray, Hairline, 12px corners, 12.5px Graphite) state bounded aggregation.
Standing warnings that need action use an amber-tinted line at the top of the page
body. On Nodes, this names nodes still bound to an old Core address; each of those
nodes' status reads Old address (amber dot) with "Remove and add again" under it
in 12px Graphite. Partial-data chips are amber-tinted pills with a help tip. Safety
notices (a key shown once, a destructive consequence) stay visible in body text.

A local-only installation has the same amber notice on Overview, Nodes and System:
other machines cannot connect, followed by Core's configuration path and apply
command as copyable values. If Core has no configuration snapshot, state that
those instructions are unavailable; never fill in a path or command. Add node is
disabled with its reason beside the action, and Getting started leaves its first
step to do with the address fix visible. A pending or failed installation read
cannot complete that step; a failed read shows Unknown and Retry.

### Onboarding
Signing in and the console tour share one frame: a dark stage on the left (always
dark, whatever the theme) and the task panel on the right, which follows the
theme. The stage is the product's one authored moment: a flickering indigo dot
grid under slow light rays (Magic UI's flickering grid and light rays), Core as
the OpenAgentCore mark on a tile with a travelling border beam, and two orbits of
Agents, Sessions, Skills, Vaults, files, templates and machines around it; the
OpenAgentCore mark is itself nodes on a ring. Brand copy sits bottom-left in solid
ink; it is a paragraph, not a heading, because the panel's title names the task.
Signing in asks for one thing, the deployment's Core key, in a single password
field; the default key location and a copyable read command stay visible beneath
it, with a reminder to substitute a custom installation directory. The key’s
authority stays in a help tip. A refused key, too many attempts or an unavailable console is an error
beside the field. Signing in opens the console on the Overview. The optional tour
has three chapters — Monitor, Resources, Platform — whose stage shows a real dark
screenshot of those pages, tilted towards the panel; it takes the place of the
console until its last button, Skip or Escape, and then returns the focus to the
control that opened it. Entering the console or the tour, and leaving the tour,
happen inside a View Transition: the old page dissolves forward and the new one
is revealed in a circle growing from the pressed button. With reduced motion the
orbits hold their places, the grid is a still frame and no transition runs.

### Getting started
The first card on the Overview while any step is to do: a card header ("Getting
started", "n of 4 done", a help tip, then a ghost Take the tour button and an icon
button that hides it) over four rows split by Faint Rules. Each row has a 22px
numbered ring (a check on the tile wash when done), a 13px/600 title over one
12.5px Graphite line, a status dot (Done in green, To do in Idle Gray, Checking
pending, Unknown for a failed read) and one outline action while the step is to
do: Set up sandboxes, Add node, Open Nodes or Open sandbox backend; Open System;
Create project (which continues to the new project's first key) or Issue key;
See how to call (the newest active project, preferring one with an active key), or
Projects and keys without an active project. Add node, Create project and Issue key
open their page with the dialog already open; Open System brings the Default
model provider section to the top of the page body and focuses the default harness's Set or
Replace; See how to call opens the project and, once its keys, usage and address
are read, brings its How to call heading to the top of the page body, focused. Only the page body scrolls; the page header stays. Every step done turns it into one line, "You're set", with Take the tour and
Dismiss; it stays, through the tour, until dismissed, and the checklist does not
come back on its own. The choice is kept per installation in the browser, also
while the deployment cannot be read; Show Getting started, a quiet row above the
sidebar's account controls, opens it again at any time.

### Sandbox setup
Setting up hosted sandboxes, and changing the provider or resources in maintenance,
is a set of pages inside the Nodes page, one decision each: where sandboxes run (own
machines or E2B), then the backend or the E2B account, then the size of each sandbox
(three presets; E2B skips it, since each sandbox takes the template build's size),
then a review. Choices are large cards that advance on a click; short indigo dashes
show the progress; pages slide and blur across. The backend page compares
microsandbox and Docker behind a help tip; microsandbox comes first, preselected (a
saved backend stays selected), with a neutral Recommended pill beside its title.
Docker takes a confirmation (see Dialogs) once per visit to setup; a saved Docker
deployment has already made it. The review states where sandboxes run, the size, the
Runtime (taken from this console's distribution manifest) and the Core address,
read-only: it is config.json's `public_url`, and the console never asks for it. A
loopback address carries an amber line under it: only the Core machine reaches it.
When Core rejects the configuration for it (E2B with a loopback `public_url`), a
red-tinted block under the review keeps Core's message and adds the config file and
apply command as copyable values. A save attempt clears the E2B key, so the review
then says to enter it again, with a link to that step.
Advanced settings, one link away, hold the complete form: resources (not for
E2B), the Runtime release and the E2B template. A change keeps the saved size
and Runtime while the backend stays the same (a saved size outside the presets is
offered as Current); another backend starts from its standard size and this
console's Runtime, and E2B always needs its key again. Rules sit behind help tips.

### System page
Four sections, each saying where it changes. Installation: the public address, API
base URL, installation ID and source commit as a fact card. Default model provider, the one
section changed here: one card per harness in an auto-fill grid, its header holding
the harness name and outline actions (Set, or Replace and Clear); fact rows give the
harness's read-only startup state (a status dot and a Default pill, its source behind
a help tip), then the provider's protocol, base URL, whether a key is configured,
token limits when set and the update time, or Not set. Set and Replace open one form
dialog; the key field is a required password input, never prefilled or shown and
forgotten when the form closes. Core's rejection stays in red inside the form; Clear
is a ConfirmDialog. Sandboxes: the shared sandbox configuration, with a "Change on
the Nodes page" text action in the section header. Startup settings: a line naming
the config file and the apply command as copyable chips, with when they were last
applied, over a table of each setting, its value and the services a change restarts.
Sensitive settings show only Configured or Not set; Default and Fixed after install
are neutral pills beside the value.

### Loading and motion
The console has no spinners and no "Loading…" lines. Reads are cached (TanStack
Query) and prefetched on navigation hover, so revisits show data at once and
refreshes keep the last data on screen. Only a first read shows a skeleton in the
final layout's cards: table rows, a headline strip with chart panels, or a facts
card with a table, swept once under a second. Work in progress is the Agent's
shimmering "Working…" line in the conversation and the breathing pending dot.

Motion reports state and never makes anyone wait: the navigation chip and segmented
thumbs glide (Motion, one 320ms spring without bounce), figures roll, charts draw in
once per range, new conversation messages settle 6px upward in 260ms, pages fade in
160ms, popovers and dialogs scale from 98%. Reduced motion makes all of it instant.

## Do's and Don'ts

### Do:
- **Do** put every explanation of a figure, column, section or page behind a
  circled "?" help tip; report errors in a dialog or a toast; keep warnings and
  safety notices (deletion consequences, a key shown once) visible.
- **Do** start every project-scoped toolbar with the project filter, then search,
  with the count on the right.
- **Do** end every resource table with the Creator column and then the row actions.
- **Do** confirm every deletion in ConfirmDialog.
- **Do** keep meters in neutral ink and let amber and red mean a threshold was
  crossed.
- **Do** reserve OpenAgentCore Indigo for selection, focus, primary actions and the single
  `--data` series.
- **Do** place figures, charts and tables in one card divided by 1px internal rules.
- **Do** render missing data as "—", a chart gap, "Unavailable" or "Unknown".
- **Do** show status as a 7px dot plus a plain label.
- **Do** use tabular numerals for every aligned figure and right-align numeric
  columns.
- **Do** build every page from PageHeader, PageBody and Section.

### Don't:
- **Don't** add lines of small explanatory print under headings, KPIs, fields or
  charts.
- **Don't** colour healthy meters, bars or states; colour is for problems and data.
- **Don't** colour multi-series data with the indigo accent.
- **Don't** nest cards inside cards or draw a dashed empty state.
- **Don't** render an unreported value as 0 or draw a missing interval as a zero line.
- **Don't** use coloured status pills or colour-only status.
- **Don't** reassign a categorical colour by rank when data re-sorts.
- **Don't** show full IDs in list columns; show the compact ID with its copy button.
- **Don't** add uppercase letter-spaced micro-labels or eyebrow lines above
  headings; a section is named by its title alone.
- **Don't** mix synonyms in zh-CN copy (for example alternating 沙盒 with 沙箱, or
  API 密钥 with API key).
