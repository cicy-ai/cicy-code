import { Terminal as XtermTerminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import { WebLinksAddon } from "@xterm/addon-web-links";
import { lib } from "libapps";
import { applyMonoFontVar, isMacPlatform } from "./font";
import { openExternalLinkWithConfirm, openFileReferencePopup } from "./link_confirm";
import { normalizeTerminalText } from "./webtty";

const deviceAttributesRe = /\x1b\[\??[\d;]*c/g;
const mouseClickRe = /\x1b\[<(?:0|1|2|3|32|33|34|35);\d+;\d+[Mm]|\x1b\[M[\s\S]{3}/g;
const filenameLinkRe = /(?:\.{1,2}\/|~\/|\/)?(?:[A-Za-z0-9._-]+\/)*[A-Za-z0-9._-]+\.[A-Za-z0-9_-]+(?::\d+){0,2}/g;
const filenameExcludedPrefixes = ["http://", "https://", "ws://", "wss://"];
const localResourceProtocolUrlRegex = /(?:file|image):\/\/[^\s"'!*(){}|\\\^<>`]*[^\s"':,.!?{}|\\\^~\[\]`()<>]/;
function normalizeTerminalInput(value: string): string {
    return normalizeTerminalText(value);
}
function isLikelyFilenameLink(value: string, lineText?: string, matchIndex?: number): boolean {
    var text = String(value || "").trim();
    if (!text) {
        return false;
    }
    if (lineText !== undefined && typeof matchIndex === "number") {
        var lowerLine = lineText.toLowerCase();
        if ((matchIndex >= 7 && lowerLine.slice(matchIndex - 7, matchIndex) === "file://")
            || (matchIndex >= 8 && lowerLine.slice(matchIndex - 8, matchIndex) === "image://")) {
            return false;
        }
    }
    for (var i = 0; i < filenameExcludedPrefixes.length; i += 1) {
        if (text.indexOf(filenameExcludedPrefixes[i]) === 0) {
            return false;
        }
    }
    return /\.[A-Za-z0-9_-]+(?::\d+){0,2}$/.test(text);
}
function mapStringIndexToBufferPosition(term: XtermTerminal, lineIndex: number, rowIndex: number, stringIndex: number): [number, number] {
    const buf = term.buffer.active;
    const cell = buf.getNullCell();
    let start = rowIndex;
    while (stringIndex) {
        const line = buf.getLine(lineIndex);
        if (!line) {
            return [-1, -1];
        }
        for (let i = start; i < line.length; ++i) {
            line.getCell(i, cell);
            const chars = cell.getChars();
            if (cell.getWidth()) {
                stringIndex -= chars.length || 1;
                if (i === line.length - 1 && chars === '') {
                    const nextLine = buf.getLine(lineIndex + 1);
                    if (nextLine && nextLine.isWrapped) {
                        nextLine.getCell(0, cell);
                        if (cell.getWidth() === 2) {
                            stringIndex += 1;
                        }
                    }
                }
            }
            if (stringIndex < 0) {
                return [lineIndex, i];
            }
        }
        lineIndex += 1;
        start = 0;
    }
    return [lineIndex, start];
}
function collectFilenameLinks(lineText: string, term: XtermTerminal, bufferLineNumber: number): Array<{ text: string, range: { start: { x: number, y: number }, end: { x: number, y: number } } }> {
    var matches: Array<{ text: string, range: { start: { x: number, y: number }, end: { x: number, y: number } } }> = [];
    var match: RegExpExecArray | null;
    filenameLinkRe.lastIndex = 0;
    while ((match = filenameLinkRe.exec(lineText)) !== null) {
        var text = match[0];
        if (!isLikelyFilenameLink(text, lineText, match.index)) {
            continue;
        }
        const [startY, startX] = mapStringIndexToBufferPosition(term, bufferLineNumber - 1, 0, match.index);
        const [endY, endX] = mapStringIndexToBufferPosition(term, startY, startX, text.length);
        if (startY === -1 || startX === -1 || endY === -1 || endX === -1) {
            continue;
        }
        matches.push({
            text: text,
            range: {
                start: { x: startX + 1, y: startY + 1 },
                end: { x: endX, y: endY + 1 },
            },
        });
    }
    return matches;
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
            scrollback: 20000,
            fontFamily: "var(--cp-mono-font)",
            theme: {
                background: "#000000",
                foreground: "#b9adad",
            },
        });
        this.fitAddon = new FitAddon();
        this.term.loadAddon(this.fitAddon);
        const clearSelection = (): void => {
            this.term.clearSelection();
            var selection = this.elem.ownerDocument.getSelection();
            if (selection) {
                try {
                    selection.removeAllRanges();
                } catch {}
            }
        };
        this.term.loadAddon(new WebLinksAddon((event: MouseEvent, uri: string) => {
            event.preventDefault();
            event.stopPropagation();
            if ((event as any).stopImmediatePropagation) {
                (event as any).stopImmediatePropagation();
            }
            clearSelection();
            if (uri.toLowerCase().indexOf("file://") === 0 || uri.toLowerCase().indexOf("image://") === 0) {
                openFileReferencePopup(this.elem.ownerDocument, uri);
                return;
            }
            openExternalLinkWithConfirm(this.elem.ownerDocument, uri);
        }, { urlRegex: localResourceProtocolUrlRegex }));
        this.term.loadAddon(new WebLinksAddon((event: MouseEvent, uri: string) => {
            event.preventDefault();
            event.stopPropagation();
            if ((event as any).stopImmediatePropagation) {
                (event as any).stopImmediatePropagation();
            }
            clearSelection();
            openExternalLinkWithConfirm(this.elem.ownerDocument, uri);
        }));
        this.term.registerLinkProvider({
            provideLinks: (bufferLineNumber: number, callback: (links: any[] | undefined) => void): void => {
                var line = this.term.buffer.active.getLine(bufferLineNumber - 1);
                if (!line) {
                    callback(undefined);
                    return;
                }
                var lineText = line.translateToString(true);
                var matches = collectFilenameLinks(lineText, this.term, bufferLineNumber);
                if (!matches.length) {
                    callback(undefined);
                    return;
                }
                callback(matches.map((match) => ({
                    range: match.range,
                    text: match.text,
                    decorations: {
                        underline: true,
                        pointerCursor: true,
                    },
                    activate: (event: MouseEvent, text: string): void => {
                        event.preventDefault();
                        event.stopPropagation();
                        if ((event as any).stopImmediatePropagation) {
                            (event as any).stopImmediatePropagation();
                        }
                        clearSelection();
                        openFileReferencePopup(this.elem.ownerDocument, text);
                    },
                })));
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
            var isCopyShortcut = String(event.key).toLowerCase() === "c" && !event.altKey && (isMacPlatform() ? event.metaKey && !event.ctrlKey : event.ctrlKey && !event.metaKey);
            if (isCopyShortcut) {
                var selection = this.elem.ownerDocument.getSelection();
                var hasDocumentSelection = !!selection && !selection.isCollapsed && String(selection).length > 0;
                return !(hasDocumentSelection || this.term.hasSelection());
            }
            return true;
        });

        this.term.open(elem);

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
