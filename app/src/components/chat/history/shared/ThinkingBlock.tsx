import { useState } from 'react';
import { ChevronRight } from 'lucide-react';
import { MarkdownBlock } from './Markdown';

export function ThinkingBlock({ text, live = false }: { text: string; live?: boolean }) {
  // 两态,不再做 3 行折叠:
  // - live(正在流式输出这一轮):强制全展开、无折叠 —— 实时看它思考。
  // - 完成态(committed / 新 q 之前):整块折叠成一个 "thinking" 小标,默认收起,点开看全文。
  const [expanded, setExpanded] = useState(false);
  const open = live || expanded;
  return (
    <div data-id="current-history-view-thinking-block" className="mb-2 border-l-2 border-amber-300/25 pl-3">
      {/* 完成态留一个可点开的小标;流式期不显示开关(强制展开,不可折叠)。 */}
      {!live ? (
        <button
          type="button"
          data-id="current-history-view-thinking-block-toggle"
          onClick={() => setExpanded((v) => !v)}
          aria-label={expanded ? 'collapse thinking' : 'expand thinking'}
          className="inline-flex items-center gap-1 rounded px-0.5 py-0.5 text-[11px] italic text-zinc-600 transition-colors hover:bg-white/[0.04] hover:text-zinc-400"
        >
          <ChevronRight className={`h-3 w-3 transition-transform ${expanded ? 'rotate-90' : ''}`} />
          thinking
        </button>
      ) : null}
      {/* thinking 要和正文区分:小一号(text-xs)、斜体、更暗的颜色。颜色用内联 style 强制 ——
          .chat-markdown{color:#d4d4d8} 是非分层规则,会盖掉 Tailwind 的 text-zinc-* 工具类,
          所以必须内联(内联优先级高于样式表类规则),<p> 子元素再继承这个颜色。 */}
      {open ? (
        <div
          data-id="current-history-view-thinking-block-body"
          className="chat-markdown current-history-markdown text-xs italic leading-[1.7]"
          style={{ color: '#52525b' }}
        >
          <MarkdownBlock text={text} />
        </div>
      ) : null}
    </div>
  );
}
