# Gotty 终端文本选择/复制失效问题

## 日期
2026-03-10

## 症状
浏览器中 gotty 终端无法用鼠标选中文字、无法复制。

## 根因
tmux `set -g mouse on` → pty 发送 `\x1b[?1000h` → xterm.js `InputHandler.setMode()` 执行：
- `terminal.mouseEvents = true` → 鼠标事件被 xterm.js 拦截
- `terminal.selectionManager.disable()` → 选择功能被禁用

仅在 websocket 层过滤鼠标序列（前端 `onInput` + 后端 `filterDAQuery`）不够，因为 xterm.js 在收到 escape sequence 时就已经在内部禁用了选择。

## 修复
`js/src/xterm.ts`，在 `this.term.open(elem, true)` 之后：

```typescript
Object.defineProperty(this.term, 'mouseEvents', {
    get: () => false,
    set: () => {},
});
if (this.term.selectionManager) {
    this.term.selectionManager.disable = () => {};
}
```

## 原理
- `defineProperty` 让 `mouseEvents` 永远返回 `false`，`setMode` 写不进去
- `selectionManager.disable` 被替换为空函数，`setMode` 调用无效
- 这样 xterm.js 的 copy handler 始终走 `copyHandler` 分支，鼠标事件不被拦截

## 构建
```bash
cd js && npx webpack && cp dist/gotty-bundle.js ../static/gotty-bundle.js
```
`ws.go` 用 `?v=timestamp` 自动 bust 浏览器缓存。

## 防御层级（三层）
1. **xterm.ts defineProperty** — 阻止 xterm.js 进入鼠标模式（根本修复）
2. **xterm.ts onInput** — 过滤鼠标序列不发给 websocket
3. **ws.go filterDAQuery** — 后端过滤鼠标序列不转发给 tmux
