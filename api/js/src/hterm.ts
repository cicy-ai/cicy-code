// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

import * as bare from "libapps";
import { applyMonoFontVar, monoFontStack, isMacPlatform } from "./font";
import { openExternalLinkWithConfirm, openFileReferencePopup } from "./link_confirm";

export class Hterm {
    elem: HTMLElement;

    term: bare.hterm.Terminal;
    io: bare.hterm.IO;

    columns: number = 0;
    rows: number = 0;

    // to "show" the current message when removeMessage() is called
    message: string = "";
    suppressNextSigintFromCopy: boolean;
    copyShortcutListener: (event: KeyboardEvent) => void;

    constructor(elem: HTMLElement) {
        this.elem = elem;
        applyMonoFontVar(this.elem.ownerDocument);
        this.suppressNextSigintFromCopy = false;
        bare.hterm.defaultStorage = new bare.lib.Storage.Memory();
        this.term = new bare.hterm.Terminal();
        this.term.getPrefs().set("font-family", monoFontStack());
        this.term.getPrefs().set("send-encoding", "raw");
        this.term.decorate(this.elem);
        (this.term as any).openUrl = (url: string) => {
            var lowerUrl = String(url || "").toLowerCase();
            if (lowerUrl.indexOf("file://") === 0 || lowerUrl.indexOf("image://") === 0) {
                openFileReferencePopup(this.elem.ownerDocument, url);
                return;
            }
            openExternalLinkWithConfirm(this.elem.ownerDocument, url);
        };

        this.io = this.term.io.push();
        this.term.installKeyboard();

        this.copyShortcutListener = (event: KeyboardEvent) => {
            var isCopyShortcut = String(event.key).toLowerCase() === "c" && !event.altKey && (isMacPlatform() ? event.metaKey && !event.ctrlKey : event.ctrlKey && !event.metaKey);
            if (isCopyShortcut) {
                var selection = this.elem.ownerDocument.getSelection();
                this.suppressNextSigintFromCopy = !!selection && !selection.isCollapsed && String(selection).length > 0;
                setTimeout(() => {
                    this.suppressNextSigintFromCopy = false;
                }, 0);
            }
        };
        this.elem.addEventListener("keydown", this.copyShortcutListener, true);
    };

    info(): { columns: number, rows: number } {
        return { columns: this.columns, rows: this.rows };
    };

    output(data: string) {
        if (this.term.io != null) {
            this.term.io.writeUTF8(data);
        }
    };

    showMessage(message: string, timeout: number) {
        this.message = message;
        if (timeout > 0) {
            this.term.io.showOverlay(message, timeout);
        } else {
            this.term.io.showOverlay(message, null);
        }
    };

    removeMessage(): void {
        // there is no hideOverlay(), so show the same message with 0 sec
        this.term.io.showOverlay(this.message, 0);
    }

    showReconnecting(attempt: number, max: number, onRetry?: () => void) {
        if (attempt > max) {
            this.showMessage("Connection lost. Refresh to reconnect.", 0);
        } else {
            this.removeMessage();
        }
    };

    hideReconnecting() {
        this.removeMessage();
    };

    setWindowTitle(title: string) {
        this.term.setWindowTitle(title);
    };

    setPreferences(value: { [key: string]: any }) {
        Object.keys(value).forEach((key) => {
            this.term.getPrefs().set(key, value[key]);
        });
    };

    configure(_options: { scrollback?: number, fontFamily?: string, letterSpacing?: number }): void {
    };

    fit(): void {
    };

    onInput(callback: (input: string) => void) {
        this.io.onVTKeystroke = (data) => {
            if (this.suppressNextSigintFromCopy && data === "\x03") {
                this.suppressNextSigintFromCopy = false;
                return;
            }
            callback(data);
        };
        this.io.sendString = (data) => {
            if (this.suppressNextSigintFromCopy && data === "\x03") {
                this.suppressNextSigintFromCopy = false;
                return;
            }
            callback(data);
        };
    };

    onPaste(_callback: (input: string) => void): void {
    };

    onResize(callback: (colmuns: number, rows: number) => void) {
        this.io.onTerminalResize = (columns: number, rows: number) => {
            this.columns = columns;
            this.rows = rows;
            callback(columns, rows);
        };
    };

    deactivate(): void {
        this.io.onVTKeystroke    = function(){};
        this.io.sendString       = function(){};
        this.io.onTerminalResize = function(){};
        this.term.uninstallKeyboard();
    }

    reset(): void {
        this.removeMessage();
        this.term.installKeyboard();
    }

    close(): void {
        this.elem.removeEventListener("keydown", this.copyShortcutListener, true);
        this.term.uninstallKeyboard();
    }
}
