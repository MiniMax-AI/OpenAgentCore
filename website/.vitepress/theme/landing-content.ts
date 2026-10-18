// Landing page copy. Every product claim here is backed by a page in docs/ or
// contracts/; link labels equal the target page titles.

export type Lang = 'en' | 'zh'

export interface FlowCopy {
  label: string
  stages: string[]
  captions: string[]
  app: string
  api: string
  state: string
  orchestration: string
  events: string
  workspace: string
  loop: string
  runtime: string
  model: string
  provider: string
  compute: string
  next: string
  back: string
  play: string
  pause: string
  step: string
}

export interface LandingCopy {
  hero: { title: [string, string]; lede: string; primary: string; secondary: string; copied: string; copy: string; installTabs: [string, string] }
  video: { title: string; expand: string; close: string }
  compose: {
    harness: string
    protocol: string
    environment: string
    accepted: string
    rejected: (harness: string, protocols: string) => string
    envs: Record<'openai_hosted' | 'self_hosted', string>
    input: string
    footnote: string
    footnoteLink: string
  }
  architecture: { title: string; titleAccent: string; alt: string; more: string }
  session: { title: string; titleAccent: string; more: string; flow: FlowCopy }
  observe: { title: string; titleAccent: string; tabs: { id: 'overview' | 'metrics'; label: string; alt: string }[] }
  docs: { title: string; links: { label: string; path: string }[] }
  ecosystem: { title: string; titleAccent: string; pause: string; resume: string }
  start: { title: string; titleAccent: string; steps: { label: string; path: string }[] }
}

const en: LandingCopy = {
  hero: {
    title: ['One core.', 'Many agents.'],
    lede: 'The OpenAI Agents API, open source and self-hosted. Run Codex, Claude Code or MiniMax Code on your own machines with your own models, through the official OpenAI SDK.',
    primary: 'Get started',
    secondary: 'Read the docs',
    copied: 'copied',
    copy: 'copy',
    installTabs: ['macOS / Linux', 'Windows'],
  },
  video: { title: 'Meet OpenAgentCore', expand: 'Watch the launch film', close: 'Close video' },
  compose: {
    harness: 'Harness',
    protocol: 'Model protocol',
    environment: 'Environment',
    accepted: 'accepted · Session created',
    rejected: (harness, protocols) => `rejected before the Session is created · ${harness} speaks only ${protocols}`,
    envs: { openai_hosted: 'managed sandbox', self_hosted: 'your machine' },
    input: 'Fix the failing test and explain the change.',
    footnote: 'Each Harness declares the protocols it speaks; Core rejects other combinations before it creates the Session.',
    footnoteLink: 'Harness capabilities',
  },
  architecture: {
    title: 'One API. Replaceable parts.', titleAccent: 'Replaceable parts.',
    alt: 'Applications call Core through the Agents API; native Harnesses run inside sandboxes and call models directly. Separate adapters manage compute and persistent workspace storage.',
    more: 'Architecture',
  },
  session: {
    title: 'One Session, from request to result.', titleAccent: 'from request to result.',
    more: 'A Session, end to end',
    flow: {
      label: 'Inside a Session', stages: ['Native agent', 'Managed execution', 'Many combinations'],
      captions: ['Runtime prepares the environment. The native Harness owns the model and tool loop.', 'Your app creates a Session through the Agents API. Core manages execution and durable state.', 'Sessions use different Harnesses and models. Managed Sessions here share one microsandbox deployment; independent workspaces outlive compute.'],
      app: 'Your application', api: 'OpenAI SDK · Agents API', state: 'Sessions & Turns', orchestration: 'Schedule · cancel · interact', events: 'Events · results · durable state', workspace: 'Mounted workspace · shell · tools', loop: 'Native agent loop', runtime: 'Prepare · execute · report', model: 'Model provider', provider: 'Sandbox Provider', compute: 'Provision & reclaim compute', next: 'Next', back: 'Back', play: 'Play', pause: 'Pause', step: 'Architecture stage',
    },
  },
  observe: {
    title: 'Run it from one console.', titleAccent: 'one console.',
    tabs: [
      { id: 'overview', label: 'Overview', alt: 'Web console overview: service status, running Sessions, sandbox capacity, fleet and Projects.' },
      { id: 'metrics', label: 'Agent metrics', alt: 'Web console agent metrics: requests, errors, latency, tokens and tool calls.' },
    ],
  },
  docs: { title: 'Documentation', links: [
    { label: 'Run your first Session', path: '/docs/getting-started/quickstart' },
    { label: 'Agents API guide', path: '/docs/api/public-agent-api' },
    { label: 'Install Core and Web', path: '/docs/getting-started/install' },
    { label: 'Develop OpenAgentCore', path: '/docs/development' },
  ] },
  ecosystem: { title: 'An open ecosystem.', titleAccent: 'open ecosystem.', pause: 'Pause', resume: 'Resume' },
  start: {
    title: 'From install to first Session.', titleAccent: 'first Session.',
    steps: [
      { label: 'Install Core and Web', path: '/docs/getting-started/install' },
      { label: 'Add and manage nodes', path: '/docs/getting-started/nodes' },
      { label: 'Run your first Session', path: '/docs/getting-started/quickstart' },
    ],
  },
}

const zh: LandingCopy = {
  hero: {
    title: ['One core.', 'Many agents.'],
    lede: '开源、可自部署的 OpenAI Agents API。用官方 OpenAI SDK，在你自己的机器上用你自己的模型运行 Codex、Claude Code 或 MiniMax Code。',
    primary: '开始使用',
    secondary: '阅读文档',
    copied: '已复制',
    copy: '复制',
    installTabs: ['macOS / Linux', 'Windows'],
  },
  video: { title: '认识 OpenAgentCore', expand: '观看发布视频', close: '关闭视频' },
  compose: {
    harness: 'Harness',
    protocol: '模型协议',
    environment: '执行环境',
    accepted: '通过校验 · Session 已创建',
    rejected: (harness, protocols) => `创建前即被拒绝 · ${harness} 只支持 ${protocols}`,
    envs: { openai_hosted: '托管沙箱', self_hosted: '你自己的机器' },
    input: '修复失败的测试，并说明改动。',
    footnote: '每个 Harness 声明自己支持的协议；其他组合在创建 Session 前即被拒绝。',
    footnoteLink: 'Harness 能力',
  },
  architecture: {
    title: '一个 API，部件自由替换。', titleAccent: '部件自由替换。',
    alt: '应用通过 Agents API 接入 Core；原生 Harness 在沙箱内运行并调用模型，计算与持久工作区存储由独立 adapter 管理。',
    more: '架构',
  },
  session: {
    title: '一个 Session，从请求到结果。', titleAccent: '从请求到结果。',
    more: '一个 Session 的完整流程',
    flow: {
      label: 'Session 内部', stages: ['原生 Agent', '统一执行', '自由组合'],
      captions: ['Runtime 准备环境，原生 Harness 运行模型与工具循环。', '应用通过 Agents API 创建 Session，Core 管理执行与持久状态。', '不同 Session 使用不同 Harness 与模型；此示例中的托管 Session 共用 microsandbox 部署，独立工作区可跨计算生命周期保留。'],
      app: '你的应用', api: 'OpenAI SDK · Agents API', state: 'Session 与 Turn', orchestration: '调度 · 取消 · 交互', events: '事件 · 结果 · 持久状态', workspace: '挂载工作区 · Shell · 工具', loop: '原生 Agent 循环', runtime: '环境准备 · 执行管理', model: '模型提供商', provider: '沙箱提供商', compute: '创建与回收计算资源', next: '下一步', back: '上一步', play: '播放', pause: '暂停', step: '演示阶段',
    },
  },
  observe: {
    title: '一个控制台管理全部运行。', titleAccent: '一个控制台',
    tabs: [
      { id: 'overview', label: '概览', alt: 'Web 控制台概览：服务状态、运行中的 Session、沙箱容量、节点与项目。' },
      { id: 'metrics', label: 'Agent 监控', alt: 'Web 控制台 Agent 监控：请求、错误、耗时、Token 与工具调用。' },
    ],
  },
  docs: { title: '文档', links: [
    { label: '运行第一个 Session', path: '/docs/getting-started/quickstart' },
    { label: 'Agents API 指南', path: '/docs/api/public-agent-api' },
    { label: '安装 Core 和 Web', path: '/docs/getting-started/install' },
    { label: '开发 OpenAgentCore', path: '/docs/development' },
  ] },
  ecosystem: { title: '开放的生态。', titleAccent: '开放的生态。', pause: '暂停', resume: '继续' },
  start: {
    title: '从安装到第一个 Session。', titleAccent: '第一个 Session。',
    steps: [
      { label: '安装 Core 和 Web', path: '/docs/getting-started/install' },
      { label: '添加和管理节点', path: '/docs/getting-started/nodes' },
      { label: '运行第一个 Session', path: '/docs/getting-started/quickstart' },
    ],
  },
}

export const copy: Record<Lang, LandingCopy> = { en, zh }

/** Native protocols per harness, default first (contracts/agents-api/model-execution.md). */
export const harnesses = [
  { id: 'codex', label: 'Codex', protocols: ['responses'] },
  { id: 'claude_sdk', label: 'Claude Code', protocols: ['anthropic'] },
  { id: 'mcode', label: 'MiniMax Code', protocols: ['anthropic', 'responses', 'chat_completions'] },
] as const

export const protocols = ['responses', 'anthropic', 'chat_completions'] as const

export const installCommands = {
  unix: 'curl -fsSL https://openagentcore.dev/install.sh | bash',
  windows: 'irm https://github.com/MiniMax-AI/OpenAgentCore/releases/latest/download/install.ps1 | iex',
} as const

export const repoUrl = 'https://github.com/MiniMax-AI/OpenAgentCore'
