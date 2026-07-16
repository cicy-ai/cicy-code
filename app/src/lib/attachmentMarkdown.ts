// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

// How an uploaded attachment is written into an agent's input.
//
// There are two destinations and they do NOT take the same form. Getting this
// wrong is not cosmetic — one of them silently eats the attachment.

/**
 * For a CLI agent's REPL (claude / codex / opencode in tmux), and for pasting
 * into the terminal.
 *
 * ALWAYS a plain link, never the markdown image form `![name](path)` — not even
 * for images.
 *
 * In Claude Code a line that STARTS with `!` is the run-a-shell-command prefix.
 * Attachments are joined with "\n\n" and sent as one blob, so `![x.png](/path)`
 * gets executed as the shell command `[x.png](/path)`; zsh reads `[...]` as a
 * glob and answers `bad pattern: [x.png](…)`. The image never reaches the agent.
 *
 * It is also INTERMITTENT — only the first attachment sits at the start of the
 * input — which is why it survived so long, and why someone once "fixed" the
 * mime-sniffing to make images get `!` more reliably, making the bug more
 * reliable too. Hence this function, and the test next to it.
 *
 * The `!` only ever bought an inline thumbnail in the web history. The agent
 * Reads the path either way.
 */
export function replAttachmentMarkdown(name: string, absPath: string): string {
  return `[${name}](${absPath})`
}

/**
 * For the cicy chat (a headless in-process agent — there is no shell and no
 * REPL, and the web history renders markdown), where the image form is both
 * safe and wanted.
 */
export function chatAttachmentMarkdown(name: string, absPath: string, isImage: boolean): string {
  return isImage ? `![${name}](${absPath})` : `[${name}](${absPath})`
}
