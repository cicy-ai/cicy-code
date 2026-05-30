// Tiny i18n helper for the ttyd UI side.
// The hosting app passes ?lang=<code> through the WebFrame URL; we
// fall back to navigator.language when the query string is missing,
// and to 'en' if no match.

type Lang = "en" | "zh-CN";

function detectLang(): Lang {
  try {
    const params = new URLSearchParams(window.location.search);
    const raw = (params.get("lang") || "").trim();
    if (raw === "zh-CN" || raw === "zh") return "zh-CN";
    if (raw === "en" || raw.startsWith("en-")) return "en";
    const nav = (navigator.language || "").trim();
    if (nav.startsWith("zh")) return "zh-CN";
    return "en";
  } catch {
    return "en";
  }
}

const STRINGS: Record<Lang, Record<string, string>> = {
  en: {
    tipAddCliWindow: "New CLI window\n\nOpen a fresh tmux window in this session.",
    tipRestartAgent: "Restart pane (full)\n\nKills the tmux pane and respawns it from\nscratch. Loses scrollback. Use when the\nshell is wedged.",
    tipLaunchAgent: "Launch Agent\n\nRe-source .cicy/boot.sh,\nlaunch Agent.",
    tipUpdateAgent: "Update Agent\n\nUpdate Agent to the latest official release.",
    tipReloadPage: "Reload page\n\nReload this cicy-code UI tab.",
    tipPromptArea: "Prompt input area\n\nClick to focus the bottom prompt box —\ntype + Enter sends to the agent.\nUse this when the slow-network server-side\nIME makes typing directly in the terminal\nunreliable.",
    confirmLaunchAgent: "Re-source `.cicy/boot.sh` in this pane? Env vars get refreshed and {agent} restarts.",
    confirmUpdateAgent: "Run `npm install -g <pkg>@latest` for {agent} in this pane? Progress prints live in the terminal.",
    confirmRestartAgent: "Kill and respawn this tmux pane? Scrollback is lost.",
    // Echoed in the update window after npm install succeeds — the install
    // runs async and the agent in the original pane is still on the old
    // version, so the user has to take a manual restart step. Sent to the
    // server as request body so localization stays in one place (here).
    updateCompleteRestartHint: "✅ Update complete — click the ▶ Launch button in the top bar to restart {agent} with the new version.",
    restartPaneTitle: "Restart pane",
    launchAgentTitle: "Launch {agent}",
    updateAgentTitle: "Update {agent}",
    actionRestart: "Restart",
    actionLaunch: "Launch",
    actionUpdate: "Update",
    promptAreaPlaceholder: "Enter your prompt…",
    enterSendPromptEnter: "Send prompt with: Enter",
    enterSendPromptShiftEnter: "Send prompt with: Shift+Enter",
    closeCliWindow: "Close CLI window",
    clickAgainToConfirm: "Click again to confirm closing the CLI window",
    restartingAgent: "Restarting agent",
    uploadContent: "Upload",
    singleFileCount: "1 file",
    multiFileCount: "{n} files",
    fileName: "File name",
    fileType: "Type",
    fileSize: "Size",
    fileCount: "Files",
    totalSize: "Total size",
    sendPastedImage: "Send pasted image",
    sendPastedFiles: "Send pasted files",
    confirmUploadHint: "After confirming, the files are uploaded and the file:// URLs are written into the terminal.",
    cancel: "Cancel",
    send: "Send",
    sendPastedText: "Send pasted text",
    pastedTextDetected: "Detected pasted text. Confirm to send it straight to the terminal.",
    openFile: "📄 Open file",
    openLink: "Open link",
    openLinkPrompt: "Open this URL?",
    openLinkAction: "Open link",
    imageFile: "Image file",
    imagePreviewHint: "Preview this image or download directly.",
    close: "Close",
    download: "Download",
    fileDownload: "File download",
    fileDownloadHint: "Click to download this file.",
    filePath: "File path",
    filePathHint: "Detected a file path in the terminal.",
    open: "Open",
    windowConfirmDelete: "Close window {idx}?",
    confirm: "Confirm",
    imagePasteEyebrow: "Image Paste",
    filePasteEyebrow: "File Paste",
    voiceMode: "Voice Mode",
  },
  "zh-CN": {
    tipAddCliWindow: "新建 CLI Window\n\n在当前 session 新开一个 tmux window。",
    tipRestartAgent: "重启整个 Pane\n\n销毁当前 tmux pane 后从头重建,\n滚动历史会丢失。Shell 卡死时用。",
    tipLaunchAgent: "启动 Agent\n\n重新 source .cicy/boot.sh,\n启动 Agent。",
    tipUpdateAgent: "更新 Agent\n\n更新 Agent 到官方最新版。",
    tipReloadPage: "刷新页面\n\n刷新当前 cicy-code UI 标签。",
    tipPromptArea: "Prompt 输入区\n\n点这里聚焦底部 Prompt 输入框,\n输入后回车直接发给 agent。\n用于网络较慢时服务端输入法\n在终端里直接打字不稳的情况。",
    confirmLaunchAgent: "在当前 pane 重新 source `.cicy/boot.sh`? 环境变量会刷新,{agent} 重新启动。",
    confirmUpdateAgent: "在当前 pane 执行 npm install -g <pkg>@latest 更新 {agent}? 实时进度直接打在终端里。",
    confirmRestartAgent: "销毁并重建当前 tmux pane?滚动历史会丢失。",
    updateCompleteRestartHint: "✅ 升级完成 — 点击顶栏的 ▶ 启动按钮重启 {agent} 启用新版本。",
    restartPaneTitle: "重启 Pane",
    launchAgentTitle: "启动 {agent}",
    updateAgentTitle: "更新 {agent}",
    actionRestart: "重启",
    actionLaunch: "启动",
    actionUpdate: "更新",
    promptAreaPlaceholder: "请输入提示词",
    enterSendPromptEnter: "发送Prompt方式:Enter",
    enterSendPromptShiftEnter: "发送Prompt方式:Shift+Enter",
    closeCliWindow: "关闭CLI Window",
    clickAgainToConfirm: "再点一次确认关闭CLI Window",
    restartingAgent: "重启智能体中",
    uploadContent: "上传内容",
    singleFileCount: "1 个文件",
    multiFileCount: "{n} 个文件",
    fileName: "文件名",
    fileType: "类型",
    fileSize: "大小",
    fileCount: "文件数",
    totalSize: "总大小",
    sendPastedImage: "发送粘贴图片",
    sendPastedFiles: "发送粘贴文件",
    confirmUploadHint: "确认后上传这些文件，并在终端里写入 file:// 地址。",
    cancel: "取消",
    send: "发送",
    sendPastedText: "发送粘贴内容",
    pastedTextDetected: "检测到粘贴文本，确认后将直接发送到终端。",
    openFile: "📄 打开文件",
    openLink: "打开链接",
    openLinkPrompt: "是否打开这个网址？",
    openLinkAction: "打开链接",
    imageFile: "图片文件",
    imagePreviewHint: "预览这张图片，或直接下载。",
    close: "关闭",
    download: "下载",
    fileDownload: "文件下载",
    fileDownloadHint: "点击下载这个文件。",
    filePath: "文件路径",
    filePathHint: "终端里检测到一个文件路径。",
    open: "打开",
    windowConfirmDelete: "关闭 Window {idx}?",
    confirm: "确定",
    imagePasteEyebrow: "图片粘贴",
    filePasteEyebrow: "文件粘贴",
    voiceMode: "语音模式",
  },
};

export const cicyLang: Lang = detectLang();

export function ttydT(key: string, params?: Record<string, string | number>): string {
  const table = STRINGS[cicyLang] || STRINGS.en;
  let value = table[key] ?? STRINGS.en[key] ?? key;
  if (params) {
    for (const k of Object.keys(params)) {
      value = value.replace("{" + k + "}", String(params[k]));
    }
  }
  return value;
}
