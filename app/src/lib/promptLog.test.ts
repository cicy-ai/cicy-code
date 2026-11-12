// SPDX-License-Identifier: Apache-2.0
import { describe, expect, it } from 'vitest'
import { filterPromptLog, groupPromptLogByDay, mergePromptLogs, parsePromptContent, promptLogToText } from './promptLog'

describe('promptLog', () => {
  it('separates attachments from the words and drops paste wrappers', () => {
    const parsed = parsePromptContent('<pasted_content id="1">\n看下这个\n</pasted_content id="1">\n\n[shot.png](file://home/cicy/cicy-ai/assets/2026/a__shot.png)\n\n[notes.pdf](/home/cicy/cicy-ai/assets/notes.pdf) 以及 [官网](https://example.com)')
    expect(parsed.text).toBe('看下这个\n\n以及 [官网](https://example.com)')
    expect(parsed.attachments).toEqual([
      { name: 'shot.png', src: 'file://home/cicy/cicy-ai/assets/2026/a__shot.png', image: true },
      { name: 'notes.pdf', src: '/home/cicy/cicy-ai/assets/notes.pdf', image: false },
    ])
  })

  it('merges agents newest first and filters by agent, words and period', () => {
    const now = new Date(2026, 10, 12, 15, 0, 0).getTime()
    const iso = (daysAgo: number, hour: number) => new Date(2026, 10, 12 - daysAgo, hour).toISOString()
    const entries = mergePromptLogs({
      'w-1': [{ ts: iso(9, 9), content: 'old one' }, { ts: iso(0, 9), content: 'Fix the Canvas bug' }],
      'w-2': [{ ts: iso(1, 20), content: '发版' }, { ts: iso(0, 10), content: 'canvas resize' }, { ts: '', content: '  ' }],
      'w-3': null,
    })
    expect(entries.map((e) => e.content)).toEqual(['canvas resize', 'Fix the Canvas bug', '发版', 'old one'])
    expect(filterPromptLog(entries, { query: 'canvas FIX', now }).map((e) => e.content)).toEqual(['Fix the Canvas bug'])
    expect(filterPromptLog(entries, { agentId: 'w-2', now })).toHaveLength(2)
    expect(filterPromptLog(entries, { range: 'today', now })).toHaveLength(2)
    expect(filterPromptLog(entries, { range: 'week', now })).toHaveLength(3)
    expect(groupPromptLogByDay(entries).map((g) => g.entries.length)).toEqual([2, 1, 1])
    const text = promptLogToText(entries.slice(0, 2), (id) => `Agent ${id}`)
    expect(text).toBe('[2026-11-12 09:00] Agent w-1\nFix the Canvas bug\n\n[2026-11-12 10:00] Agent w-2\ncanvas resize')
  })
})
