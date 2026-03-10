import * as bare from "xterm";
import { lib } from "libapps"


bare.loadAddon("fit");

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
            this.term.scrollToBottom();
            this.showMessage(String(this.term.cols) + "x" + String(this.term.rows), this.messageTimeout);
        };

        this.term.on("open", () => {
            this.resizeListener();
            window.addEventListener("resize", () => { this.resizeListener(); });
        });

        this.term.open(elem, true);

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
                <div>Reconnecting...</div>
                <div style="margin-top: 8px;">Attempt ${attempt}/${max}</div>
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
