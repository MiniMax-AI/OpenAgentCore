// Brand artwork is bundled locally; see the website guide for sources and licenses.
import codex from './assets/logos/codex.svg'
import claudecode from './assets/logos/claudecode.svg'
import minimax from './assets/logos/minimax.svg'
import kimi from './assets/logos/kimi.svg'
import deepseek from './assets/logos/deepseek.svg'
import qwen from './assets/logos/qwen.svg'
import zai from './assets/logos/zai.svg'
import gemini from './assets/logos/gemini.svg'
import grok from './assets/logos/grok.svg'
import mistral from './assets/logos/mistral.svg'
import meta from './assets/logos/meta.svg'
import openai from './assets/logos/openai.svg'
import cohere from './assets/logos/cohere.svg'
import doubao from './assets/logos/doubao.svg'
import aws from './assets/logos/aws.svg'
import azure from './assets/logos/azure.svg'
import googlecloud from './assets/logos/googlecloud.svg'
import alibabacloud from './assets/logos/alibabacloud.svg'
import tencentcloud from './assets/logos/tencentcloud.svg'
import huaweicloud from './assets/logos/huaweicloud.svg'
import volcengine from './assets/logos/volcengine.svg'
import baiducloud from './assets/logos/baiducloud.svg'
import digitalocean from './assets/logos/digitalocean.svg'
import hetzner from './assets/logos/hetzner.svg'
import docker from './assets/logos/docker.svg'
import e2b from './assets/logos/e2b.svg'

export const agentLogos = [
  { name: 'Codex', src: codex },
  { name: 'Claude Code', src: claudecode },
  { name: 'MiniMax Code', src: minimax },
  { name: 'Kimi', src: kimi },
  { name: 'DeepSeek', src: deepseek },
  { name: 'Qwen', src: qwen },
  { name: 'GLM', src: zai },
  { name: 'Gemini', src: gemini },
  { name: 'Grok', src: grok },
  { name: 'Mistral', src: mistral },
  { name: 'Meta', src: meta },
  { name: 'OpenAI', src: openai },
  { name: 'Cohere', src: cohere },
  { name: 'Doubao', src: doubao },
] as const

export const computeLogos = [
  { name: 'AWS', src: aws },
  { name: 'Microsoft Azure', src: azure },
  { name: 'Google Cloud', src: googlecloud },
  { name: 'Alibaba Cloud', src: alibabacloud },
  { name: 'Tencent Cloud', src: tencentcloud },
  { name: 'Huawei Cloud', src: huaweicloud },
  { name: 'Volcengine', src: volcengine },
  { name: 'Baidu AI Cloud', src: baiducloud },
  { name: 'DigitalOcean', src: digitalocean },
  { name: 'Hetzner', src: hetzner },
  { name: 'Docker', src: docker },
  { name: 'E2B', src: e2b },
] as const
