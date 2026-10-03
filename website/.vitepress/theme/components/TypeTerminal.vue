<script setup lang="ts">
// A terminal that types its lines once it scrolls into view.
import { onBeforeUnmount, onMounted, ref } from 'vue'

export interface TermLine {
  kind: 'cmd' | 'comment' | 'out'
  text: string
}

const props = defineProps<{ lines: TermLine[]; title: string }>()

const root = ref<HTMLElement>()
const shown = ref<string[]>(props.lines.map(() => ''))
const active = ref(-1)
let timer: ReturnType<typeof setTimeout> | undefined
let observer: IntersectionObserver | undefined

function finish() {
  shown.value = props.lines.map((line) => line.text)
  active.value = props.lines.length - 1
}

function run() {
  let line = 0
  let char = 0
  const step = () => {
    if (line >= props.lines.length) return
    active.value = line
    const target = props.lines[line]
    if (target.kind !== 'cmd') {
      shown.value[line] = target.text
      line++
      timer = setTimeout(step, 140)
      return
    }
    char = Math.min(target.text.length, char + 2)
    shown.value[line] = target.text.slice(0, char)
    if (char >= target.text.length) {
      line++
      char = 0
      timer = setTimeout(step, 420)
    } else {
      timer = setTimeout(step, 18 + Math.random() * 30)
    }
  }
  step()
}

onMounted(() => {
  if (window.matchMedia('(prefers-reduced-motion: reduce)').matches) return finish()
  observer = new IntersectionObserver(
    ([entry]) => {
      if (!entry.isIntersecting) return
      observer?.disconnect()
      run()
    },
    { threshold: 0.4 },
  )
  observer.observe(root.value!)
})

onBeforeUnmount(() => {
  clearTimeout(timer)
  observer?.disconnect()
})
</script>

<template>
  <div ref="root" class="term">
    <div class="term-bar">
      <span class="dots"><i /><i /><i /></span>
      <span>{{ title }}</span>
    </div>
    <pre class="term-body" :aria-label="lines.map((l) => l.text).join('\n')"><template v-for="(line, i) in lines" :key="i"><span v-if="shown[i] || active === i" :class="line.kind"><span v-if="line.kind === 'cmd'" class="prompt">❯ </span>{{ shown[i] }}<span v-if="active === i" class="caret" aria-hidden="true">█</span>
</span></template></pre>
  </div>
</template>

<style scoped>
.term {
  border: 1px solid var(--l-line);
  border-radius: 12px;
  overflow: hidden;
  background: var(--l-code-bg);
  box-shadow: 0 30px 80px -40px var(--l-glow);
}
.term-bar {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 10px 14px;
  border-bottom: 1px solid rgba(232, 241, 235, 0.08);
  font: 12px var(--l-mono);
  color: #7f8f85;
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
.term-body {
  margin: 0;
  min-height: 15.5em;
  padding: 16px 18px;
  overflow-x: auto;
  font: 13px/1.75 var(--l-mono);
  color: var(--l-code-text);
  white-space: pre-wrap;
  word-break: break-all;
}
.prompt {
  color: #4ade80;
}
.comment {
  color: #5f7166;
}
.out {
  color: #b7c5bc;
}
.caret {
  color: #4ade80;
  animation: blink 1s steps(1) infinite;
}
@keyframes blink {
  50% {
    opacity: 0;
  }
}
</style>
