#!/usr/bin/env python3
"""Draw the README architecture diagram, docs/assets/architecture.svg.

Edit the layout below and run this script; never edit the SVG by hand. The SVG
embeds IBM Plex Sans from Web's @fontsource dependency, so GitHub and the
website render the same text metrics: install Web's packages first
(`npm ci --prefix apps/web`).
"""

import base64
from html import escape
from pathlib import Path
import struct
import zlib

ROOT = Path(__file__).resolve().parent.parent
OUTPUT = ROOT / "docs/assets/architecture.svg"
FONTS = ROOT / "apps/web/node_modules/@fontsource/ibm-plex-sans/files"
WIDTH, HEIGHT = 1560, 1270

# Text roles: font weight and size in px.
TYPE = {
    "rt": (600, 17),    # region title
    "rs": (400, 14),    # region note
    "t1": (600, 17),    # card title
    "t2": (400, 14),    # card text and free notes
    "pl": (600, 12),    # protocol pill
    "lg": (400, 13.5),  # legend
}
PILL_HEIGHT, PILL_PADDING = 22, 22
RADIUS = {"canvas": 18, "region": 10, "card": 7, "pill": 11}

# Colors follow the README banner: sand control plane, sea-mist compute,
# sunlit E2B, olive user machine and stone storage.
CSS = """
.canvas{fill:#f9f5ee}
.region{stroke-width:1.2}
.control{fill:#f3eadb;stroke:#d5be97}.control+g .band{fill:#ece0ca}
.compute{fill:#e9eff0;stroke:#a6bdc3}.compute+g .band{fill:#dce7e9}
.sandbox{fill:#f7fafa;stroke:#6f929d}.sandbox+g .band{fill:#e8f0f1}
.managed{fill:#fbf1de;stroke:#d6a457;stroke-dasharray:6 5}.managed+g .band{fill:#f6e5c4}
.user{fill:#eef0e4;stroke:#b0b68b}.user+g .band{fill:#e3e7d3}
.storage{fill:#f1ebe2;stroke:#bfaa8e}.storage+g .band{fill:#e8dfd1}
.external{fill:#f4f2ee;stroke:#cbc3b7}.external+g .band{fill:#ebe7e0}
.sep{stroke:rgba(60,48,30,.14);stroke-width:1}
.card{fill:#fffdf9;stroke:#dcd2c3;stroke-width:1}
.rt,.t1{fill:#1f1c17}.rs,.t2,.lg{fill:#675e51}
.pill rect{fill:#24211c}.pl{fill:#f7f1e6}
.line{fill:none;stroke:#5b5346;stroke-width:1.4}.dash{stroke-dasharray:6 5}
.arrow,.dot{fill:#5b5346}
text{text-rendering:geometricPrecision;font-kerning:normal}
"""


def woff_advances(data):
    """Return units per em and a code point to advance width map of a WOFF 1.0 font."""
    tables = {}
    for i in range(struct.unpack_from(">H", data, 12)[0]):
        tag, offset, length, original, _ = struct.unpack_from(">4s4I", data, 44 + 20 * i)
        raw = data[offset:offset + length]
        tables[tag.decode()] = zlib.decompress(raw) if length < original else raw
    units = struct.unpack_from(">H", tables["head"], 18)[0]
    count = struct.unpack_from(">H", tables["hhea"], 34)[0]
    widths = struct.unpack_from(f">{2 * count}H", tables["hmtx"])[0::2]
    cmap = tables["cmap"]
    for i in range(struct.unpack_from(">H", cmap, 2)[0]):
        platform, encoding, offset = struct.unpack_from(">HHI", cmap, 4 + 8 * i)
        if (platform, encoding) == (3, 1) and struct.unpack_from(">H", cmap, offset)[0] == 4:
            break
    else:
        raise ValueError("font has no Unicode BMP character map")
    segments = struct.unpack_from(">H", cmap, offset + 6)[0] // 2
    ends = struct.unpack_from(f">{segments}H", cmap, offset + 14)
    starts = struct.unpack_from(f">{segments}H", cmap, offset + 16 + 2 * segments)
    deltas = struct.unpack_from(f">{segments}H", cmap, offset + 16 + 4 * segments)
    ranges_at = offset + 16 + 6 * segments
    ranges = struct.unpack_from(f">{segments}H", cmap, ranges_at)
    advances = {}
    for k in range(segments):
        for code in range(starts[k], min(ends[k], 0xFFFE) + 1):
            glyph = code
            if ranges[k]:
                glyph = struct.unpack_from(">H", cmap, ranges_at + 2 * k + ranges[k] + 2 * (code - starts[k]))[0]
                if not glyph:
                    continue
            glyph = (glyph + deltas[k]) & 0xFFFF
            advances[code] = widths[min(glyph, count - 1)]
    return units, advances


class Diagram:
    def __init__(self):
        self.parts = []
        self.fonts = {}
        self.overflows = []
        for weight in {weight for weight, _ in TYPE.values()}:
            data = (FONTS / f"ibm-plex-sans-latin-{weight}-normal.woff").read_bytes()
            self.fonts[weight] = (data, *woff_advances(data))

    def measure(self, s, role):
        weight, size = TYPE[role]
        _, units, advances = self.fonts[weight]
        missing = [c for c in s if ord(c) not in advances]
        if missing:
            raise ValueError(f"IBM Plex Sans Latin has no glyph for {missing!r} in {s!r}")
        return sum(advances[ord(c)] for c in s) / units * size

    def text(self, x, y, s, role, anchor="start", bounds=None):
        width = self.measure(s, role)
        if bounds:
            left = {"start": x, "middle": x - width / 2, "end": x - width}[anchor]
            if left < bounds[0] or left + width > bounds[1]:
                self.overflows.append(s)
        a = "" if anchor == "start" else f' text-anchor="{anchor}"'
        self.parts.append(f'<text x="{x:g}" y="{y:g}" class="{role}"{a}>{escape(s)}</text>')
        return width

    def card(self, x, y, w, h, title, text):
        self.parts.append(f'<rect x="{x}" y="{y}" width="{w}" height="{h}" rx="{RADIUS["card"]}" class="card"/>')
        self.text(x + 16, y + h / 2 - 3, title, "t1", bounds=(x, x + w - 12))
        self.text(x + 16, y + h / 2 + 18, text, "t2", bounds=(x, x + w - 12))

    def region(self, x, y, w, h, tone, title, note=""):
        """A deployment location or sandbox with a 40 px title band."""
        r = RADIUS["region"]
        self.parts.append(f'<rect x="{x}" y="{y}" width="{w}" height="{h}" rx="{r}" class="region {tone}"/><g>')
        self.parts.append(f'<path class="band" d="M{x},{y + 40} V{y + r} Q{x},{y} {x + r},{y} '
                          f'H{x + w - r} Q{x + w},{y} {x + w},{y + r} V{y + 40} Z"/>')
        self.parts.append(f'<path d="M{x},{y + 40} H{x + w}" class="sep"/>')
        width = self.text(x + 16, y + 27, title, "rt", bounds=(x, x + w - 16))
        if note:
            self.text(x + w - 16, y + 27, note, "rs", "end", bounds=(x + 40 + width, x + w))
        self.parts.append("</g>")

    def line(self, d, arrows=True, dashed=False):
        markers = ' marker-start="url(#arrow)" marker-end="url(#arrow)"' if arrows else ""
        self.parts.append(f'<path d="{d}" class="line{" dash" if dashed else ""}"{markers}/>')

    def pill(self, cx, cy, label, rotate=False):
        w = self.measure(label, "pl") + PILL_PADDING
        h = PILL_HEIGHT
        if rotate:
            self.parts.append(f'<g transform="translate({cx} {cy}) rotate(-90)">')
            cx = cy = 0
        self.parts.append(f'<g class="pill"><rect x="{cx - w / 2:.1f}" y="{cy - h / 2:g}" width="{w:.1f}" '
                          f'height="{h}" rx="{RADIUS["pill"]}"/><text x="{cx:g}" y="{cy + TYPE["pl"][1] * 0.36:.1f}" '
                          f'text-anchor="middle" class="pl">{escape(label)}</text></g>')
        if rotate:
            self.parts.append("</g>")

    def svg(self):
        if self.overflows:
            raise ValueError(f"text overflows its box: {self.overflows!r}")
        css = [CSS.strip()]
        for weight, (data, _, _) in sorted(self.fonts.items()):
            css.append(f'@font-face{{font-family:"OAC Plex";font-weight:{weight};'
                       f'src:url(data:font/woff;base64,{base64.b64encode(data).decode()}) format("woff")}}')
        family = '"OAC Plex",-apple-system,BlinkMacSystemFont,"Segoe UI",Helvetica,Arial,sans-serif'
        for role, (weight, size) in TYPE.items():
            css.append(f".{role}{{font-family:{family};font-size:{size}px;font-weight:{weight}}}")
        return (
            "<!-- Generated by scripts/generate-architecture-diagram.py; edit that script and run it again. -->\n"
            f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {WIDTH} {HEIGHT}" width="{WIDTH}" height="{HEIGHT}" '
            'role="img" aria-labelledby="title"><title id="title">OpenAgentCore deployment and protocol boundaries</title>'
            f'<style>{"".join(css)}</style><defs><marker id="arrow" viewBox="0 0 10 10" refX="8.6" refY="5" '
            'markerWidth="7" markerHeight="7" markerUnits="userSpaceOnUse" orient="auto-start-reverse">'
            '<path d="M1,1.5 L9,5 L1,8.5 Z" class="arrow"/></marker></defs>'
            f'<rect width="{WIDTH}" height="{HEIGHT}" rx="{RADIUS["canvas"]}" class="canvas"/>'
            + "".join(self.parts) + "</svg>\n"
        )


def draw(d):
    # Callers and their Core APIs.
    for x, title, text, api in [
        (80, "Applications / official SDK", "Submit work · stream results", "P1 · Agents API /v1"),
        (560, "Web console / operators", "Configure the platform · view status", "P2 · Core API /core/v1"),
        (1040, "Nodes / Runtime connections", "Machine identity · register and connect", "P3 · Machine API /api/v1"),
    ]:
        d.card(x, 28, 440, 66, title, text)
        d.line(f"M{x + 220},94 V194")
        d.pill(x + 220, 145, api)

    d.region(80, 194, 1400, 260, "control", "Control server · OpenAgentCore", "Web, Core and the database can share one host or run separately")
    d.card(104, 254, 952, 76, "Core · scheduling and state", "Accepts work, selects where it runs, controls lifecycles and records results")
    d.card(1104, 254, 352, 76, "PostgreSQL", "Sessions · Turns · configuration")
    d.line("M1056,292 H1104")
    d.card(104, 362, 420, 64, "Sandbox Provider adapter", "Create / suspend / resume compute")
    d.card(548, 362, 432, 64, "Runtime connections", "Dispatch work / receive events")
    d.card(1004, 362, 452, 64, "Workspace FS adapter", "Manages file storage and mount bindings on its own")

    # Every execution location connects through the same protocols.
    for cx in (400, 950, 1330):
        d.line(f"M{cx},454 V618")
    d.pill(400, 489, "P4 · Sandbox Provider · compute lifecycle")
    d.pill(400, 528, "P5 · Node · host management")
    d.pill(400, 567, "P7 · Runtime · work and events")
    d.pill(950, 508, "P4 · Sandbox Provider")
    d.pill(950, 557, "P7 · Runtime")
    d.pill(1330, 534, "P7 · Runtime")

    d.region(80, 618, 640, 302, "compute", "Compute servers × N · microsandbox")
    d.card(104, 672, 592, 52, "sandbox node · host manager", "Manages compute from outside the sandboxes")
    d.region(104, 758, 592, 138, "sandbox", "microsandbox VM × N", "Isolation boundary")
    d.card(128, 810, 544, 62, "Runtime + Harness + tools", "Agent loop runs in the sandbox · workspace on independent FS")

    d.region(780, 618, 340, 302, "managed", "E2B managed platform")
    d.region(800, 672, 300, 224, "sandbox", "E2B Sandbox")
    d.card(820, 734, 260, 62, "Runtime + Harness", "Agent and tools in the sandbox")
    d.text(950, 841, "Provider-owned filesystem", "t2", "middle")
    d.text(950, 868, "No independent FS yet", "t2", "middle")

    d.region(1180, 618, 300, 302, "user", "Your machine · self_hosted")
    d.card(1200, 734, 260, 62, "Runtime + Harness", "You install and maintain it")
    d.text(1330, 841, "Local files and tools", "t2", "middle")
    d.text(1330, 868, "Core does not create or reclaim it", "t2", "middle")

    # File I/O and model traffic leave the execution locations directly.
    d.line("M240,920 V1060")
    d.text(258, 976, "NFS · file reads, writes and mount", "t2")
    d.line("M550,920 V1010 H1330 V920", arrows=False)
    d.line("M950,920 V1060")
    d.parts.append('<circle cx="950" cy="1010" r="3.5" class="dot"/>')
    d.pill(1040, 1010, "P13 · Model protocol")
    d.text(1190, 998, "HTTP MCP", "t2")

    d.region(80, 1060, 640, 156, "storage", "Independent storage server", "Deployed apart from compute servers")
    d.card(104, 1114, 352, 76, "Persistent workspaces · NFS", "Files remain after compute is released")
    d.card(472, 1114, 224, 76, "Memory snapshots", "Separate retention period")
    d.region(780, 1060, 700, 156, "external", "External services", "Reached directly from execution environments")
    d.card(804, 1114, 312, 76, "Model providers", "Native model APIs")
    d.card(1140, 1114, 316, 76, "Remote MCP servers", "HTTP MCP")

    d.line("M80,410 H40 V1148 H80", dashed=True)
    d.pill(40, 785, "P14 · Workspace filesystem · storage lifecycle and binding", rotate=True)
    x = 80
    for item in ["Solid: communication or mount", "Dashed: independent storage management",
                 "Outer frame: deployment location", "Inner frame: sandbox isolation boundary"]:
        x += d.text(x, 1250, item, "lg", bounds=(80, WIDTH - 80)) + 2 * TYPE["lg"][1]


def main():
    diagram = Diagram()
    draw(diagram)
    OUTPUT.write_text(diagram.svg())


if __name__ == "__main__":
    main()
