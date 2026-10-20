<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref } from 'vue'
import { copy, type Lang } from '../landing-content'

const props = defineProps<{ lang: Lang }>()
const t = computed(() => copy[props.lang].video)
const source = 'https://file.cdn.minimax.io/public/OpenAgentCore-launch.mp4'
const player = ref<HTMLVideoElement>()
const dialog = ref<HTMLDialogElement>()
// The video loads only after the first click.
const opened = ref(false)

async function open() {
  opened.value = true
  dialog.value?.showModal()
  await nextTick()
  void player.value?.play().catch(() => {})
}
function close() {
  player.value?.pause()
}
onBeforeUnmount(() => {
  player.value?.pause()
  dialog.value?.close()
})
</script>

<template>
  <div class="launch-video">
    <button class="watch" type="button" aria-haspopup="dialog" @click="open">
      <span class="play" aria-hidden="true">▶</span>{{ t.expand }}
    </button>
    <dialog ref="dialog" class="video-dialog" :aria-label="t.title" @close="close" @click="($event.target === dialog) && dialog?.close()">
      <div class="player-heading"><span>{{ t.title }}</span><button type="button" :aria-label="t.close" autofocus @click="dialog?.close()">✕</button></div>
      <video ref="player" :src="opened ? source : undefined" controls playsinline preload="none" :aria-label="t.title" />
    </dialog>
  </div>
</template>

<style scoped>
.watch { display: inline-flex; align-items: center; gap: 14px; padding: 6px 0; color: var(--l-text); font: 500 16px/1.4 var(--l-mono); cursor: pointer; }
.watch:hover { color: var(--l-accent); }
.play { display: grid; place-items: center; width: 40px; height: 40px; border: 1px solid var(--l-line-strong); border-radius: 50%; color: var(--l-accent); font-size: 14px; padding-left: 2px; }
.watch:hover .play { border-color: var(--l-accent); }
button:focus-visible { outline: 2px solid var(--l-accent); outline-offset: 4px; }
.video-dialog { position: fixed; inset: 0; width: min(1040px, calc(100vw - 32px)); max-width: none; max-height: calc(100dvh - 32px); margin: auto; padding: 0; border: 1px solid var(--l-line-strong); border-radius: 6px; background: var(--l-code-bg); color: var(--l-code-text); box-shadow: 0 24px 100px #0008; }
.video-dialog::backdrop { background: #050907d9; backdrop-filter: blur(8px); }
.player-heading { display: flex; align-items: center; justify-content: space-between; gap: 16px; padding: 10px 16px; border-bottom: 1px solid #ffffff20; font: 14px var(--l-mono); }
.player-heading button { width: 32px; height: 32px; cursor: pointer; }
.video-dialog video { display: block; width: 100%; max-height: calc(100dvh - 90px); background: #000; }
</style>

<style>
html:has(.video-dialog[open]) { overflow: hidden; }
</style>
