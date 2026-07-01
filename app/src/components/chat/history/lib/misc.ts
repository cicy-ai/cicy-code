import type { HistoryTurn } from '../types';

// Relative "x 分钟前" label for a prompt's ISO timestamp (RFC3339). Falls back to
// the local date string for anything older than ~30 days, '' for unparseable.
export function formatPromptTimeAgo(iso: string): string {
  const t = Date.parse(iso);
  if (!Number.isFinite(t)) return '';
  const diff = Date.now() - t;
  if (diff < 0) return '刚刚';
  if (diff < 60_000) return '刚刚';
  if (diff < 3_600_000) return `${Math.floor(diff / 60_000)} 分钟前`;
  if (diff < 86_400_000) return `${Math.floor(diff / 3_600_000)} 小时前`;
  if (diff < 30 * 86_400_000) return `${Math.floor(diff / 86_400_000)} 天前`;
  return new Date(t).toLocaleDateString();
}

export function isActiveAssistantStatus(status: string) {
  const value = String(status || '').trim().toLowerCase();
  return value === 'thinking' || value === 'working' || value === 'tool_use' || value === 'tool_call' || value === 'streaming';
}

// live 尾巴步骤的内容规模(text/thinking 总字符数 + tool 数)。WS 直推(cicy)下本地
// 尾巴可能比 reply.json 快照更超前 —— 替换守卫用它实现"同一 turn 内容只前进不回退"。
export function liveStepsContentSize(steps: HistoryTurn['steps']): { textLen: number; toolCount: number } {
  let textLen = 0;
  let toolCount = 0;
  for (const s of (steps || []) as any[]) {
    if (s?.type === 'text' || s?.type === 'thinking') textLen += String(s?.text || '').length;
    else if (s?.type === 'tool') toolCount += Array.isArray(s?.tools) ? s.tools.length : 0;
  }
  return { textLen, toolCount };
}

export function scheduleScrollToBottom(el: HTMLDivElement) {
  const apply = () => {
    el.scrollTop = el.scrollHeight;
  };
  apply();
  const raf = window.requestAnimationFrame(apply);
  const timers = [80, 240, 600, 1200, 2000].map((delay) => window.setTimeout(apply, delay));
  return { raf, timers };
}

export function isExternalUrl(href: string): boolean {
  return /^(https?:)?\/\//i.test(href) || /^mailto:/i.test(href);
}

// 上传附件在消息里发的是**绝对路径**(LLM/agent 据此 Read 文件)。图片要在 UI 里预览,
// 就把绝对路径(如 /home/cicy/cicy-ai/workers/w-1001/assets/2026/…/x.png)解析成公开的取图
// URL(/assets/files/<pane>/<rel>,无 token)。非附件路径/普通 URL 原样返回。
export function assetAbsPathToURL(src: string): string {
  const s = String(src || '');
  // 共享存储:绝对路径形如 /home/cicy/cicy-ai/assets/<date>/<hash>__name → 取 /cicy-ai/assets/
  // 后面的 rel,映射成公开取图 URL /assets/files/<rel>。已是 /assets/files/ URL 或外链则原样。
  const m = '/cicy-ai/assets/';
  const i = s.indexOf(m);
  if (i >= 0) {
    const rel = s.slice(i + m.length);
    if (rel) return `/assets/files/${rel}`;
  }
  return s;
}
