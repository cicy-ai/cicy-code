import { assetUrl } from './assets'

export type NormalizedAgentType = '' | 'claude' | 'codex' | 'opencode' | 'cursor' | 'kiro-cli' | 'copilot' | 'cicy-wechat' | 'cicy-feishu' | 'openclaw' | 'hermes' | 'cicy-claude'

export type AgentTypeOption = {
  value: string
  label: string
  description?: string
}

export type AgentTypeIconMeta = {
  label: string
  src?: string
  text?: string
}

export const AGENT_TYPE_OPTIONS: AgentTypeOption[] = [
  { value: 'claude', label: 'Claude', description: '偏稳健，适合分析与写作' },
  { value: 'codex', label: 'Codex', description: '代码执行和自动修改更强' },
  { value: 'opencode', label: 'OpenCode', description: '终端式编码体验，更轻量' },
  { value: 'cursor', label: 'Cursor', description: '适合 Cursor Agent 工作流' },
  { value: 'kiro-cli', label: 'Kiro CLI', description: '适合任务推进与工具流' },
  { value: 'copilot', label: 'Copilot', description: '偏 GitHub 工作流与补全' },
  { value: 'cicy-wechat', label: 'WeChat', description: '适合微信通道与消息协同' },
  { value: 'cicy-feishu', label: 'Feishu', description: '适合飞书通道与办公协同' },
  { value: 'openclaw', label: 'OpenClaw', description: '适合长连通道与外部集成' },
  { value: 'hermes', label: 'Hermes', description: '默认主控型员工，适合统筹协调' },
  { value: 'cicy-claude', label: 'CiCy', description: 'Claude 兼容包装，便于统一接入' },
]

const AGENT_TYPE_ICON_MAP: Record<Exclude<NormalizedAgentType, ''>, AgentTypeIconMeta> = {
  claude: { label: 'Claude', src: assetUrl('/assets/logos/claude-symbol.svg') },
  codex: { label: 'Codex', src: assetUrl('/assets/logos/openai.svg') },
  opencode: { label: 'OpenCode', src: assetUrl('/assets/logos/opencode.svg') },
  cursor: { label: 'Cursor', src: assetUrl('/assets/logos/cursor.svg') },
  'kiro-cli': { label: 'Kiro', src: assetUrl('/assets/logos/kiro.png') },
  copilot: { label: 'Copilot', src: assetUrl('/assets/logos/copilot.svg') },
  'cicy-wechat': { label: 'WeChat', text: '微' },
  'cicy-feishu': { label: 'Feishu', text: '飞' },
  openclaw: { label: 'OpenClaw', text: '🦞' },
  hermes: { label: 'Hermes', text: 'HE' },
  'cicy-claude': { label: 'CiCy', src: assetUrl('/assets/logos/cicy.svg') },
}

export function normalizeAgentType(agentType?: string): NormalizedAgentType {
  switch ((agentType || '').trim().toLowerCase()) {
    case 'openclaw':
    case 'opencraw':
      return 'openclaw'
    case 'codex':
    case 'openai':
      return 'codex'
    case 'cursor':
    case 'cursor-agent':
    case 'cursor agent':
      return 'cursor'
    case 'kiro-cli':
    case 'kiro':
    case 'kiro-cli chat':
      return 'kiro-cli'
    case 'copilot':
    case 'github-copilot':
    case 'ghcopilot':
      return 'copilot'
    case 'cicy-wechat':
    case 'wechat':
      return 'cicy-wechat'
    case 'cicy-feishu':
    case 'feishu':
      return 'cicy-feishu'
    case 'gemini':
      return 'codex'
    case 'claude':
    case 'claude code':
    case 'claude-code':
      return 'claude'
    case 'cicy':
    case 'cicy-claude':
      return 'cicy-claude'
    case 'opencode':
    case 'open code':
    case 'open-code':
      return 'opencode'
    case 'hermes':
    case 'hermes-agent':
    case 'hermes agent':
      return 'hermes'
    default:
      return ''
  }
}

export function getAgentTypeIconMeta(agentType?: string): AgentTypeIconMeta | null {
  const normalized = normalizeAgentType(agentType)
  if (!normalized) return null
  return AGENT_TYPE_ICON_MAP[normalized]
}
