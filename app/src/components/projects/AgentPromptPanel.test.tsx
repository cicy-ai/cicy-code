// SPDX-License-Identifier: Apache-2.0
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import AgentPromptPanel from './AgentPromptPanel'

const getAgentPromptLog = vi.hoisted(() => vi.fn())
vi.mock('../../services/api', () => ({ default: { getAgentPromptLog } }))

const hoursAgo = (h: number) => new Date(Date.now() - h * 3_600_000).toISOString()
const log = [
  { ts: hoursAgo(30 * 24), content: 'ancient prompt' },
  { ts: hoursAgo(0.2), content: 'ship the release' },
  { ts: hoursAgo(0.1), content: '调整画布大小\n\n[shot.png](/home/cicy/cicy-ai/assets/2026/11/12/a__shot.png)' },
]
const rows = () => Array.from(document.querySelectorAll('[data-id="agent-prompt-row-text"]')).map((el) => el.textContent)

describe('AgentPromptPanel', () => {
  beforeEach(() => {
    getAgentPromptLog.mockReset()
    getAgentPromptLog.mockImplementation(async (ids: string[]) => ({ data: { agents: { [ids[0]]: log } } }))
  })

  it('lists only this agent\'s prompts newest first, each with its time', async () => {
    render(<AgentPromptPanel agentId="w-1" />)
    await waitFor(() => expect(rows()).toEqual(['调整画布大小', 'ship the release', 'ancient prompt']))
    expect(getAgentPromptLog).toHaveBeenCalledWith(['w-1'])
    const times = Array.from(document.querySelectorAll('[data-id="agent-prompt-row-time"]')).map((el) => el.textContent)
    expect(times.every((value) => /^\d\d:\d\d$/.test(String(value)))).toBe(true)
    expect(document.querySelector('[data-id="agent-prompt-count"]')?.textContent).toBe('3')
  })

  it('previews an attached image instead of printing its path', async () => {
    render(<AgentPromptPanel agentId="w-1" />)
    await waitFor(() => expect(rows()).toHaveLength(3))
    const image = document.querySelector('[data-id="agent-prompt-row-images"] img') as HTMLImageElement
    expect(image.getAttribute('src')).toBe('/assets/files/2026/11/12/a__shot.png')
    fireEvent.click(image)
    expect(document.querySelector('[data-id="current-history-md-img-lightbox"]')).not.toBeNull()
  })

  it('filters by search and period', async () => {
    render(<AgentPromptPanel agentId="w-1" />)
    await waitFor(() => expect(rows()).toHaveLength(3))
    fireEvent.change(screen.getByPlaceholderText(/prompt/), { target: { value: 'release' } })
    expect(rows()).toEqual(['ship the release'])
    expect(document.querySelector('[data-id="agent-prompt-count"]')?.textContent).toBe('1 / 3')
    fireEvent.change(screen.getByPlaceholderText(/prompt/), { target: { value: '' } })
    fireEvent.click(document.querySelector('[data-id="agent-prompt-range-today"]')!)
    expect(rows()).toEqual(['调整画布大小', 'ship the release'])
  })

  it('puts a prompt back into the composer without the attachment markdown', async () => {
    const onReuse = vi.fn()
    render(<AgentPromptPanel agentId="w-1" onReuse={onReuse} />)
    await waitFor(() => expect(rows()).toHaveLength(3))
    fireEvent.click(document.querySelectorAll('[data-id="agent-prompt-row-reuse"]')[0])
    expect(onReuse).toHaveBeenCalledWith('调整画布大小')
  })

  it('reloads when the agent starts or finishes a turn', async () => {
    const view = render(<AgentPromptPanel agentId="w-1" busy={false} />)
    await waitFor(() => expect(getAgentPromptLog).toHaveBeenCalledTimes(1))
    view.rerender(<AgentPromptPanel agentId="w-1" busy />)
    await waitFor(() => expect(getAgentPromptLog).toHaveBeenCalledTimes(2))
  })

  it('shows the error when the log cannot be loaded', async () => {
    getAgentPromptLog.mockRejectedValueOnce(new Error('boom'))
    render(<AgentPromptPanel agentId="w-1" />)
    await waitFor(() => expect(screen.getByRole('alert').textContent).toContain('boom'))
  })
})
