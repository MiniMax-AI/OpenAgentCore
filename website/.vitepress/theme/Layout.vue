<script setup lang="ts">
import DefaultTheme from 'vitepress/theme'
import { useData } from 'vitepress'

const { frontmatter } = useData()
</script>

<template>
  <DefaultTheme.Layout>
    <template #doc-before>
      <!-- Documentation pages carry their title in frontmatter, not in an H1. -->
      <h1 v-if="frontmatter.title" class="oac-doc-title">{{ frontmatter.title }}</h1>
    </template>
  </DefaultTheme.Layout>
</template>
