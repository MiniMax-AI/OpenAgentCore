<script setup lang="ts">
import { computed } from 'vue'
const props = defineProps<{ title: string; accent: string }>()
const parts = computed(() => {
  const index = props.title.indexOf(props.accent)
  return index < 0 ? [props.title, '', ''] : [props.title.slice(0, index), props.accent, props.title.slice(index + props.accent.length)]
})
</script>

<template><span>{{ parts[0] }}<em>{{ parts[1] }}</em>{{ parts[2] }}</span></template>

<style scoped>
span { font-family: var(--l-display); }
em { color: var(--l-accent); font-family: inherit; font-style: italic; font-weight: inherit; }
</style>
