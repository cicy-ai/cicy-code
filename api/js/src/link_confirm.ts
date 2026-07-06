// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

import { applyMonoFontVar } from "./font";
import { ttydT } from "./cicy_i18n";

// Sentinel prefix the gotty guest prints on `console.log` to ask the host to
// open a file in the code editor. The host's WebFrame `console-message`
// listener matches this exact string and forwards the JSON tail. Kept in sync
// with app/src/components/WebFrame.tsx (CODE_FILE_CONSOLE_SENTINEL).
export const CODE_FILE_CONSOLE_SENTINEL = "[[CICY_OPEN_CODE_FILE]]";

function ensureLinkConfirmStyle(doc: Document): void {
    if (doc.getElementById("cicy-link-confirm-style")) {
        return;
    }
    applyMonoFontVar(doc);
    var style = doc.createElement("style");
    style.id = "cicy-link-confirm-style";
    style.textContent = `
        .cicy-link-confirm-overlay {
            position: fixed;
            inset: 0;
            z-index: 2147483646;
            display: flex;
            align-items: center;
            justify-content: flex-start;
            background: rgba(0, 0, 0, 0.55);
            padding: 24px;
            box-sizing: border-box;
        }
        .cicy-link-confirm-modal {
            width: min(520px, 100%);
            margin-left: 0;
            border-radius: 14px;
            background: #111214;
            border: 1px solid rgba(255, 255, 255, 0.08);
            box-shadow: 0 24px 80px rgba(0, 0, 0, 0.45);
            padding: 18px;
            color: #f5f5f5;
            font-family: var(--cp-mono-font);
        }
        .cicy-link-confirm-title {
            margin: 0 0 10px;
            font-size: 16px;
            font-weight: 600;
        }
        .cicy-link-confirm-desc {
            margin: 0 0 12px;
            font-size: 13px;
            line-height: 1.5;
            color: rgba(255, 255, 255, 0.72);
        }
        .cicy-link-confirm-url {
            margin: 0 0 16px;
            padding: 12px;
            border-radius: 10px;
            background: rgba(255, 255, 255, 0.04);
            border: 1px solid rgba(255, 255, 255, 0.06);
            font-size: 12px;
            line-height: 1.6;
            word-break: break-all;
            color: rgba(255, 255, 255, 0.86);
        }
        .cicy-link-confirm-preview {
            margin: 0 0 16px;
            padding: 12px;
            border-radius: 10px;
            background: rgba(255, 255, 255, 0.04);
            border: 1px solid rgba(255, 255, 255, 0.06);
        }
        .cicy-link-confirm-preview img {
            display: block;
            max-width: 100%;
            max-height: min(60vh, 560px);
            margin: 0 auto;
            border-radius: 8px;
            background: rgba(255, 255, 255, 0.03);
        }
        .cicy-link-confirm-actions {
            display: flex;
            justify-content: flex-start;
            gap: 10px;
        }
        .cicy-link-confirm-btn {
            appearance: none;
            border: none;
            border-radius: 10px;
            padding: 9px 14px;
            font-size: 13px;
            cursor: pointer;
            font-family: var(--cp-mono-font);
        }
        .cicy-link-confirm-btn-cancel {
            background: rgba(255, 255, 255, 0.08);
            color: rgba(255, 255, 255, 0.86);
        }
        .cicy-link-confirm-btn-open {
            background: #2f6df6;
            color: #fff;
        }
    `;
    doc.head.appendChild(style);
}

function removeExistingOverlay(doc: Document): void {
    var existing = doc.getElementById("cicy-link-confirm-overlay");
    if (existing && existing.parentNode) {
        existing.parentNode.removeChild(existing);
    }
}

function clearSelection(doc: Document): void {
    var selection = doc.getSelection();
    if (selection) {
        try {
            selection.removeAllRanges();
        } catch (_error) {
        }
    }
}

function downloadURL(doc: Document, url: string): void {
    var anchor = doc.createElement("a");
    anchor.href = url;
    anchor.download = "";
    anchor.rel = "noopener";
    anchor.target = "_blank";
    doc.body.appendChild(anchor);
    anchor.click();
    if (anchor.parentNode) {
        anchor.parentNode.removeChild(anchor);
    }
}

function stripResourceProtocolPrefix(rawValue: string): string {
    var text = String(rawValue || "").trim();
    if (!text) {
        return "";
    }
    if (text.indexOf("file://") === 0) {
        text = text.slice(7);
    } else if (text.indexOf("image://") === 0) {
        text = text.slice(8);
    }
    return text;
}

function isAssetFileProtocolTarget(doc: Document, rawValue: string): boolean {
    var text = stripResourceProtocolPrefix(rawValue);
    if (!text) {
        return false;
    }
    if (/^https?:\/\//i.test(text)) {
        try {
            return new URL(text).pathname.indexOf("/assets/") === 0;
        } catch (_error) {
            return false;
        }
    }
    var view = doc.defaultView;
    if (text.indexOf("/assets/") === 0 || text.indexOf("assets/") === 0) {
        return true;
    }
    if (view && text.indexOf(view.location.host + "/assets/") === 0) {
        return true;
    }
    return false;
}

function resolveFileProtocolURL(doc: Document, rawValue: string): string {
    var text = stripResourceProtocolPrefix(rawValue);
    if (!text) {
        return "";
    }
    if (/^https?:\/\//i.test(text)) {
        return text;
    }
    var view = doc.defaultView;
    if (!view) {
        return text;
    }
    if (text.charAt(0) === "/") {
        return view.location.origin + text;
    }
    if (text.indexOf(view.location.host + "/") === 0) {
        return view.location.protocol + "//" + text;
    }
    return text;
}

function resolveLocalFileProtocolPath(doc: Document, rawValue: string): string {
    var text = stripResourceProtocolPrefix(rawValue);
    if (!text || isAssetFileProtocolTarget(doc, rawValue) || /^https?:\/\//i.test(text)) {
        return "";
    }
    var view = doc.defaultView;
    if (view && text.indexOf(view.location.host + "/") === 0) {
        text = text.slice(view.location.host.length);
    }
    if (/^[A-Za-z]:[\\/]/.test(text)) {
        return text;
    }
    if (text.charAt(0) === "/" || text.indexOf("~/") === 0 || text.indexOf("./") === 0 || text.indexOf("../") === 0) {
        return text;
    }
    return "/" + text.replace(/^\/+/, "");
}

function isImageFileURL(url: string): boolean {
    return /\.(png|apng|jpe?g|gif|webp|bmp|svg)(?:$|[?#])/i.test(url);
}

function copyText(doc: Document, text: string): void {
    var value = String(text || "");
    var win = doc.defaultView as (Window & typeof globalThis) | null;
    if (win && win.navigator && win.navigator.clipboard && typeof win.navigator.clipboard.writeText === "function") {
        win.navigator.clipboard.writeText(value).catch(function(): void {});
        return;
    }
    var textarea = doc.createElement("textarea");
    textarea.value = value;
    textarea.setAttribute("readonly", "readonly");
    textarea.style.position = "fixed";
    textarea.style.opacity = "0";
    textarea.style.pointerEvents = "none";
    doc.body.appendChild(textarea);
    textarea.focus();
    textarea.select();
    try {
        doc.execCommand("copy");
    } catch (_error) {
    }
    if (textarea.parentNode) {
        textarea.parentNode.removeChild(textarea);
    }
}

function mountConfirmOverlay(doc: Document, options: {
    title: string;
    description: string;
    bodyText: string;
    cancelText: string;
    confirmText: string;
    previewImageURL?: string;
    onConfirm: () => void;
}): void {
    ensureLinkConfirmStyle(doc);
    removeExistingOverlay(doc);
    clearSelection(doc);

    var overlay = doc.createElement("div");
    overlay.id = "cicy-link-confirm-overlay";
    overlay.className = "cicy-link-confirm-overlay";

    var modal = doc.createElement("div");
    modal.className = "cicy-link-confirm-modal";

    var title = doc.createElement("h3");
    title.className = "cicy-link-confirm-title";
    title.textContent = options.title;

    var desc = doc.createElement("p");
    desc.className = "cicy-link-confirm-desc";
    desc.textContent = options.description;

    var previewBlock: HTMLDivElement | null = null;
    if (options.previewImageURL) {
        previewBlock = doc.createElement("div");
        previewBlock.className = "cicy-link-confirm-preview";
        var previewImage = doc.createElement("img");
        previewImage.src = options.previewImageURL;
        previewImage.alt = options.title;
        previewBlock.appendChild(previewImage);
    }

    var bodyBlock = doc.createElement("div");
    bodyBlock.className = "cicy-link-confirm-url";
    bodyBlock.textContent = options.bodyText;

    var actions = doc.createElement("div");
    actions.className = "cicy-link-confirm-actions";

    var cancelBtn = doc.createElement("button");
    cancelBtn.className = "cicy-link-confirm-btn cicy-link-confirm-btn-cancel";
    cancelBtn.textContent = options.cancelText;

    var confirmBtn = doc.createElement("button");
    confirmBtn.className = "cicy-link-confirm-btn cicy-link-confirm-btn-open";
    confirmBtn.textContent = options.confirmText;

    actions.appendChild(confirmBtn);
    actions.appendChild(cancelBtn);
    modal.appendChild(title);
    modal.appendChild(desc);
    if (previewBlock) {
        modal.appendChild(previewBlock);
    }
    modal.appendChild(bodyBlock);
    modal.appendChild(actions);
    overlay.appendChild(modal);
    doc.body.appendChild(overlay);

    function close(): void {
        doc.removeEventListener("keydown", onKeyDown, true);
        if (overlay.parentNode) {
            overlay.parentNode.removeChild(overlay);
        }
    }

    function onKeyDown(event: KeyboardEvent): void {
        if (event.key === "Escape") {
            event.preventDefault();
            close();
        }
    }

    overlay.addEventListener("click", function(event: MouseEvent): void {
        if (event.target === overlay) {
            close();
        }
    });

    cancelBtn.addEventListener("click", function(): void {
        close();
    });

    confirmBtn.addEventListener("click", function(): void {
        close();
        options.onConfirm();
    });

    doc.addEventListener("keydown", onKeyDown, true);
    confirmBtn.focus();
}

function openLocalFilePath(doc: Document, filePath: string): void {
    var view = doc.defaultView;
    if (view && view.parent && view.parent !== view) {
        try {
            var parentOpen = (view.parent as any).__cicyOpenCodeFile;
            if (typeof parentOpen === "function") {
                parentOpen(filePath);
                return;
            }
        } catch (_error) {
        }
        try {
            view.parent.postMessage({
                type: "cicy-open-code-file",
                path: filePath,
            }, "*");
            return;
        } catch (_error) {
        }
    }
    // Electron <webview> guest: the gotty terminal runs in its own top-level
    // WebContents (window.parent === window), so the iframe hooks above never
    // fire. The host WebFrame wrapper listens to this guest's `console-message`
    // event — emit a sentinel line it parses and forwards to
    // __cicyOpenCodeFile. Cheap, needs no preload/nodeintegration.
    try {
        if (view) {
            (view.console || console).log(CODE_FILE_CONSOLE_SENTINEL + JSON.stringify({ path: filePath }));
        }
    } catch (_error) {
    }
    var tokenMatch = doc.defaultView && doc.defaultView.location ? doc.defaultView.location.search.match(/[?&]token=([^&]+)/) : null;
    var token = tokenMatch ? decodeURIComponent(tokenMatch[1]) : "";
    var headers: Record<string, string> = {
        "Content-Type": "application/json",
    };
    if (token) {
        headers.Authorization = "Bearer " + token;
    }
    fetch("/api/notify", {
        method: "POST",
        headers,
        body: JSON.stringify({
            action: "open_file",
            file: filePath,
            message: ttydT("openFile"),
        }),
    }).catch(function(): void {});
}

export function openExternalLinkWithConfirm(doc: Document, rawUrl: string): void {
    var url = String(rawUrl || "").trim();
    if (!url) {
        return;
    }

    mountConfirmOverlay(doc, {
        title: ttydT("openLink"),
        description: ttydT("openLinkPrompt"),
        bodyText: url,
        cancelText: ttydT("cancel"),
        confirmText: ttydT("openLinkAction"),
        onConfirm: function(): void {
            var win = window.open(url, "_blank");
            if (win) {
                win.focus();
            }
        },
    });
}

export function openFileProtocolLink(doc: Document, rawValue: string): void {
    var fileRef = String(rawValue || "").trim();
    if (!fileRef) {
        return;
    }
    var resolvedURL = resolveFileProtocolURL(doc, fileRef);
    if (!resolvedURL) {
        return;
    }
    if (isImageFileURL(resolvedURL)) {
        mountConfirmOverlay(doc, {
            title: ttydT("imageFile"),
            description: ttydT("imagePreviewHint"),
            bodyText: fileRef,
            cancelText: ttydT("close"),
            confirmText: ttydT("download"),
            previewImageURL: resolvedURL,
            onConfirm: function(): void {
                downloadURL(doc, resolvedURL);
            },
        });
        return;
    }
    mountConfirmOverlay(doc, {
        title: ttydT("fileDownload"),
        description: ttydT("fileDownloadHint"),
        bodyText: fileRef,
        cancelText: ttydT("close"),
        confirmText: ttydT("download"),
        onConfirm: function(): void {
            downloadURL(doc, resolvedURL);
        },
    });
}

export function openFileReferencePopup(doc: Document, rawPath: string): void {
    var originalPath = String(rawPath || "").trim();
    if (!originalPath) {
        return;
    }
    var filePath = originalPath;
    if (originalPath.indexOf("file://") === 0 || originalPath.indexOf("image://") === 0) {
        if (isAssetFileProtocolTarget(doc, originalPath)) {
            openFileProtocolLink(doc, originalPath);
            return;
        }
        var localFilePath = resolveLocalFileProtocolPath(doc, originalPath);
        if (!localFilePath) {
            return;
        }
        filePath = localFilePath;
    }

    mountConfirmOverlay(doc, {
        title: ttydT("filePath"),
        description: ttydT("filePathHint"),
        bodyText: originalPath,
        cancelText: ttydT("close"),
        confirmText: ttydT("open"),
        onConfirm: function(): void {
            openLocalFilePath(doc, filePath);
        },
    });
}
