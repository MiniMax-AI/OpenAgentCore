---
name: Parsar Core Console
description: The operator's back office for one self-hosted Parsar Core deployment; health, capacity, usage and failures on hairline-ruled white.
colors:
  ink: "#37352f"
  ink-muted: "#787774"
  ink-subtle: "#9b9a97"
  sidebar-ink: "#5f5e5a"
  surface: "#ffffff"
  surface-subtle: "#fafafa"
  surface-muted: "#f1f1f0"
  line: "#e9e9ec"
  line-muted: "#efeff1"
  line-strong: "#d6d7dc"
  hover: "rgb(55 53 47 / 3%)"
  pressed: "rgb(55 53 47 / 6%)"
  tile: "rgb(55 53 47 / 7%)"
  accent: "#4f46e5"
  accent-emphasis: "#4338ca"
  accent-fg: "#ffffff"
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
  meter-track: "rgb(55 53 47 / 8%)"
typography:
  display:
    fontFamily: "-apple-system, BlinkMacSystemFont, \"PingFang SC\", \"Hiragino Sans GB\", \"Segoe UI\", \"Microsoft YaHei\", \"Noto Sans SC\", \"Helvetica Neue\", Helvetica, Arial, sans-serif"
    fontSize: "24px"
    fontWeight: 600
    lineHeight: "28px"
    letterSpacing: "-0.02em"
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
  frame: "8px"
  pill: "999px"
spacing:
  xs: "4px"
  sm: "8px"
  md: "12px"
  lg: "16px"
  gutter: "24px"
  section: "28px"
components:
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
  button-secondary:
    backgroundColor: "{colors.surface-muted}"
    textColor: "{colors.ink}"
    rounded: "{rounded.control}"
    padding: "0 10px"
    height: "28px"
  button-ghost:
    textColor: "{colors.ink-muted}"
    rounded: "{rounded.control}"
    padding: "0 10px"
    height: "28px"
  input-field:
    backgroundColor: "{colors.surface}"
    textColor: "{colors.ink}"
    rounded: "{rounded.control}"
    padding: "4px 8px"
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
  help-tip:
    textColor: "{colors.ink-subtle}"
    rounded: "{rounded.pill}"
    size: "18px"
  meter:
    backgroundColor: "{colors.meter-track}"
    rounded: "{rounded.pill}"
    height: "6px"
---

# Design System: Parsar Core Console

## Overview

**Creative North Star: "The Operator's Ledger"**

The console is an operations back office, not a developer showroom. Every screen reads like a ledger page: one header rule, one strip of figures, then ruled sections of evidence (charts, bar lists, tables) sitting on white. Structure comes from 1px hairlines and whitespace, never from floating cards; colour is spent on data and state, almost never on decoration. The one indigo accent means "you selected this" or "this is the primary action," and nothing else.

Density is deliberately high and calm: 13px body, 44px table rows, 28px controls, tabular figures in every column. The system is bilingual (zh-CN and English) and ships light and dark themes on the same token names; dark swaps values, never structure. It honours reduced motion and treats keyboard focus as a first-class state (2px indigo outline).

The data contract is part of the look. Core reports only what it observes, so the interface shows absence honestly: an em dash, a gap in a line, the word "Unavailable". An explained figure keeps its explanation one click away behind a circled question mark, so the page stays a ledger rather than a leaflet.

**Key Characteristics:**
- Neutral warm-gray ink on white, one indigo accent for selection and primary actions only.
- Hairline frames divided by internal rules; no nested cards, no decorative shadows.
- A six-slot categorical palette for data, bound to the entity, not its rank.
- Tabular numerals everywhere a number can line up.
- Status is always a dot plus a plain-language label.
- Explanations live behind "?" help tips; errors, warnings and safety notices stay visible.

## Colors

A restrained neutral ledger with one indigo voice, three semantic signal colours, and a separate categorical palette that belongs to data alone.

### Primary
- **Parsar Indigo** (accent): keyboard focus outlines and rings, primary buttons, link hover on table names and text actions, text selection wash. Deepens to **Pressed Indigo** (accent-emphasis) on primary hover. It is the brand colour shared with the public Parsar landing.

### Neutral
- **Ledger Ink** (ink): all primary text, figures, table cells, headings.
- **Graphite** (ink-muted): secondary text, column headers, KPI labels, axis ticks, inactive controls.
- **Pencil** (ink-subtle): help-tip glyphs, crosshairs, untoned KPI dots; the quietest legible mark.
- **Sidebar Ink** (sidebar-ink): navigation text on the sidebar's subtle ground.
- **Paper** (surface): page ground, popovers, the active segment's raised face.
- **Margin Gray** (surface-subtle): sidebar, table header band, the fleet list pane, coverage notes.
- **Well Gray** (surface-muted): segmented-control track, secondary buttons.
- **Hairline** (line): every frame border and internal divider; chart gridlines.
- **Faint Rule** (line-muted): row dividers inside tables.
- **Firm Rule** (line-strong): control borders (inputs, outline buttons, active filter tab, selected fleet target), dashed empty-state border.
- **Hover / Pressed / Tile washes**: translucent ink at 3% / 6% / 7% for hover, active nav item, and count pills.

### Signal
- **Healthy Green** (success), **Caution Amber** (warning), **Fault Red** (danger): used only for status dots, KPI tone dots, meter fills past thresholds (warning at 80%, danger at 95%), error text, and error notices. **Queued Gray** (status-queued) is the pending tone dot in the KPI strip; **Idle Gray** (status-idle) marks idle and neutral status dots.

### Data (categorical)
- **Series 1-6** (series-1 Cobalt, series-2 Persimmon, series-3 Jade, series-4 Saffron, series-5 Rose, series-6 Forest) and **Series Other** (series-other): chart lines, stacked bars, bar-list fills, legend keys. Series 1 doubles as the running-status colour and the default meter fill (`--meter-fill`, `--status-running`). Dark theme re-tunes each slot under the same name.
- **Meter Track** (meter-track): the empty rail under meters and bar-list fills.

### Named Rules
**The One Voice Rule.** Indigo is for selection and primary actions only. Data never wears the accent; charts, bar lists and meters draw from `--series-1..6`, `--series-other` and `--meter-fill`.

**The Entity Owns Its Colour Rule.** A categorical colour follows the entity (model, tool, node), never its rank. An entity keeps its slot while visible; only slots of entities that left the view are reused. Anything beyond six series collapses into Series Other.

**The Signal Is Not Decoration Rule.** Green, amber and red appear only when they report a state. A healthy meter stays in the series ramp; it turns amber or red only when the value crosses its threshold.

## Typography

**Display Font:** system UI sans (-apple-system, Segoe UI, with PingFang SC / Microsoft YaHei / Noto Sans SC for Chinese)
**Body Font:** the same system stack
**Label/Mono Font:** ui-monospace / SF Mono / Menlo for identifiers and code

**Character:** One quiet system sans in several weights, sized for dense reading; hierarchy comes from weight and a tight scale, not from a second typeface. Mono appears only for machine identifiers.

### Hierarchy
- **Display** (600, 24px, 28px, -0.02em, tabular): KPI figures in the strip. The largest type on any console page.
- **Headline** (600, 20px, 26px, -0.02em): the page title in the 64px page header; one per page.
- **Title** (600, 15px, 22px, -0.01em): section headings. Chart captions and empty-state titles step down to 600 at 13-14px.
- **Body** (400, 13px, 18px): table cells, controls, form fields. The document base is 14px/20px for longer prose (help popovers run 12.5px/19px).
- **Label** (500, 12.5px, 18px): KPI labels (400), status labels, text actions, segmented options; column headers 500 at 12px; axis ticks 11px.
- **Mono** (400, 11.5px): IDs and code inside section headers and tables, in Graphite.

### Named Rules
**The Columns Line Up Rule.** Every figure that can share a column uses tabular numerals (`font-variant-numeric: tabular-nums`): KPI values, numeric table cells (right-aligned), bar-list values, axis ticks, tooltip values, counts.

**The Honest Figure Rule.** Missing data renders as "—", a gap in the line, or "Unavailable"; never as 0. Compact numbers keep two decimals only when the integer part is a single digit ("1.04M"), otherwise one ("415.7万"); values under 10,000 print whole. Durations read "850 ms / 12.4 s / 4m 12s / 3h 5m".

**The Plain Vocabulary Rule.** zh-CN copy uses one term per concept: 沙箱 (sandbox), 运行时 (runtime), 已上报 (reported), 活跃 (active), 提供方 (provider). Time ranges read "1 小时 / 6 小时 / 24 小时 / 7 天" (English "1h / 6h / 24h / 7d"), always in the one segmented control style.

## Layout

A fixed sidebar (232px, Margin Gray, hairline right edge) beside a full-height page column. The desktop minimum is 960px. Every page uses the same frame: a 64px header (title, optional help tip, actions on the right: range control then Refresh) over a hairline rule, then a scrolling body padded `20px 24px 48px` with sections stacked 28px apart. Inside a section the heading row sits 12px above its content.

The recurring shapes in the body are the full-width KPI strip (auto-fit columns, min 158px; three per row below 1180px), paired chart frames (two equal columns, or 2:3 when a bar list sits beside its trend; single column below 1180px), and full-width table frames. The Overview uses a fleet split-pane inside one frame: target list at roughly one third on the subtle ground, selected-target evidence at two thirds, an attention table spanning beneath.

Spacing follows a 4px base: 4, 8, 12, 16, 24 (page gutter), 28 (section gap). Controls are 28px tall, segmented options 24px, table rows 44px (32px compact), headers 34px.

**The One Page Grammar Rule.** Every page, new or legacy, uses PageHeader, PageBody and Section. No page invents its own header height, gutter or section rhythm.

## Elevation & Depth

The system is flat. Depth is conveyed by tone (subtle and muted grounds) and hairlines, not by shadow. Only two shadows exist, and each has a job.

### Shadow Vocabulary
- **Control lift** (`box-shadow: 0 1px 2px rgb(0 0 0 / 6%)`): raised controls only: primary and outline buttons, inputs and selects, the active segment, the active filter tab, the selected fleet target.
- **Floating** (`box-shadow: 0 1px 2px rgb(24 24 27 / 4%), 0 8px 24px -12px rgb(24 24 27 / 18%)`): things that float above the page: help-tip popovers, chart tooltips, menus and dialogs.

### Named Rules
**The One Frame Rule.** Charts and KPI figures sit in one hairline frame divided by internal rules (inset 1px lines), never in nested cards. A frame never contains another bordered, shadowed container.

## Shapes

Soft but disciplined corners: 8px on frames (KPI strip, chart grid, tables, empty states, notices, popovers), 6px on controls, 7px on the segmented track and fleet targets, 5px on inner segments, 4px on small inline marks and the chart plot, full pills for meters, bar tracks and count badges, circles for status dots (7px) and KPI tone dots (8px). Legend keys are 9px squares with 2px corners, or 12x2px strokes for line series. Borders are always 1px; the empty state alone uses a dashed Firm Rule.

## Components

### Buttons
Compact and quiet; the primary button is the only filled colour on a page header.
- **Shape:** gently rounded (6px), 28px tall, 0 10px padding, 13px/500 label, optional 14px Lucide icon at 1.5 stroke.
- **Primary:** Parsar Indigo fill, white text, control lift. Hover deepens to Pressed Indigo.
- **Outline:** Paper face, Firm Rule border, control lift; hover takes the ink wash. Refresh is always an outline button whose last-updated time lives in its tooltip, not on the page.
- **Secondary / Ghost:** Well Gray fill, or transparent with Graphite text; both darken on hover.
- **Focus / Press:** focus draws a 1px indigo border plus 1px indigo ring; press scales to 0.97 on the spring curve.
- **Text action:** borderless Graphite 12.5px/500 link with a trailing arrow, turning indigo on hover ("Sandbox metrics →").

### Segmented control
The single style for ranges and mode switches. A Well Gray track (2px padding, 7px corners) holds 24px options in Graphite; the chosen option rises to Paper with Ledger Ink text and control lift. It is a radiogroup with arrow-key movement.

### Filter tabs and selects
Filter tabs are 28px transparent buttons that gain Paper, a Firm Rule border and control lift when active; counts beside them are tabular Graphite. Selects are 28px Paper fields with a Firm Rule border and a drawn chevron; focus swaps the border to indigo with a 1px ring.

### Inputs / Fields
- **Style:** Paper ground, Firm Rule 1px border, 6px corners, 28px min height, 13px text, control lift.
- **Focus:** indigo border plus 1px indigo ring; no glow.
- **Error:** Fault Red text beneath, kept visible.

### Navigation
Sidebar groups (Monitor, Resources, Infrastructure, Settings, Playground) with 12px Graphite group labels; items are 30px rows with a 16px outline icon and Sidebar Ink text. Hover takes the ink wash; the active item takes the pressed wash with Ledger Ink at 500. The Playground group sits below a hairline. The footer holds the Core connection switcher (status icon plus "API ready"), sign-out, and the language/theme menu.

### KPI strip
One hairline frame of equal cells separated by inset rules. Each cell: a 12.5px Graphite label with an optional help tip, then the Display figure, optionally led by an 8px tone dot (ok, warning, danger, pending). Figures ellipsize rather than wrap.

### Help tip
An 18px circular button holding a 13px circled "?" in Pencil; hover or open takes Ledger Ink on the ink wash. It opens on hover, focus or click (click pins it), closes on Escape, scroll or resize, and renders a 12.5px popover (Paper, Hairline, 8px, floating shadow, max 288px) in a portal. The text also exists in a visually hidden element for assistive technology.

### Status dot
A 7px circle plus a plain label at 12.5px (ok green, warning amber, danger red, pending Series 1, neutral Idle Gray). Never a coloured pill, never colour alone.

### Meter and bar list
A 6px pill rail in Meter Track with a fill in `--meter-fill`; the fill turns amber at 80% and red at 95% of its limit, and a nonzero ratio shows at least 3% width. Unknown ratios draw an empty rail. Bar lists use the same idea at 8px with categorical fills: label (with legend key) at ~42%, the track, then a right-aligned tabular value.

### Charts
Time-series charts live in chart panels (caption 13px/600, legend, plot) inside one chart-grid frame. Lines are 2px round-joined with a surface-ringed end dot; gridlines are crisp Hairlines with 11px tabular ticks; hovering draws a Pencil crosshair, a hover-wash band and a floating tooltip. Missing buckets are gaps, not zeros. Every chart has a table toggle (26px icon button, top right) that reveals the numbers in a 220px scrolling table.

### Tables
A hairline frame with a sticky 34px Margin Gray header in Graphite 12px/500, 44px rows divided by Faint Rules, hover wash, right-aligned tabular numerics, a 500-weight primary cell that turns indigo on hover when it links, and inline Fault Red error lines under the row title.

### Notices and empty states
Coverage notes (Margin Gray, Hairline, 8px, 12.5px Graphite) state bounded aggregation; the error variant tints toward Fault Red. Partial-data chips are amber-tinted pills with a help tip. Empty states are dashed-border frames with a 20px outline icon, a 14px/600 title, one line of description and an optional action.

## Do's and Don'ts

### Do:
- **Do** put every explanation of a figure, section or page behind a circled "?" help tip; keep errors, warnings and safety notices (deletion confirmation, uncertain writes, secrets) visible on the page.
- **Do** reserve Parsar Indigo for selection, focus and primary actions; draw data from `--series-1..6` and `--series-other`, and meters from `--meter-fill`.
- **Do** place charts and KPI figures in one hairline frame (8px) divided by 1px internal rules.
- **Do** render missing data as "—", a chart gap, or "Unavailable".
- **Do** show status as a 7px dot plus a plain label.
- **Do** use tabular numerals for every aligned figure and right-align numeric columns.
- **Do** use the one segmented control for time ranges, labelled "1 小时 / 6 小时 / 24 小时 / 7 天".
- **Do** keep compact numbers at two decimals only when the integer part is one digit.
- **Do** build every page from PageHeader, PageBody and Section with the 24px gutter and 28px section gap.

### Don't:
- **Don't** add lines of small explanatory print under headings, KPIs or charts.
- **Don't** colour data, bars, lines or meters with the indigo accent.
- **Don't** nest cards inside frames or lift sections with shadows; shadows are for raised controls and floating layers only.
- **Don't** render an unreported value as 0 or draw a missing interval as a zero line.
- **Don't** use coloured status pills or colour-only status.
- **Don't** reassign a categorical colour by rank when data re-sorts.
- **Don't** add uppercase letter-spaced micro-labels or eyebrow lines above headings; a section is named by its title alone.
- **Don't** mix synonyms in zh-CN copy (for example alternating 沙盒 with 沙箱).
