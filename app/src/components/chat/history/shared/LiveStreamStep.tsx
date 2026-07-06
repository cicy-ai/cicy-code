// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

import { memo, useEffect, useLayoutEffect, useRef, useState } from 'react';
import { MarkdownBlock } from './Markdown';
import { ThinkingBlock } from './ThinkingBlock';

// 把"每 ~500ms 轮询一次、整块替换"的流式文本平滑成逐字增长 —— 否则每个 poll 一大坨
// 文字"啪"地出现,看着像蹦字。poll 给的是「目标全文」,这里用 rAF 让显示长度按指数
// 逼近目标(剩得越多走得越快,poll 间隔内必然追平,绝不越拉越远),在两次 poll 之间
// 把那一坨摊成连续生长,观感连续。
// 关键:只对「正在流式输出的 live 尾巴的块」用。挂载瞬间直接 snap 到当前全文;换流
// (切 agent / 新一轮 / 重开)或非流式(已完成)一律瞬时显示(snap),绝不补演历史
// 回放 —— 那正是之前修掉的"打字进场"坑。
const SMOOTH_TICK_MS = 33;
export function useSmoothStreamText(target: string, smooth: boolean): string {
  const [shown, setShown] = useState(target);
  const shownRef = useRef(target);
  useEffect(() => {
    // 非流式 / 目标不是当前显示的前缀延伸(换流、整块替换、回退)→ 瞬时 snap。
    if (!smooth || !target.startsWith(shownRef.current)) {
      shownRef.current = target;
      setShown(target);
      return;
    }
    if (target === shownRef.current) return;
    let raf = 0;
    let last = 0;
    const tick = (now: number) => {
      if (now - last >= SMOOTH_TICK_MS) {
        last = now;
        const cur = shownRef.current.length;
        const remain = target.length - cur;
        if (remain <= 0) return;
        const step = Math.max(2, Math.ceil(remain * 0.12));
        const next = target.slice(0, cur + step);
        shownRef.current = next;
        setShown(next);
        if (next.length >= target.length) return;
      }
      raf = window.requestAnimationFrame(tick);
    };
    raf = window.requestAnimationFrame(tick);
    return () => window.cancelAnimationFrame(raf);
  }, [target, smooth]);
  return smooth ? shown : target;
}

// live 尾巴里的一个 thinking / text 块。smooth=true(本轮仍在流式且是最后一个块)时
// 文本走平滑生长;每长一点回调 onGrow 让父级在贴底时跟一次底 —— 生长发生在本组件
// 内部 state,父级的贴底 useLayoutEffect(依赖 liveTurn)看不到这些 tick。
export const LiveStreamStep = memo(function LiveStreamStep({ kind, text, smooth, dataId, onGrow }: {
  kind: 'text' | 'thinking';
  text: string;
  smooth: boolean;
  dataId?: string;
  onGrow?: () => void;
}) {
  const shown = useSmoothStreamText(text, smooth);
  useLayoutEffect(() => { onGrow?.(); }, [shown, onGrow]);
  if (kind === 'thinking') return <ThinkingBlock text={shown} live={true} />;
  return (
    <div data-id={dataId} className="chat-markdown current-history-markdown text-sm leading-[1.7] text-zinc-300">
      <MarkdownBlock text={shown} />
    </div>
  );
});
