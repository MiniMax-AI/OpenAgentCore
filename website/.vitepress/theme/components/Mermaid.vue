<script setup lang="ts">
// Renders a Mermaid diagram in the browser. The source is shown until the
// diagram is ready, so the page works without JavaScript, and the diagram is
// redrawn when the colour theme changes.
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'

const props = defineProps<{ code: string }>()

const source = computed(() => decodeURIComponent(props.code))
const svg = ref('')
const failed = ref(false)
let themeObserver: MutationObserver | undefined
let renderCount = 0

async function render() {
  const { default: mermaid } = await import('mermaid')
  // Mermaid measures label text, so the web font must be ready first.
  await document.fonts?.ready
  const dark = document.documentElement.classList.contains('dark')
  mermaid.initialize({
    startOnLoad: false,
    securityLevel: 'strict',
    theme: dark ? 'dark' : 'neutral',
    fontFamily: "'Inter Variable', Inter, system-ui, sans-serif",
    themeVariables: dark
      ? { primaryColor: '#121817', primaryBorderColor: '#4ade80', lineColor: '#7f8f85', clusterBkg: '#0b100e', clusterBorder: '#2f7a4c', edgeLabelBackground: '#121817' }
      : { primaryColor: '#f2f3f0', primaryBorderColor: '#15803d', lineColor: '#5d6b62', clusterBkg: '#fafaf9', clusterBorder: '#86b89a', edgeLabelBackground: '#f2f3f0' },
  })
  const id = `oac-mermaid-${Math.random().toString(36).slice(2)}-${renderCount++}`
  try {
    svg.value = (await mermaid.render(id, source.value)).svg
    failed.value = false
  } catch (error) {
    console.error('Mermaid diagram failed to render', error)
    failed.value = true
    document.getElementById(`d${id}`)?.remove()
  }
}

onMounted(() => {
  render()
  themeObserver = new MutationObserver(() => render())
  themeObserver.observe(document.documentElement, { attributes: true, attributeFilter: ['class'] })
})

onBeforeUnmount(() => themeObserver?.disconnect())
</script>

<template>
  <div class="oac-mermaid" :class="{ failed }">
    <div v-if="svg && !failed" class="oac-mermaid-diagram" v-html="svg" />
    <pre v-else class="oac-mermaid-source"><code>{{ source }}</code></pre>
  </div>
</template>

<style scoped>
.oac-mermaid {
  margin: 16px 0;
  padding: 16px;
  overflow-x: auto;
  border: 1px solid var(--vp-c-divider);
  border-radius: 8px;
  background: var(--vp-c-bg-soft);
}
.oac-mermaid-diagram :deep(svg) {
  display: block;
  max-width: 100%;
  height: auto;
  margin: 0 auto;
}
/* Documentation paragraph styles must not reach Mermaid's HTML labels, whose
   size Mermaid measures before the page styles apply. */
.oac-mermaid-diagram :deep(foreignObject p),
.oac-mermaid-diagram :deep(foreignObject div),
.oac-mermaid-diagram :deep(foreignObject span) {
  margin: 0;
  line-height: 1.5;
}
.oac-mermaid-source {
  margin: 0;
  font: 13px/1.6 var(--vp-font-family-mono);
  white-space: pre;
  color: var(--vp-c-text-2);
}
</style>
