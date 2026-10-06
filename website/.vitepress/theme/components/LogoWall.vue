<script setup lang="ts">
import TitleAccent from './TitleAccent.vue'
import { computed, ref } from 'vue'
import { agentLogos, computeLogos } from '../ecosystem-logos'
import { copy, type Lang } from '../landing-content'

const props = defineProps<{ lang: Lang }>()
const t = computed(() => copy[props.lang].ecosystem)
const paused = ref(false)
const rows = computed(() => [
  { label: t.value.agents, logos: agentLogos },
  { label: t.value.compute, logos: computeLogos },
])
</script>

<template>
  <section class="logo-wall" :class="{ paused }" aria-labelledby="ecosystem-title">
    <header class="logo-wall-heading">
      <h2 id="ecosystem-title"><TitleAccent :title="t.title" :accent="t.titleAccent" /></h2>
      <button type="button" :aria-pressed="paused" @click="paused = !paused">
        <span aria-hidden="true">{{ paused ? '▶' : 'Ⅱ' }}</span>
        {{ paused ? t.resume : t.pause }}
      </button>
    </header>
    <div v-for="(row, index) in rows" :key="index" class="logo-row" :class="{ reverse: index === 1 }">
      <p class="logo-row-label">{{ row.label }}</p>
      <div class="logo-viewport" tabindex="0" role="region" :aria-label="row.label">
        <div class="logo-track">
          <ul v-for="run in 2" :key="run" class="logo-run" :aria-hidden="run === 2 ? true : undefined">
            <li v-for="logo in row.logos" :key="logo.name" class="logo-item">
              <img :src="logo.src" alt="" width="36" height="36" loading="lazy" decoding="async" />
              <span>{{ logo.name }}</span>
            </li>
          </ul>
        </div>
      </div>
    </div>
  </section>
</template>

<style scoped>
.logo-wall {
  position: relative;
  padding: 20px 0;
  background: transparent;
  color: #fff;
}
.logo-wall-heading {
  max-width: 1200px;
  margin: 0 auto 16px;
  padding: 0 28px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 20px;
}
.logo-wall-heading h2 {
  margin: 0;
  font: 500 clamp(20px, 2.5vw, 28px) / 1.3 var(--l-display);
  letter-spacing: -0.035em;
  color: #fff;
}
.logo-wall-heading button {
  flex-shrink: 0;
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 10px;
  border: 1px solid rgba(255, 255, 255, 0.28);
  border-radius: 3px;
  color: #fff;
  font: 11px var(--l-mono);
  cursor: pointer;
}
.logo-wall-heading button:hover { background: rgba(255, 255, 255, 0.08); }
.logo-wall-heading button:focus-visible,
.logo-viewport:focus-visible { outline: 2px solid #4ade80; outline-offset: -2px; }
.logo-row + .logo-row { margin-top: 18px; }
.logo-row-label {
  margin: 0 0 6px;
  text-align: center;
  color: #9aaba0;
  font: 10px / 1.5 var(--l-mono);
  letter-spacing: 0.15em;
  text-transform: uppercase;
}
.logo-viewport {
  overflow-x: auto;
  scrollbar-width: none;
  mask-image: linear-gradient(to right, transparent, #000 5%, #000 95%, transparent);
}
.logo-viewport::-webkit-scrollbar { display: none; }
.logo-track {
  display: flex;
  width: max-content;
  animation: logo-scroll 85s linear infinite;
}
.reverse .logo-track { animation-direction: reverse; animation-duration: 95s; }
.logo-run {
  display: flex;
  align-items: center;
  flex-shrink: 0;
  min-width: 100vw;
  justify-content: space-around;
  gap: 64px;
  margin: 0;
  padding: 18px 32px;
  list-style: none;
}
.logo-item {
  display: flex;
  align-items: center;
  flex-shrink: 0;
  gap: 12px;
  white-space: nowrap;
  font: 600 23px / 1.2 var(--l-display);
  letter-spacing: -0.04em;
}
.logo-item img {
  display: block;
  width: 36px;
  height: 36px;
  object-fit: contain;
  filter: brightness(0) invert(1);
}
.logo-wall.paused .logo-track,
.logo-wall:hover .logo-track,
.logo-viewport:focus-within .logo-track { animation-play-state: paused; }
@keyframes logo-scroll { to { transform: translateX(-50%); } }
@media (max-width: 640px) {
  .logo-wall-heading { padding: 0 20px; }
  .logo-run { gap: 40px; padding: 16px 20px; }
  .logo-item { font-size: 20px; gap: 10px; }
  .logo-item img { width: 30px; height: 30px; }
}
@media (prefers-reduced-motion: reduce) {
  .logo-track { animation: none; }
  .logo-run[aria-hidden='true'] { display: none; }
  .logo-wall-heading button { display: none; }
}
</style>
