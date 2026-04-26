import { assetUrl } from './assets'

export type NormalizedAgentType = '' | 'openclaw' | 'codex' | 'kiro-cli' | 'copilot' | 'claude' | 'cicy-claude' | 'opencode' | 'hermes'

export type AgentTypeOption = {
  value: Exclude<NormalizedAgentType, ''>
  label: string
}

export type AgentTypeIconMeta = {
  label: string
  src?: string
  text?: string
}

export const AGENT_TYPE_OPTIONS: AgentTypeOption[] = [
  { value: 'openclaw', label: 'OpenClaw' },
  { value: 'codex', label: 'Codex' },
  { value: 'claude', label: 'Claude' },
  { value: 'kiro-cli', label: 'Kiro CLI' },
  { value: 'copilot', label: 'Copilot' },
  { value: 'opencode', label: 'OpenCode' },
  { value: 'cicy-claude', label: 'CiCy' },
  { value: 'hermes', label: 'Hermes' },
]

const AGENT_TYPE_ICON_MAP: Record<Exclude<NormalizedAgentType, ''>, AgentTypeIconMeta> = {
  openclaw: { label: 'OpenClaw', text: '🦞' },
  codex: { label: 'Codex', src: assetUrl('/assets/logos/openai.svg') },
  claude: { label: 'Claude', src: assetUrl('/assets/logos/claude-symbol.svg') },
  'kiro-cli': { label: 'Kiro', src: assetUrl('/assets/logos/kiro.png') },
  copilot: { label: 'Copilot', src: assetUrl('/assets/logos/copilot.svg') },
  opencode: { label: 'OpenCode', src: assetUrl('/assets/logos/opencode.svg') },
  'cicy-claude': { label: 'CiCy', src: 'https://cicy-ai.com/logo.svg' },
  hermes: { label: 'Hermes', text: 'HE' },
}

export function normalizeAgentType(agentType?: string): NormalizedAgentType {
  switch ((agentType || '').trim().toLowerCase()) {
    case 'openclaw':
    case 'opencraw':
      return 'openclaw'
    case 'codex':
    case 'openai':
      return 'codex'
    case 'kiro-cli':
    case 'kiro':
    case 'kiro-cli chat':
      return 'kiro-cli'
    case 'copilot':
    case 'github-copilot':
    case 'ghcopilot':
      return 'copilot'
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
