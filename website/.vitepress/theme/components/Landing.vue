<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { withBase } from 'vitepress'
import '@fontsource-variable/space-grotesk'
import ComposeLab from './ComposeLab.vue'
import LogoWall from './LogoWall.vue'
import LaunchVideo from './LaunchVideo.vue'
import SessionFlow from './SessionFlow.vue'
import TitleAccent from './TitleAccent.vue'
import { copy, installCommands, repoUrl, type Lang } from '../landing-content'
import architectureLight from '../../../../docs/assets/architecture.svg'
import architectureDark from '../../../../docs/assets/architecture-dark.svg'
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

const installOs = ref<'unix' | 'windows'>('unix')
const installCommand = computed(() => installCommands[installOs.value])
const copied = ref(false)
async function copyInstall() {
  try {
    await navigator.clipboard.writeText(installCommand.value)
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
      <div class="wrap">
        <div class="hero-grid">
          <div class="hero-head">
            <h1 class="hero-title" :aria-label="t.hero.title.join(' ')">
              <span aria-hidden="true">{{ titleLines[0] }}</span>
              <span aria-hidden="true" class="accent">{{ titleLines[1] }}<span class="cursor">_</span></span>
            </h1>
            <LaunchVideo :lang="lang" />
          </div>
          <div class="hero-copy">
            <p class="lede">{{ t.hero.lede }}</p>
            <div class="ctas">
              <a class="btn primary" :href="localLink('/docs/getting-started/install')">{{ t.hero.primary }} <span aria-hidden="true">→</span></a>
              <a class="btn ghost" :href="localLink('/docs/getting-started/')">{{ t.hero.secondary }}</a>
              <a class="btn ghost icon" :href="repoUrl" target="_blank" rel="noreferrer" aria-label="GitHub">
              <svg viewBox="0 0 24 24" width="18" height="18" aria-hidden="true"><path fill="currentColor" d="M12 .5a11.5 11.5 0 0 0-3.64 22.41c.58.1.79-.25.79-.56v-2c-3.2.7-3.88-1.37-3.88-1.37-.53-1.33-1.28-1.69-1.28-1.69-1.05-.71.08-.7.08-.7 1.16.08 1.77 1.19 1.77 1.19 1.03 1.77 2.71 1.26 3.37.96.1-.75.4-1.26.73-1.55-2.55-.29-5.24-1.28-5.24-5.68 0-1.25.45-2.28 1.19-3.08-.12-.29-.52-1.46.11-3.05 0 0 .97-.31 3.17 1.18a11 11 0 0 1 5.77 0c2.2-1.49 3.17-1.18 3.17-1.18.63 1.59.23 2.76.11 3.05.74.8 1.19 1.83 1.19 3.08 0 4.41-2.69 5.38-5.25 5.67.41.36.78 1.06.78 2.14v3.17c0 .31.21.67.8.56A11.5 11.5 0 0 0 12 .5Z" /></svg>
              GitHub
            </a>
            </div>
            <div class="install-tabs" role="group">
              <button v-for="(label, i) in t.hero.installTabs" :key="label" type="button" :aria-pressed="installOs === (i ? 'windows' : 'unix')" :class="{ on: installOs === (i ? 'windows' : 'unix') }" @click="installOs = i ? 'windows' : 'unix'">{{ label }}</button>
            </div>
            <div class="install">
              <code><span class="prompt">{{ installOs === 'unix' ? '$' : '>' }}</span> {{ installCommand }}</code>
              <button type="button" class="copy" @click="copyInstall">{{ copied ? t.hero.copied : t.hero.copy }}</button>
            </div>
          </div>
        </div>
        <div class="hero-lab" data-reveal><ComposeLab :t="t.compose" :lang="lang" /></div>
      </div>
    </section>

    <!-- Architecture -->
    <section class="sec">
      <div class="wrap">
        <h2 data-reveal><TitleAccent :title="t.architecture.title" :accent="t.architecture.titleAccent" /></h2>
        <div class="architecture-visual" data-reveal>
          <a class="light-only" :href="architectureLight" target="_blank" rel="noreferrer"><img :src="architectureLight" :alt="t.architecture.alt" loading="lazy" decoding="async" /></a>
          <a class="dark-only" :href="architectureDark" target="_blank" rel="noreferrer"><img :src="architectureDark" :alt="t.architecture.alt" loading="lazy" decoding="async" /></a>
        </div>
        <a class="more" :href="localLink('/docs/architecture')">{{ t.architecture.more }} →</a>
      </div>
    </section>

    <!-- Session -->
    <section class="sec">
      <div class="wrap">
        <h2 data-reveal><TitleAccent :title="t.session.title" :accent="t.session.titleAccent" /></h2>
        <SessionFlow :lang="lang" />
        <a class="more" :href="localLink('/docs/architecture#a-session-end-to-end')">{{ t.session.more }} →</a>
      </div>
    </section>

    <!-- Console -->
    <section class="sec">
      <div class="wrap">
        <div class="observe-heading" data-reveal>
          <h2><TitleAccent :title="t.observe.title" :accent="t.observe.titleAccent" /></h2>
          <div class="tabs" :aria-label="t.observe.title">
            <button v-for="tab in t.observe.tabs" :key="tab.id" type="button" :aria-pressed="consoleTab === tab.id" :class="{ on: consoleTab === tab.id }" @click="consoleTab = tab.id">{{ tab.label }}</button>
          </div>
        </div>
        <div class="window" data-reveal>
          <img :key="consoleTab + lang" :src="consoleImages[consoleTab]" :alt="consoleAlt" loading="lazy" decoding="async" />
        </div>
      </div>
    </section>

    <nav class="wrap doc-shortcuts" :aria-label="t.docs.title">
      <a v-for="link in t.docs.links" :key="link.path" :href="localLink(link.path)"><span>{{ link.label }}</span><span aria-hidden="true">↗</span></a>
    </nav>

    <LogoWall :lang="lang" />

    <!-- Next steps -->
    <section class="outro">
      <div class="wrap">
        <h2><TitleAccent :title="t.start.title" :accent="t.start.titleAccent" /></h2>
        <ol class="start-path"><li v-for="(step, i) in t.start.steps" :key="step.path"><span>0{{ i + 1 }}</span><a :href="localLink(step.path)">{{ step.label }} →</a></li></ol>
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
}
:where(.landing) button { font-size: 14px; }
.landing :is(.install, .code) code { font-size: 14px; }
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

/* ---------- reveal ---------- */
.animate [data-reveal] {
  opacity: 0;
  transform: translateY(14px);
  transition: opacity 0.7s ease, transform 0.7s cubic-bezier(0.2, 0.7, 0.2, 1);
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
  margin-top: 32px;
}
.btn {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  height: 44px;
  padding: 0 20px;
  border-radius: 2px;
  font: 500 15px/1 var(--l-mono);
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
  box-shadow: 3px 3px 0 var(--l-text);
}
.btn.primary:hover {
  box-shadow: 5px 5px 0 var(--l-text);
}
.btn.ghost {
  background: var(--l-panel);
  backdrop-filter: blur(6px);
}
.btn:focus-visible,
.copy:focus-visible,
.install-tabs button:focus-visible,
.tabs button:focus-visible {
  outline: 2px solid var(--l-accent);
  outline-offset: 4px;
}

/* ---------- hero ---------- */
.hero {
  position: relative;
  padding: 88px 0 0;
}
.hero-grid {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(0, 1fr);
  gap: 56px;
  align-items: center;
}
.hero-head {
  display: grid;
  gap: 40px;
  justify-items: start;
}
.hero-title {
  display: flex;
  flex-direction: column;
  margin: 0;
  font: 700 clamp(44px, 6vw, 78px) / 0.98 var(--l-display);
  letter-spacing: -0.045em;
  white-space: nowrap;
  border: 0;
  padding: 0;
}
.hero-title .accent {
  font-style: italic;
}
.landing[lang='zh-CN'] .hero-title .accent,
.landing[lang='zh-CN'] :deep(em) {
  font-style: normal;
}
.cursor {
  color: var(--l-accent);
  font-style: normal;
  animation: blink 1.1s steps(1) infinite;
}
.lede {
  max-width: 34em;
  margin: 0;
  font-size: 18px;
  line-height: 1.6;
  color: var(--l-text-2);
}
.install-tabs {
  display: flex;
  gap: 4px;
  margin-top: 32px;
}
.install-tabs button {
  padding: 6px 12px;
  border-radius: 4px;
  font: 14px var(--l-mono);
  color: var(--l-muted);
}
.install-tabs button.on {
  color: var(--l-accent);
  background: var(--l-accent-soft);
}
.install-tabs button:hover {
  color: var(--l-text);
}
.install {
  display: flex;
  align-items: center;
  gap: 8px;
  max-width: 100%;
  margin-top: 8px;
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
  font: 14px/2.2 var(--l-mono);
  color: var(--l-code-text);
  background: none;
  padding: 0;
}
.install .prompt {
  color: #4ade80;
}
.copy {
  flex: none;
  padding: 7px 14px;
  border-radius: 6px;
  border: 1px solid rgba(232, 241, 235, 0.16);
  font: 14px var(--l-mono);
  color: #b7c5bc;
}
.copy:hover {
  color: #4ade80;
  border-color: #4ade80;
}
.hero-lab {
  margin-top: 88px;
}

/* ---------- sections ---------- */
.sec {
  position: relative;
  margin-top: 96px;
  padding-top: 72px;
  border-top: 1px solid var(--l-line-strong);
}
h2 {
  max-width: 22em;
  margin: 0 0 48px;
  font: 700 clamp(28px, 3.6vw, 44px) / 1.12 var(--l-display);
  letter-spacing: -0.045em;
  border: 0;
  padding: 0;
}
.landing[lang='zh-CN'] h2 {
  letter-spacing: -0.01em;
}
.more {
  display: inline-block;
  margin-top: 24px;
  font: 16px var(--l-mono);
  color: var(--l-accent);
}
.architecture-visual {
  display: block;
  max-width: 980px;
  margin: 0 auto;
}
.architecture-visual a {
  display: block;
}
.architecture-visual .dark-only,
.dark .architecture-visual .light-only {
  display: none;
}
.dark .architecture-visual .dark-only {
  display: block;
}
.architecture-visual img {
  display: block;
  width: 100%;
  height: auto;
}

/* console */
.observe-heading {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 24px;
}
.tabs {
  display: flex;
  flex-shrink: 0;
  gap: 4px;
  padding: 6px;
  border: 1px solid var(--l-line);
}
.tabs button {
  padding: 8px 16px;
  border-radius: 7px;
  font: 14px var(--l-mono);
  color: var(--l-muted);
  white-space: nowrap;
  transition: color 0.15s, background 0.15s;
}
.tabs button.on {
  color: var(--l-accent);
  background: var(--l-accent-soft);
}
.tabs button:hover {
  color: var(--l-text);
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

/* documentation shortcuts */
.doc-shortcuts {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 16px;
  margin-top: 96px;
}
.doc-shortcuts a {
  display: flex;
  align-items: center;
  gap: 16px;
  padding: 28px 20px;
  border: 1px solid var(--l-line-strong);
  font: 500 15px/1.4 var(--l-mono);
  color: var(--l-text);
}
.doc-shortcuts a:hover {
  border-color: var(--l-accent);
  color: var(--l-accent);
}
.doc-shortcuts a > :last-child {
  margin-left: auto;
}

/* outro */
.outro {
  position: relative;
  margin-top: 56px;
  padding: 72px 0 120px;
  border-top: 1px solid var(--l-line);
}
.start-path {
  display: flex;
  gap: 48px;
  margin: 0;
  padding: 0;
  list-style: none;
  font: 16px var(--l-mono);
}
.start-path li {
  display: flex;
  gap: 14px;
  align-items: baseline;
}
.start-path span {
  color: var(--l-accent);
}
.start-path a {
  color: var(--l-text);
}
.start-path a:hover {
  color: var(--l-accent);
}

/* ---------- motion ---------- */
@keyframes blink {
  50% { opacity: 0; }
}
@keyframes fade {
  from { opacity: 0; }
}
@media (prefers-reduced-motion: reduce) {
  .cursor { animation: none; }
}

/* ---------- responsive ---------- */
@media (max-width: 900px) {
  .hero { padding-top: 48px; }
  .hero-grid { grid-template-columns: minmax(0, 1fr); gap: 40px; }
  .hero-lab { margin-top: 64px; }
  .sec { margin-top: 72px; padding-top: 56px; }
  .doc-shortcuts { grid-template-columns: repeat(2, minmax(0, 1fr)); margin-top: 72px; }
}
@media (max-width: 640px) {
  .wrap { padding: 0 18px; }
  .hero-title { font-size: clamp(38px, 12vw, 56px); letter-spacing: -0.05em; }
  .lede { font-size: 16px; }
  .observe-heading { display: block; }
  .observe-heading .tabs { margin-bottom: 28px; width: fit-content; }
  .doc-shortcuts { grid-template-columns: minmax(0, 1fr); }
  .start-path { flex-direction: column; gap: 20px; }
  /* Keep the figure and the console at a readable size and let them scroll sideways. */
  .architecture-visual, .window { overflow-x: auto; }
  .architecture-visual img { min-width: 960px; }
  .window img { min-width: 900px; }
  .outro { padding-bottom: 80px; }
}
</style>
