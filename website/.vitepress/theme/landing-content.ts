// Landing page copy. Every product claim here is backed by a page in docs/ or
// contracts/; each section links to the page that owns the detail.

export type Lang = 'en' | 'zh'

export interface LandingCopy {
  hero: {
    eyebrow: string
    title: [string, string]
    lede: string
    primary: string
    secondary: string
    installLabel: string
    copied: string
    copy: string
    hud: [string, string][]
  }
  ticker: string[]
  compose: {
    index: string
    kicker: string
    title: string
    titleAccent: string
    lede: string
    harness: string
    protocol: string
    environment: string
    accepted: string
    rejected: (harness: string, protocols: string) => string
    envs: Record<'openai_hosted' | 'self_hosted', { label: string; note: string }>
    input: string
    footnote: string
    footnoteLink: string
  }
  architecture: {
    index: string
    kicker: string
    title: string
    titleAccent: string
    lede: string
    tabs: { id: string; label: string; alt: string }[]
    more: string
  }
  session: {
    index: string
    kicker: string
    title: string
    titleAccent: string
    lede: string
    rules: { title: string; body: string; art: string }[]
    animation: { label: string; steps: string[]; messages: string[]; states: string[]; pause: string; play: string; replay: string; next: string; note: string }
    more: string
  }
  observe: {
    index: string
    kicker: string
    title: string
    titleAccent: string
    lede: string
    tabs: { id: 'overview' | 'metrics'; label: string; alt: string }[]
    caption: string
  }
  start: {
    index: string
    kicker: string
    title: string
    titleAccent: string
    lede: string
    steps: { title: string; body: string }[]
    cta: string
    trial: string
  }
  docs: { title: string; links: { label: string; path: string }[] }
  ecosystem: { title: string; titleAccent: string; agents: string; compute: string; pause: string; resume: string }
}

const en: LandingCopy = {
  hero: {
    eyebrow: 'open source · self-hosted · OpenAI Agents API',
    title: ['One core.', 'Many agents.'],
    lede: 'An open-source, self-hosted implementation of the OpenAI Agents API. Run Codex, Claude Code or MiniMax Code with your own models, on your own machines, through the official OpenAI SDK.',
    primary: 'Run your first Session',
    secondary: 'Read the docs',
    installLabel: 'Linux amd64 · Docker · Python 3.9+',
    copied: 'copied',
    copy: 'copy',
    hud: [
      ['api', '/v1 · OpenAI Agents API'],
      ['harness', 'codex | claude_sdk | mcode'],
      ['protocol', 'responses | anthropic | chat_completions'],
      ['sandbox', 'docker | microsandbox | e2b'],
      ['machine', 'linux | macos | windows'],
    ],
  },
  ticker: ['Codex', 'Claude Code', 'MiniMax Code', 'Responses API', 'Anthropic Messages', 'Chat Completions', 'Docker', 'microsandbox', 'E2B', 'Linux', 'macOS', 'Windows', 'PostgreSQL', 'OpenAI SDK'],
  compose: {
    index: '01',
    kicker: 'pick each part',
    title: 'Your agent. Your model. Your machine.', titleAccent: 'Your machine.',
    lede: 'Choose a combination. See the API call.',
    harness: 'harness',
    protocol: 'model protocol',
    environment: 'environment',
    accepted: 'accepted · Session created',
    rejected: (harness, protocols) => `rejected before the Session is created · ${harness} speaks only ${protocols}`,
    envs: {
      openai_hosted: { label: 'managed sandbox', note: 'Docker · microsandbox · E2B' },
      self_hosted: { label: 'your machine', note: 'Linux · macOS · Windows' },
    },
    input: 'Fix the failing test and explain the change.',
    footnote: 'The supported combinations are declared by each harness, not guessed.',
    footnoteLink: 'Harness capabilities',
  },
  architecture: {
    index: '02',
    kicker: 'protocols at every boundary',
    title: 'One API. Replaceable parts.', titleAccent: 'Replaceable parts.',
    lede: 'Core orchestrates. Native harnesses execute. Protocols connect them.',
    tabs: [
      { id: 'ecosystem', label: 'ecosystem', alt: 'Applications reach OpenAgentCore through the Agents API; harnesses, models and compute connect through their own boundaries.' },
      { id: 'protocols', label: 'components', alt: 'Agents API and Core API on top of Core; Sandbox Provider, Runtime, Harness and Model Provider connected by protocols.' },
      { id: 'surfaces', label: 'api namespaces', alt: 'Three namespaces with three credentials: Agents API for applications, Core API for operators, Machine API for nodes.' },
    ],
    more: 'Read the architecture',
  },
  session: {
    index: '03',
    kicker: 'execution you can manage',
    title: 'Session is all you need.', titleAccent: 'Session',
    lede: 'Submit work, follow its progress, and pick up the conversation.',
    rules: [
      { title: 'Connection ≠ work', body: 'Submitted work continues after the stream closes.', art: 'client ──────╳  disconnect\nturn   ━━━━━━━━━━━━━━▶ completed' },
      { title: 'Retries have identity', body: 'Stable keys prevent duplicates on supported submission paths.', art: 'POST  Idempotency-Key: a1f ──▶ turn_01\nPOST  Idempotency-Key: a1f ──▶ turn_01' },
      { title: 'Cancel has a receipt', body: 'Core confirms the target Turn\'s execution and cleanup.', art: 'cancel ──▶ turn  in_progress\n       ──▶ turn  cancelled  ✓ confirmed' },
      { title: 'Execution ≠ machine', body: 'Track Turns, executors and Environments separately.', art: 'turn        ━━━━┫\nexecutor    ━━━━━━━━┫\nenvironment ━━━━━━━━━━━━━┫' },
    ],
    animation: {
      label: 'Execution demo', steps: ['Submit', 'Run', 'Input needed', 'Resume', 'Complete'],
      messages: ['Your application submits a task.', 'The native harness runs the task.', 'The turn waits for input from your application.', 'Your application responds. Execution continues.', 'The result is ready for your application.'],
      states: ['Submitted', 'Running', 'Waiting', 'Running', 'Completed'],
      pause: 'Pause', play: 'Play', replay: 'Replay', next: 'Next', note: 'Illustrative workflow · Each harness declares its supported interactions.',
    },
    more: 'Session lifecycle',
  },
  observe: {
    index: '04',
    kicker: 'see it run',
    title: 'Operations you can actually see.', titleAccent: 'actually see.',
    lede: 'Sessions · Compute capacity · Usage',
    tabs: [
      { id: 'overview', label: 'overview', alt: 'Web console overview: service status, running Sessions, sandbox capacity, fleet and Projects.' },
      { id: 'metrics', label: 'agent metrics', alt: 'Web console agent metrics: requests, errors, latency, tokens and tool calls.' },
    ],
    caption: 'Screenshot values are illustrative. Usage visibility depends on what each native harness reports.',
  },
  start: {
    index: '05',
    kicker: 'get started',
    title: 'From install to first Session.', titleAccent: 'first Session.',
    lede: 'On a Linux amd64 host with Docker and Python 3.9+:',
    steps: [
      { title: 'Install & sign in', body: '' },
      { title: 'Configure model & compute', body: '' },
      { title: 'Run your first Session', body: '' },
    ],
    cta: 'Installation guide',
    trial: 'Quickstart',
  },
  docs: { title: 'Explore the docs', links: [
    { label: 'Quickstart', path: '/docs/getting-started/quickstart' },
    { label: 'API reference', path: '/docs/api/public-agent-api' },
    { label: 'Deployment', path: '/docs/getting-started/install' },
    { label: 'Build an adapter', path: '/docs/development' },
  ] },
  ecosystem: { title: 'One core. An open ecosystem.', titleAccent: 'An open ecosystem.', agents: 'Harnesses & models', compute: 'Cloud & compute', pause: 'Pause', resume: 'Resume' },
}

const zh: LandingCopy = {
  hero: {
    eyebrow: '开源 · 可自部署 · OpenAI Agents API',
    title: ['One core.', 'Many agents.'],
    lede: 'OpenAI Agents API 的开源实现。用官方 OpenAI SDK，在你自己的模型和机器上运行 Codex、Claude Code 或 MiniMax Code。',
    primary: '运行第一个 Session',
    secondary: '阅读文档',
    installLabel: 'Linux amd64 · Docker · Python 3.9+',
    copied: '已复制',
    copy: '复制',
    hud: [
      ['api', '/v1 · OpenAI Agents API'],
      ['harness', 'codex | claude_sdk | mcode'],
      ['protocol', 'responses | anthropic | chat_completions'],
      ['sandbox', 'docker | microsandbox | e2b'],
      ['machine', 'linux | macos | windows'],
    ],
  },
  ticker: en.ticker,
  compose: {
    index: '01',
    kicker: '自由组合',
    title: 'Agent、模型、机器，自由组合。', titleAccent: '自由组合。',
    lede: '选一个组合，看看对应的 API 调用。',
    harness: 'harness',
    protocol: '模型协议',
    environment: '执行环境',
    accepted: '通过校验 · Session 已创建',
    rejected: (harness, protocols) => `创建前即被拒绝 · ${harness} 只支持 ${protocols}`,
    envs: {
      openai_hosted: { label: '托管沙箱', note: 'Docker · microsandbox · E2B' },
      self_hosted: { label: '你自己的机器', note: 'Linux · macOS · Windows' },
    },
    input: '修复失败的测试，并说明改动。',
    footnote: '支持哪些组合，由每个 Harness 明确声明，而不是猜测。',
    footnoteLink: 'Harness 能力表',
  },
  architecture: {
    index: '02',
    kicker: '每条边界都是协议',
    title: '一个 API，部件自由替换。', titleAccent: '部件自由替换。',
    lede: 'Core 编排，原生 Harness 执行，协议连接各个部件。',
    tabs: [
      { id: 'ecosystem', label: '生态', alt: '上层应用通过 Agents API 接入，Harness、模型服务和计算资源通过各自边界连接。' },
      { id: 'protocols', label: '组件', alt: 'Core 之上是 Agents API 与 Core API；Sandbox Provider、Runtime、Harness、Model Provider 通过协议连接。' },
      { id: 'surfaces', label: 'API 命名空间', alt: '三个命名空间、三种凭证：应用用 Agents API，管理员用 Core API，节点用 Machine API。' },
    ],
    more: '阅读架构文档',
  },
  session: {
    index: '03',
    kicker: '可管理的执行',
    title: 'Session is all you need.', titleAccent: 'Session',
    lede: '提交任务，追踪进度，继续对话。',
    rules: [
      { title: '连接 ≠ 任务', body: '关闭输出流，已提交的任务仍继续执行。', art: en.session.rules[0].art },
      { title: '重试有身份', body: '支持的提交路径用稳定标识避免重复任务。', art: en.session.rules[1].art },
      { title: '取消要有回执', body: 'Core 确认任务停止与资源清理。', art: en.session.rules[2].art },
      { title: '执行 ≠ 机器', body: '分别追踪 Turn、Executor 与 Environment。', art: en.session.rules[3].art },
    ],
    animation: {
      label: '执行流程演示', steps: ['提交', '执行', '等待输入', '继续执行', '完成'],
      messages: ['应用提交一项任务。', '原生 Harness 开始执行。', 'Turn 等待应用提供输入。', '应用回应，任务继续执行。', '结果已就绪，应用可以读取。'],
      states: ['已提交', '执行中', '等待中', '执行中', '已完成'],
      pause: '暂停', play: '播放', replay: '重播', next: '下一步', note: '流程示意 · 交互支持范围由各 Harness 声明。',
    },
    more: 'Session 生命周期',
  },
  observe: {
    index: '04',
    kicker: '看见运行',
    title: '运行情况，看得见。', titleAccent: '看得见。',
    lede: 'Session 状态 · 计算资源 · 用量',
    tabs: [
      { id: 'overview', label: '部署概览', alt: 'Web 控制台概览：服务状态、运行中的 Session、沙箱容量、节点与 Project。' },
      { id: 'metrics', label: 'Agent 监控', alt: 'Web 控制台 Agent 监控：请求、错误、耗时、Token 与工具调用。' },
    ],
    caption: '截图数值仅用于展示界面。用量可见性取决于原生 Harness 提供的数据。',
  },
  start: {
    index: '05',
    kicker: '开始使用',
    title: '从安装到第一个 Session。', titleAccent: '第一个 Session。',
    lede: '在装有 Docker 和 Python 3.9+ 的 Linux amd64 主机上：',
    steps: [
      { title: '安装并登录', body: '' },
      { title: '配置模型与计算资源', body: '' },
      { title: '运行第一个 Session', body: '' },
    ],
    cta: '安装指南',
    trial: '快速开始',
  },
  docs: { title: '文档入口', links: [
    { label: '快速开始', path: '/docs/getting-started/quickstart' },
    { label: 'API 参考', path: '/docs/api/public-agent-api' },
    { label: '部署指南', path: '/docs/getting-started/install' },
    { label: '扩展适配器', path: '/docs/development' },
  ] },
  ecosystem: { title: '一个内核，开放的生态。', titleAccent: '开放的生态。', agents: 'Harness 与模型', compute: '云计算与沙箱', pause: '暂停', resume: '继续' },
}

export const copy: Record<Lang, LandingCopy> = { en, zh }

/** Native protocols per harness, default first (contracts/agents-api/model-execution.md). */
export const harnesses = [
  { id: 'codex', label: 'Codex', protocols: ['responses'] },
  { id: 'claude_sdk', label: 'Claude Code', protocols: ['anthropic'] },
  { id: 'mcode', label: 'MiniMax Code', protocols: ['anthropic', 'responses', 'chat_completions'] },
] as const

export const protocols = ['responses', 'anthropic', 'chat_completions'] as const

export const installCommand = 'curl -fsSL https://github.com/MiniMax-AI/OpenAgentCore/releases/latest/download/install.sh | bash'

export const repoUrl = 'https://github.com/MiniMax-AI/OpenAgentCore'
