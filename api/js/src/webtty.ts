export const protocols = ["webtty"];

export const msgInputUnknown = '0';
export const msgInput = '1';
export const msgPing = '2';
export const msgResizeTerminal = '3';

export const msgUnknownOutput = '0';
export const msgOutput = '1';
export const msgPong = '2';
export const msgSetWindowTitle = '3';
export const msgSetPreferences = '4';
export const msgSetReconnect = '5';
export const msgAPI = '6';

export interface APIRequestMessage {
    id: string;
    method: string;
    path: string;
    body?: object;
    headers?: { [key: string]: string };
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
    info(): { columns: number, rows: number };
    output(data: string): void;
    showMessage(message: string, timeout: number): void;
    removeMessage(): void;
    showReconnecting(attempt: number, max: number, onRetry?: () => void): void;
    hideReconnecting(): void;
    setWindowTitle(title: string): void;
    setPreferences(value: object): void;
    onInput(callback: (input: string) => void): void;
    onResize(callback: (colmuns: number, rows: number) => void): void;
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

export class WebTTY {
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
    apiPendingRequests: { [key: string]: { resolve: (value: any) => void, reject: (reason?: any) => void } };

    constructor(term: Terminal, connectionFactory: ConnectionFactory, args: string, authToken: string) {
        this.term = term;
        this.connectionFactory = connectionFactory;
        this.connection = null;
        this.args = args;
        this.authToken = authToken;
        this.reconnect = -1;
        this.reconnectAttempts = 0;
        this.maxReconnectAttempts = 10;
        this.reconnectDelay = 2000;
        this.connectionStateListeners = [];
        this.apiRequestSeq = 0;
        this.apiPendingRequests = {};
    };

    onConnectionStateChange(callback: ConnectionStateListener) {
        this.connectionStateListeners.push(callback);
    };

    emitConnectionState(isOpen: boolean) {
        this.connectionStateListeners.forEach((callback) => {
            callback(isOpen);
        });
    };

    isConnectionOpen(): boolean {
        return this.connection !== null && this.connection.isOpen();
    };

    sendInput(input: string): boolean {
        if (!this.isConnectionOpen() || this.connection === null) {
            return false;
        }
        this.connection.send(msgInput + input);
        return true;
    };

    sendLine(input: string): boolean {
        return this.sendInput(input + "\r");
    };

    requestAPI(method: string, path: string, body?: object, headers?: { [key: string]: string }, bodyBase64?: string, contentType?: string): Promise<any> {
        var PromiseCtor = (window as any).Promise;
        if (!this.isConnectionOpen() || this.connection === null) {
            return PromiseCtor.resolve(null);
        }

        this.apiRequestSeq += 1;
        const id = "req-" + String(this.apiRequestSeq);
        const message: APIRequestMessage = {
            id: id,
            method: method,
            path: path,
        };
        if (body !== undefined) {
            message.body = body;
        }
        if (headers !== undefined) {
            message.headers = headers;
        }
        if (bodyBase64 !== undefined) {
            message.bodyBase64 = bodyBase64;
        }
        if (contentType !== undefined) {
            message.contentType = contentType;
        }

        return new PromiseCtor((resolve: (value?: any) => void, reject: (reason?: any) => void): void => {
            this.apiPendingRequests[id] = { resolve: resolve, reject: reject };
            this.connection!.send(msgAPI + JSON.stringify(message));
        });
    };

    handleAPIResponse(payload: string) {
        const message = JSON.parse(payload) as APIResponseMessage;
        const pending = this.apiPendingRequests[message.id];
        if (!pending) {
            return;
        }
        delete this.apiPendingRequests[message.id];
        if (message.ok) {
            pending.resolve(message.body);
            return;
        }
        pending.reject(new Error(message.error || ("request failed with status " + String(message.status))));
    };

    open() {
        let connection = this.connectionFactory.create();
        this.connection = connection;
        let pingTimer: number;
        let reconnectTimeout: number;

        const doReconnect = () => {
            this.reconnectAttempts = 0;
            connection = this.connectionFactory.create();
            this.connection = connection;
            setup();
        };

        const setup = () => {
            connection.onOpen(() => {
                this.connection = connection;
                this.reconnectAttempts = 0;
                this.term.hideReconnecting();
                this.emitConnectionState(true);
                
                const termInfo = this.term.info();

                connection.send(JSON.stringify(
                    {
                        Arguments: this.args,
                        AuthToken: this.authToken,
                    }
                ));


                const resizeHandler = (colmuns: number, rows: number) => {
                    connection.send(
                        msgResizeTerminal + JSON.stringify(
                            {
                                columns: colmuns,
                                rows: rows
                            }
                        )
                    );
                };

                this.term.onResize(resizeHandler);
                resizeHandler(termInfo.columns, termInfo.rows);

                this.term.onInput(
                    (input: string) => {
                        connection.send(msgInput + input);
                    }
                );

                pingTimer = setInterval(() => {
                    connection.send(msgPing)
                }, 30 * 1000);

            });

            connection.onReceive((data) => {
                const payload = data.slice(1);
                switch (data[0]) {
                    case msgOutput:
                        this.term.output(atob(payload));
                        break;
                    case msgPong:
                        break;
                    case msgSetWindowTitle:
                        this.term.setWindowTitle(payload);
                        break;
                    case msgSetPreferences:
                        const preferences = JSON.parse(payload);
                        this.term.setPreferences(preferences);
                        break;
                    case msgSetReconnect:
                        const autoReconnect = JSON.parse(payload);
                        console.log("Enabling reconnect: " + autoReconnect + " seconds")
                        this.reconnect = autoReconnect;
                        break;
                    case msgAPI:
                        this.handleAPIResponse(payload);
                        break;
                }
            });

            connection.onClose(() => {
                clearInterval(pingTimer);
                this.term.deactivate();
                Object.keys(this.apiPendingRequests).forEach((id) => {
                    this.apiPendingRequests[id].reject(new Error("webtty connection closed"));
                    delete this.apiPendingRequests[id];
                });
                this.emitConnectionState(false);
                
                if (this.reconnectAttempts < this.maxReconnectAttempts) {
                    this.reconnectAttempts++;
                    const delay = Math.min(this.reconnectDelay * Math.pow(2, this.reconnectAttempts - 1), 30000);
                    this.term.showReconnecting(this.reconnectAttempts, this.maxReconnectAttempts);
                    
                    reconnectTimeout = setTimeout(() => {
                        connection = this.connectionFactory.create();
                        this.connection = connection;
                        setup();
                    }, delay);
                } else {
                    this.term.showReconnecting(this.reconnectAttempts, this.maxReconnectAttempts, doReconnect);
                }
            });

            connection.open();
        }

        setup();
        return () => {
            clearTimeout(reconnectTimeout);
            this.connection = null;
            connection.close();
        }
    };
};
