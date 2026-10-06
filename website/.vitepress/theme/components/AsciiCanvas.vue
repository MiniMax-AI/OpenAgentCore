<script setup lang="ts">
// Renders a shape as animated ASCII on a canvas: the OpenAgentCore mark (from
// the canonical SVG path) or a line of text. Characters decode in, a scan line
// sweeps through, and the pointer stirs the field around it. Motion stops when
// the canvas is off screen or the user prefers reduced motion.
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import logoSvg from '../../../../docs/assets/openagentcore-logo.svg?raw'

const props = withDefaults(
  defineProps<{
    kind?: 'logo' | 'text'
    text?: string
    /** Character cell height in CSS pixels. */
    cell?: number
    /** Density ramp from empty to full. */
    ramp?: string
    /** Probability of a background character in empty cells. */
    noise?: number
    /** Fraction of the canvas the shape may fill. */
    fill?: number
    /** Vertical offset of the shape's centre, as a fraction of the height. */
    shiftY?: number
    label?: string
  }>(),
  { kind: 'logo', text: '', cell: 14, ramp: ' .:-=+*#%@', noise: 0.035, fill: 0.86, shiftY: 0, label: '' },
)

const canvas = ref<HTMLCanvasElement>()
const GLYPHS = '01{}[]<>/\\|=+*#%$&@?!~^;:'
const logoPath = /\sd="([^"]+)"/.exec(logoSvg)?.[1] ?? ''

interface Cell { target: string; level: number; reveal: number; glyph: string; seed: number }

let ctx: CanvasRenderingContext2D | null = null
let cells: Cell[] = []
let cols = 0
let rows = 0
let cw = 8
let ch = 14
let frame = 0
let start = 0
let visible = false
let reduced = false
let pointer = { x: -1e4, y: -1e4, t: 0 }
let colors = { dim: '#1f3a2b', mid: '#2f7a4c', hot: '#4ade80', white: '#e9fff1' }
let resizeObserver: ResizeObserver | undefined
let intersection: IntersectionObserver | undefined
let themeObserver: MutationObserver | undefined

function readColors() {
  const style = getComputedStyle(canvas.value!)
  const v = (name: string, fallback: string) => style.getPropertyValue(name).trim() || fallback
  colors = { dim: v('--ascii-dim', colors.dim), mid: v('--ascii-mid', colors.mid), hot: v('--ascii-hot', colors.hot), white: v('--ascii-white', colors.white) }
}

function randomGlyph() {
  return GLYPHS[(Math.random() * GLYPHS.length) | 0]
}

/** Rasterises the shape at 3x3 samples per cell and maps coverage onto the ramp. */
function layout() {
  const el = canvas.value
  if (!el || !ctx) return
  const rect = el.getBoundingClientRect()
  if (!rect.width || !rect.height) return
  const dpr = Math.min(window.devicePixelRatio || 1, 2)
  el.width = Math.round(rect.width * dpr)
  el.height = Math.round(rect.height * dpr)
  ctx.setTransform(dpr, 0, 0, dpr, 0, 0)

  ch = props.cell
  ctx.font = `${Math.round(ch * 0.78)}px "Geist Mono Variable", ui-monospace, SFMono-Regular, Menlo, monospace`
  cw = Math.max(5, ctx.measureText('M').width * 1.04)
  ctx.textBaseline = 'middle'
  ctx.textAlign = 'center'
  cols = Math.max(1, Math.floor(rect.width / cw))
  rows = Math.max(1, Math.floor(rect.height / ch))

  const ss = 3
  const mask = document.createElement('canvas')
  mask.width = cols * ss
  mask.height = rows * ss
  const m = mask.getContext('2d', { willReadFrequently: true })!
  m.fillStyle = '#fff'
  // One mask pixel is cw/ss by ch/ss screen pixels; scale so the shape keeps its
  // on-screen proportions.
  const sx = ss / cw
  const sy = ss / ch
  if (props.kind === 'logo') {
    // The mark occupies x 214–988 and y 245–1026 of its 1254-unit viewBox.
    const bounds = { x: 214, y: 245, size: 781 }
    const size = Math.min(rect.width, rect.height) * props.fill
    const scale = size / bounds.size
    const ox = (rect.width - size) / 2 - bounds.x * scale
    const oy = (rect.height - size) / 2 + rect.height * props.shiftY - bounds.y * scale
    m.setTransform(scale * sx, 0, 0, scale * sy, ox * sx, oy * sy)
    m.fill(new Path2D(logoPath), 'evenodd')
  } else {
    const lines = props.text.split('\n')
    let fontSize = rect.height * props.fill / lines.length / 1.05
    m.font = `800 ${fontSize}px "Inter Variable", Inter, system-ui, sans-serif`
    const widest = Math.max(...lines.map((line) => m.measureText(line).width))
    if (widest > rect.width * 0.96) fontSize *= (rect.width * 0.96) / widest
    m.setTransform(sx, 0, 0, sy, 0, 0)
    m.font = `800 ${fontSize}px "Inter Variable", Inter, system-ui, sans-serif`
    m.textAlign = 'center'
    m.textBaseline = 'middle'
    const lineHeight = fontSize * 1.05
    const top = rect.height / 2 - (lineHeight * (lines.length - 1)) / 2
    lines.forEach((line, i) => m.fillText(line, rect.width / 2, top + i * lineHeight))
  }
  const data = m.getImageData(0, 0, mask.width, mask.height).data

  const ramp = props.ramp
  const cx = cols / 2
  const cy = rows / 2
  const maxDist = Math.hypot(cx, cy)
  cells = new Array(cols * rows)
  for (let r = 0; r < rows; r++) {
    for (let c = 0; c < cols; c++) {
      let sum = 0
      for (let y = 0; y < ss; y++) for (let x = 0; x < ss; x++) sum += data[((r * ss + y) * mask.width + (c * ss + x)) * 4 + 3]
      const level = sum / (ss * ss * 255)
      const index = level < 0.04 ? 0 : Math.min(ramp.length - 1, 1 + Math.floor(level * (ramp.length - 1)))
      const dist = Math.hypot(c - cx, (r - cy) * (ch / cw)) / maxDist
      cells[r * cols + c] = {
        target: ramp[index],
        level,
        reveal: 250 + dist * 900 + Math.random() * 650,
        glyph: randomGlyph(),
        seed: Math.random(),
      }
    }
  }
}

function draw(now: number) {
  if (!ctx || !canvas.value) return
  const t = reduced ? 1e6 : now - start
  const w = canvas.value.width
  const h = canvas.value.height
  ctx.clearRect(0, 0, w, h)

  // A diagonal scan line crosses the shape every few seconds.
  const period = 5200
  const phase = ((t % period) / period) * (cols + rows) * 1.6 - rows * 0.3
  const pointerAge = now - pointer.t
  const pointerStrength = reduced ? 0 : Math.max(0, 1 - pointerAge / 2200)
  const pc = pointer.x / cw
  const pr = pointer.y / ch
  const radius = 9

  const buckets: Record<string, number[]> = { dim: [], mid: [], hot: [], white: [] }
  const chars: string[] = new Array(cells.length)

  for (let i = 0; i < cells.length; i++) {
    const cell = cells[i]
    const c = i % cols
    const r = (i / cols) | 0
    const inside = cell.target !== ' '
    let ch2 = ' '
    let tone: keyof typeof buckets = 'dim'

    const scan = Math.abs(c + r * 1.4 - phase)
    const dx = c - pc
    const dy = (r - pr) * (ch / cw)
    const near = pointerStrength > 0 ? Math.max(0, 1 - Math.hypot(dx, dy) / radius) * pointerStrength : 0

    if (inside) {
      if (t < cell.reveal) {
        if (t > cell.reveal - 700) {
          ch2 = (frame + i) % 3 === 0 ? randomGlyph() : cell.glyph
          tone = 'mid'
        }
      } else {
        ch2 = cell.target
        tone = cell.level > 0.7 ? 'hot' : 'mid'
        if (t < cell.reveal + 160) tone = 'white'
        if (!reduced && scan < 1.6) {
          ch2 = scan < 0.8 ? randomGlyph() : cell.target
          tone = 'white'
        } else if (!reduced && cell.seed < 0.012 && (frame + i) % 40 < 3) {
          ch2 = randomGlyph()
          tone = 'white'
        }
        if (near > 0.25) {
          ch2 = near > 0.6 ? randomGlyph() : ch2
          tone = 'white'
        }
      }
    } else {
      // Background field: sparse, slowly drifting characters that the pointer stirs up.
      const drift = reduced ? 0 : Math.floor(t / 1400 + cell.seed * 40)
      const on = ((cell.seed * 997 + drift * 0.61803) % 1) < props.noise
      if (near > 0.15 && cell.seed < near * 0.9) {
        ch2 = randomGlyph()
        tone = near > 0.55 ? 'hot' : 'mid'
      } else if (on && t > cell.reveal * 0.6) {
        ch2 = cell.glyph
        tone = 'dim'
      }
    }
    if (ch2 !== ' ') {
      chars[i] = ch2
      buckets[tone].push(i)
    }
  }

  for (const tone of ['dim', 'mid', 'hot', 'white'] as const) {
    ctx.fillStyle = colors[tone]
    for (const i of buckets[tone]) ctx.fillText(chars[i], (i % cols) * cw + cw / 2, ((i / cols) | 0) * ch + ch / 2)
  }
}

let raf = 0
let last = 0
function loop(now: number) {
  raf = 0
  if (!visible || document.hidden) return
  // About 30 frames per second is enough for character animation.
  if (now - last > 32) {
    last = now
    frame++
    draw(now)
  }
  raf = requestAnimationFrame(loop)
}

function play() {
  if (reduced) return draw(performance.now())
  if (!raf) raf = requestAnimationFrame(loop)
}

function onPointer(event: PointerEvent) {
  const rect = canvas.value!.getBoundingClientRect()
  pointer = { x: event.clientX - rect.left, y: event.clientY - rect.top, t: performance.now() }
}

function onVisibility() {
  if (!document.hidden && visible) play()
}

onMounted(() => {
  const el = canvas.value!
  ctx = el.getContext('2d')
  reduced = window.matchMedia('(prefers-reduced-motion: reduce)').matches
  readColors()
  start = performance.now()
  const relayout = () => {
    layout()
    if (reduced || !raf) draw(performance.now())
  }
  // Wait for the web fonts so cell widths match the rendered glyphs.
  document.fonts?.ready.then(relayout)
  relayout()
  resizeObserver = new ResizeObserver(relayout)
  resizeObserver.observe(el)
  intersection = new IntersectionObserver(([entry]) => {
    visible = entry.isIntersecting
    if (visible) play()
  })
  intersection.observe(el)
  themeObserver = new MutationObserver(() => {
    readColors()
    draw(performance.now())
  })
  themeObserver.observe(document.documentElement, { attributes: true, attributeFilter: ['class'] })
  document.addEventListener('visibilitychange', onVisibility)
})

onBeforeUnmount(() => {
  cancelAnimationFrame(raf)
  resizeObserver?.disconnect()
  intersection?.disconnect()
  themeObserver?.disconnect()
  document.removeEventListener('visibilitychange', onVisibility)
})

watch(() => props.text, () => layout())
</script>

<template>
  <canvas
    ref="canvas"
    class="ascii-canvas"
    role="img"
    :aria-label="label"
    @pointermove="onPointer"
    @pointerdown="onPointer"
  />
</template>

<style scoped>
.ascii-canvas {
  display: block;
  width: 100%;
  height: 100%;
  touch-action: pan-y;
}
</style>
