// Productized in-file search panel for the CodeEditor.
//
// CodeMirror ships a default search panel (via basicSetup's searchKeymap), but
// it renders at the bottom with native-styled inputs that clash with our dark
// zinc/oneDark chrome, has English labels, and — most importantly — gives no
// match counter. This module replaces it with a top-anchored, themed panel that
// matches the editor chrome, speaks Chinese, and shows "第 x / n 个".
//
// It is a plain-DOM CodeMirror Panel (not React) because the search panel lives
// inside the editor's own panel slot, outside the React tree.
import { EditorView, Panel, ViewUpdate, runScopeHandlers } from '@codemirror/view';
import {
  search,
  SearchQuery,
  getSearchQuery,
  setSearchQuery,
  findNext,
  findPrevious,
  replaceNext,
  replaceAll,
  closeSearchPanel,
  selectMatches,
} from '@codemirror/search';

// Cap match counting so a pathological query on a huge buffer can't freeze the
// panel. Beyond this we render "999+".
const MATCH_COUNT_CAP = 1000;

function countMatches(view: EditorView): { count: number; current: number; capped: boolean } {
  const query = getSearchQuery(view.state);
  if (!query.search || (query.regexp && !query.valid)) return { count: 0, current: 0, capped: false };
  const sel = view.state.selection.main;
  let count = 0;
  let current = 0;
  let capped = false;
  try {
    const cursor = query.getCursor(view.state);
    for (let next = cursor.next(); !next.done; next = cursor.next()) {
      count += 1;
      const m = next.value;
      if (m.from === sel.from && m.to === sel.to) current = count;
      if (count >= MATCH_COUNT_CAP) {
        capped = true;
        break;
      }
    }
  } catch {
    return { count: 0, current: 0, capped: false };
  }
  return { count, current, capped };
}

function el<K extends keyof HTMLElementTagNameMap>(
  tag: K,
  attrs: Record<string, string> = {},
  children: (Node | string)[] = [],
): HTMLElementTagNameMap[K] {
  const node = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) node.setAttribute(k, v);
  for (const c of children) node.append(c);
  return node;
}

function createCicySearchPanel(view: EditorView): Panel {
  const initial = getSearchQuery(view.state);

  const input = el('input', {
    'data-id': 'code-editor-search-input',
    class: 'cm-cicy-search-field',
    placeholder: '查找',
    'aria-label': '查找',
    type: 'text',
  }) as HTMLInputElement;

  const replaceInput = el('input', {
    'data-id': 'code-editor-search-replace',
    class: 'cm-cicy-search-field',
    placeholder: '替换为',
    'aria-label': '替换为',
    type: 'text',
  }) as HTMLInputElement;

  const counter = el('span', {
    'data-id': 'code-editor-search-count',
    class: 'cm-cicy-search-count',
  });

  // Option toggles (case / word / regexp) rendered as pill buttons.
  const mkToggle = (label: string, title: string, id: string) => {
    const b = el('button', {
      'data-id': id,
      class: 'cm-cicy-search-toggle',
      type: 'button',
      title,
      'aria-pressed': 'false',
    }, [label]);
    return b as HTMLButtonElement;
  };
  const caseBtn = mkToggle('Aa', '区分大小写', 'code-editor-search-case');
  const wordBtn = mkToggle('W', '全字匹配', 'code-editor-search-word');
  const reBtn = mkToggle('.*', '正则表达式', 'code-editor-search-regexp');

  const mkIconBtn = (label: string, title: string, id: string) =>
    el('button', { 'data-id': id, class: 'cm-cicy-search-btn', type: 'button', title }, [label]) as HTMLButtonElement;
  const prevBtn = mkIconBtn('↑', '上一个 (Shift+Enter)', 'code-editor-search-prev');
  const nextBtn = mkIconBtn('↓', '下一个 (Enter)', 'code-editor-search-next');
  const allBtn = mkIconBtn('全选', '选中全部匹配', 'code-editor-search-all');
  const closeBtn = mkIconBtn('✕', '关闭 (Esc)', 'code-editor-search-close');

  const replaceBtn = el('button', { 'data-id': 'code-editor-search-replace-one', class: 'cm-cicy-search-btn', type: 'button', title: '替换当前' }, ['替换']) as HTMLButtonElement;
  const replaceAllBtn = el('button', { 'data-id': 'code-editor-search-replace-all', class: 'cm-cicy-search-btn', type: 'button', title: '全部替换' }, ['全部']) as HTMLButtonElement;

  const toggleReplaceBtn = el('button', {
    'data-id': 'code-editor-search-toggle-replace',
    class: 'cm-cicy-search-expand',
    type: 'button',
    title: '展开替换',
    'aria-pressed': 'false',
  }, ['⌄']) as HTMLButtonElement;

  const findRow = el('div', { class: 'cm-cicy-search-row' }, [
    toggleReplaceBtn,
    input,
    counter,
    el('div', { class: 'cm-cicy-search-toggles' }, [caseBtn, wordBtn, reBtn]),
    prevBtn,
    nextBtn,
    allBtn,
    closeBtn,
  ]);
  const replaceRow = el('div', { class: 'cm-cicy-search-row cm-cicy-search-replace-row' }, [
    el('span', { class: 'cm-cicy-search-spacer' }),
    replaceInput,
    replaceBtn,
    replaceAllBtn,
  ]);
  replaceRow.style.display = 'none';

  const dom = el('div', { class: 'cm-cicy-search', 'data-id': 'code-editor-search-panel' }, [findRow, replaceRow]);
  // Keep clicks inside the panel from bubbling to the editor's context-menu /
  // pointerdown handlers.
  dom.addEventListener('pointerdown', (e) => e.stopPropagation());
  dom.addEventListener('mousedown', (e) => e.stopPropagation());

  // Mirror current option state into the SearchQuery on every change.
  const commitQuery = (opts: { scrollToMatch?: boolean } = {}) => {
    const query = new SearchQuery({
      search: input.value,
      caseSensitive: caseBtn.getAttribute('aria-pressed') === 'true',
      wholeWord: wordBtn.getAttribute('aria-pressed') === 'true',
      regexp: reBtn.getAttribute('aria-pressed') === 'true',
      replace: replaceInput.value,
    });
    input.classList.toggle('cm-cicy-search-invalid', !!input.value && !query.valid);
    view.dispatch({ effects: setSearchQuery.of(query) });
    if (opts.scrollToMatch && query.valid && query.search) findNext(view);
  };

  const flipToggle = (btn: HTMLButtonElement) => {
    btn.setAttribute('aria-pressed', btn.getAttribute('aria-pressed') === 'true' ? 'false' : 'true');
    btn.classList.toggle('cm-cicy-search-toggle-on', btn.getAttribute('aria-pressed') === 'true');
    commitQuery();
    input.focus();
  };

  caseBtn.onclick = () => flipToggle(caseBtn);
  wordBtn.onclick = () => flipToggle(wordBtn);
  reBtn.onclick = () => flipToggle(reBtn);

  prevBtn.onclick = () => { findPrevious(view); input.focus(); };
  nextBtn.onclick = () => { findNext(view); input.focus(); };
  allBtn.onclick = () => { selectMatches(view); };
  closeBtn.onclick = () => closeSearchPanel(view);
  replaceBtn.onclick = () => { replaceNext(view); input.focus(); };
  replaceAllBtn.onclick = () => { replaceAll(view); input.focus(); };

  toggleReplaceBtn.onclick = () => {
    const open = replaceRow.style.display !== 'none';
    replaceRow.style.display = open ? 'none' : 'flex';
    toggleReplaceBtn.setAttribute('aria-pressed', open ? 'false' : 'true');
    toggleReplaceBtn.classList.toggle('cm-cicy-search-expand-on', !open);
    (open ? input : replaceInput).focus();
  };

  // Debounce live-search a touch so typing isn't one dispatch per keystroke on
  // big files. Keep it snappy.
  let typingTimer = 0 as unknown as ReturnType<typeof setTimeout>;
  input.addEventListener('input', () => {
    clearTimeout(typingTimer);
    typingTimer = setTimeout(() => commitQuery(), 120);
  });
  replaceInput.addEventListener('input', () => {
    clearTimeout(typingTimer);
    typingTimer = setTimeout(() => commitQuery(), 120);
  });

  const onKey = (e: KeyboardEvent) => {
    if (e.key === 'Enter') {
      e.preventDefault();
      commitQuery();
      if (e.shiftKey) findPrevious(view);
      else findNext(view);
    } else if (e.key === 'Escape') {
      e.preventDefault();
      closeSearchPanel(view);
      view.focus();
    } else if (runScopeHandlers(view, e, 'search-panel')) {
      e.preventDefault();
    }
  };
  input.addEventListener('keydown', onKey);
  replaceInput.addEventListener('keydown', onKey);

  const renderCount = () => {
    const q = getSearchQuery(view.state);
    if (!q.search) {
      counter.textContent = '';
      counter.classList.remove('cm-cicy-search-nomatch');
      return;
    }
    if (q.regexp && !q.valid) {
      counter.textContent = '无效正则';
      counter.classList.add('cm-cicy-search-nomatch');
      return;
    }
    const { count, current, capped } = countMatches(view);
    if (count === 0) {
      counter.textContent = '无匹配';
      counter.classList.add('cm-cicy-search-nomatch');
      return;
    }
    counter.classList.remove('cm-cicy-search-nomatch');
    const total = capped ? `${MATCH_COUNT_CAP - 1}+` : `${count}`;
    counter.textContent = current > 0 ? `${current} / ${total}` : `${total} 个`;
  };

  return {
    dom,
    top: true,
    mount() {
      // Seed initial option state from the existing query, then pre-fill the
      // field from the active query or the current selection.
      caseBtn.setAttribute('aria-pressed', String(initial.caseSensitive));
      caseBtn.classList.toggle('cm-cicy-search-toggle-on', initial.caseSensitive);
      wordBtn.setAttribute('aria-pressed', String(initial.wholeWord));
      wordBtn.classList.toggle('cm-cicy-search-toggle-on', initial.wholeWord);
      reBtn.setAttribute('aria-pressed', String(initial.regexp));
      reBtn.classList.toggle('cm-cicy-search-toggle-on', initial.regexp);

      const sel = view.state.selection.main;
      const selText = sel.empty ? '' : view.state.sliceDoc(sel.from, sel.to);
      input.value = initial.search || (selText && !selText.includes('\n') ? selText : '');
      if (input.value && input.value !== initial.search) commitQuery();
      renderCount();
      // Focus + select so the user can immediately type or overwrite.
      input.focus();
      input.select();
    },
    update(u: ViewUpdate) {
      if (u.docChanged || u.selectionSet || u.transactions.some((t) => t.effects.some((e) => e.is(setSearchQuery)))) {
        renderCount();
      }
    },
  };
}

// Themed to match the editor's zinc-950 / oneDark chrome.
const cicySearchTheme = EditorView.theme({
  '.cm-panels': { backgroundColor: 'transparent', color: '#e4e4e7' },
  '.cm-panels.cm-panels-top': { borderBottom: '1px solid rgba(255,255,255,0.06)' },
  '.cm-cicy-search': {
    padding: '6px 8px',
    backgroundColor: '#111113',
    fontSize: '12px',
    display: 'flex',
    flexDirection: 'column',
    gap: '6px',
  },
  '.cm-cicy-search-row': { display: 'flex', alignItems: 'center', gap: '4px' },
  '.cm-cicy-search-spacer': { width: '20px', flexShrink: '0' },
  '.cm-cicy-search-field': {
    flex: '1 1 auto',
    minWidth: '0',
    height: '26px',
    padding: '0 8px',
    borderRadius: '5px',
    border: '1px solid rgba(255,255,255,0.10)',
    backgroundColor: '#1c1c1f',
    color: '#e4e4e7',
    outline: 'none',
    fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace',
  },
  '.cm-cicy-search-field:focus': { borderColor: 'rgba(59,130,246,0.6)' },
  '.cm-cicy-search-invalid': { borderColor: 'rgba(248,113,113,0.7)' },
  '.cm-cicy-search-count': {
    flexShrink: '0',
    minWidth: '54px',
    textAlign: 'right',
    color: 'rgba(228,228,231,0.55)',
    fontVariantNumeric: 'tabular-nums',
    padding: '0 2px',
  },
  '.cm-cicy-search-nomatch': { color: 'rgba(248,113,113,0.85)' },
  '.cm-cicy-search-toggles': { display: 'flex', gap: '2px', flexShrink: '0' },
  '.cm-cicy-search-toggle': {
    height: '24px',
    minWidth: '26px',
    padding: '0 5px',
    borderRadius: '4px',
    border: '1px solid transparent',
    backgroundColor: 'transparent',
    color: 'rgba(228,228,231,0.55)',
    cursor: 'pointer',
    fontFamily: 'ui-monospace, monospace',
    fontSize: '11px',
  },
  '.cm-cicy-search-toggle:hover': { backgroundColor: 'rgba(255,255,255,0.06)', color: '#e4e4e7' },
  '.cm-cicy-search-toggle-on': {
    backgroundColor: 'rgba(59,130,246,0.18)',
    borderColor: 'rgba(59,130,246,0.45)',
    color: '#93c5fd',
  },
  '.cm-cicy-search-btn': {
    height: '24px',
    padding: '0 8px',
    borderRadius: '4px',
    border: '1px solid rgba(255,255,255,0.08)',
    backgroundColor: '#1c1c1f',
    color: 'rgba(228,228,231,0.8)',
    cursor: 'pointer',
    flexShrink: '0',
    fontSize: '12px',
    lineHeight: '1',
  },
  '.cm-cicy-search-btn:hover': { backgroundColor: '#27272a', color: '#fff' },
  '.cm-cicy-search-expand': {
    height: '24px',
    width: '20px',
    flexShrink: '0',
    borderRadius: '4px',
    border: 'none',
    backgroundColor: 'transparent',
    color: 'rgba(228,228,231,0.5)',
    cursor: 'pointer',
    fontSize: '13px',
  },
  '.cm-cicy-search-expand:hover': { backgroundColor: 'rgba(255,255,255,0.06)', color: '#e4e4e7' },
  '.cm-cicy-search-expand-on': { transform: 'rotate(180deg)' },
  // Brighter highlight for the live match decorations so hits are easy to spot
  // against oneDark.
  '.cm-searchMatch': { backgroundColor: 'rgba(250,204,21,0.22)', outline: '1px solid rgba(250,204,21,0.35)' },
  '.cm-searchMatch-selected': { backgroundColor: 'rgba(250,204,21,0.45)' },
});

/** In-file search extension: top-anchored, themed, Chinese, with match counts. */
export function cicySearch() {
  return [search({ top: true, createPanel: createCicySearchPanel }), cicySearchTheme];
}
