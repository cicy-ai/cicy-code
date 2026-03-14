---
inclusion: always
---

# 前端开发规范

## data-id 命名规范

所有关键 DOM 元素必须添加 `data-id` 属性，用于快速定位和沟通。

**规则：**
- 使用 kebab-case：`data-id="drag-handle"`
- 语义化命名，描述元素用途而非样式
- 容器用名词：`desktop-canvas`, `drawer`, `top-bar`
- 交互元素加动词/功能：`drag-handle`, `resizer`, `global-drag-mask`
- 子元素用父级前缀：`drawer-tabs`, `drawer-content`, `drawer-history`

**必须加 data-id 的元素：**
- 页面根容器
- 布局区块（顶栏、侧栏、主内容区）
- 可拖拽/可交互的容器和手柄
- 浮窗、弹窗、遮罩层
- Tab 容器和内容区
- 动态显示/隐藏的区域

**不需要加的：**
- 纯样式装饰（背景纹理等）
- 列表中的重复项（用 key 即可）
- 第三方组件内部

**已有 data-id 清单 (AgentPage.tsx)：**

| data-id | 元素 |
|---------|------|
| `agent-page` | 根容器 |
| `top-bar` | 顶栏 |
| `desktop-canvas` | 桌面画布 |
| `desktop-bg` | 桌面背景 |
| `app-grid` | 应用图标网格 |
| `settings-float` | 设置浮窗 |
| `draggable-box` | 可拖拽 CommandPanel 容器 |
| `drag-handle` | 拖拽手柄 |
| `drag-overlay` | 拖拽遮罩 |
| `drawer` | 右侧抽屉 |
| `drawer-tabs` | 抽屉 tab 栏 |
| `drawer-content` | 抽屉内容区 |
| `drawer-history` | History tab |
| `drawer-terminal` | Terminal tab |
| `resizer` | 宽度调节条 |
| `global-drag-mask` | 全局拖拽遮罩 |
| `toast` | 提示消息 |

## 组件规范

- 新组件必须给根元素加 `data-id`
- 浮动面板/弹窗的遮罩层必须加 `data-id`
- 可拖拽元素的 handle 必须加 `data-id`
