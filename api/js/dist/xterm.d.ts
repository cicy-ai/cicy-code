import { Terminal as XtermTerminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import { lib } from "libapps";
interface DisposableLike {
    dispose(): void;
}
export declare class Xterm {
    elem: HTMLElement;
    term: XtermTerminal;
    fitAddon: FitAddon;
    decoder: lib.UTF8Decoder;
    private modelMask;
    private maskCarry;
    message: HTMLElement;
    messageTimeout: number;
    messageTimer: number;
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
    lastNotifiedFit: {
        columns: number;
        rows: number;
    };
    isSelecting: boolean;
    private _selTimer;
    private _pendingFit;
    private _endSelecting;
    constructor(elem: HTMLElement);
    configure(options: {
        scrollback?: number;
        fontFamily?: string;
        letterSpacing?: number;
    }): void;
    fit(): void;
    private notifyFitSize;
    private fitSoon;
    private _fitDebounce;
    private _resizeMask;
    info(): {
        columns: number;
        rows: number;
    };
    setModelMask(model: string): void;
    private applyModelMask;
    output(data: string): void;
    showMessage(message: string, timeout: number): void;
    showReconnecting(attempt: number, max: number, onRetry?: () => void): void;
    hideReconnecting(): void;
    removeMessage(): void;
    setWindowTitle(title: string): void;
    setPreferences(_value: object): void;
    onInput(callback: (input: string) => void): void;
    onPaste(callback: (input: string) => void): void;
    onResize(callback: (colmuns: number, rows: number) => void): void;
    onFit(callback: (colmuns: number, rows: number) => void): void;
    deactivate(): void;
    reset(): void;
    close(): void;
}
export {};
