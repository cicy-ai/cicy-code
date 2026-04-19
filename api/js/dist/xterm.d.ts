import { Terminal as XtermTerminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import { lib } from "libapps";
interface DisposableLike {
    dispose(): void;
}
interface ImageRectOverlay {
    marker: {
        line: number;
    } | null;
    element: HTMLDivElement;
    widthCells: number;
    heightCells: number;
    col: number;
}
export declare class Xterm {
    elem: HTMLElement;
    term: XtermTerminal;
    fitAddon: FitAddon;
    decoder: lib.UTF8Decoder;
    message: HTMLElement;
    messageTimeout: number;
    messageTimer: number;
    resizeListener: () => void;
    suppressNextSigintFromCopy: boolean;
    inputDisposable: DisposableLike | null;
    resizeDisposable: DisposableLike | null;
    renderDisposable: DisposableLike | null;
    resizeObserver: ResizeObserver | null;
    imageOverlays: ImageRectOverlay[];
    initialFitDone: boolean;
    constructor(elem: HTMLElement);
    configure(options: {
        scrollback?: number;
        fontFamily?: string;
        letterSpacing?: number;
    }): void;
    fit(): void;
    private fitSoon;
    private handleImageOsc;
    private layoutImageOverlays;
    info(): {
        columns: number;
        rows: number;
    };
    output(data: string): void;
    showMessage(message: string, timeout: number): void;
    showReconnecting(attempt: number, max: number, onRetry?: () => void): void;
    hideReconnecting(): void;
    removeMessage(): void;
    setWindowTitle(title: string): void;
    setPreferences(_value: object): void;
    onInput(_callback: (input: string) => void): void;
    onResize(callback: (colmuns: number, rows: number) => void): void;
    deactivate(): void;
    reset(): void;
    close(): void;
}
export {};
