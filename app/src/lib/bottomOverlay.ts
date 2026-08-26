// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

// 底部浮层登记处。状态栏里的 popover(全局代理的「出口 IP」等)是向上展开的,
// 会伸进画布右下角,正好压在 ProjectsPanel 的 FAB 上。两者分属不同组件树,
// z-index 调不出好结果 —— 真正要的是 FAB 往上让位,而不是谁盖住谁。
//
// 所以浮层打开时把自己顶边的**视口坐标**登记进来,FAB 订阅到之后据此上移;
// 关闭时注销,FAB 落回原位。多个浮层同时开就取最靠上的那条边。
//
// 用视口坐标而不是高度:FAB 和浮层不在同一个定位父级里,只有视口坐标能让
// 两边直接比较,布局怎么改都不用同步这里的假设。

const EVENT = 'cicy-bottom-overlay-change';

const tops = new Map<string, number>();

/** 登记(或以 null 注销)一个底部浮层的顶边视口坐标。 */
export function setBottomOverlayTop(id: string, top: number | null): void {
  if (top == null) {
    if (!tops.delete(id)) return;
  } else {
    if (tops.get(id) === top) return;
    tops.set(id, top);
  }
  window.dispatchEvent(new CustomEvent(EVENT));
}

/** 当前最靠上的浮层顶边;没有浮层打开时为 null。 */
export function getBottomOverlayTop(): number | null {
  let top: number | null = null;
  tops.forEach((v) => { if (top == null || v < top) top = v; });
  return top;
}

export function subscribeBottomOverlay(fn: () => void): () => void {
  window.addEventListener(EVENT, fn);
  return () => window.removeEventListener(EVENT, fn);
}
