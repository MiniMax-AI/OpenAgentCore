import DefaultTheme from 'vitepress/theme'
import type { Theme } from 'vitepress'
import '@fontsource-variable/inter'
import '@fontsource-variable/space-grotesk'
import '@fontsource-variable/geist-mono'
import Layout from './Layout.vue'
import Landing from './components/Landing.vue'
import Mermaid from './components/Mermaid.vue'
import './style.css'

export default {
  extends: DefaultTheme,
  Layout,
  enhanceApp({ app }) {
    app.component('Landing', Landing)
    app.component('Mermaid', Mermaid)
  },
} satisfies Theme
