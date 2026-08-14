// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from 'vitest';
import { stripMarkdownFrontmatter } from './markdownFrontmatter';

describe('stripMarkdownFrontmatter', () => {
  it('keeps only the Markdown body after YAML metadata', () => {
    expect(stripMarkdownFrontmatter('---\nname: Example\ntags: one two\n---\n# Body\nText')).toBe('# Body\nText');
  });

  it('supports CRLF, BOM, and the YAML ellipsis terminator', () => {
    expect(stripMarkdownFrontmatter('\ufeff---\r\nname: Example\r\n...\r\nBody')).toBe('Body');
  });

  it('does not strip ordinary or unterminated Markdown', () => {
    expect(stripMarkdownFrontmatter('---\nOrdinary content')).toBe('---\nOrdinary content');
    expect(stripMarkdownFrontmatter('# Body\n---\nText')).toBe('# Body\n---\nText');
  });
});
