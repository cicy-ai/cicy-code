export const WEB_FRAME_MASK_EVENT = 'cicy:webframe-mask';

export interface WebFrameMaskEventDetail {
  action: 'start' | 'end';
  key: string;
  reason: 'window-drag' | 'window-resize' | 'canvas-drag' | 'canvas-zoom';
}

export function emitWebFrameMaskEvent(detail: WebFrameMaskEventDetail) {
  window.dispatchEvent(new CustomEvent(WEB_FRAME_MASK_EVENT, { detail }));
}
