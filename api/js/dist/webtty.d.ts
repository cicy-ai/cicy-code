export declare const protocols: string[];
export declare const msgInputUnknown = "0";
export declare const msgInput = "1";
export declare const msgPing = "2";
export declare const msgResizeTerminal = "3";
export declare const msgUnknownOutput = "0";
export declare const msgOutput = "1";
export declare const msgPong = "2";
export declare const msgSetWindowTitle = "3";
export declare const msgSetPreferences = "4";
export declare const msgSetReconnect = "5";
export declare const msgAPI = "6";
export declare function normalizeTerminalText(value: string): string;
export interface APIRequestMessage {
    id: string;
    method: string;
    path: string;
    body?: object;
    headers?: {
        [key: string]: string;
    };
    bodyBase64?: string;
    contentType?: string;
}
export interface APIResponseMessage {
    id: string;
    ok: boolean;
    status: number;
    body?: any;
    error?: string;
}
export interface Terminal {
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
    setPreferences(value: object): void;
    configure(options: {
        scrollback?: number;
        fontFamily?: string;
        letterSpacing?: number;
    }): void;
    fit(): void;
    onInput(callback: (input: string) => void): void;
    onPaste?(callback: (input: string) => void): void;
    onResize(callback: (colmuns: number, rows: number) => void): void;
    onFit?(callback: (colmuns: number, rows: number) => void): void;
    reset(): void;
    deactivate(): void;
    close(): void;
}
export interface Connection {
    open(): void;
    close(): void;
    send(data: string): void;
    isOpen(): boolean;
    onOpen(callback: () => void): void;
    onReceive(callback: (data: string) => void): void;
    onClose(callback: () => void): void;
}
export interface ConnectionFactory {
    create(): Connection;
}
export interface ConnectionStateListener {
    (isOpen: boolean): void;
}
export declare class WebTTY {
    term: Terminal;
    connectionFactory: ConnectionFactory;
    connection: Connection | null;
    args: string;
    authToken: string;
    reconnect: number;
    reconnectAttempts: number;
    maxReconnectAttempts: number;
    reconnectDelay: number;
    connectionStateListeners: ConnectionStateListener[];
    apiRequestSeq: number;
    apiPendingRequests: {
        [key: string]: {
            resolve: (value: any) => void;
            reject: (reason?: any) => void;
        };
    };
    constructor(term: Terminal, connectionFactory: ConnectionFactory, args: string, authToken: string);
    onConnectionStateChange(callback: ConnectionStateListener): void;
    emitConnectionState(isOpen: boolean): void;
    isConnectionOpen(): boolean;
    sendInput(input: string): boolean;
    sendLine(input: string): boolean;
    requestAPI(method: string, path: string, body?: object, headers?: {
        [key: string]: string;
    }, bodyBase64?: string, contentType?: string): Promise<any>;
    handleAPIResponse(payload: string): void;
    open(): () => void;
}
