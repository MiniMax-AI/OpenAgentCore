<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { copy, type Lang } from '../landing-content'

const props = defineProps<{ lang: Lang }>()
const t = computed(() => copy[props.lang].video)
const source = 'https://file.cdn.minimax.io/public/OpenAgentCore-launch.mp4'
const preview = ref<HTMLVideoElement>()
const player = ref<HTMLVideoElement>()
const dialog = ref<HTMLDialogElement>()
const paused = ref(true)
let resumePreview = false

function play(video?: HTMLVideoElement) {
  void video?.play().catch(() => {})
}
function expand() {
  if (!preview.value || !player.value || !dialog.value) return
  resumePreview = !preview.value.paused
  preview.value.pause()
  dialog.value.showModal()
  player.value.currentTime = preview.value.currentTime
  play(player.value)
}
function close() {
  player.value?.pause()
  if (preview.value && player.value) preview.value.currentTime = player.value.currentTime
  if (resumePreview) play(preview.value)
}
onMounted(() => {
  if (!window.matchMedia('(prefers-reduced-motion: reduce)').matches) play(preview.value)
})
onBeforeUnmount(() => {
  preview.value?.pause()
  player.value?.pause()
  dialog.value?.close()
})
</script>

<template>
  <div class="launch-video">
    <div class="preview-frame">
      <button class="preview-open" type="button" :aria-label="t.expand" aria-haspopup="dialog" @click="expand">
        <video ref="preview" :src="source" muted loop playsinline preload="metadata" aria-hidden="true" @play="paused = false" @pause="paused = true" />
        <span class="expand-icon" aria-hidden="true">↗</span>
      </button>
      <span class="video-corner top-left" aria-hidden="true" /><span class="video-corner top-right" aria-hidden="true" /><span class="video-corner bottom-left" aria-hidden="true" /><span class="video-corner bottom-right" aria-hidden="true" />
      <button class="preview-pause" type="button" :aria-label="paused ? t.play : t.pause" @click="paused ? play(preview) : preview?.pause()">{{ paused ? '▶' : 'Ⅱ' }}</button>
    </div>
    <button class="video-caption" type="button" @click="expand">{{ t.title }} <span aria-hidden="true">↗</span></button>
    <dialog ref="dialog" class="video-dialog" :aria-label="t.title" @close="close" @click="($event.target === dialog) && dialog?.close()">
      <div class="player-heading"><span>{{ t.title }}</span><button type="button" :aria-label="t.close" autofocus @click="dialog?.close()">✕</button></div>
      <video ref="player" :src="source" controls playsinline preload="none" :aria-label="t.title" />
    </dialog>
  </div>
</template>

<style scoped>
.launch-video { width: 100%; }
.preview-frame { position: relative; padding: 6px; background: transparent; }
.video-corner { position: absolute; width: 18px; height: 18px; border-style: solid; border-color: var(--l-accent); opacity: 0.65; pointer-events: none; }
.top-left { top: 0; left: 0; border-width: 1px 0 0 1px; }
.top-right { top: 0; right: 0; border-width: 1px 1px 0 0; }
.bottom-left { bottom: 0; left: 0; border-width: 0 0 1px 1px; }
.bottom-right { bottom: 0; right: 0; border-width: 0 1px 1px 0; }
.preview-open { display: block; width: 100%; cursor: pointer; }
.preview-open video { display: block; width: 100%; aspect-ratio: 16 / 9; object-fit: contain; }
.expand-icon, .preview-pause { position: absolute; bottom: 10px; display: grid; place-items: center; width: 30px; height: 30px; border: 1px solid #ffffff50; border-radius: 3px; color: #fff; background: #0b100ecc; }
.expand-icon { right: 10px; }
.preview-pause { left: 10px; cursor: pointer; }
.video-caption { display: flex; align-items: center; justify-content: space-between; width: 100%; padding: 12px 6px; border-bottom: 1px solid var(--l-line); color: var(--l-text-2); font: 12px var(--l-mono); cursor: pointer; }
button:focus-visible { outline: 2px solid var(--l-accent); outline-offset: -3px; }
.preview-frame:hover .video-corner { opacity: 1; }
.video-dialog { position: fixed; inset: 0; width: min(1040px, calc(100vw - 32px)); max-width: none; max-height: calc(100dvh - 32px); margin: auto; padding: 0; border: 1px solid var(--l-line-strong); border-radius: 6px; background: var(--l-code-bg); color: var(--l-code-text); box-shadow: 0 24px 100px #0008; }
.video-dialog::backdrop { background: #050907d9; backdrop-filter: blur(8px); }
.player-heading { display: flex; align-items: center; justify-content: space-between; gap: 16px; padding: 10px 16px; border-bottom: 1px solid #ffffff20; font: 13px var(--l-mono); }
.player-heading button { width: 32px; height: 32px; cursor: pointer; }
.video-dialog video { display: block; width: 100%; max-height: calc(100dvh - 90px); background: #000; }
</style>

<style>
html:has(.video-dialog[open]) { overflow: hidden; }
</style>
