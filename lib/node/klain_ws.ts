// klain:ws — a WebSocket server (RFC 6455) over the http module's 'upgrade'
// event: the WebSocket layer the `ws` package gives a Node program, with the
// browser WebSocket's onmessage/send/close shape on each connection.
//
//	const wss = new WebSocketServer({ server })
//	wss.on('connection', (socket) => {
//	  socket.onmessage = (ev) => socket.send('echo: ' + ev.data)
//	})
import { EventEmitter } from 'events';
import { createHash } from 'crypto';
import type { Server, IncomingMessage } from 'http';
import type { Socket } from 'net';
import { encodeFrame, parseFrame } from './internal_websocket_frame';

const GUID = '258EAFA5-E914-47DA-95CA-C5AB0DC85B11';

// A received message: its text, or its bytes for a binary frame.
export class WSMessageEvent {
    data: string;
    isBinary: boolean;
    private bytes: Buffer;

    constructor(bytes: Buffer, isBinary: boolean) {
        this.bytes = bytes;
        this.isBinary = isBinary;
        this.data = bytes.toString('utf8');
    }

    dataBytes(): Uint8Array {
        return this.bytes;
    }
}

// One accepted connection.
export class WSConnection {
    // 0 CONNECTING, 1 OPEN, 2 CLOSING, 3 CLOSED, as the browser's WebSocket.
    readyState = 1;
    onmessage: ((ev: WSMessageEvent) => void) | null = null;
    onclose: (() => void) | null = null;
    onerror: ((err: Error) => void) | null = null;
    private socket: Socket;
    private pending: Buffer = Buffer.alloc(0);
    private fragments: Buffer[] = [];
    private fragmentBinary = false;

    constructor(socket: Socket) {
        this.socket = socket;
        socket.on('data', (chunk: Buffer) => { this.feed(chunk); });
        socket.on('close', () => { this.closed(); });
        socket.on('error', (err: Error) => {
            if (this.onerror !== null) this.onerror(err);
        });
    }

    send(data: string | Uint8Array): void {
        if (this.readyState !== 1) return;
        if (typeof data === 'string') this.writeFrame(1, Buffer.from(data, 'utf8'));
        else this.writeFrame(2, Buffer.from(data));
    }

    close(code?: number): void {
        if (this.readyState !== 1) return;
        this.readyState = 2;
        const payload = Buffer.alloc(2);
        payload.writeUInt16BE(code ?? 1000, 0);
        this.writeFrame(8, payload);
        this.socket.end();
    }

    private closed(): void {
        if (this.readyState === 3) return;
        this.readyState = 3;
        if (this.onclose !== null) this.onclose();
    }

    private writeFrame(opcode: number, payload: Buffer): void {
        this.socket.write(encodeFrame(opcode, payload, false));
    }

    // Parse every complete frame buffered so far.
    feed(chunk: Buffer): void {
        this.pending = this.pending.length === 0 ? chunk : Buffer.concat([this.pending, chunk]);
        while (this.readyState === 1 || this.readyState === 2) {
            const parsed = parseFrame(this.pending);
            if (parsed === null) return;
            this.pending = parsed.rest;
            this.frame(parsed.frame.fin, parsed.frame.opcode, parsed.frame.payload);
        }
    }

    private frame(fin: boolean, opcode: number, payload: Buffer): void {
        switch (opcode) {
            case 0: // continuation
                this.fragments.push(payload);
                if (fin) {
                    const whole = Buffer.concat(this.fragments);
                    this.fragments = [];
                    this.deliver(whole, this.fragmentBinary);
                }
                break;
            case 1:
            case 2:
                if (fin) {
                    this.deliver(payload, opcode === 2);
                } else {
                    this.fragments = [payload];
                    this.fragmentBinary = opcode === 2;
                }
                break;
            case 8: // close: echo it, then end
                if (this.readyState === 1) {
                    this.readyState = 2;
                    this.writeFrame(8, payload);
                    this.socket.end();
                }
                break;
            case 9: // ping: the same payload back
                this.writeFrame(10, payload);
                break;
            case 10: // pong
                break;
            default:
                this.socket.destroy();
        }
    }

    private deliver(bytes: Buffer, isBinary: boolean): void {
        const on = this.onmessage;
        if (on !== null) on(new WSMessageEvent(bytes, isBinary));
    }
}

export interface WebSocketServerOptions {
    server: Server;
}

export class WebSocketServer extends EventEmitter {
    constructor(options: WebSocketServerOptions) {
        super();
        options.server.on('upgrade', (req: IncomingMessage, socket: Socket, head: Buffer) => {
            this.handleUpgrade(req, socket, head);
        });
    }

    on(event: 'connection', listener: (socket: WSConnection, req: IncomingMessage) => void): this;
    on(event: string | symbol, listener: (...args: any[]) => void): this;
    on(event: string | symbol, listener: (...args: any[]) => void): this {
        return super.on(event, listener);
    }

    private handleUpgrade(req: IncomingMessage, socket: Socket, head: Buffer): void {
        const upgrade = req.headers.upgrade;
        const key = req.headers['sec-websocket-key'];
        if (upgrade === undefined || upgrade.toLowerCase() !== 'websocket' || typeof key !== 'string') {
            socket.end('HTTP/1.1 400 Bad Request\r\nConnection: close\r\n\r\n');
            return;
        }
        const accept = createHash('sha1').update(key + GUID).digest('base64');
        socket.write('HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: ' + accept + '\r\n\r\n');
        const conn = new WSConnection(socket);
        this.emit('connection', conn, req);
        if (head.length > 0) conn.feed(head);
    }
}
