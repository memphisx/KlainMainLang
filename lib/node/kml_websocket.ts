// kml:global
// WebSocket — the global, as Node's (undici's lib/web/websocket) has it
// (TDD-00232 Stage 2): a WHATWG WebSocket client over net/tls. The
// constructor returns CONNECTING; the connection opens on the event loop.
import * as net from 'net';
import * as tls from 'tls';
import { createHash, randomBytes } from 'crypto';
import { encodeFrame, parseFrame, isUtf8 } from './internal_websocket_frame';

const GUID = '258EAFA5-E914-47DA-95CA-C5AB0DC85B11';

const CONNECTING = 0;
const OPEN = 1;
const CLOSING = 2;
const CLOSED = 3;

interface SendItem {
    opcode: number;
    bytes: Buffer | null;
    blob: Blob | null;
}

export class WebSocket extends EventTarget {
    static readonly CONNECTING = 0;
    static readonly OPEN = 1;
    static readonly CLOSING = 2;
    static readonly CLOSED = 3;
    readonly CONNECTING = 0;
    readonly OPEN = 1;
    readonly CLOSING = 2;
    readonly CLOSED = 3;

    #url: string;
    #readyState = CONNECTING;
    #protocol = '';
    #extensions = '';
    #bufferedAmount = 0;
    #binaryType = 'blob';
    #socket: any = null;
    #key = '';
    #offered: string[] = [];
    #head = Buffer.alloc(0);
    #pending = Buffer.alloc(0);
    #fragments: Buffer[] = [];
    #fragmentBinary = false;
    #queue: SendItem[] = [];
    #sentClose = false;
    #receivedClose = false;
    #closeCode = 1005;
    #closeReason = '';
    #failed = false;
    #onopen: any = null;
    #onmessage: any = null;
    #onerror: any = null;
    #onclose: any = null;
    #handlers = new Set<string>();

    constructor(url: string | URL, protocols?: string | string[]) {
        super();
        let parsed: URL;
        try {
            parsed = new URL(`${url}`);
        } catch (e) {
            throw new DOMException(`${e}`, 'SyntaxError');
        }
        if (parsed.protocol === 'http:') parsed.protocol = 'ws:';
        else if (parsed.protocol === 'https:') parsed.protocol = 'wss:';
        if (parsed.protocol !== 'ws:' && parsed.protocol !== 'wss:') {
            throw new DOMException('expected a ws: or wss: url', 'SyntaxError');
        }
        if (parsed.hash !== '' || `${url}`.endsWith('#')) {
            throw new DOMException('hash', 'SyntaxError');
        }
        const list: string[] = protocols === undefined ? [] : typeof protocols === 'string' ? [protocols] : protocols;
        for (let i = 0; i < list.length; i++) {
            if (list.indexOf(list[i]) !== i) throw new DOMException('Invalid Sec-WebSocket-Protocol value', 'SyntaxError');
        }
        this.#url = parsed.href;
        this.#offered = list;
        this.#connect(parsed);
    }

    get url(): string { return this.#url; }
    get readyState(): number { return this.#readyState; }
    get protocol(): string { return this.#protocol; }
    get extensions(): string { return this.#extensions; }
    get bufferedAmount(): number { return this.#bufferedAmount; }
    get binaryType(): string { return this.#binaryType; }
    set binaryType(type: string) {
        if (type === 'blob' || type === 'arraybuffer') this.#binaryType = type;
    }

    get onopen(): any { return this.#onopen; }
    set onopen(h: any) { this.#onopen = this.#handler('open', h); }
    get onmessage(): any { return this.#onmessage; }
    set onmessage(h: any) { this.#onmessage = this.#handler('message', h); }
    get onerror(): any { return this.#onerror; }
    set onerror(h: any) { this.#onerror = this.#handler('error', h); }
    get onclose(): any { return this.#onclose; }
    set onclose(h: any) { this.#onclose = this.#handler('close', h); }

    // An event handler attribute: registered once, at its first assignment.
    #handler(type: string, h: any): any {
        const fn = typeof h === 'function' ? h : null;
        if (fn !== null && !this.#handlers.has(type)) {
            this.#handlers.add(type);
            this.addEventListener(type, (ev: Event) => {
                const cur: any = type === 'open' ? this.#onopen : type === 'message' ? this.#onmessage : type === 'error' ? this.#onerror : this.#onclose;
                if (cur !== null) cur.call(this, ev);
            });
        }
        return fn;
    }

    send(data: any): void {
        if (this.#readyState === CONNECTING) {
            throw new DOMException('Sent before connected.', 'InvalidStateError');
        }
        let item: SendItem;
        if (typeof data === 'string') {
            item = { opcode: 1, bytes: Buffer.from(data, 'utf8'), blob: null };
        } else if (data instanceof Blob) {
            item = { opcode: 2, bytes: null, blob: data };
        } else {
            item = { opcode: 2, bytes: Buffer.from(data), blob: null };
        }
        const size = item.blob !== null ? item.blob.size : item.bytes!.length;
        if (this.#readyState !== OPEN || this.#sentClose) {
            this.#bufferedAmount += size;
            return;
        }
        // A Blob's bytes arrive later; frames stay in the order sent
        // (undici's SendQueue).
        if (this.#queue.length === 0 && item.blob === null) {
            this.#writeFrame(item.opcode, item.bytes!);
            return;
        }
        this.#bufferedAmount += size;
        this.#queue.push(item);
        if (this.#queue.length === 1) this.#drain();
    }

    #drain(): void {
        const item = this.#queue[0];
        const done = (bytes: Buffer): void => {
            this.#bufferedAmount -= bytes.length;
            this.#queue.shift();
            if (this.#readyState === OPEN) this.#writeFrame(item.opcode, bytes);
            if (this.#queue.length > 0) this.#drain();
        };
        if (item.blob === null) {
            done(item.bytes!);
            return;
        }
        item.blob.arrayBuffer().then((ab: ArrayBuffer) => { done(Buffer.from(ab)); });
    }

    close(code?: number, reason?: string): void {
        if (code !== undefined && code !== 1000 && (code < 3000 || code > 4999)) {
            throw new DOMException('invalid code', 'InvalidAccessError');
        }
        const reasonBytes = reason === undefined ? Buffer.alloc(0) : Buffer.from(reason, 'utf8');
        if (reasonBytes.length > 123) {
            throw new DOMException(`Reason must be less than 123 bytes; received ${reasonBytes.length}`, 'SyntaxError');
        }
        if (this.#readyState === CLOSING || this.#readyState === CLOSED) return;
        if (this.#readyState === CONNECTING) {
            this.#fail('WebSocket was closed before the connection was established');
            return;
        }
        this.#readyState = CLOSING;
        let payload = Buffer.alloc(0);
        if (code !== undefined) {
            payload = Buffer.alloc(2 + reasonBytes.length);
            payload.writeUInt16BE(code, 0);
            reasonBytes.copy(payload, 2);
        }
        this.#sentClose = true;
        this.#writeFrame(8, payload);
    }

    #connect(url: URL): void {
        const secure = url.protocol === 'wss:';
        const port = url.port !== '' ? Number(url.port) : secure ? 443 : 80;
        const host = url.hostname;
        this.#key = randomBytes(16).toString('base64');
        const path = (url.pathname === '' ? '/' : url.pathname) + url.search;
        let request = `GET ${path} HTTP/1.1\r\nHost: ${url.host}\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n` +
            `Sec-WebSocket-Key: ${this.#key}\r\nSec-WebSocket-Version: 13\r\n`;
        if (this.#offered.length > 0) request += `Sec-WebSocket-Protocol: ${this.#offered.join(', ')}\r\n`;
        request += '\r\n';
        const socket: any = secure ? tls.connect({ host, port }) : net.connect({ host, port });
        this.#socket = socket;
        socket.on(secure ? 'secureConnect' : 'connect', () => { socket.write(request); });
        socket.on('data', (chunk: Buffer) => { this.#data(chunk); });
        socket.on('error', (err: Error) => { this.#fail(err.message); });
        socket.on('close', () => { this.#socketClosed(); });
    }

    #data(chunk: Buffer): void {
        if (this.#readyState === CONNECTING) {
            this.#head = this.#head.length === 0 ? chunk : Buffer.concat([this.#head, chunk]);
            const end = this.#head.indexOf('\r\n\r\n');
            if (end < 0) return;
            const rest = this.#head.subarray(end + 4);
            if (!this.#handshake(this.#head.subarray(0, end).toString('latin1'))) return;
            this.#readyState = OPEN;
            this.dispatchEvent(new Event('open'));
            if (rest.length > 0) this.#feed(rest);
            return;
        }
        this.#feed(chunk);
    }

    // Validates the server's handshake response (RFC 6455 §4.1).
    #handshake(text: string): boolean {
        const lines = text.split('\r\n');
        const status = lines[0].split(' ');
        if (status.length < 2 || status[1] !== '101') {
            this.#fail('Received network error or non-101 status code.');
            return false;
        }
        const headers = new Map<string, string>();
        for (let i = 1; i < lines.length; i++) {
            const colon = lines[i].indexOf(':');
            if (colon > 0) headers.set(lines[i].substring(0, colon).trim().toLowerCase(), lines[i].substring(colon + 1).trim());
        }
        if ((headers.get('upgrade') ?? '').toLowerCase() !== 'websocket' || (headers.get('connection') ?? '').toLowerCase() !== 'upgrade') {
            this.#fail('Server did not set Upgrade or Connection header correctly.');
            return false;
        }
        const accept = createHash('sha1').update(this.#key + GUID).digest('base64');
        if (headers.get('sec-websocket-accept') !== accept) {
            this.#fail('Incorrect hash received in Sec-WebSocket-Accept header.');
            return false;
        }
        const protocol = headers.get('sec-websocket-protocol');
        if (protocol !== undefined) {
            if (this.#offered.indexOf(protocol) < 0) {
                this.#fail('Protocol was not set in the opening handshake.');
                return false;
            }
            this.#protocol = protocol;
        }
        this.#extensions = headers.get('sec-websocket-extensions') ?? '';
        return true;
    }

    #feed(chunk: Buffer): void {
        this.#pending = this.#pending.length === 0 ? chunk : Buffer.concat([this.#pending, chunk]);
        while (!this.#failed) {
            const parsed = parseFrame(this.#pending);
            if (parsed === null) return;
            if (parsed.frame.masked) {
                this.#fail('Received a masked frame from the server.');
                return;
            }
            this.#pending = parsed.rest;
            this.#frame(parsed.frame.fin, parsed.frame.opcode, parsed.frame.payload);
        }
    }

    #frame(fin: boolean, opcode: number, payload: Buffer): void {
        switch (opcode) {
            case 0:
                this.#fragments.push(payload);
                if (fin) {
                    const whole = Buffer.concat(this.#fragments);
                    this.#fragments = [];
                    this.#message(whole, this.#fragmentBinary);
                }
                break;
            case 1:
            case 2:
                if (fin) {
                    this.#message(payload, opcode === 2);
                } else {
                    this.#fragments = [payload];
                    this.#fragmentBinary = opcode === 2;
                }
                break;
            case 8:
                this.#receivedClose = true;
                if (payload.length >= 2) {
                    this.#closeCode = payload.readUInt16BE(0);
                    this.#closeReason = payload.subarray(2).toString('utf8');
                }
                if (!this.#sentClose) {
                    this.#sentClose = true;
                    this.#readyState = CLOSING;
                    this.#writeFrame(8, payload.length >= 2 ? payload.subarray(0, 2) : Buffer.alloc(0));
                }
                this.#socket.end();
                break;
            case 9:
                this.#writeFrame(10, payload);
                break;
            case 10:
                break;
            default:
                this.#fail('Unrecognized frame opcode.');
        }
    }

    #message(bytes: Buffer, binary: boolean): void {
        if (this.#readyState !== OPEN) return;
        let data: any;
        if (!binary) {
            if (!isUtf8(bytes)) {
                this.#fail('Invalid UTF-8 received.', 1007);
                return;
            }
            data = bytes.toString('utf8');
        } else if (this.#binaryType === 'arraybuffer') {
            const ab = new ArrayBuffer(bytes.length);
            new Uint8Array(ab).set(bytes);
            data = ab;
        } else {
            data = new Blob([bytes]);
        }
        this.dispatchEvent(new MessageEvent('message', { data, origin: new URL(this.#url).origin }));
    }

    #writeFrame(opcode: number, payload: Buffer): void {
        const len = payload.length;
        this.#bufferedAmount += len;
        this.#socket.write(encodeFrame(opcode, payload, true), () => { this.#bufferedAmount -= len; });
    }

    // Fails the connection (undici's failWebsocketConnection): an open one
    // is sent a close frame with code first; error, then close with 1006.
    #fail(message: string, code: number = 0): void {
        if (this.#failed || this.#readyState === CLOSED) return;
        if (code !== 0 && this.#readyState === OPEN && !this.#sentClose) {
            const payload = Buffer.alloc(2);
            payload.writeUInt16BE(code, 0);
            this.#sentClose = true;
            this.#writeFrame(8, payload);
        }
        this.#failed = true;
        this.#readyState = CLOSING;
        if (this.#socket !== null) this.#socket.destroy();
        this.dispatchEvent(new ErrorEvent('error', { message, error: new TypeError(message) }));
    }

    #socketClosed(): void {
        if (this.#readyState === CLOSED) return;
        this.#readyState = CLOSED;
        const clean = this.#sentClose && this.#receivedClose && !this.#failed;
        const code = clean ? this.#closeCode : 1006;
        this.dispatchEvent(new CloseEvent('close', { wasClean: clean, code, reason: clean ? this.#closeReason : '' }));
    }
}
