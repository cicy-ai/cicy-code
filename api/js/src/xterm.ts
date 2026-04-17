import * as bare from "xterm";
import { lib } from "libapps"
import { openExternalLinkWithConfirm } from "./link_confirm";


bare.loadAddon("fit");

const MAX_VISIBLE_ROWS = 42;

export class Xterm {
    elem: HTMLElement;
    term: bare;
    resizeListener: () => void;
    decoder: lib.UTF8Decoder;

    message: HTMLElement;
    messageTimeout: number;
    messageTimer: number;


    constructor(elem: HTMLElement) {
        this.elem = elem;
        this.term = new bare({ fontSize: 12 });

        this.message = elem.ownerDocument.createElement("div");
        this.message.className = "xterm-overlay";
        this.messageTimeout = 2000;

        // 添加 CSS 样式
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
            }
            .xterm-reconnect-btn:hover {
                background: #555;
            }
        `;
        elem.ownerDocument.head.appendChild(style);

        this.resizeListener = () => {
            this.term.fit();
            if (this.term.rows > MAX_VISIBLE_ROWS) {
                this.elem.style.height = (MAX_VISIBLE_ROWS * this.term.charMeasure.height) + "px";
                this.term.fit();
            } else {
                this.elem.style.height = "100%";
            }
            this.term.scrollToBottom();
            this.showMessage(String(this.term.cols) + "x" + String(this.term.rows), this.messageTimeout);
        };

        this.term.on("open", () => {
            this.resizeListener();
            window.addEventListener("resize", () => { this.resizeListener(); });
        });

        this.term.open(elem, true);
        const blockScroll = (event: Event): void => {
            event.preventDefault();
            event.stopPropagation();
            var anyEvent = event as any;
            if (typeof anyEvent.stopImmediatePropagation === "function") {
                anyEvent.stopImmediatePropagation();
            }
        };
        const hardDisableViewportScroll = (): void => {
            const nodes = this.elem.querySelectorAll(".xterm-viewport, .xterm-scroll-area");
            for (let i = 0; i < nodes.length; i++) {
                const node = nodes[i] as HTMLElement;
                node.style.overflow = "hidden";
                node.style.overflowY = "hidden";
                node.style.overflowX = "hidden";
                node.style.maxHeight = "100%";
                node.scrollTop = 0;
                node.scrollLeft = 0;
                node.addEventListener("wheel", blockScroll, { passive: false, capture: true });
                node.addEventListener("touchmove", blockScroll, { passive: false, capture: true });
            }
        };
        hardDisableViewportScroll();
        const viewportObserver = new MutationObserver((): void => {
            hardDisableViewportScroll();
        });
        viewportObserver.observe(this.elem, { childList: true, subtree: true, attributes: true });
        window.addEventListener("wheel", blockScroll, { passive: false, capture: true });
        window.addEventListener("touchmove", blockScroll, { passive: false, capture: true });
        document.addEventListener("wheel", blockScroll, { passive: false, capture: true });
        document.addEventListener("touchmove", blockScroll, { passive: false, capture: true });
        this.elem.addEventListener("wheel", blockScroll, { passive: false, capture: true });
        this.elem.addEventListener("touchmove", blockScroll, { passive: false, capture: true });
        this.term.attachCustomKeyEventHandler((event: KeyboardEvent) => {
            if ((event.ctrlKey || event.metaKey) && !event.altKey && String(event.key).toLowerCase() === "c") {
                return false;
            }
            return true;
        });
        if ((this.term as any).setHypertextLinkHandler) {
            (this.term as any).setHypertextLinkHandler((event: MouseEvent, uri: string) => {
                event.preventDefault();
                event.stopPropagation();
                if ((event as any).stopImmediatePropagation) {
                    (event as any).stopImmediatePropagation();
                }
                openExternalLinkWithConfirm(this.elem.ownerDocument, uri);
            });
        }

        // Prevent tmux mouse mode from disabling text selection.
        // tmux sends \x1b[?1000h which makes xterm.js call selectionManager.disable().
        // We block both mouseEvents and selectionManager.disable() to keep selection working.
        Object.defineProperty(this.term, 'mouseEvents', {
            get: () => false,
            set: () => {},
        });
        if (this.term.selectionManager) {
            this.term.selectionManager.disable = () => {};
        }

        this.decoder = new lib.UTF8Decoder()
    };

    info(): { columns: number, rows: number } {
        return { columns: this.term.cols, rows: this.term.rows };
    };

    output(data: string) {
        this.term.write(this.decoder.decode(data));
    };

    showMessage(message: string, timeout: number) {
        this.message.textContent = message;
        this.elem.appendChild(this.message);

        if (this.messageTimer) {
            clearTimeout(this.messageTimer);
        }
        if (timeout > 0) {
            this.messageTimer = setTimeout(() => {
                this.elem.removeChild(this.message);
            }, timeout);
        }
    };

    showReconnecting(attempt: number, max: number, onRetry?: () => void) {
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
    };

    hideReconnecting() {
        const overlays = this.elem.querySelectorAll(".xterm-reconnect-overlay");
        for (let i = 0; i < overlays.length; i++) overlays[i].parentNode!.removeChild(overlays[i]);
    };

    removeMessage(): void {
        if (this.message.parentNode == this.elem) {
            this.elem.removeChild(this.message);
        }
    }

    setWindowTitle(title: string) {
        document.title = title;
    };

    setPreferences(value: object) {
    };

    onInput(callback: (input: string) => void) {
        this.term.on("data", (data) => {
            // Block mouse sequences (SGR + X10) - let browser handle selection
            if (data.indexOf('\x1b[<') >= 0 || data.indexOf('\x1b[M') >= 0) return;
            // Block Device Attributes response (e.g. ESC[?0;276;0c)
            if (/\x1b\[\??[\d;]*c/.test(data)) return;
            callback(data);
        });

    };

    onResize(callback: (colmuns: number, rows: number) => void) {
        this.term.on("resize", (data) => {
            callback(data.cols, data.rows);
        });
    };

    deactivate(): void {
        this.term.off("data");
        this.term.off("resize");
        this.term.blur();
    }

    reset(): void {
        this.removeMessage();
        this.term.clear();
    }

    close(): void {
        window.removeEventListener("resize", this.resizeListener);
        this.term.destroy();
    }
}
