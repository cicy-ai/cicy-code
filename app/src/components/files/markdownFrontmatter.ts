// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

/**
 * Remove a leading YAML frontmatter block before rendering Markdown.
 *
 * The editor must keep the original source intact, but passing frontmatter to
 * CommonMark makes the closing `---` act as a setext heading underline. That
 * turns the whole metadata block into a giant heading in preview mode.
 */
export function stripMarkdownFrontmatter(source: string): string {
  const start = source.charCodeAt(0) === 0xfeff ? 1 : 0;
  const firstLineEnd = source.indexOf('\n', start);
  if (firstLineEnd < 0 || source.slice(start, firstLineEnd).replace(/\r$/, '').trim() !== '---') {
    return source;
  }

  let lineStart = firstLineEnd + 1;
  while (lineStart <= source.length) {
    const nextLineEnd = source.indexOf('\n', lineStart);
    const lineEnd = nextLineEnd < 0 ? source.length : nextLineEnd;
    const line = source.slice(lineStart, lineEnd).replace(/\r$/, '').trim();
    if (line === '---' || line === '...') {
      return nextLineEnd < 0 ? '' : source.slice(nextLineEnd + 1);
    }
    if (nextLineEnd < 0) break;
    lineStart = nextLineEnd + 1;
  }

  // An unmatched opening delimiter is ordinary Markdown, not frontmatter.
  return source;
}
