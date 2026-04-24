import * as bare from "libapps";
export declare class Hterm {
    elem: HTMLElement;
    term: bare.hterm.Terminal;
    io: bare.hterm.IO;
    columns: number;
    rows: number;
    message: string;
    suppressNextSigintFromCopy: boolean;
    copyShortcutListener: (event: KeyboardEvent) => void;
    constructor(elem: HTMLElement);
    info(): {
        columns: number;
        rows: number;
    };
    output(data: string): void;
    showMessage(message: string, timeout: number): void;
    removeMessage(): void;
    showReconnecting(attempt: number, max: number, onRetry?: () => void): void;
    hideReconnecting(): void;
    setWindowTitle(title: string): void;
    setPreferences(value: {
        [key: string]: any;
    }): void;
    configure(_options: {
        scrollback?: number;
        fontFamily?: string;
        letterSpacing?: number;
    }): void;
    fit(): void;
    onInput(callback: (input: string) => void): void;
    onPaste(_callback: (input: string) => void): void;
    onResize(callback: (colmuns: number, rows: number) => void): void;
    deactivate(): void;
    reset(): void;
    close(): void;
}
