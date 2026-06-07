import { assetUrl } from './assets'

type NormalizedAgentType = '' | 'claude' | 'codex' | 'opencode' | 'cursor' | 'kiro-cli' | 'copilot' | 'openclaw' | 'hermes' | 'cicy-claude' | 'cicy'

export type AgentTypeOption = {
  value: string
  label: string
  description?: string
}

type AgentTypeIconMeta = {
  label: string
  src?: string
  text?: string
}

export const AGENT_TYPE_OPTIONS: AgentTypeOption[] = [
  { value: 'claude', label: 'Claude' },
  { value: 'codex', label: 'Codex' },
  { value: 'opencode', label: 'OpenCode' },
  { value: 'cursor', label: 'Cursor' },
  { value: 'kiro-cli', label: 'Kiro CLI' },
  { value: 'copilot', label: 'Copilot' },
  { value: 'openclaw', label: 'OpenClaw' },
  { value: 'hermes', label: 'Hermes' },
  { value: 'cicy', label: 'CiCy' },
  { value: 'cicy-claude', label: 'CiCy (Claude)' },
]

const AGENT_TYPE_ICON_MAP: Record<Exclude<NormalizedAgentType, ''>, AgentTypeIconMeta> = {
  claude: { label: 'Claude', src: assetUrl('/assets/logos/claude-symbol.svg') },
  codex: { label: 'Codex', src: assetUrl('/assets/logos/openai.svg') },
  opencode: { label: 'OpenCode', src: assetUrl('/assets/logos/opencode.svg') },
  cursor: { label: 'Cursor', src: assetUrl('/assets/logos/cursor.svg') },
  'kiro-cli': { label: 'Kiro', src: assetUrl('/assets/logos/kiro.png') },
  copilot: { label: 'Copilot', src: assetUrl('/assets/logos/copilot.svg') },
  openclaw: { label: 'OpenClaw', text: '🦞' },
  hermes: { label: 'Hermes', text: 'HE' },
  'cicy-claude': { label: 'CiCy', src: assetUrl('/assets/logos/cicy.svg') },
  cicy: { label: 'CiCy', src: assetUrl('/assets/logos/cicy.svg') },
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
    case 'gemini':
      return 'codex'
    case 'claude':
    case 'claude code':
    case 'claude-code':
      return 'claude'
    case 'cicy-claude':
      return 'cicy-claude'
    // CiCy 原生 lite agent(原 dispatcher,todo #104 改名)。头像用产品 logo。
    case 'cicy':
    case 'dispatcher':
    case 'secretary':
      return 'cicy'
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

// Mirrors guidanceFilenameForAgentType in api/mgr/tmux.go. Returns the per-agent
// guidance file path (relative to the workspace), or null when the agent has
// none. kiro reads .kiro/steering/*.md, not CLAUDE.md. Keep the two switch
// tables in sync.
export function guidanceFilenameForAgentType(agentType?: string): string | null {
  switch (normalizeAgentType(agentType)) {
    case 'claude':
    case 'cicy-claude':
      return 'CLAUDE.md'
    case 'codex':
    case 'opencode':
    case 'cursor':
    case 'cicy':
      return 'AGENTS.md'
    case 'kiro-cli':
      return '.kiro/steering/memory.md'
    default:
      return null
  }
}

// isCicyLiteAgent reports whether an agent_type is the CiCy native lite agent
// (canonical "cicy"; "dispatcher"/"secretary" are legacy aliases). Use this for
// chat-routing decisions instead of comparing the raw string, so both migrated
// ("cicy") and un-migrated ("dispatcher") rows behave identically.
export function isCicyLiteAgent(agentType?: string): boolean {
  return normalizeAgentType(agentType) === 'cicy'
}
