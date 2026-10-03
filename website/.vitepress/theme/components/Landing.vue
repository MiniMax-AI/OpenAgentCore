<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { withBase } from 'vitepress'
import AsciiCanvas from './AsciiCanvas.vue'
import ComposeLab from './ComposeLab.vue'
import LogoWall from './LogoWall.vue'
import SessionFlow from './SessionFlow.vue'
import TitleAccent from './TitleAccent.vue'
import { copy, installCommand, repoUrl, type Lang } from '../landing-content'
import architectureImg from '../../../../docs/assets/architecture.png'
import overviewEn from '../../../../docs/assets/console-overview-en.webp'
import overviewZh from '../../../../docs/assets/console-overview-zh.webp'
import metricsEn from '../../../../docs/assets/console-agent-metrics-en.webp'
import metricsZh from '../../../../docs/assets/console-agent-metrics-zh.webp'

const props = withDefaults(defineProps<{ lang?: Lang }>(), { lang: 'en' })
const t = computed(() => copy[props.lang])

const consoleImages = computed(() =>
  props.lang === 'zh' ? { overview: overviewZh, metrics: metricsZh } : { overview: overviewEn, metrics: metricsEn },
)
const consoleTab = ref<'overview' | 'metrics'>('overview')
const consoleAlt = computed(() => t.value.observe.tabs.find((tab) => tab.id === consoleTab.value)!.alt)

function localLink(path: string) { return withBase(props.lang === 'zh' ? `/zh${path}` : path) }


const copied = ref(false)
async function copyInstall() {
  try {
    await navigator.clipboard.writeText(installCommand)
    copied.value = true
    setTimeout(() => (copied.value = false), 1600)
  } catch {
    copied.value = false
  }
}

// Scrambles the hero title in from random glyphs.
const GLYPHS = '01<>/\\{}[]#%&@$=+*'
const titleLines = ref<string[]>([...t.value.hero.title])
let scrambleTimer: ReturnType<typeof setInterval> | undefined
function scramble() {
  const finals = t.value.hero.title
  let tick = 0
  scrambleTimer = setInterval(() => {
    tick++
    titleLines.value = finals.map((line, li) =>
      line
        .split('')
        .map((ch, i) => (ch === ' ' || tick > i * 1.4 + li * 6 + 4 ? ch : GLYPHS[(Math.random() * GLYPHS.length) | 0]))
        .join(''),
    )
    if (tick > finals.join('').length * 1.4 + 16) {
      clearInterval(scrambleTimer)
      titleLines.value = [...finals]
    }
  }, 40)
}

let revealObserver: IntersectionObserver | undefined
const root = ref<HTMLElement>()
onMounted(() => {
  const reduced = window.matchMedia('(prefers-reduced-motion: reduce)').matches
  if (!reduced) scramble()
  if (reduced) return
  // Content stays visible without JavaScript; hiding for the reveal starts here,
  // after anything already on screen is marked as shown.
  const targets = root.value!.querySelectorAll('[data-reveal]')
  targets.forEach((el) => {
    if (el.getBoundingClientRect().top < window.innerHeight) el.classList.add('in')
  })
  root.value!.classList.add('animate')
  revealObserver = new IntersectionObserver(
    (entries) => {
      for (const entry of entries) {
        if (!entry.isIntersecting) continue
        entry.target.classList.add('in')
        revealObserver?.unobserve(entry.target)
      }
    },
    { rootMargin: '0px 0px -8% 0px' },
  )
  targets.forEach((el) => {
    if (!el.classList.contains('in')) revealObserver!.observe(el)
  })
})
onBeforeUnmount(() => {
  clearInterval(scrambleTimer)
  revealObserver?.disconnect()
})
</script>

<template>
  <div ref="root" class="landing" :lang="lang === 'zh' ? 'zh-CN' : 'en'">
    <div class="bg-grid" aria-hidden="true" />

    <!-- Hero -->
    <section class="hero">
      <div class="wrap hero-grid">
        <div class="hero-copy">
          <p class="eyebrow"><span class="led" aria-hidden="true" />{{ t.hero.eyebrow }}</p>
          <h1 class="hero-title" :aria-label="t.hero.title.join(' ')">
            <span aria-hidden="true">{{ titleLines[0] }}</span>
            <span aria-hidden="true" class="accent">{{ titleLines[1] }}<span class="cursor">_</span></span>
          </h1>
          <p class="lede">{{ t.hero.lede }}</p>
          <div class="ctas">
            <a class="btn primary" :href="localLink('/docs/getting-started/quickstart')">{{ t.hero.primary }} <span aria-hidden="true">→</span></a>
            <a class="btn ghost" :href="localLink('/docs/getting-started/')">{{ t.hero.secondary }}</a>
            <a class="btn ghost icon" :href="repoUrl" target="_blank" rel="noreferrer" aria-label="GitHub">
              <svg viewBox="0 0 24 24" width="18" height="18" aria-hidden="true"><path fill="currentColor" d="M12 .5a11.5 11.5 0 0 0-3.64 22.41c.58.1.79-.25.79-.56v-2c-3.2.7-3.88-1.37-3.88-1.37-.53-1.33-1.28-1.69-1.28-1.69-1.05-.71.08-.7.08-.7 1.16.08 1.77 1.19 1.77 1.19 1.03 1.77 2.71 1.26 3.37.96.1-.75.4-1.26.73-1.55-2.55-.29-5.24-1.28-5.24-5.68 0-1.25.45-2.28 1.19-3.08-.12-.29-.52-1.46.11-3.05 0 0 .97-.31 3.17 1.18a11 11 0 0 1 5.77 0c2.2-1.49 3.17-1.18 3.17-1.18.63 1.59.23 2.76.11 3.05.74.8 1.19 1.83 1.19 3.08 0 4.41-2.69 5.38-5.25 5.67.41.36.78 1.06.78 2.14v3.17c0 .31.21.67.8.56A11.5 11.5 0 0 0 12 .5Z" /></svg>
              GitHub
            </a>
          </div>
          <div class="install">
            <code><span class="prompt">$</span> {{ installCommand }}</code>
            <button type="button" class="copy" @click="copyInstall">{{ copied ? t.hero.copied : t.hero.copy }}</button>
          </div>
          <p class="install-note">{{ t.hero.installLabel }}</p>
        </div>

        <div class="hero-art">
          <div class="corner tl" aria-hidden="true" /><div class="corner tr" aria-hidden="true" /><div class="corner bl" aria-hidden="true" /><div class="corner br" aria-hidden="true" />
          <AsciiCanvas kind="logo" :cell="12" :fill="0.68" :shift-y="-0.08" label="OpenAgentCore" />
          <dl class="hud">
            <div v-for="[k, v] in t.hero.hud" :key="k"><dt>{{ k }}</dt><dd>{{ v }}</dd></div>
          </dl>
        </div>
      </div>

      <div class="ticker" aria-hidden="true">
        <div class="ticker-track">
          <span v-for="n in 2" :key="n" class="ticker-run">
            <span v-for="item in t.ticker" :key="item + n">{{ item }}<b>/</b></span>
          </span>
        </div>
      </div>
    </section>

    <!-- 01 Compose -->
    <section class="sec">
      <div class="wrap">
        <header class="sec-head" data-reveal><span class="idx">// {{ t.compose.index }}</span><span class="kicker">{{ t.compose.kicker }}</span></header>
        <h2 data-reveal><TitleAccent :title="t.compose.title" :accent="t.compose.titleAccent" /></h2>
        <p class="sec-lede" data-reveal>{{ t.compose.lede }}</p>
        <div data-reveal><ComposeLab :t="t.compose" :lang="lang" /></div>

      </div>
    </section>

    <!-- 02 Architecture -->
    <section class="sec">
      <div class="wrap">
        <header class="sec-head" data-reveal><span class="idx">// {{ t.architecture.index }}</span><span class="kicker">{{ t.architecture.kicker }}</span></header>
        <h2 data-reveal><TitleAccent :title="t.architecture.title" :accent="t.architecture.titleAccent" /></h2>
        <p class="sec-lede" data-reveal>{{ t.architecture.lede }}</p>
        <a class="architecture-visual" :href="architectureImg" target="_blank" rel="noreferrer" data-reveal>
          <img :src="architectureImg" :alt="t.architecture.tabs[0].alt" loading="lazy" decoding="async" />
        </a>
        <a class="more" :href="localLink('/docs/architecture')">{{ t.architecture.more }} →</a>
      </div>
    </section>

    <!-- 03 Session lifecycle -->
    <section class="sec">
      <div class="wrap">
        <header class="sec-head" data-reveal><span class="idx">// {{ t.session.index }}</span><span class="kicker">{{ t.session.kicker }}</span></header>
        <h2 data-reveal><TitleAccent :title="t.session.title" :accent="t.session.titleAccent" /></h2>
        <p class="sec-lede" data-reveal>{{ t.session.lede }}</p>
        <SessionFlow :lang="lang" />
        <a class="more" :href="localLink('/docs/api/public-agent-api')">{{ t.session.more }} →</a>
      </div>
    </section>

    <!-- 04 Observe -->
    <section class="sec">
      <div class="wrap">
        <header class="sec-head" data-reveal><span class="idx">// {{ t.observe.index }}</span><span class="kicker">{{ t.observe.kicker }}</span></header>
        <div class="observe-heading" data-reveal>
          <div><h2><TitleAccent :title="t.observe.title" :accent="t.observe.titleAccent" /></h2><p class="sec-lede">{{ t.observe.lede }}</p></div>
          <div class="tabs" :aria-label="t.observe.title">
            <button v-for="tab in t.observe.tabs" :key="tab.id" type="button" :aria-pressed="consoleTab === tab.id" :class="{ on: consoleTab === tab.id }" @click="consoleTab = tab.id">{{ tab.label }}</button>
          </div>
        </div>
        <div class="window console-wide" data-reveal>
          <div class="win-bar"><span class="dots"><i /><i /><i /></span><span>web · {{ consoleTab }}</span></div>
          <img :key="consoleTab + lang" :src="consoleImages[consoleTab]" :alt="consoleAlt" loading="lazy" decoding="async" />
        </div>
        <p class="caption">{{ t.observe.caption }}</p>
      </div>
    </section>

    <nav class="wrap doc-shortcuts" :aria-label="t.docs.title">
      <a v-for="(link, i) in t.docs.links" :key="link.path" :href="localLink(link.path)">
        <span class="shortcut-index">0{{ i + 1 }}</span><span>{{ link.label }}</span><span aria-hidden="true">↗</span>
      </a>
    </nav>

    <LogoWall :lang="lang" />

    <!-- 05 Get started -->
    <section class="outro">
      <div class="wrap start-compact">
        <header class="sec-head"><span class="idx">// {{ t.start.index }}</span><span class="kicker">{{ t.start.kicker }}</span></header>
        <h2><TitleAccent :title="t.start.title" :accent="t.start.titleAccent" /></h2>
        <p class="install-note">{{ t.hero.installLabel }}</p>
        <div class="install"><code><span class="prompt">$</span> {{ installCommand }}</code><button type="button" class="copy" @click="copyInstall">{{ copied ? t.hero.copied : t.hero.copy }}</button></div>
        <ol class="start-path"><li v-for="(step, i) in t.start.steps" :key="step.title"><span>0{{ i + 1 }}</span>{{ step.title }}</li></ol>
        <div class="ctas"><a class="btn primary" :href="localLink('/docs/getting-started/install')">{{ t.start.cta }} →</a><a class="btn ghost" :href="localLink('/docs/getting-started/quickstart')">{{ t.start.trial }}</a></div>
      </div>
      <div class="outro-art">
        <AsciiCanvas kind="text" text="ONE CORE.&#10;MANY AGENTS." :cell="11" ramp=" .:░▒▓█" :noise="0.02" :fill="0.82" label="One core. Many agents." />
      </div>
    </section>
  </div>
</template>

<style>
/* ---------- palette ---------- */
.landing {
  --l-bg: #f3efe3;
  --l-panel: rgba(255, 255, 255, 0.72);
  --l-hover: rgba(21, 128, 61, 0.06);
  --l-text: #0b1510;
  --l-text-2: #2b3a31;
  --l-muted: #5d6b62;
  --l-faint: #7c8a81;
  --l-line: rgba(11, 21, 16, 0.12);
  --l-line-strong: rgba(11, 21, 16, 0.22);
  --l-accent: #15803d;
  --l-accent-soft: rgba(21, 128, 61, 0.09);
  --l-danger: #dc2626;
  --l-danger-soft: rgba(220, 38, 38, 0.08);
  --l-glow: rgba(21, 128, 61, 0.35);
  --l-grid: rgba(11, 21, 16, 0.05);
  --ascii-dim: #c4d1c7;
  --ascii-mid: #3f8a5c;
  --ascii-hot: #15803d;
  --ascii-white: #04210f;
  /* Code and terminals stay dark in both themes. */
  --l-code-bg: #0b100e;
  --l-code-text: #d7e5dc;
  --l-kw: #f472b6;
  --l-str: #a3e6b8;
  --l-fn: #7dd3fc;
  --l-mono: 'Geist Mono Variable', ui-monospace, SFMono-Regular, Menlo, monospace;
  --l-display: 'Space Grotesk Variable', 'PingFang SC', 'Microsoft YaHei', sans-serif;
  --l-sans: 'Inter Variable', Inter, system-ui, -apple-system, 'PingFang SC', 'Microsoft YaHei', sans-serif;

  position: relative;
  overflow: hidden;
  background: var(--l-bg);
  color: var(--l-text);
  font-family: var(--l-sans);
}
.dark .landing {
  --l-bg: #111712;
  --l-panel: rgba(18, 24, 21, 0.72);
  --l-hover: rgba(74, 222, 128, 0.06);
  --l-text: #e8f1eb;
  --l-text-2: #b7c5bc;
  --l-muted: #7f8f85;
  --l-faint: #56645b;
  --l-line: rgba(232, 241, 235, 0.09);
  --l-line-strong: rgba(232, 241, 235, 0.18);
  --l-accent: #4ade80;
  --l-accent-soft: rgba(74, 222, 128, 0.08);
  --l-danger: #fb7185;
  --l-danger-soft: rgba(251, 113, 133, 0.08);
  --l-glow: rgba(74, 222, 128, 0.35);
  --l-grid: rgba(232, 241, 235, 0.035);
  --ascii-dim: #1d3326;
  --ascii-mid: #2f8a52;
  --ascii-hot: #4ade80;
  --ascii-white: #eafff1;
}
</style>

<style scoped>
.landing :deep(a) {
  text-decoration: none;
}
.wrap {
  position: relative;
  max-width: 1200px;
  margin: 0 auto;
  padding: 0 28px;
}
.bg-grid {
  position: absolute;
  inset: 0;
  pointer-events: none;
  background-image: linear-gradient(var(--l-grid) 1px, transparent 1px), linear-gradient(90deg, var(--l-grid) 1px, transparent 1px);
  background-size: 96px 96px;
  mask-image: linear-gradient(to bottom, #000 0, #000 70%, transparent);
}
.bg-grid::after {
  content: '';
  position: absolute;
  inset: 0;
  background: radial-gradient(60% 40% at 75% 8%, var(--l-accent-soft), transparent 70%);
}
.accent {
  color: var(--l-accent);
}
.sr-only {
  position: absolute;
  width: 1px;
  height: 1px;
  overflow: hidden;
  clip: rect(0 0 0 0);
}

/* ---------- reveal ---------- */
.animate [data-reveal] {
  opacity: 0;
  transform: translateY(14px);
  transition: opacity 0.7s ease, transform 0.7s cubic-bezier(0.2, 0.7, 0.2, 1);
  transition-delay: calc(var(--i, 0) * 70ms);
}
.animate [data-reveal].in {
  opacity: 1;
  transform: none;
}

/* ---------- buttons ---------- */
.ctas {
  display: flex;
  flex-wrap: wrap;
  gap: 10px;
  margin-top: 28px;
}
.ctas.center {
  justify-content: center;
}
.btn {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  height: 42px;
  padding: 0 18px;
  border-radius: 2px;
  font: 500 14px/1 var(--l-mono);
  border: 1px solid var(--l-line-strong);
  color: var(--l-text);
  transition: transform 0.15s, background 0.15s, border-color 0.15s, box-shadow 0.15s;
}
.btn:hover {
  transform: translateY(-1px);
  border-color: var(--l-accent);
}
.btn.primary {
  background: var(--l-accent);
  border-color: var(--l-accent);
  color: var(--l-bg);
  box-shadow: 0 0 0 0 var(--l-glow);
}
.btn.primary:hover {
  box-shadow: 0 8px 30px -8px var(--l-glow);
}
.btn.ghost {
  background: var(--l-panel);
  backdrop-filter: blur(6px);
}

/* ---------- hero ---------- */
.hero {
  position: relative;
  padding: 56px 0 0;
}
.hero-grid {
  display: grid;
  grid-template-columns: minmax(0, 1.2fr) minmax(0, 0.9fr);
  gap: 32px;
  align-items: center;
  min-height: min(72vh, 660px);
}
.eyebrow {
  display: inline-flex;
  align-items: center;
  gap: 10px;
  margin: 0 0 22px;
  padding: 6px 12px;
  border: 1px solid var(--l-line);
  border-radius: 999px;
  background: var(--l-panel);
  font: 12px/1 var(--l-mono);
  letter-spacing: 0.06em;
  text-transform: uppercase;
  color: var(--l-text-2);
}
.led {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: var(--l-accent);
  box-shadow: 0 0 10px var(--l-accent);
  animation: pulse 2s ease-in-out infinite;
}
.hero-title {
  display: flex;
  flex-direction: column;
  margin: 0;
  font: 700 clamp(44px, 6vw, 78px) / 0.98 var(--l-display);
  letter-spacing: -0.045em;
  white-space: nowrap;
}
.cursor {
  color: var(--l-accent);
  animation: blink 1.1s steps(1) infinite;
}
.lede {
  max-width: 34em;
  margin: 26px 0 0;
  font-size: 18px;
  line-height: 1.6;
  color: var(--l-text-2);
}
.install {
  display: flex;
  align-items: center;
  gap: 8px;
  max-width: 100%;
  margin-top: 26px;
  padding: 6px 6px 6px 14px;
  border: 1px solid var(--l-line);
  border-radius: 3px;
  background: var(--l-code-bg);
}
.install code {
  flex: 1;
  min-width: 0;
  overflow-x: auto;
  white-space: nowrap;
  scrollbar-width: none;
  font: 13px/2.2 var(--l-mono);
  color: var(--l-code-text);
  background: none;
  padding: 0;
}
.install .prompt {
  color: #4ade80;
}
.copy {
  flex: none;
  padding: 6px 12px;
  border-radius: 6px;
  border: 1px solid rgba(232, 241, 235, 0.16);
  font: 12px var(--l-mono);
  color: #b7c5bc;
}
.copy:hover {
  color: #4ade80;
  border-color: #4ade80;
}
.install-note {
  margin: 10px 0 0;
  font: 12px var(--l-mono);
  color: var(--l-muted);
}
.hero-art {
  position: relative;
  aspect-ratio: 1 / 1;
  width: 100%;
  max-width: 600px;
  justify-self: end;
}
.corner {
  position: absolute;
  width: 18px;
  height: 18px;
  border-color: var(--l-accent);
  border-style: solid;
  opacity: 0.7;
}
.corner.tl { top: 0; left: 0; border-width: 1px 0 0 1px; }
.corner.tr { top: 0; right: 0; border-width: 1px 1px 0 0; }
.corner.bl { bottom: 0; left: 0; border-width: 0 0 1px 1px; }
.corner.br { bottom: 0; right: 0; border-width: 0 1px 1px 0; }
.hud {
  position: absolute;
  left: 14px;
  bottom: 12px;
  margin: 0;
  font: 11px/1.7 var(--l-mono);
  color: var(--l-muted);
  pointer-events: none;
}
.hud div {
  display: flex;
  gap: 8px;
}
.hud dt {
  min-width: 6.5em;
  color: var(--l-accent);
}
.hud dt::after {
  content: ':';
}
.hud dd {
  margin: 0;
}

/* ticker */
.ticker {
  margin-top: 48px;
  border-block: 1px solid var(--l-line);
  overflow: hidden;
  background: var(--l-panel);
  mask-image: linear-gradient(90deg, transparent, #000 10%, #000 90%, transparent);
}
.ticker-track {
  display: flex;
  width: max-content;
  animation: marquee 48s linear infinite;
}
.ticker-run span {
  display: inline-block;
  padding: 14px 0 14px 22px;
  font: 13px var(--l-mono);
  color: var(--l-text-2);
  text-transform: lowercase;
}
.ticker-run b {
  margin-left: 22px;
  font-weight: 400;
  color: var(--l-accent);
}

/* ---------- sections ---------- */
.sec {
  position: relative;
  padding: 64px 0 0;
}
.sec-head {
  display: flex;
  align-items: center;
  gap: 14px;
  margin-bottom: 20px;
  font: 12px/1 var(--l-mono);
  text-transform: uppercase;
  letter-spacing: 0.12em;
}
.sec-head::after {
  content: '';
  flex: 1;
  height: 1px;
  background: linear-gradient(90deg, var(--l-line-strong), transparent);
}
.idx {
  color: var(--l-accent);
}
.kicker {
  color: var(--l-muted);
}
h2 {
  max-width: 22em;
  margin: 0;
  font: 700 clamp(28px, 3.6vw, 44px) / 1.12 var(--l-sans);
  letter-spacing: -0.03em;
  border: 0;
  padding: 0;
}
.landing[lang='zh-CN'] h2 {
  letter-spacing: -0.01em;
}
h3 {
  margin: 0;
  font: 600 17px/1.35 var(--l-sans);
  letter-spacing: -0.01em;
}
.sec-lede {
  max-width: 44em;
  margin: 18px 0 36px;
  font-size: 17px;
  line-height: 1.65;
  color: var(--l-text-2);
}
.split {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(0, 1.1fr);
  gap: 56px;
  align-items: start;
}
.split.narrow-right {
  grid-template-columns: minmax(0, 0.8fr) minmax(0, 1.5fr);
}
.mono-label {
  margin: 0 0 12px;
  font: 12px var(--l-mono);
  text-transform: uppercase;
  letter-spacing: 0.1em;
  color: var(--l-muted);
}
.sigil {
  display: inline-block;
  width: 1.6em;
  font-family: var(--l-mono);
  color: var(--l-accent);
}

/* 03 */
.figure {
  border: 1px solid var(--l-line);
  border-radius: 3px;
  background: var(--l-panel);
  overflow: hidden;
}
.tabs {
  display: flex;
  gap: 4px;
  padding: 8px;
  border-bottom: 1px solid var(--l-line);
  overflow-x: auto;
}
.tabs button {
  padding: 7px 14px;
  border-radius: 7px;
  font: 13px var(--l-mono);
  color: var(--l-muted);
  white-space: nowrap;
  transition: color 0.15s, background 0.15s;
}
.tabs button::before {
  content: '> ';
  opacity: 0;
}
.tabs button.on {
  color: var(--l-accent);
  background: var(--l-accent-soft);
}
.tabs button.on::before {
  opacity: 1;
}
.tabs button:hover {
  color: var(--l-text);
}
.tabs.vertical {
  flex-direction: column;
  align-items: flex-start;
  padding: 0;
  border: 0;
}
.plate {
  display: block;
  padding: 18px;
  background: #fff;
  cursor: zoom-in;
}
.plate img {
  display: block;
  width: 100%;
  height: auto;
  animation: fade 0.4s ease;
}
.boundaries {
  display: grid;
  gap: 0;
  margin-top: 28px;
}
.boundary {
  display: grid;
  grid-template-columns: 9em minmax(40px, 1fr) 9em minmax(0, 1.3fr);
  align-items: center;
  gap: 14px;
  padding: 12px 6px;
  border-bottom: 1px solid var(--l-line);
  font: 14px var(--l-mono);
  color: var(--l-text);
  transition: background 0.15s;
}
.boundary:hover {
  background: var(--l-hover);
}
.b-to {
  text-align: left;
}
.b-wire {
  position: relative;
  height: 1px;
  background: var(--l-line-strong);
}
.b-wire::after {
  content: '▶';
  position: absolute;
  right: -2px;
  top: 50%;
  transform: translateY(-52%);
  font-size: 9px;
  color: var(--l-line-strong);
}
.b-wire i {
  position: absolute;
  top: -1px;
  left: 0;
  width: 26px;
  height: 3px;
  border-radius: 2px;
  background: var(--l-accent);
  box-shadow: 0 0 10px var(--l-accent);
  animation: packet 2.6s linear infinite;
  animation-delay: calc(var(--i) * -0.43s);
}
.b-doc {
  justify-self: end;
  color: var(--l-muted);
  font-size: 13px;
}
.boundary:hover .b-doc {
  color: var(--l-accent);
}
.more {
  justify-self: start;
  margin-top: 18px;
  font: 14px var(--l-mono);
  color: var(--l-accent);
}

/* 04 */
.rules {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 14px;
  margin: 0;
  padding: 0;
  list-style: none;
}
.rules li {
  padding: 20px;
  border: 1px solid var(--l-line);
  border-radius: 3px;
  background: var(--l-panel);
}
.art {
  margin: 0 0 18px;
  padding: 14px 16px;
  border-radius: 2px;
  background: var(--l-code-bg);
  color: #4ade80;
  font: 12.5px/1.7 var(--l-mono);
  overflow-x: auto;
  white-space: pre;
}
.note {
  display: flex;
  margin: 26px 0 0;
  font: 14px/1.6 var(--l-mono);
  color: var(--l-text-2);
}

/* 05 */
.caption {
  margin: 22px 0 0;
  font-size: 13px;
  line-height: 1.6;
  color: var(--l-muted);
}
.window {
  border: 1px solid var(--l-line);
  border-radius: 3px;
  overflow: hidden;
  background: var(--l-panel);
  box-shadow: 0 40px 100px -50px var(--l-glow);
}
.window img {
  display: block;
  width: 100%;
  height: auto;
  animation: fade 0.4s ease;
}
.win-bar {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 10px 14px;
  border-bottom: 1px solid var(--l-line);
  font: 12px var(--l-mono);
  color: var(--l-muted);
}
.dots {
  display: flex;
  gap: 6px;
}
.dots i {
  width: 10px;
  height: 10px;
  border-radius: 50%;
}
.dots i:nth-child(1) { background: #ff5f57; }
.dots i:nth-child(2) { background: #febc2e; }
.dots i:nth-child(3) { background: #28c840; }

/* outro */
.outro {
  position: relative;
  margin-top: 24px;
  padding-bottom: 96px;
  border-top: 1px solid var(--l-line);
}
.outro-art {
  height: clamp(200px, 30vw, 380px);
  max-width: 1280px;
  margin: 0 auto;
}
.outro-copy {
  text-align: center;
}
.outro-copy p {
  margin: 8px auto 0;
  max-width: 36em;
  font-size: 18px;
  line-height: 1.6;
  color: var(--l-text-2);
}

/* ---------- motion ---------- */
@keyframes blink {
  50% { opacity: 0; }
}
@keyframes pulse {
  50% { opacity: 0.35; }
}
@keyframes marquee {
  to { transform: translateX(-50%); }
}
@keyframes packet {
  from { left: 0; opacity: 0; }
  10% { opacity: 1; }
  90% { opacity: 1; }
  to { left: calc(100% - 26px); opacity: 0; }
}
@keyframes fade {
  from { opacity: 0; }
}
@media (prefers-reduced-motion: reduce) {
  .ticker-track, .b-wire i, .led, .cursor { animation: none; }
}

/* ---------- responsive ---------- */
@media (max-width: 1080px) {
  .pillars, .roadmap, .docs-grid { grid-template-columns: repeat(2, minmax(0, 1fr)); }
}
@media (max-width: 900px) {
  .hero { padding-top: 24px; }
  .hero-grid { grid-template-columns: minmax(0, 1fr); gap: 8px; min-height: 0; }
  .hero-art { order: -1; width: min(100%, 340px); justify-self: center; }
  .hud { display: none; }
  .split, .split.narrow-right { grid-template-columns: minmax(0, 1fr); gap: 32px; }
  .tradeoffs { grid-template-columns: minmax(0, 1fr); }
  .rules { grid-template-columns: minmax(0, 1fr); }
  .boundary { grid-template-columns: 7.5em minmax(24px, 1fr) 7.5em; }
  .b-doc { grid-column: 1 / -1; justify-self: start; }
  .sec { padding-top: 84px; }
}
@media (max-width: 560px) {
  .wrap { padding: 0 18px; }
  .pillars, .roadmap, .docs-grid { grid-template-columns: minmax(0, 1fr); }
  .eyebrow { flex-wrap: wrap; border-radius: 3px; line-height: 1.5; }
  .hero-title { font-size: clamp(38px, 12vw, 56px); }
  .lede { font-size: 16px; }
  .compare tbody th { white-space: normal; }
}

/* Editorial typography and ruled sections retain the terminal demonstrations. */
.hero-title .accent { font-style: italic; }
.cursor { font-style: normal; }
.eyebrow { border-radius: 0; background: transparent; border: 0; border-left: 3px solid var(--l-accent); padding-left: 12px; }
.sec { border-top: 1px solid var(--l-line-strong); padding-bottom: 56px; }

.sec-head { align-items: center; }
.idx { background: var(--l-accent); color: var(--l-bg); padding: 7px 10px; }
h2, .motto { font-family: var(--l-display); letter-spacing: -0.045em; }
h3 { font-family: var(--l-display); }
.hero-art { border: 1px solid var(--l-line-strong); background: var(--l-panel); transform: rotate(1deg); }
.ticker { background: var(--l-text); color: var(--l-bg); border-top: 3px solid var(--l-accent); }
.ticker-run span { color: inherit; }
.btn.primary { box-shadow: 3px 3px 0 var(--l-text); }
.btn.primary:hover { box-shadow: 5px 5px 0 var(--l-text); }
.btn:focus-visible, .copy:focus-visible { outline: 2px solid var(--l-accent); outline-offset: 5px; }
@media (max-width: 640px) {
  .hero-art { transform: none; }
  .hero-title { letter-spacing: -0.05em; }
}
</style>

<style scoped>
.architecture-visual { display: block; max-width: 980px; margin: 0 auto 20px; padding: 16px; background: #fff; border-radius: 4px; }
.architecture-visual img { display: block; width: 100%; height: auto; }
.more { display: inline-block; }
.session-flow { display: flex; align-items: center; justify-content: center; gap: 28px; padding: 32px 24px; margin: 24px 0; border: 1px solid var(--l-line-strong); background: var(--l-panel); }
.flow-node { display: flex; align-items: center; gap: 12px; padding: 18px 24px; border: 1px solid var(--l-line-strong); font: 500 17px var(--l-mono); }
.flow-dot { width: 8px; height: 8px; border-radius: 50%; background: var(--l-muted); }
.flow-node.active { border-color: var(--l-accent); color: var(--l-accent); }
.active .flow-dot { background: var(--l-accent); box-shadow: 0 0 16px var(--l-glow); }
.flow-arrow { color: var(--l-accent); font-size: 24px; }
.flow-outcomes { display: grid; gap: 10px; }
.compact-rules { grid-template-columns: repeat(4, minmax(0, 1fr)); }
.compact-rules li { padding: 16px; }
.rule-icon { width: 36px; height: 36px; margin-bottom: 18px; color: var(--l-accent); }
.compact-rules h3 { font-size: 16px; }
.compact-rules p { font-size: 13px; }
.observe-heading { display: flex; align-items: center; justify-content: space-between; gap: 24px; }
.observe-heading .tabs { flex-shrink: 0; border: 1px solid var(--l-line); }
.console-wide { width: 100%; }
.console-wide img { width: 100%; display: block; }
.doc-shortcuts { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: 12px; padding-top: 28px; padding-bottom: 28px; }
.doc-shortcuts a { display: flex; align-items: center; gap: 16px; padding: 22px 16px; border: 1px solid var(--l-line-strong); font: 500 14px var(--l-mono); }
.doc-shortcuts a:hover { border-color: var(--l-accent); color: var(--l-accent); }
.doc-shortcuts a > :last-child { margin-left: auto; }
.shortcut-index { color: var(--l-accent); font-size: 11px; }
.start-compact { padding-top: 40px; }
.start-compact .install { max-width: 100%; }
.start-path { display: flex; gap: 32px; list-style: none; padding: 0; margin: 24px 0; font: 14px var(--l-mono); }
.start-path li { display: flex; gap: 12px; align-items: center; }
.start-path span { color: var(--l-accent); }
.outro { padding-bottom: 0; }
.outro-art { margin-top: 32px; }
@media (max-width: 900px) {
  .compact-rules, .doc-shortcuts { grid-template-columns: repeat(2, minmax(0, 1fr)); }
  .session-flow { gap: 14px; padding: 24px 12px; }
  .flow-node { padding: 12px; font-size: 14px; }
}
@media (max-width: 640px) {
  .observe-heading { display: block; }
  .observe-heading .tabs { margin-bottom: 20px; width: fit-content; }
  .session-flow { flex-wrap: wrap; gap: 10px; }
  .flow-node { font-size: 11px; padding: 10px; gap: 6px; }
  .flow-arrow { font-size: 18px; }
  .flow-dot { width: 5px; height: 5px; }
  .start-path { flex-direction: column; gap: 16px; }
  .doc-shortcuts a { font-size: 12px; gap: 8px; padding: 16px 10px; }
}
</style>

<style scoped>
@font-face { font-family: 'OAC Hand'; src: url('../assets/fonts/Virgil.woff2') format('woff2'); font-weight: 400; font-display: swap; }
</style>
