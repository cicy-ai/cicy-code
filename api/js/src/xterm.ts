import { Terminal as XtermTerminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import { WebLinksAddon } from "@xterm/addon-web-links";
import { lib } from "libapps";
import { applyMonoFontVar } from "./font";
import { openExternalLinkWithConfirm } from "./link_confirm";

const deviceAttributesRe = /\x1b\[\??[\d;]*c/g;
const mouseClickRe = /\x1b\[<(?:0|1|2|3|32|33|34|35);\d+;\d+[Mm]|\x1b\[M[\s\S]{3}/g;
const stripModeSequenceRes = [
    /\x1b\[\?1000[hl]/g,
    /\x1b\[\?1002[hl]/g,
    /\x1b\[\?1003[hl]/g,
    /\x1b\[\?1005[hl]/g,
    /\x1b\[\?1006[hl]/g,
    /\x1b\[\?1015[hl]/g,
    /\x1b\[\?47[hl]/g,
    /\x1b\[\?1047[hl]/g,
    /\x1b\[\?1048[hl]/g,
    /\x1b\[\?1049[hl]/g,
    /\x1b\[3J/g,
];

function flattenParams(params: (number | number[])[]): number[] {
    var values: number[] = [];
    params.forEach((param: number | number[]) => {
        if (Array.isArray(param)) {
            param.forEach((value: number) => values.push(value));
            return;
        }
        values.push(param);
    });
    return values;
}

interface DisposableLike {
    dispose(): void;
}

interface ImageRectOverlay {
    marker: { line: number } | null;
    element: HTMLDivElement;
    widthCells: number;
    heightCells: number;
    col: number;
}

export class Xterm {
    elem: HTMLElement;
    term: XtermTerminal;
    fitAddon: FitAddon;
    decoder: lib.UTF8Decoder;

    message: HTMLElement;
    messageTimeout: number;
    messageTimer: number = 0;
    resizeListener: () => void;
    suppressNextSigintFromCopy: boolean;
    inputDisposable: DisposableLike | null;
    resizeDisposable: DisposableLike | null;
    renderDisposable: DisposableLike | null;
    resizeObserver: ResizeObserver | null;
    imageOverlays: ImageRectOverlay[];
    initialFitDone: boolean;

    constructor(elem: HTMLElement) {
        this.elem = elem;
        applyMonoFontVar(elem.ownerDocument);
        this.suppressNextSigintFromCopy = false;
        this.inputDisposable = null;
        this.resizeDisposable = null;
        this.renderDisposable = null;
        this.resizeObserver = null;
        this.imageOverlays = [];
        this.initialFitDone = false;

        this.term = new XtermTerminal({
            fontSize: 12,
            convertEol: false,
            allowTransparency: true,
            scrollback: 20000,
            fontFamily: "var(--cp-mono-font)",
            theme: {
                background: "#000000",
            },
        });
        this.fitAddon = new FitAddon();
        this.term.loadAddon(this.fitAddon);
        this.term.loadAddon(new WebLinksAddon((event: MouseEvent, uri: string) => {
            event.preventDefault();
            event.stopPropagation();
            if ((event as any).stopImmediatePropagation) {
                (event as any).stopImmediatePropagation();
            }
            openExternalLinkWithConfirm(this.elem.ownerDocument, uri);
        }));

        this.message = elem.ownerDocument.createElement("div");
        this.message.className = "xterm-overlay";
        this.messageTimeout = 2000;

        const style = elem.ownerDocument.createElement("style");
        style.textContent = `
            .xterm-reconnect-overlay {
                position: absolute;
                inset: 0;
                background: rgba(0,0,0,0.85);
                z-index: 999;
                display: flex;
                flex-direction: column;
                align-items: center;
                justify-content: center;
                color: #888;
                font-size: 14px;
                font-family: var(--cp-mono-font);
            }
            .xterm-reconnect-spinner {
                width: 40px;
                height: 40px;
                border: 3px solid #333;
                border-top-color: #888;
                border-radius: 50%;
                animation: xterm-spin 1s linear infinite;
                margin-bottom: 16px;
            }
            @keyframes xterm-spin {
                to { transform: rotate(360deg); }
            }
            .xterm-reconnect-btn {
                margin-top: 16px;
                padding: 8px 16px;
                background: #444;
                border: 1px solid #666;
                border-radius: 4px;
                color: #ccc;
                cursor: pointer;
                font-size: 14px;
                font-family: var(--cp-mono-font);
            }
            .xterm-reconnect-btn:hover {
                background: #555;
            }
            .xterm-image-overlay {
                position: absolute;
                z-index: 50;
                border: 1px solid rgba(255,255,255,0.14);
                background: rgba(255,255,255,0.04);
                box-sizing: border-box;
                overflow: hidden;
                pointer-events: none;
            }
            .xterm-image-overlay img {
                width: 100%;
                height: 100%;
                object-fit: contain;
                display: block;
            }
        `;
        elem.ownerDocument.head.appendChild(style);

        this.resizeListener = () => {
            this.elem.style.height = "100%";
            this.fit();
            this.term.scrollToBottom();
            this.showMessage(String(this.term.cols) + "x" + String(this.term.rows), this.messageTimeout);
        };

        this.term.attachCustomKeyEventHandler((event: KeyboardEvent) => {
            if ((event.ctrlKey || event.metaKey) && !event.altKey && String(event.key).toLowerCase() === "c") {
                this.suppressNextSigintFromCopy = true;
                setTimeout(() => {
                    this.suppressNextSigintFromCopy = false;
                }, 0);
                return false;
            }
            return true;
        });

        this.term.open(elem);

        // IME composing state
        var isComposing = false;
        const textarea = this.term.textarea;
        if (textarea) {
            textarea.addEventListener('compositionstart', () => { isComposing = true; });
            textarea.addEventListener('compositionend', () => { isComposing = false; });
        }
        // Expose for onInput
        (this as any)._isComposing = () => isComposing;

        this.fitSoon();
        setTimeout(() => this.fitSoon(), 50);
        setTimeout(() => this.fitSoon(), 200);
        this.renderDisposable = this.term.onRender(() => {
            if (!this.initialFitDone) {
                this.fitSoon();
            }
            this.layoutImageOverlays();
        });
        if (typeof ResizeObserver !== "undefined") {
            this.resizeObserver = new ResizeObserver(() => {
                this.fitSoon();
                this.layoutImageOverlays();
            });
            this.resizeObserver.observe(this.elem);
        }
        this.term.parser.registerOscHandler(9999, (data: string) => {
            this.handleImageOsc(data);
            return true;
        });
        this.term.parser.registerCsiHandler({ prefix: "?", final: "h" }, (params: (number | number[])[]) => {
            const values = flattenParams(params);
            return values.some((value: number) => [47, 1000, 1002, 1003, 1005, 1006, 1015, 1047, 1048, 1049].indexOf(value) >= 0);
        });
        this.term.parser.registerCsiHandler({ prefix: "?", final: "l" }, (params: (number | number[])[]) => {
            const values = flattenParams(params);
            return values.some((value: number) => [47, 1000, 1002, 1003, 1005, 1006, 1015, 1047, 1048, 1049].indexOf(value) >= 0);
        });
        this.term.parser.registerCsiHandler({ final: "J" }, (params: (number | number[])[]) => {
            const values = flattenParams(params);
            return values.indexOf(3) >= 0;
        });
        this.resizeListener();
        window.addEventListener("resize", this.resizeListener);

        this.decoder = new lib.UTF8Decoder();
    }

    configure(options: { scrollback?: number, fontFamily?: string, letterSpacing?: number }): void {
        if (typeof options.scrollback === "number") {
            this.term.options.scrollback = options.scrollback;
        }
        if (typeof options.fontFamily === "string" && options.fontFamily) {
            this.term.options.fontFamily = options.fontFamily;
        }
        if (typeof options.letterSpacing === "number") {
            this.term.options.letterSpacing = options.letterSpacing;
        }
        this.fit();
    }

    fit(): void {
        this.fitAddon.fit();
    }

    private fitSoon(): void {
        requestAnimationFrame(() => {
            this.fit();
            requestAnimationFrame(() => {
                this.fit();
                this.layoutImageOverlays();
            });
        });
    }

    private handleImageOsc(data: string): void {
        var raw = String(data || "").trim();
        if (!raw) {
            return;
        }
        var parts = raw.split(";");
        var values: { [key: string]: string } = {};
        parts.forEach((part: string) => {
            var idx = part.indexOf("=");
            if (idx <= 0) {
                return;
            }
            values[part.slice(0, idx)] = part.slice(idx + 1);
        });
        var src = values.src || values.image || "";
        if (!src) {
            return;
        }
        var widthCells = Math.max(1, parseInt(values.w || values.width || "24", 10) || 24);
        var heightCells = Math.max(1, parseInt(values.h || values.height || "12", 10) || 12);
        var col = Math.max(0, parseInt(values.x || values.col || "0", 10) || 0);
        var marker = this.term.registerMarker(0);
        var overlay = this.elem.ownerDocument.createElement("div");
        overlay.className = "xterm-image-overlay";
        var img = this.elem.ownerDocument.createElement("img");
        img.src = src;
        overlay.appendChild(img);
        this.elem.appendChild(overlay);
        this.imageOverlays.push({
            marker: marker ? { line: marker.line } : null,
            element: overlay,
            widthCells: widthCells,
            heightCells: heightCells,
            col: col,
        });
        this.layoutImageOverlays();
    }

    private layoutImageOverlays(): void {
        var cellWidth = this.term.element ? (this.term.element.clientWidth / Math.max(this.term.cols, 1)) : 0;
        var cellHeight = this.elem.querySelector(".xterm-rows") ? ((this.elem.querySelector(".xterm-rows") as HTMLElement).clientHeight / Math.max(this.term.rows, 1)) : 0;
        if (!cellWidth || !cellHeight) {
            return;
        }
        var viewportY = this.term.buffer.active.viewportY;
        this.imageOverlays.forEach((overlay: ImageRectOverlay) => {
            if (!overlay.marker || overlay.marker.line < 0) {
                overlay.element.style.display = "none";
                return;
            }
            var rowInViewport = overlay.marker.line - viewportY;
            if (rowInViewport + overlay.heightCells < 0 || rowInViewport > this.term.rows) {
                overlay.element.style.display = "none";
                return;
            }
            overlay.element.style.display = "block";
            overlay.element.style.left = String(overlay.col * cellWidth) + "px";
            overlay.element.style.top = String(rowInViewport * cellHeight) + "px";
            overlay.element.style.width = String(overlay.widthCells * cellWidth) + "px";
            overlay.element.style.height = String(overlay.heightCells * cellHeight) + "px";
        });
    }

    info(): { columns: number, rows: number } {
        return { columns: this.term.cols, rows: this.term.rows };
    }

    output(data: string): void {
        let cleaned = this.decoder.decode(data);
        cleaned = cleaned.replace(deviceAttributesRe, "");
        cleaned = cleaned.replace(mouseClickRe, "");
        for (const pattern of stripModeSequenceRes) {
            cleaned = cleaned.replace(pattern, "");
        }
        if (!cleaned) {
            return;
        }
        this.term.write(cleaned, () => {
            if (!this.initialFitDone) {
                this.initialFitDone = true;
                this.fitSoon();
                setTimeout(() => this.fitSoon(), 100);
            }
        });
    }

    showMessage(message: string, timeout: number): void {
        this.message.textContent = message;
        this.elem.appendChild(this.message);

        if (this.messageTimer) {
            clearTimeout(this.messageTimer);
        }
        if (timeout > 0) {
            this.messageTimer = window.setTimeout(() => {
                if (this.message.parentNode === this.elem) {
                    this.elem.removeChild(this.message);
                }
            }, timeout);
        }
    }

    showReconnecting(attempt: number, max: number, onRetry?: () => void): void {
        this.removeMessage();
        this.hideReconnecting();
        const overlay = this.elem.ownerDocument.createElement("div");
        overlay.className = "xterm-reconnect-overlay";
        overlay.id = "xterm-reconnect";

        if (attempt > max) {
            overlay.innerHTML = `
                <div>Connection lost</div>
                <button class="xterm-reconnect-btn">Click to reconnect</button>
            `;
            const btn = overlay.querySelector("button");
            if (btn && onRetry) {
                btn.addEventListener("click", onRetry);
            }
        } else {
            overlay.innerHTML = `
                <div class="xterm-reconnect-spinner"></div>
            `;
        }

        this.elem.appendChild(overlay);
    }

    hideReconnecting(): void {
        const overlays = this.elem.querySelectorAll(".xterm-reconnect-overlay");
        for (let i = 0; i < overlays.length; i++) {
            overlays[i].parentNode!.removeChild(overlays[i]);
        }
    }

    removeMessage(): void {
        if (this.message.parentNode === this.elem) {
            this.elem.removeChild(this.message);
        }
    }

    setWindowTitle(title: string): void {
        document.title = title;
    }

    setPreferences(_value: object): void {
    }

    onInput(callback: (input: string) => void): void {
        if (this.inputDisposable) {
            this.inputDisposable.dispose();
            this.inputDisposable = null;
        }
        var buffer = '';
        var timer: number | null = null;
        const flush = () => {
            if (buffer) { callback(buffer); buffer = ''; }
            if (timer) { clearTimeout(timer); timer = null; }
        };
        const isComposing = (this as any)._isComposing as () => boolean;
        this.inputDisposable = this.term.onData((data: string) => {
            if (isComposing()) return;
            // 控制字符立即发送
            if (data.length === 1 && data.charCodeAt(0) < 32 || data.charCodeAt(0) === 127 || data[0] === '\x1b') {
                flush();
                callback(data);
                return;
            }
            buffer += data;
            if (timer) clearTimeout(timer);
            timer = window.setTimeout(flush, 50);
        });
    }

    onResize(callback: (colmuns: number, rows: number) => void): void {
        if (this.resizeDisposable) {
            this.resizeDisposable.dispose();
        }
        this.resizeDisposable = this.term.onResize((data: { cols: number, rows: number }) => {
            callback(data.cols, data.rows);
        });
    }

    deactivate(): void {
        if (this.inputDisposable) {
            this.inputDisposable.dispose();
            this.inputDisposable = null;
        }
        if (this.resizeDisposable) {
            this.resizeDisposable.dispose();
            this.resizeDisposable = null;
        }
        this.term.blur();
    }

    reset(): void {
        this.removeMessage();
        this.term.clear();
    }

    close(): void {
        window.removeEventListener("resize", this.resizeListener);
        if (this.renderDisposable) {
            this.renderDisposable.dispose();
            this.renderDisposable = null;
        }
        if (this.resizeObserver) {
            this.resizeObserver.disconnect();
            this.resizeObserver = null;
        }
        this.deactivate();
        this.term.dispose();
    }
}
