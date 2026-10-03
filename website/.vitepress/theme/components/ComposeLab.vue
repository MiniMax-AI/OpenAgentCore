<script setup lang="ts">
// Interactive Session builder: pick a harness, a model protocol and an
// Environment, and see the exact SDK call plus Core's validation outcome.
// Supported protocols come from contracts/agents-api/model-execution.md.
import { computed, ref, watch } from 'vue'
import { withBase } from 'vitepress'
import { harnesses, protocols, type LandingCopy } from '../landing-content'

const props = defineProps<{ t: LandingCopy['compose']; lang: 'en' | 'zh' }>()

type HarnessId = (typeof harnesses)[number]['id']
type Protocol = (typeof protocols)[number]
type Env = 'openai_hosted' | 'self_hosted'

const harness = ref<HarnessId>('codex')
const protocol = ref<Protocol>('responses')
const env = ref<Env>('openai_hosted')
const pulse = ref(0)

const selected = computed(() => harnesses.find((h) => h.id === harness.value)!)
const ok = computed(() => (selected.value.protocols as readonly string[]).includes(protocol.value))

watch([harness, protocol, env], () => pulse.value++)

// Switching harness moves to its default protocol; choosing an unsupported
// protocol afterwards shows Core's rejection.
function pickHarness(id: HarnessId) {
  harness.value = id
  const native = harnesses.find((h) => h.id === id)!.protocols as readonly string[]
  if (!native.includes(protocol.value)) protocol.value = native[0] as Protocol
}

type Tok = { t: string; k?: 'kw' | 'str' | 'com' | 'fn' | 'hl' | 'bad' }

const lines = computed<Tok[][]>(() => {
  const s = (v: string, hl = false): Tok => ({ t: `"${v}"`, k: hl ? 'hl' : 'str' })
  // The harness and protocol values are the conflicting pair when Core rejects.
  const pair = (v: string): Tok => ({ t: `"${v}"`, k: ok.value ? 'hl' : 'bad' })
  const out: Tok[][] = [
    [{ t: 'from', k: 'kw' }, { t: ' openai ' }, { t: 'import', k: 'kw' }, { t: ' OpenAI' }],
    [],
    [{ t: 'client = ' }, { t: 'OpenAI', k: 'fn' }, { t: '()  ' }, { t: '# OPENAI_BASE_URL → your Core', k: 'com' }],
    [],
    [{ t: 'session = client.beta.agents.sessions.' }, { t: 'create', k: 'fn' }, { t: '(' }],
  ]
  if (env.value === 'openai_hosted') {
    out.push([{ t: '    environment={' }, s('type'), { t: ': ' }, s('openai_hosted', true), { t: '},' }])
  } else {
    out.push([{ t: '    environment={' }, s('type'), { t: ': ' }, s('self_hosted', true), { t: ',' }])
    out.push([{ t: '                 ' }, s('workspace_directory'), { t: ': ' }, s('/home/dev/project'), { t: '},' }])
  }
  out.push(
    [{ t: '    input=' }, s(props.t.input), { t: ',' }],
    [{ t: '    extra_body={' }],
    [{ t: '        ' }, s('agent'), { t: ': {' }, s('model'), { t: ': MODEL, ' }, s('x_agents_core'), { t: ': {' }, s('harness'), { t: ': ' }, pair(harness.value), { t: '}},' }],
    [{ t: '        ' }, s('x_agents_core'), { t: ': {' }, s('model_provider'), { t: ': {' }],
    [{ t: '            ' }, s('protocol'), { t: ': ' }, pair(protocol.value), { t: ',' }],
    [{ t: '            ' }, s('base_url'), { t: ': BASE_URL, ' }, s('api_key'), { t: ': API_KEY,' }],
    // MiniMax Code requires positive token limits (contracts/agents-api/model-execution.md).
    ...(harness.value === 'mcode'
      ? [[{ t: '            ' }, s('context_window'), { t: ': ' }, { t: '200000' }, { t: ', ' }, s('max_output_tokens'), { t: ': ' }, { t: '8000' }, { t: ',' }]]
      : []),
    [{ t: '        }},' }],
    [{ t: '    },' }],
    [{ t: ')' }],
  )
  if (env.value === 'self_hosted') {
    out.push([{ t: '# x_agents_core.installation → one command connects your machine', k: 'com' }])
  }
  return out
})
</script>

<template>
  <div class="lab">
    <div class="lab-controls">
      <fieldset>
        <legend><span class="dollar">$</span> {{ t.harness }}</legend>
        <button
          v-for="h in harnesses"
          :key="h.id"
          type="button"
          class="opt"
          :class="{ on: harness === h.id }"
          :aria-pressed="harness === h.id"
          @click="pickHarness(h.id)"
        >
          <span class="box">{{ harness === h.id ? '■' : '□' }}</span>
          <span class="name">{{ h.label }}</span>
          <code>{{ h.id }}</code>
        </button>
      </fieldset>

      <fieldset>
        <legend><span class="dollar">$</span> {{ t.protocol }}</legend>
        <button
          v-for="p in protocols"
          :key="p"
          type="button"
          class="opt"
          :class="{ on: protocol === p, unsupported: !(selected.protocols as readonly string[]).includes(p) }"
          :aria-pressed="protocol === p"
          @click="protocol = p"
        >
          <span class="box">{{ protocol === p ? '■' : '□' }}</span>
          <span class="name">{{ p }}</span>
          <code>{{ (selected.protocols as readonly string[]).includes(p) ? 'native' : '—' }}</code>
        </button>
      </fieldset>

      <fieldset>
        <legend><span class="dollar">$</span> {{ t.environment }}</legend>
        <button
          v-for="(e, id) in t.envs"
          :key="id"
          type="button"
          class="opt"
          :class="{ on: env === id }"
          :aria-pressed="env === id"
          @click="env = id as Env"
        >
          <span class="box">{{ env === id ? '■' : '□' }}</span>
          <span class="name">{{ e.label }}</span>
          <code>{{ e.note }}</code>
        </button>
      </fieldset>
    </div>

    <div class="lab-code">
      <div class="win-bar">
        <span class="dots"><i /><i /><i /></span>
        <span class="win-title">session.py</span>
      </div>
      <pre :key="pulse" class="code" aria-live="polite"><code><template v-for="(line, i) in lines" :key="i"><span class="ln">{{ String(i + 1).padStart(2, ' ') }}</span><template v-for="(tok, j) in line" :key="j"><span :class="tok.k">{{ tok.t }}</span></template>
</template></code></pre>
      <div class="verdict" :class="ok ? 'pass' : 'fail'" role="status">
        <span class="mark">{{ ok ? '✓' : '✗' }}</span>
        <span v-if="ok">{{ t.accepted }}</span>
        <span v-else>{{ t.rejected(selected.label, selected.protocols.join(', ')) }}</span>
      </div>
    </div>
    <p class="lab-foot">
      {{ t.footnote }}
      <a :href="withBase(`${lang === 'zh' ? '/zh' : ''}/contracts/agents-api/harness-capabilities`)">{{ t.footnoteLink }} →</a>
    </p>
  </div>
</template>

<style scoped>
.lab {
  display: grid;
  grid-template-columns: minmax(0, 0.9fr) minmax(0, 1.4fr);
  gap: 20px;
}
.lab-controls {
  display: grid;
  gap: 14px;
  align-content: start;
}
fieldset {
  border: 1px solid var(--l-line);
  border-radius: 10px;
  padding: 10px 10px 8px;
  margin: 0;
  background: var(--l-panel);
}
legend {
  padding: 0 6px;
  font: 500 12px/1 var(--l-mono);
  text-transform: uppercase;
  letter-spacing: 0.08em;
  color: var(--l-muted);
}
.dollar {
  color: var(--l-accent);
}
.opt {
  display: grid;
  grid-template-columns: 18px 1fr auto;
  align-items: center;
  gap: 8px;
  width: 100%;
  padding: 7px 8px;
  border-radius: 6px;
  font: 14px/1.3 var(--l-mono);
  color: var(--l-text-2);
  text-align: left;
  transition: background 0.15s, color 0.15s;
}
.opt:hover {
  background: var(--l-hover);
  color: var(--l-text);
}
.opt.on {
  color: var(--l-accent);
  background: var(--l-accent-soft);
}
.opt.unsupported .name {
  text-decoration: line-through;
  text-decoration-color: var(--l-danger);
}
.opt.unsupported.on {
  color: var(--l-danger);
  background: var(--l-danger-soft);
}
.opt.unsupported.on .name {
  color: var(--l-danger);
}
.opt code {
  font-size: 11px;
  color: var(--l-muted);
  white-space: nowrap;
}
.box {
  font-size: 12px;
}
.lab-code {
  display: flex;
  flex-direction: column;
  border: 1px solid var(--l-line);
  border-radius: 12px;
  overflow: hidden;
  background: var(--l-code-bg);
  min-width: 0;
}
.win-bar {
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
.code {
  flex: 1;
  margin: 0;
  padding: 16px 18px;
  overflow-x: auto;
  font: 13px/1.7 var(--l-mono);
  color: var(--l-code-text);
  animation: settle 0.35s ease-out;
}
.ln {
  display: inline-block;
  width: 2.2em;
  margin-right: 0.8em;
  color: #56645b;
  user-select: none;
  text-align: right;
}
.kw {
  color: var(--l-kw);
}
.str {
  color: var(--l-str);
}
.com {
  color: #5f7166;
  font-style: italic;
}
.fn {
  color: var(--l-fn);
}
.hl {
  color: #04210f;
  background: #4ade80;
  border-radius: 3px;
  padding: 0 2px;
}
.verdict {
  display: flex;
  gap: 10px;
  align-items: baseline;
  padding: 12px 18px;
  border-top: 1px solid rgba(232, 241, 235, 0.08);
  font: 13px/1.5 var(--l-mono);
}
.verdict.pass {
  color: #4ade80;
  background: rgba(74, 222, 128, 0.08);
}
.verdict.fail {
  color: #fb7185;
  background: rgba(251, 113, 133, 0.1);
}
.bad {
  color: #2a0710;
  background: #fb7185;
  border-radius: 3px;
  padding: 0 2px;
}
.mark {
  font-weight: 700;
}
.lab-foot {
  grid-column: 1 / -1;
  margin: 0;
  font-size: 14px;
  color: var(--l-muted);
}
.lab-foot a {
  color: var(--l-accent);
  font-family: var(--l-mono);
  text-decoration: none;
}
@keyframes settle {
  from {
    opacity: 0.4;
    filter: blur(1px);
  }
}
@media (max-width: 860px) {
  .lab {
    grid-template-columns: minmax(0, 1fr);
  }
}
@media (prefers-reduced-motion: reduce) {
  .code {
    animation: none;
  }
}
</style>
