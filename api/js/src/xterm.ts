import { Terminal as XtermTerminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import { Unicode11Addon } from "@xterm/addon-unicode11";
import { WebglAddon } from "@xterm/addon-webgl";
import { lib } from "libapps";
import { applyMonoFontVar, isMacPlatform, isWindowsPlatform } from "./font";
import { openExternalLinkWithConfirm, openFileReferencePopup } from "./link_confirm";
import { normalizeTerminalText } from "./webtty";
import { scanLinksOnText, type LinkKind } from "./link_detect";

const deviceAttributesRe = /\x1b\[\??[\d;]*c/g;
const mouseClickRe = /\x1b\[<(?:0|1|2|3|32|33|34|35);\d+;\d+[Mm]|\x1b\[M[\s\S]{3}/g;

function normalizeTerminalInput(value: string): string {
    return normalizeTerminalText(value);
}

interface LogicalLine {
    text: string;
    /** For each code unit in `text`, the buffer cell that produced it. y is the 0-based bufferLine index, x is the 0-based column. */
    cellMap: Array<{ y: number; x: number }>;
    anchorY: number;
    endY: number;
}

const MAX_WRAP_ROWS = 32;

function findAnchorY(term: XtermTerminal, y: number): number {
    const buf = term.buffer.active;
    let cur = y;
    while (cur > 0) {
        const line = buf.getLine(cur);
        if (!line || !line.isWrapped) break;
        cur -= 1;
    }
    return cur;
}

function buildLogicalLine(term: XtermTerminal, anchorY: number): LogicalLine | null {
    const buf = term.buffer.active;
    if (!buf.getLine(anchorY)) return null;
    const cell = buf.getNullCell();
    let text = "";
    const cellMap: Array<{ y: number; x: number }> = [];
    let endY = anchorY;
    for (let i = 0; i < MAX_WRAP_ROWS; i += 1) {
        const ry = anchorY + i;
        const line = buf.getLine(ry);
        if (!line) break;
        if (i > 0 && !line.isWrapped) break;
        endY = ry;
        const cols = line.length;
        for (let x = 0; x < cols; x += 1) {
            line.getCell(x, cell);
            if (cell.getWidth() === 0) continue; // wide-char trail half
            const chars = cell.getChars();
            const code = chars || " ";
            for (let k = 0; k < code.length; k += 1) {
                text += code[k];
                cellMap.push({ y: ry, x });
            }
        }
        const next = buf.getLine(ry + 1);
        if (!next || !next.isWrapped) break;
    }
    return { text, cellMap, anchorY, endY };
}

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
    initialFitDone: boolean;
    isComposing: boolean;
    pasteCallback: ((input: string) => void) | null;
    fitCallbacks: Array<(columns: number, rows: number) => void>;
    lastNotifiedFit: { columns: number; rows: number };

    constructor(elem: HTMLElement) {
        this.elem = elem;
        applyMonoFontVar(elem.ownerDocument);
        this.suppressNextSigintFromCopy = false;
        this.inputDisposable = null;
        this.resizeDisposable = null;
        this.renderDisposable = null;
        this.resizeObserver = null;
        this.initialFitDone = false;
        this.isComposing = false;
        this.pasteCallback = null;
        this.fitCallbacks = [];
        this.lastNotifiedFit = { columns: 0, rows: 0 };

        this.term = new XtermTerminal({
            fontSize: 13,
            convertEol: false,
            allowTransparency: true,
            allowProposedApi: true,
            scrollback: 500,
            fontFamily: "var(--cp-mono-font)",
            theme: {
                background: "#000000",
                foreground: "#b9adad",
            },
        });
        this.fitAddon = new FitAddon();
        this.term.loadAddon(this.fitAddon);
        // Unicode 11 width tables — correctly measure wide emojis, ZWJ
        // sequences and skin-tone modifiers so they don't render half-cut.
        this.term.loadAddon(new Unicode11Addon());
        this.term.unicode.activeVersion = "11";
        const clearSelection = (): void => {
            this.term.clearSelection();
            var selection = this.elem.ownerDocument.getSelection();
            if (selection) {
                try {
                    selection.removeAllRanges();
                } catch {}
            }
        };
        const handleLinkActivate = (event: MouseEvent, uri: string, kind: LinkKind): void => {
            event.preventDefault();
            event.stopPropagation();
            if ((event as any).stopImmediatePropagation) {
                (event as any).stopImmediatePropagation();
            }
            clearSelection();
            if (kind === "url") {
                openExternalLinkWithConfirm(this.elem.ownerDocument, uri);
                return;
            }
            // "local" (file://, image://) and "file" (bare paths) both route to the file popup.
            openFileReferencePopup(this.elem.ownerDocument, uri);
        };
        this.term.registerLinkProvider({
            provideLinks: (bufferLineNumber: number, callback: (links: any[] | undefined) => void): void => {
                const y = bufferLineNumber - 1;
                const anchorY = findAnchorY(this.term, y);
                const logical = buildLogicalLine(this.term, anchorY);
                if (!logical || !logical.text) {
                    callback(undefined);
                    return;
                }
                const matches = scanLinksOnText(logical.text);
                if (!matches.length) {
                    callback(undefined);
                    return;
                }
                const cols = this.term.cols;
                const links: any[] = [];
                for (const match of matches) {
                    const startCell = logical.cellMap[match.start];
                    const endCell = logical.cellMap[match.end - 1];
                    if (!startCell || !endCell) continue;
                    if (startCell.y > y || endCell.y < y) continue;
                    // Clip the link's range to the cells living on the queried row so
                    // that decoration spans each wrapped segment correctly.
                    const startX = startCell.y === y ? startCell.x : 0;
                    const endX = endCell.y === y ? endCell.x : Math.max(0, cols - 1);
                    const kind = match.kind;
                    const uri = match.uri;
                    links.push({
                        range: {
                            start: { x: startX + 1, y: y + 1 },
                            end: { x: endX + 1, y: y + 1 },
                        },
                        text: uri,
                        decorations: {
                            underline: true,
                            pointerCursor: true,
                        },
                        activate: (event: MouseEvent): void => {
                            handleLinkActivate(event, uri, kind);
                        },
                    });
                }
                callback(links.length ? links : undefined);
            },
        });

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
            .xterm .xterm-helper-textarea,
            .xterm .composition-view {
                font-family: var(--cp-mono-font);
                font-size: 13px;
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
            var key = String(event.key).toLowerCase();
            var isCopyShortcut = key === "c" && !event.altKey && (isMacPlatform() ? event.metaKey && !event.ctrlKey : event.ctrlKey && !event.metaKey);
            if (isCopyShortcut) {
                var selection = this.elem.ownerDocument.getSelection();
                var hasDocumentSelection = !!selection && !selection.isCollapsed && String(selection).length > 0;
                return !(hasDocumentSelection || this.term.hasSelection());
            }
            // Windows users expect Ctrl+V to paste (same as every other Windows app).
            // xterm.js's default keydown handler converts Ctrl+V → \x16 and calls
            // preventDefault, which kills the browser's native paste event on the
            // hidden textarea. Returning false here skips xterm's processing so the
            // textarea receives the paste event and `showPasteDialog` below runs.
            // Mac (Cmd+V) already works because meta-V is not intercepted. Linux
            // is left alone (Ctrl+Shift+V or middle-click remain the paste path).
            if (isWindowsPlatform() && key === "v" && event.ctrlKey && !event.metaKey && !event.altKey && !event.shiftKey) {
                return false;
            }
            return true;
        });

        this.term.open(elem);

        // GPU-accelerated renderer. The WebGL addon must load AFTER open().
        // 5-10x render throughput over the default DOM renderer — visible as
        // smooth scrolling under heavy tmux output. On context loss (driver
        // reset, too many live WebGL canvases) dispose the addon and xterm
        // falls back to the DOM renderer transparently; same if the platform
        // has no WebGL2 at all (loadAddon throws → caught → DOM renderer).
        try {
            const webgl = new WebglAddon();
            webgl.onContextLoss(() => {
                try {
                    webgl.dispose();
                } catch {}
            });
            this.term.loadAddon(webgl);
        } catch (e) {
            console.warn("[webtty] WebGL renderer unavailable, using DOM renderer", e);
        }

        // IME composing state
        const textarea = this.term.textarea;
        const clearComposing = () => { this.isComposing = false; };
        const refocus = () => {
            window.requestAnimationFrame(() => {
                try {
                    this.term.focus();
                } catch {}
            });
        };
        const showPasteDialog = (event: ClipboardEvent) => {
            clearComposing();
            const text = normalizeTerminalInput(event.clipboardData?.getData('text/plain') || '');
            if (!text) {
                refocus();
                return;
            }
            const lineCount = text.split(/\r\n|\r|\n/).length;
            const shouldConfirm = lineCount > 1 || text.length > 100;
            if (!shouldConfirm || !this.pasteCallback) {
                refocus();
                return;
            }
            event.preventDefault();
            event.stopPropagation();
            this.pasteCallback(text);
            refocus();
        };
        if (textarea) {
            textarea.addEventListener('compositionstart', () => { this.isComposing = true; });
            textarea.addEventListener('compositionend', clearComposing);
            textarea.addEventListener('blur', clearComposing);
            textarea.addEventListener('paste', showPasteDialog, true);
        }
        this.elem.addEventListener('paste', showPasteDialog, true);
        this.elem.addEventListener('mousedown', refocus);

        this.fitSoon();
        setTimeout(() => this.fitSoon(), 50);
        setTimeout(() => this.fitSoon(), 200);
        this.renderDisposable = this.term.onRender(() => {
            if (!this.initialFitDone) {
                this.fitSoon();
            }
        });
        if (typeof ResizeObserver !== "undefined") {
            this.resizeObserver = new ResizeObserver(() => {
                this.fitSoon();
            });
            this.resizeObserver.observe(this.elem);
        }
        // Re-fit when iframe becomes visible (e.g. agent tab switch)
        if (typeof IntersectionObserver !== "undefined") {
            const visObs = new IntersectionObserver((entries) => {
                if (entries[0]?.isIntersecting) {
                    this.fitSoon();
                }
            });
            visObs.observe(this.elem);
        }
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
        // Skip fit when container is hidden (display:none) or too small
        const rect = this.elem.getBoundingClientRect();
        if (rect.width < 20 || rect.height < 20) return;
        this.fitAddon.fit();
        this.notifyFitSize();
    }

    private notifyFitSize(force: boolean = false): void {
        const columns = this.term.cols;
        const rows = this.term.rows;
        if (columns <= 0 || rows <= 0) return;
        if (!force && this.lastNotifiedFit.columns === columns && this.lastNotifiedFit.rows === rows) return;
        this.lastNotifiedFit = { columns, rows };
        for (const callback of this.fitCallbacks) {
            callback(columns, rows);
        }
    }

    private fitSoon(): void {
        // Show a black overlay during resize to hide tmux redraw flicker
        if (!this._resizeMask) {
            this._resizeMask = this.elem.ownerDocument.createElement('div');
            this._resizeMask.style.cssText = 'position:absolute;inset:0;background:#000;z-index:10;pointer-events:none;';
        }
        if (!this._resizeMask.parentNode) {
            this.elem.style.position = 'relative';
            this.elem.appendChild(this._resizeMask);
        }
        if (this._fitDebounce) clearTimeout(this._fitDebounce);
        this._fitDebounce = window.setTimeout(() => {
            this._fitDebounce = 0;
            this.fit();
            setTimeout(() => {
                this._resizeMask?.remove();
            }, 100);
        }, 150);
    }
    private _fitDebounce: number = 0;
    private _resizeMask: HTMLDivElement | null = null;

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
        this.inputDisposable = this.term.onData((data: string) => {
            if (this.isComposing) return;
            const normalized = normalizeTerminalInput(data);
            // Send control characters immediately
            if (normalized.length === 1 && normalized.charCodeAt(0) < 32 || normalized.charCodeAt(0) === 127 || normalized[0] === '\x1b') {
                flush();
                callback(normalized);
                return;
            }
            buffer += normalized;
            if (timer) clearTimeout(timer);
            timer = window.setTimeout(flush, 50);
        });
    }

    onPaste(callback: (input: string) => void): void {
        this.pasteCallback = callback;
    }

    onResize(callback: (colmuns: number, rows: number) => void): void {
        if (this.resizeDisposable) {
            this.resizeDisposable.dispose();
        }
        this.resizeDisposable = this.term.onResize((data: { cols: number, rows: number }) => {
            this.lastNotifiedFit = { columns: data.cols, rows: data.rows };
            callback(data.cols, data.rows);
        });
    }

    onFit(callback: (colmuns: number, rows: number) => void): void {
        this.fitCallbacks.push(callback);
        this.notifyFitSize(true);
    }

    deactivate(): void {
        // Don't dispose inputDisposable here — reconnect may have already
        // registered a new one. Only blur to indicate inactive state.
        this.isComposing = false;
        this.term.blur();
    }

    reset(): void {
        this.removeMessage();
        this.isComposing = false;
        this.term.clear();
        this.term.focus();
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
