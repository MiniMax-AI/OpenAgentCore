import { translationRewrites, sourceRoute } from './locales.mts'
import { createRequire } from 'node:module'
import { copyFileSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { defineConfig, type SiteConfig } from 'vitepress'
import { mermaidFences } from './mermaid.mts'
import { repositoryLinks } from './repository-links.mts'
import { siteBase } from './site-base.mts'
import { docsSidebar, legacyRedirects, pageTitle, readDocsJson, repoRoot } from './docs-nav.mts'

// Pages outside this package resolve Vue from here, not from the repository root.
const require = createRequire(import.meta.url)
const vueDir = dirname(require.resolve('vue/package.json'))

const repo = 'https://github.com/MiniMax-AI/OpenAgentCore'
const description = 'An open-source, self-hosted implementation of the OpenAI Agents API with multiple native harnesses.'

const base = siteBase(process.env.WEBSITE_URL)

// The canonical mark, inlined so the favicon has no second copy of the logo.
const logoSvg = readFileSync(resolve(repoRoot, 'docs/assets/openagentcore-logo.svg'), 'utf8')
const favicon = `data:image/svg+xml,${encodeURIComponent(logoSvg.replace('currentColor', '#4ADE80'))}`

const docsNav = [
  { text: 'Docs', link: '/docs/getting-started/', activeMatch: '^/docs/(getting-started|examples|configuration|web)' },
  { text: 'API', link: '/docs/api/public-agent-api', activeMatch: '^/(docs/api|contracts)/' },
  { text: 'Architecture', link: '/docs/architecture', activeMatch: '^/docs/(architecture|concepts|runtime|sandbox|development)' },
]

export default defineConfig({
  title: 'OpenAgentCore',
  description,
  base,
  cleanUrls: true,
  lastUpdated: true,
  appearance: 'dark',

  // The existing documentation is published in place: the repository root is
  // the content root, docs/ and contracts/ keep their paths, and the website's
  // own pages are mapped onto `/` and `/zh/`.
  srcDir: '..',
  srcExclude: [
    '*.md',
    '.github/**',
    'apps/**',
    'deploy/**',
    'example/**',
    'internal/**',
    'packages/**',
    'scripts/**',
    'services/**',
    'website/README.md',
    'website/node_modules/**',
    '**/node_modules/**',
  ],
  rewrites: {
    'website/index.md': 'index.md',
    'website/zh/index.md': 'zh/index.md',
    ...translationRewrites(),
    'contracts/agents-api/zh/harness-catalog.md': 'zh/contracts/agents-api/harness-catalog.md',
  },

  head: [
    ['link', { rel: 'icon', type: 'image/svg+xml', href: favicon }],
    ['meta', { name: 'theme-color', content: '#0a0d0c' }],
    ['meta', { property: 'og:title', content: 'OpenAgentCore — One core. Many agents.' }],
    ['meta', { property: 'og:description', content: description }],
    ['meta', { property: 'og:image', content: 'https://raw.githubusercontent.com/MiniMax-AI/OpenAgentCore/main/docs/assets/openagentcore-banner.jpeg' }],
    ['meta', { name: 'twitter:card', content: 'summary_large_image' }],
  ],

  locales: {
    root: {
      label: 'English',
      lang: 'en',
      themeConfig: { nav: docsNav },
    },
    zh: {
      label: '简体中文',
      lang: 'zh-CN',
      link: '/zh/',
      description: '开源、可自部署的 OpenAI Agents API 实现，支持多种原生 Harness。',
      themeConfig: {
        nav: docsNav.map((item, index) => ({
          ...item,
          text: ['文档', 'API', '架构'][index],
          link: `/zh${item.link}`,
          activeMatch: item.activeMatch.replace('^/', '^/zh/'),
        })),
        langMenuLabel: '切换语言',
        lastUpdatedText: '最后更新',
        footer: { message: '基于 MIT 许可证发布。', copyright: 'One core. Many agents.' },
        returnToTopLabel: '返回顶部',
        sidebarMenuLabel: '目录',
        darkModeSwitchLabel: '主题',
        lightModeSwitchTitle: '切换为浅色主题',
        darkModeSwitchTitle: '切换为深色主题',
        editLink: { pattern: `${repo}/edit/main/:path`, text: '在 GitHub 上编辑此页' },
        outline: { label: '本页目录' },
        docFooter: { prev: '上一页', next: '下一页' },
      },
    },
  },

  themeConfig: {
    i18nRouting: true,
    logo: { src: favicon, alt: '' },
    siteTitle: 'OpenAgentCore',
    socialLinks: [{ icon: 'github', link: repo }],
    sidebar: {
      '/docs/': docsSidebar(),
      '/contracts/': docsSidebar(),
      '/zh/docs/': docsSidebar('zh'),
      '/zh/contracts/': docsSidebar('zh'),
    },
    outline: { level: [2, 3] },
    search: { provider: 'local', options: { locales: { zh: { translations: { button: { buttonText: '搜索文档', buttonAriaLabel: '搜索文档' }, modal: { noResultsText: '没有找到相关内容', resetButtonTitle: '清除搜索', displayDetails: '显示详细内容', footer: { selectText: '选择', navigateText: '切换', closeText: '关闭' } } } } } } },
    editLink: {
      pattern: `${repo}/edit/main/:path`,
      text: 'Edit this page on GitHub',
    },
    footer: {
      message: 'Released under the MIT License.',
      copyright: 'One core. Many agents.',
    },
  },

  markdown: {
    theme: { light: 'github-light', dark: 'github-dark' },
    languageAlias: { caddyfile: 'nginx' },
    config: (md) => {
      repositoryLinks(md, { repoRoot, blobBase: `${repo}/blob/main`, treeBase: `${repo}/tree/main` })
      mermaidFences(md)
    },
  },

  vite: {
    // Repository Markdown may sit outside the website package.
    server: { fs: { allow: [repoRoot] } },
    // The local search index is one lazily loaded chunk of about 0.5 MB.
    build: { chunkSizeWarningLimit: 800 },
    resolve: {
      alias: [
        { find: /^vue\/server-renderer$/, replacement: require.resolve('vue/server-renderer') },
        { find: /^vue$/, replacement: resolve(vueDir, 'dist/vue.runtime.esm-bundler.js') },
      ],
    },
  },

  buildEnd: (site) => {
    writeMarkdownCopies(site)
    writeLegacyRedirects(site)
  },
})

/**
 * Serves each documentation page's source Markdown next to its HTML page (for
 * example `/docs/architecture.md`), keeping the `.md` URLs that the repository
 * uses, and lists them in `/llms.txt` for agents.
 */
function writeMarkdownCopies(site: SiteConfig) {
  const pages = site.pages.filter((page) => /^(docs|contracts)\//.test(page))
  for (const page of pages) {
    const target = resolve(site.outDir, sourceRoute(page))
    mkdirSync(dirname(target), { recursive: true })
    copyFileSync(resolve(site.srcDir, page), target)
  }

  const origin = site.site.base
  const lines = ['# OpenAgentCore', '', `> ${description}`, '']
  for (const group of readDocsJson().navigation.groups) {
    lines.push(`## ${group.group}`, '')
    for (const page of group.pages) lines.push(`- [${pageTitle(page)}](${origin}${page}.md)`)
    lines.push('')
  }
  writeFileSync(resolve(site.outDir, 'llms.txt'), lines.join('\n'))
}

/** Writes static redirects for the non-Markdown legacy paths declared in docs.json. */
function writeLegacyRedirects(site: SiteConfig) {
  for (const { from, to } of legacyRedirects()) {
    const target = resolve(site.outDir, `${from}.html`)
    const url = `${site.site.base}${to.replace(/^\//, '')}`
    mkdirSync(dirname(target), { recursive: true })
    writeFileSync(
      target,
      `<!doctype html><meta charset="utf-8"><title>Redirecting…</title><link rel="canonical" href="${url}"><meta http-equiv="refresh" content="0; url=${url}"><a href="${url}">${url}</a>\n`,
    )
  }
}
