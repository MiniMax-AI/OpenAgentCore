<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import type { Lang } from '../landing-content'
import codex from '../assets/logos/codex.svg'
import claude from '../assets/logos/claudecode.svg'
import minimax from '../assets/logos/minimax.svg'

const props = defineProps<{ lang: Lang }>()
const copy = computed(() => props.lang === 'zh' ? {
  stages: ['原生 Agent', '统一执行', '自由组合'],
  captions: ['Runtime 准备环境，原生 Harness 运行模型与工具循环。', '应用通过 Agents API 创建 Session，Core 管理执行与持久状态。', '不同 Session 选择不同的 Harness、模型与计算环境，共用一个 Core。'],
  app: '你的应用', api: 'OpenAI SDK · Agents API', state: 'Session 与 Turn', orchestration: '调度 · 取消 · 交互', events: '事件 · 结果 · 持久状态', workspace: '工作目录 · Shell · 工具', loop: '原生 Agent 循环', runtime: '环境准备 · 执行管理', model: '模型服务', provider: 'Sandbox Provider', compute: '创建与回收计算资源', next: '下一步', back: '上一步', play: '播放', pause: '暂停', label: 'Session 架构演示', step: '演示阶段',
} : {
  stages: ['Native agent', 'Managed execution', 'Many combinations'],
  captions: ['Runtime prepares the environment. The native harness owns the model and tool loop.', 'Your app creates a Session through the Agents API. Core manages execution and durable state.', 'Each Session chooses its harness, model and compute. They share one Core.'],
  app: 'Your application', api: 'OpenAI SDK · Agents API', state: 'Sessions & Turns', orchestration: 'Schedule · cancel · interact', events: 'Events · results · durable state', workspace: 'Workspace · shell · tools', loop: 'Native agent loop', runtime: 'Prepare · execute · report', model: 'Model provider', provider: 'Sandbox Provider', compute: 'Provision & reclaim compute', next: 'Next', back: 'Back', play: 'Play', pause: 'Pause', label: 'Inside a Session', step: 'Architecture stage',
})
const step = ref(0)
const playing = ref(true)
const visible = ref(false)
const root = ref<HTMLElement>()
let timer: ReturnType<typeof setInterval> | undefined
let observer: IntersectionObserver | undefined
let media: MediaQueryList | undefined
function syncTimer() {
  clearInterval(timer)
  if (playing.value && visible.value && !document.hidden) timer = setInterval(() => { step.value = (step.value + 1) % 3 }, 5200)
}
function select(value: number) { step.value = Math.max(0, Math.min(2, value)); playing.value = false; syncTimer() }
function toggle() { playing.value = !playing.value; syncTimer() }
function onMotion() { if (media?.matches) { playing.value = false; syncTimer() } }
onMounted(() => {
  media = matchMedia('(prefers-reduced-motion: reduce)')
  onMotion()
  media.addEventListener('change', onMotion)
  observer = new IntersectionObserver(([entry]) => { visible.value = entry.isIntersecting; syncTimer() }, { threshold: .2 })
  observer.observe(root.value!)
  document.addEventListener('visibilitychange', syncTimer)
})
onBeforeUnmount(() => { clearInterval(timer); observer?.disconnect(); media?.removeEventListener('change', onMotion); document.removeEventListener('visibilitychange', syncTimer) })
</script>

<template>
  <figure ref="root" class="session-sketch" :data-stage="step" :class="{ paused: !playing || !visible }" :aria-label="copy.label">
    <div class="sketch-top"><strong>{{ copy.label }}</strong><span>0{{ step + 1 }} · {{ copy.stages[step] }}</span></div>
    <div class="sketch-scroll" tabindex="0" :aria-label="copy.label">
      <div class="sketch-board">
        <svg class="wires" viewBox="0 0 1100 530" aria-hidden="true">
          <defs><marker id="session-arrow" viewBox="0 0 10 10" refX="8" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse"><path d="M1 1 8 5 1 9" /></marker></defs>
          <g class="core-wires"><path d="M205 228 Q238 222 266 228" marker-end="url(#session-arrow)" /><path d="M266 265 Q238 271 205 265" marker-end="url(#session-arrow)" /><text x="219" y="210">/v1</text>
            <path class="request" d="M586 218 Q640 212 704 218" marker-end="url(#session-arrow)" />
            <path d="M704 256 Q640 261 586 256" marker-end="url(#session-arrow)" /><text x="610" y="194">Runtime wire</text>
            <path class="provision" d="M480 437 Q637 462 704 322" marker-end="url(#session-arrow)" />
          </g>
          <g class="fleet-wires"><path d="M586 218 Q644 185 704 128" marker-end="url(#session-arrow)" /><path d="M586 252 Q647 265 704 282" marker-end="url(#session-arrow)" /><path d="M586 286 Q649 358 704 436" marker-end="url(#session-arrow)" /></g>
          <path v-if="step === 0" class="request" d="M660 225 Q703 219 745 225" marker-end="url(#session-arrow)" />
          <path v-else-if="step === 1" class="request" d="M945 215 Q974 202 996 157" marker-end="url(#session-arrow)" />
          <g v-else class="request"><path d="M945 126h40" marker-end="url(#session-arrow)" /><path d="M945 280h40" marker-end="url(#session-arrow)" /><path d="M945 434h40" marker-end="url(#session-arrow)" /></g>
        </svg>

        <article class="application hand-box"><small>application</small><strong>{{ copy.app }}</strong><span>{{ copy.api }}</span><div class="app-lines"><i /><i /><i /></div></article>
        <article class="core hand-box">
          <small>control plane</small><h3>OpenAgentCore</h3>
          <div class="hand-box core-session"><strong>{{ copy.state }}</strong><span>session_01 <i class="ink-dot" /></span></div>
          <div class="core-facts"><p>{{ copy.orchestration }}</p><p>{{ copy.events }}</p></div>
        </article>
        <article class="provider hand-box"><strong>{{ copy.provider }}</strong><small>{{ copy.compute }}</small></article>

        <article class="environment primary hand-box">
          <header><strong>Environment</strong><small>Session A</small></header>
          <div class="runtime"><b>Runtime</b><small>{{ copy.runtime }}</small></div>
          <div class="harness hand-box"><strong><img :src="codex" alt="" />Codex</strong><div class="agent-loop"><small>{{ copy.loop }}</small><div class="cycle"><span>context</span><span>model</span><span>result</span><span>tools</span><svg viewBox="0 0 200 90" aria-hidden="true"><path d="M57 13 Q100 -2 147 13M172 26Q190 45 172 68M147 80Q100 96 57 80M30 68Q13 45 30 26" /></svg></div></div></div>
          <small class="workspace">{{ copy.workspace }}</small><span class="compute-tag">Docker</span>
        </article>
        <article v-for="(item, i) in [{name:'Claude Code', icon:claude, host:'E2B'}, {name:'MiniMax Code', icon:minimax, host:'Your machine'}]" :key="item.name" class="environment secondary hand-box" :class="'env-' + i">
          <header><strong>Environment</strong><small>Session {{ i === 0 ? 'B' : 'C' }}</small></header><div class="runtime"><b>Runtime</b></div><div class="harness hand-box"><strong><img :src="item.icon" alt="" />{{ item.name }}</strong></div><span class="compute-tag">{{ item.host }}</span>
        </article>
        <article class="model hand-box"><strong>{{ copy.model }}</strong><small>Responses API</small></article>
        <article class="model model-b hand-box"><strong>{{ copy.model }}</strong><small>Messages API</small></article>
        <article class="model model-c hand-box"><strong>{{ copy.model }}</strong><small>Configured API</small></article>
      </div>
    </div>
    <figcaption>{{ copy.captions[step] }}</figcaption>
    <div class="sketch-controls">
      <button type="button" :disabled="step === 0" @click="select(step - 1)">← {{ copy.back }}</button>
      <div class="stage-picker"><input type="range" min="0" max="2" step="1" :value="step" :aria-label="copy.step" @input="select(Number(($event.target as HTMLInputElement).value))" /><div><span v-for="label in copy.stages" :key="label">{{ label }}</span></div></div>
      <button type="button" :disabled="step === 2" @click="select(step + 1)">{{ copy.next }} →</button>
      <button type="button" :aria-pressed="playing" @click="toggle">{{ playing ? 'Ⅱ ' + copy.pause : '▶ ' + copy.play }}</button>
    </div>
  </figure>
</template>

<style scoped>
.session-sketch { margin: 28px 0 24px; color: var(--l-text); font-family: 'OAC Hand', 'Kaiti SC', KaiTi, cursive; line-height: 1.35; }
.sketch-top { display: flex; justify-content: space-between; gap: 20px; padding: 0 20px 12px; font-size: 20px; }
.sketch-top > span { color: var(--l-muted); font-size: 16px; }
.sketch-scroll { overflow-x: auto; }
.sketch-board { position: relative; width: 1100px; height: 530px; margin: auto; background-image: linear-gradient(var(--l-grid) 1px,transparent 1px),linear-gradient(90deg,var(--l-grid) 1px,transparent 1px); background-size: 32px 32px; }
.hand-box { position: relative; border: 1.3px solid var(--l-text-2); border-radius: 2px 5px 3px 7px; background: var(--l-bg); }
.hand-box::after { content: ''; pointer-events: none; position: absolute; inset: 2px -3px -3px 2px; border: .7px solid var(--l-muted); opacity: .4; border-radius: 5px 2px 6px 3px; transform: rotate(-.35deg); }
.sketch-board > article { position: absolute; box-sizing: border-box; transition: left .85s cubic-bezier(.22,1,.36,1), top .85s cubic-bezier(.22,1,.36,1), width .85s, height .85s, opacity .5s, transform .85s; }
small { font-size: 13px; color: var(--l-muted); }
strong, b { font-weight: 400; }
.application { left: 20px; top: 170px; width: 185px; padding: 20px; opacity: 0; transform: translateX(-20px); display: grid; gap: 10px; }
.application strong { font-size: 21px; }
.application > span { font-size: 14px; color: var(--l-muted); }
.app-lines { display: grid; gap: 6px; }
.app-lines i { height: 1px; width: 85%; background: var(--l-line-strong); }
.app-lines i:nth-child(2) { width: 60%; }
.core { left: 266px; top: 108px; width: 320px; height: 263px; padding: 20px; opacity: 0; transform: translateX(-20px); }
.core h3 { font: 400 24px 'OAC Hand', cursive; margin: 2px 0 16px; color: var(--l-accent); }
.core-session { padding: 14px; display: flex; align-items: center; justify-content: space-between; gap: 10px; font-size: 16px; }
.core-session > strong { white-space: nowrap; }
.core-session > span { white-space: nowrap; font-size: 13px; color: var(--l-muted); }
.ink-dot { display: inline-block; height: 5px; width: 5px; border-radius: 50%; background: var(--l-accent); }
.core-facts p { margin: 14px 0 0; font-size: 16px; color: var(--l-text-2); }
.provider { left: 360px; top: 407px; width: 235px; padding: 10px 16px; display: grid; font-size: 17px; opacity: 0; }
.environment { left: 350px; top: 74px; width: 310px; height: 374px; padding: 18px; }
.environment header { display: flex; gap: 8px; white-space: nowrap; justify-content: space-between; align-items: baseline; }
.environment header > strong { font-size: 19px; }
.runtime { display: flex; align-items: center; justify-content: space-between; gap: 8px; margin-top: 19px; font-size: 16px; }
.runtime small { font-size: 11px; }
.harness { padding: 12px; margin-top: 14px; }
.harness > strong { display: flex; align-items: center; gap: 9px; font-size: 19px; }
.harness img { width: 22px; height: 22px; filter: brightness(0) invert(1); }
.agent-loop { text-align: center; margin-top: 8px; }
.cycle { position: relative; height: 90px; width: 200px; margin: auto; font-size: 14px; }
.cycle span { position: absolute; z-index: 1; background: var(--l-bg); padding: 0 3px; }
.cycle span:nth-child(1) { left: 16px; top: 3px; }.cycle span:nth-child(2) { right: 16px; top: 3px; }.cycle span:nth-child(3) { left: 16px; bottom: 3px; }.cycle span:nth-child(4) { right: 16px; bottom: 3px; }
.cycle svg { width: 100%; height: 100%; fill: none; stroke: var(--l-accent); stroke-width: 1.3; stroke-dasharray: 5 4; animation: ink-flow 3s linear infinite; }
.workspace { display: block; text-align: center; margin-top: 15px; }
.compute-tag { position: absolute; bottom: -14px; left: 22px; background: var(--l-bg); padding: 0 10px; color: var(--l-accent); font-size: 16px; }
.model { left: 745px; top: 192px; width: 166px; padding: 16px; display: grid; gap: 6px; font-size: 19px; }
.model-b,.model-c,.secondary { opacity: 0; pointer-events: none; }
.secondary { width: 240px; height: 124px; left: 704px; transform: translateY(-25px); }
.env-0 { top: 228px; }.env-1 { top: 382px; }
.wires { position: absolute; inset: 0; width: 100%; height: 100%; overflow: visible; }
.wires path { fill: none; stroke: var(--l-text-2); stroke-width: 1.4; stroke-linecap: round; stroke-linejoin: round; }
.wires text { fill: var(--l-muted); font: 13px 'OAC Hand', cursive; }
.wires .request { stroke: var(--l-accent); stroke-dasharray: 6 5; animation: ink-flow 3s linear infinite; }
.wires .provision { stroke-dasharray: 3 6; }
.core-wires,.fleet-wires { opacity: 0; transition: opacity .3s .5s; }
[data-stage='1'] .application,[data-stage='2'] .application,[data-stage='1'] .core,[data-stage='2'] .core,[data-stage='1'] .provider,[data-stage='2'] .provider { opacity: 1; transform: none; }
[data-stage='1'] .core-wires { opacity: 1; }
[data-stage='1'] .primary { left: 704px; top: 130px; width: 240px; height: 305px; padding: 14px; }
[data-stage='1'] .primary .runtime { flex-direction: column; align-items: start; gap: 0; margin-top: 8px; }
[data-stage='1'] .primary .cycle { width: 180px; height: 70px; }
[data-stage='1'] .model { left: 975px; top: 72px; width: 120px; font-size: 16px; padding: 12px; }
[data-stage='2'] .primary { left: 704px; top: 74px; width: 240px; height: 124px; padding: 12px; }
[data-stage='2'] .secondary { opacity: 1; transform: none; padding: 12px; }
[data-stage='2'] .environment header > strong { font-size: 15px; }
[data-stage='2'] .environment header small { font-size: 11px; }
[data-stage='2'] .runtime { margin-top: 7px; font-size: 13px; }
[data-stage='2'] .primary .runtime small,[data-stage='2'] .agent-loop,[data-stage='2'] .workspace { display: none; }
[data-stage='2'] .harness { position: absolute; left: 83px; right: 10px; top: 38px; padding: 7px; margin: 0; }
[data-stage='2'] .harness strong { white-space: nowrap; font-size: 12px; gap: 6px; }
[data-stage='2'] .harness img { width: 17px; height: 17px; }
[data-stage='2'] .model { left: 985px; top: 98px; width: 108px; padding: 10px; font-size: 14px; }
[data-stage='2'] .model small { font-size: 11px; }
[data-stage='2'] .model-b { top: 252px; opacity: 1; }[data-stage='2'] .model-c { top: 406px; opacity: 1; }
[data-stage='2'] .fleet-wires { opacity: 1; }
[data-stage='2'] .core-wires { opacity: 1; }[data-stage='2'] .core-wires > .request,[data-stage='2'] .core-wires > path:nth-of-type(4),[data-stage='2'] .core-wires > text:last-of-type { opacity: 0; }
figcaption { min-height: 2.8em; max-width: 900px; margin: 8px auto 14px; padding: 0 16px; text-align: center; font-size: 18px; color: var(--l-text-2); }
.sketch-controls { display: flex; gap: 22px; align-items: center; justify-content: center; padding: 8px 12px; }
.sketch-controls button { border: 1px solid var(--l-line-strong); padding: 7px 14px; font: 16px 'OAC Hand', 'Kaiti SC', cursive; cursor: pointer; }.sketch-controls button:disabled { opacity: .3; cursor: default; }
.sketch-controls button:hover:enabled { border-color: var(--l-accent); color: var(--l-accent); }
.sketch-controls :focus-visible,.sketch-scroll:focus-visible { outline: 2px solid var(--l-accent); outline-offset: 3px; }
.stage-picker { width: 330px; }.stage-picker input { width: 100%; accent-color: var(--l-accent); }.stage-picker > div { display: flex; justify-content: space-between; font-size: 13px; color: var(--l-muted); }
@keyframes ink-flow { to { stroke-dashoffset: -54; } }
.paused .cycle svg,.paused .request { animation-play-state: paused; }
@media (max-width: 640px) { .sketch-top { padding: 0 4px 12px; font-size: 16px; }.sketch-top > span { font-size: 13px; }.sketch-controls { gap: 8px; flex-wrap: wrap; }.stage-picker { width: 100%; order: -1; }figcaption { font-size: 16px; } }
@media (prefers-reduced-motion: reduce) { article,.core-wires,.fleet-wires { transition: none; }.cycle svg,.wires .request { animation: none; } }
</style>
