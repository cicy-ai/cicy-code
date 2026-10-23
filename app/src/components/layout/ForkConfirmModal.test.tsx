// SPDX-License-Identifier: Apache-2.0
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, waitFor } from '@testing-library/react'

const api = vi.hoisted(() => ({ forkPreview: vi.fn(), forkPane: vi.fn() }))
vi.mock('../../services/api', () => ({
  default: new Proxy(api as Record<string, any>, { get: (target, key: string) => target[key] ?? vi.fn().mockResolvedValue({ data: {} }) }),
}))

import ForkConfirmModal from './ForkConfirmModal'

const preview = (agentType: string) => ({
  data: {
    success: true, source_pane_id: 'w-1075:main.0', source_short: 'w-1075', agent_type: agentType, workspace: '/w',
    files: { current_json: { path: '/a', size: 1 }, reply_json: { path: '/b', size: 1 }, summary: { path: '/c', size: 1, content: 'x' } },
    token_use: {}, summary_tokens_est: 1, compression: { ratio: 0.1, original_bytes: 10, summary_bytes: 1 },
    default_prompt: 'hello', default_prompts: { en: 'hello', zh: '你好' },
  },
})

const renderModal = (onForked = vi.fn(), onClose = vi.fn()) =>
  render(<ForkConfirmModal sourcePaneId="w-1075" masterPaneId="w-1001" onClose={onClose} onForked={onForked} onOpenAgentFile={vi.fn()} />)

describe('ForkConfirmModal', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    let n = 200
    api.forkPane.mockImplementation(() => Promise.resolve({ data: { pane_id: `w-${n++}:main.0` } }))
  })

  it('hides the token-use box for kiro sources and shows it otherwise', async () => {
    api.forkPreview.mockResolvedValue(preview('kiro-cli'))
    const { unmount } = renderModal()
    await waitFor(() => expect(document.querySelector('[data-id="fork-confirm-stats"]')).toBeInTheDocument())
    expect(document.querySelector('[data-id="fork-confirm-token-use"]')).not.toBeInTheDocument()
    unmount()

    api.forkPreview.mockResolvedValue(preview('claude'))
    renderModal()
    await waitFor(() => expect(document.querySelector('[data-id="fork-confirm-token-use"]')).toBeInTheDocument())
  })

  it('creates one fork by default and N forks when the count is raised', async () => {
    api.forkPreview.mockResolvedValue(preview('claude'))
    const onForked = vi.fn()
    const onClose = vi.fn()
    renderModal(onForked, onClose)
    await waitFor(() => expect(document.querySelector('[data-id="fork-confirm-send"]')).not.toBeDisabled())
    expect(document.querySelector('[data-id="fork-confirm-count-input"]')).toHaveValue(1)

    fireEvent.click(document.querySelector('[data-id="fork-confirm-count-inc"]')!)
    fireEvent.click(document.querySelector('[data-id="fork-confirm-count-inc"]')!)
    expect(document.querySelector('[data-id="fork-confirm-count-input"]')).toHaveValue(3)
    fireEvent.click(document.querySelector('[data-id="fork-confirm-send"]')!)

    await waitFor(() => expect(onClose).toHaveBeenCalled())
    expect(api.forkPane).toHaveBeenCalledTimes(3)
    expect(onForked).toHaveBeenCalledTimes(1)
    expect(onForked).toHaveBeenCalledWith(['w-200:main.0', 'w-201:main.0', 'w-202:main.0'])
  })

  it('keeps the modal open and reports progress when a later fork fails', async () => {
    api.forkPreview.mockResolvedValue(preview('claude'))
    api.forkPane
      .mockResolvedValueOnce({ data: { pane_id: 'w-300:main.0' } })
      .mockRejectedValueOnce(new Error('boom'))
    const onForked = vi.fn()
    const onClose = vi.fn()
    renderModal(onForked, onClose)
    await waitFor(() => expect(document.querySelector('[data-id="fork-confirm-send"]')).not.toBeDisabled())
    fireEvent.change(document.querySelector('[data-id="fork-confirm-count-input"]')!, { target: { value: '2' } })
    fireEvent.click(document.querySelector('[data-id="fork-confirm-send"]')!)

    await waitFor(() => expect(onForked).toHaveBeenCalledWith(['w-300:main.0']))
    expect(onClose).not.toHaveBeenCalled()
    // Remaining count left in the stepper so a retry creates only the missing one.
    expect(document.querySelector('[data-id="fork-confirm-count-input"]')).toHaveValue(1)
  })
})
