// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from 'vitest'
import { chatAttachmentMarkdown, replAttachmentMarkdown } from './attachmentMarkdown'

describe('replAttachmentMarkdown', () => {
  // The whole point. A line starting with `!` is Claude Code's run-a-shell
  // -command prefix, so `![x.png](/p)` was being executed as `[x.png](/p)` and
  // zsh answered `bad pattern`. The attachment never arrived.
  it('never starts with ! — even for an image', () => {
    for (const name of ['x.png', 'shot.jpeg', 'a.gif', 'v.webp', 'diagram.svg', 'notes.txt']) {
      const got = replAttachmentMarkdown(name, `/tmp/${name}`)
      expect(got.startsWith('!')).toBe(false)
      expect(got).toBe(`[${name}](/tmp/${name})`)
    }
  })

  // Attachments are joined with "\n\n", so EVERY one of them begins a line.
  // Only the first sits at the very start of the input, which is what made the
  // original bug intermittent — assert the whole blob, not just one part.
  it('produces a blob where no line begins with !', () => {
    const blob = [
      replAttachmentMarkdown('a.png', '/tmp/a.png'),
      replAttachmentMarkdown('b.pdf', '/tmp/b.pdf'),
    ].join('\n\n')
    for (const line of blob.split('\n')) {
      expect(line.startsWith('!')).toBe(false)
    }
  })
})

describe('chatAttachmentMarkdown', () => {
  // The cicy chat has no shell and renders markdown, so the image form is safe
  // there — and is the only reason the `!` exists at all.
  it('keeps the image form for images', () => {
    expect(chatAttachmentMarkdown('x.png', '/tmp/x.png', true)).toBe('![x.png](/tmp/x.png)')
  })

  it('uses a plain link for non-images', () => {
    expect(chatAttachmentMarkdown('a.pdf', '/tmp/a.pdf', false)).toBe('[a.pdf](/tmp/a.pdf)')
  })
})
