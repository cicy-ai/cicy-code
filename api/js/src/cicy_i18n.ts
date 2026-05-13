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
    tipAddCliWindow: "Add CLI window",
    tipRestartAgent: "Restart agent",
    tipReloadPage: "Reload page",
    tipPromptArea: "Prompt input area",
    promptAreaPlaceholder: "请输入提示词",
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
  },
  "zh-CN": {
    tipAddCliWindow: "新加CLI Window",
    tipRestartAgent: "重启智能体",
    tipReloadPage: "刷新页面",
    tipPromptArea: "Prompt 输入区",
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
