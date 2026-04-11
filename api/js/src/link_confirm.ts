function ensureLinkConfirmStyle(doc: Document): void {
    if (doc.getElementById("cicy-link-confirm-style")) {
        return;
    }
    var style = doc.createElement("style");
    style.id = "cicy-link-confirm-style";
    style.textContent = `
        .cicy-link-confirm-overlay {
            position: fixed;
            inset: 0;
            z-index: 2147483646;
            display: flex;
            align-items: center;
            justify-content: center;
            background: rgba(0, 0, 0, 0.55);
            padding: 24px;
            box-sizing: border-box;
        }
        .cicy-link-confirm-modal {
            width: min(520px, 100%);
            border-radius: 14px;
            background: #111214;
            border: 1px solid rgba(255, 255, 255, 0.08);
            box-shadow: 0 24px 80px rgba(0, 0, 0, 0.45);
            padding: 18px;
            color: #f5f5f5;
            font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
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
            padding: 10px 12px;
            border-radius: 10px;
            background: rgba(255, 255, 255, 0.04);
            border: 1px solid rgba(255, 255, 255, 0.06);
            color: #8bd5ff;
            font-size: 12px;
            line-height: 1.5;
            word-break: break-all;
            white-space: pre-wrap;
        }
        .cicy-link-confirm-actions {
            display: flex;
            justify-content: flex-end;
            gap: 10px;
        }
        .cicy-link-confirm-btn {
            appearance: none;
            border: none;
            border-radius: 10px;
            padding: 9px 14px;
            font-size: 13px;
            cursor: pointer;
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

export function openExternalLinkWithConfirm(doc: Document, rawUrl: string): void {
    var url = String(rawUrl || "").trim();
    if (!url) {
        return;
    }

    ensureLinkConfirmStyle(doc);

    var existing = doc.getElementById("cicy-link-confirm-overlay");
    if (existing && existing.parentNode) {
        existing.parentNode.removeChild(existing);
    }

    var overlay = doc.createElement("div");
    overlay.id = "cicy-link-confirm-overlay";
    overlay.className = "cicy-link-confirm-overlay";

    var modal = doc.createElement("div");
    modal.className = "cicy-link-confirm-modal";

    var title = doc.createElement("h3");
    title.className = "cicy-link-confirm-title";
    title.textContent = "打开链接";

    var desc = doc.createElement("p");
    desc.className = "cicy-link-confirm-desc";
    desc.textContent = "是否打开这个网址？";

    var urlBlock = doc.createElement("div");
    urlBlock.className = "cicy-link-confirm-url";
    urlBlock.textContent = url;

    var actions = doc.createElement("div");
    actions.className = "cicy-link-confirm-actions";

    var cancelBtn = doc.createElement("button");
    cancelBtn.className = "cicy-link-confirm-btn cicy-link-confirm-btn-cancel";
    cancelBtn.textContent = "取消";

    var openBtn = doc.createElement("button");
    openBtn.className = "cicy-link-confirm-btn cicy-link-confirm-btn-open";
    openBtn.textContent = "打开";

    actions.appendChild(cancelBtn);
    actions.appendChild(openBtn);
    modal.appendChild(title);
    modal.appendChild(desc);
    modal.appendChild(urlBlock);
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

    openBtn.addEventListener("click", function(): void {
        close();
        var win = window.open(url, "_blank");
        if (win) {
            win.focus();
        }
    });

    doc.addEventListener("keydown", onKeyDown, true);
    openBtn.focus();
}
