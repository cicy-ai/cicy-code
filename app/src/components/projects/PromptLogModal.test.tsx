// SPDX-License-Identifier: Apache-2.0
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import PromptLogModal from './PromptLogModal'

const getAgentPromptLog = vi.hoisted(() => vi.fn())
vi.mock('../../services/api', () => ({ default: { getAgentPromptLog } }))
vi.mock('../AgentAvatar', () => ({ default: () => null }))

const agents = [
  { id: 'w-1', title: 'Frontend', agentType: 'claude', inProject: true },
  { id: 'w-2', title: 'Backend', agentType: 'kiro', inProject: true },
  { id: 'w-9', title: 'Elsewhere', agentType: 'claude', inProject: false },
]
const hoursAgo = (h: number) => new Date(Date.now() - h * 3_600_000).toISOString()
const log = {
  'w-1': [
    { ts: hoursAgo(30 * 24), content: 'ancient prompt' },
    { ts: hoursAgo(0.1), content: '调整画布大小\n\n[shot.png](/home/cicy/cicy-ai/assets/2026/11/12/a__shot.png)' },
  ],
  'w-2': [{ ts: hoursAgo(0.2), content: 'ship the release' }],
  'w-9': [{ ts: hoursAgo(0.3), content: 'from another project' }],
}

const rows = () => screen.queryAllByText((_, el) => el?.getAttribute('data-id') === 'prompt-log-row-text').map((el) => el.textContent)

describe('PromptLogModal', () => {
  beforeEach(() => {
    getAgentPromptLog.mockReset()
    getAgentPromptLog.mockImplementation(async (ids: string[]) => ({ data: { agents: Object.fromEntries(ids.map((id) => [id, (log as any)[id] || []])) } }))
  })

  const open = (props: Partial<Parameters<typeof PromptLogModal>[0]> = {}) => {
    const handlers = { onClose: vi.fn(), onReuse: vi.fn(), onLocate: vi.fn() }
    render(<PromptLogModal open agents={agents} projectName="Demo" {...handlers} {...props} />)
    return handlers
  }

  it('lists the project agents prompts newest first with time, and focuses search', async () => {
    open()
    await waitFor(() => expect(rows()).toEqual(['调整画布大小', 'ship the release', 'ancient prompt']))
    expect(getAgentPromptLog).toHaveBeenCalledWith(['w-1', 'w-2'])
    await waitFor(() => expect(document.activeElement?.getAttribute('data-id')).toBe('prompt-log-search'))
    const times = Array.from(document.querySelectorAll('[data-id="prompt-log-row-time"]')).map((el) => el.textContent)
    expect(times.every((value) => /^\d\d:\d\d$/.test(String(value)))).toBe(true)
  })

  it('previews an attached image instead of printing its path', async () => {
    open()
    await waitFor(() => expect(rows()).toHaveLength(3))
    const image = document.querySelector('[data-id="prompt-log-row-images"] img') as HTMLImageElement
    expect(image.getAttribute('src')).toBe('/assets/files/2026/11/12/a__shot.png')
    fireEvent.click(image)
    expect(document.querySelector('[data-id="current-history-md-img-lightbox"]')).not.toBeNull()
  })

  it('filters by search, agent and period, and widens to all agents', async () => {
    open()
    await waitFor(() => expect(rows()).toHaveLength(3))
    fireEvent.change(screen.getByPlaceholderText(/prompt/), { target: { value: 'release' } })
    expect(rows()).toEqual(['ship the release'])
    fireEvent.change(screen.getByPlaceholderText(/prompt/), { target: { value: '' } })
    fireEvent.click(document.querySelector('[data-id="prompt-log-agent-chip"][data-agent="w-1"]')!)
    expect(rows()).toEqual(['调整画布大小', 'ancient prompt'])
    fireEvent.click(document.querySelector('[data-id="prompt-log-range-today"]')!)
    expect(rows()).toEqual(['调整画布大小'])
    fireEvent.click(document.querySelector('[data-id="prompt-log-agent-all"]')!)
    fireEvent.click(document.querySelector('[data-id="prompt-log-range-all"]')!)
    fireEvent.click(document.querySelector('[data-id="prompt-log-scope-all"]')!)
    await waitFor(() => expect(rows()).toContain('from another project'))
    expect(getAgentPromptLog).toHaveBeenLastCalledWith(['w-1', 'w-2', 'w-9'])
  })

  it('puts a prompt back into its agent input without the attachment markdown', async () => {
    const handlers = open()
    await waitFor(() => expect(rows()).toHaveLength(3))
    fireEvent.click(document.querySelectorAll('[data-id="prompt-log-row-reuse"]')[0])
    expect(handlers.onReuse).toHaveBeenCalledWith('w-1', '调整画布大小')
    expect(handlers.onClose).toHaveBeenCalled()
  })

  it('clears the search on the first Escape and closes on the second', async () => {
    const handlers = open()
    await waitFor(() => expect(rows()).toHaveLength(3))
    fireEvent.change(screen.getByPlaceholderText(/prompt/), { target: { value: 'ship' } })
    fireEvent.keyDown(document, { key: 'Escape' })
    expect(handlers.onClose).not.toHaveBeenCalled()
    expect(rows()).toHaveLength(3)
    fireEvent.keyDown(document, { key: 'Escape' })
    expect(handlers.onClose).toHaveBeenCalledTimes(1)
  })

  it('shows the error when the log cannot be loaded', async () => {
    getAgentPromptLog.mockRejectedValueOnce(new Error('boom'))
    open()
    await waitFor(() => expect(screen.getByRole('alert').textContent).toContain('boom'))
  })
})
