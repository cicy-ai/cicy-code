// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

import { keymap, EditorView } from '@codemirror/view';

// Shared clipboard ops for the CodeMirror editors (file editor + memory editor).
// The Electron host window has no Edit menu wiring copy/cut/paste roles, so macOS
// swallows Cmd+C/X/V before they reach the webContents and CodeMirror's native
// clipboard handling never fires. These drive the async Clipboard API directly
// off the live view so selection copy/cut/paste works from both the keymap and a
// context menu, regardless of the host menu.

// Copy the current selection. Returns false (lets default through) when nothing
// is selected.
export function cmCopySelection(view: EditorView): boolean {
  const { from, to } = view.state.selection.main;
  if (from === to) return false;
  void navigator.clipboard?.writeText(view.state.sliceDoc(from, to)).catch(() => {});
  return true;
}

// Cut the current selection (copy + delete). No-op on read-only buffers (the
// delete is skipped); copy still happens.
export function cmCutSelection(view: EditorView): boolean {
  const { from, to } = view.state.selection.main;
  if (from === to) return false;
  void navigator.clipboard?.writeText(view.state.sliceDoc(from, to)).catch(() => {});
  if (!view.state.readOnly) {
    view.dispatch({ changes: { from, to, insert: '' }, selection: { anchor: from } });
    view.focus();
  }
  return true;
}

// Paste clipboard text over the current selection. No-op on read-only buffers.
export function cmPasteSelection(view: EditorView): boolean {
  if (view.state.readOnly) return false;
  void navigator.clipboard?.readText().then((text) => {
    if (!text) return;
    const { from, to } = view.state.selection.main;
    view.dispatch({ changes: { from, to, insert: text }, selection: { anchor: from + text.length } });
    view.focus();
  }).catch(() => {});
  return true;
}

// Cmd/Ctrl + C/X/V bound to the helpers above.
export const CLIPBOARD_KEYMAP = keymap.of([
  { key: 'Mod-c', run: cmCopySelection },
  { key: 'Mod-x', run: cmCutSelection },
  { key: 'Mod-v', run: cmPasteSelection },
]);
