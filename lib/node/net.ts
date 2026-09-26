// Node's `net`, ported from Node v24's lib/net.js,
// lib/internal/stream_base_commons.js and lib/internal/net.js: a Socket is a
// Duplex over a native TCP handle (lib/native.d.ts), a Server an
// EventEmitter over a listening one. Pipes, IPC handles, BlockList,
// SocketAddress and happy-eyeballs address selection are not ported.
//
// kml:default-namespace — `import net from 'net'` reads this module's
// exports, as Node's default export carries them.
import { EventEmitter } from 'events';
import { Duplex } from 'stream';
import type { DuplexOptions } from 'stream';

// ---- errors (lib/internal/errors.js) ----

class NodeError extends Error {
    code: string;
    constructor(code: string, message: string) {
        super(message);
        this.code = code;
    }
}

class NodeTypeError extends TypeError {
    code: string;
    constructor(code: string, message: string) {
        super(message);
        this.code = code;
    }
}

class NodeRangeError extends RangeError {
    code: string;
    constructor(code: string, message: string) {
        super(message);
        this.code = code;
    }
}

// A system error from a native errno: `${syscall} ${code}` (ErrnoException),
// with `${address}:${port}` after it (ExceptionWithHostPort).
function errnoException(errno: number, syscall: string, address?: string, port?: number): Error {
    const code = __kml_native.errnoName(errno);
    let message = syscall + ' ' + code;
    if (address !== undefined) message += ' ' + address + (port !== undefined && port > 0 ? ':' + port : '');
    const e: any = new Error(message);
    e.errno = __kml_native.uvErrno(errno);
    e.code = code;
    e.syscall = syscall;
    if (address !== undefined) e.address = address;
    if (port !== undefined && port > 0) e.port = port;
    return e;
}

// UVExceptionWithHostPort: `${syscall} ${code}: ${description} ${address}:${port}`.
function uvExceptionWithHostPort(errno: number, syscall: string, address: string, port: number): Error {
    const code = __kml_native.errnoName(errno);
    const details = port > 0 ? ' ' + address + ':' + port : ' ' + address;
    const e: any = new Error(syscall + ' ' + code + ': ' + __kml_native.errnoDesc(errno) + details);
    e.errno = __kml_native.uvErrno(errno);
    e.code = code;
    e.syscall = syscall;
    e.address = address;
    if (port > 0) e.port = port;
    return e;
}

// A failed lookup: `getaddrinfo ENOTFOUND host`.
function dnsException(status: number, hostname: string): Error {
    const code = __kml_native.eaiCode(status - 10000);
    const e: any = new Error('getaddrinfo ' + code + ' ' + hostname);
    e.errno = code === 'ENOTFOUND' ? -3008 : code === 'EAI_AGAIN' ? -3001 : -3007;
    e.code = code;
    e.syscall = 'getaddrinfo';
    e.hostname = hostname;
    return e;
}

// ---- lib/internal/net.js ----

const v4Seg = '(?:25[0-5]|2[0-4][0-9]|1[0-9][0-9]|[1-9][0-9]|[0-9])';
const v4Str = '(?:' + v4Seg + '\\.){3}' + v4Seg;
const IPv4Reg = new RegExp('^' + v4Str + '$');
const v6Seg = '(?:[0-9a-fA-F]{1,4})';
const IPv6Reg = new RegExp('^(?:' +
    '(?:' + v6Seg + ':){7}(?:' + v6Seg + '|:)|' +
    '(?:' + v6Seg + ':){6}(?:' + v4Str + '|:' + v6Seg + '|:)|' +
    '(?:' + v6Seg + ':){5}(?::' + v4Str + '|(?::' + v6Seg + '){1,2}|:)|' +
    '(?:' + v6Seg + ':){4}(?:(?::' + v6Seg + '){0,1}:' + v4Str + '|(?::' + v6Seg + '){1,3}|:)|' +
    '(?:' + v6Seg + ':){3}(?:(?::' + v6Seg + '){0,2}:' + v4Str + '|(?::' + v6Seg + '){1,4}|:)|' +
    '(?:' + v6Seg + ':){2}(?:(?::' + v6Seg + '){0,3}:' + v4Str + '|(?::' + v6Seg + '){1,5}|:)|' +
    '(?:' + v6Seg + ':){1}(?:(?::' + v6Seg + '){0,4}:' + v4Str + '|(?::' + v6Seg + '){1,6}|:)|' +
    '(?::(?:(?::' + v6Seg + '){0,5}:' + v4Str + '|(?::' + v6Seg + '){1,7}|:))' +
    ')(?:%[0-9a-zA-Z-.:]{1,})?$');

export function isIPv4(input: string): boolean {
    return IPv4Reg.test(input);
}

export function isIPv6(input: string): boolean {
    return IPv6Reg.test(input);
}

export function isIP(input: string): number {
    if (isIPv4(input)) return 4;
    if (isIPv6(input)) return 6;
    return 0;
}

export interface AddressInfo {
    address: string;
    family: string;
    port: number;
}

// The handle's local (peer false) or remote address.
function handleAddress(handle: number, peer: boolean): AddressInfo | null {
    const r = __kml_native.tcpAddress(handle, peer);
    if (r < 0) return null;
    const family = Math.floor(r / 65536);
    return { address: __kml_native.lastString(), family: family === 6 ? 'IPv6' : 'IPv4', port: r - family * 65536 };
}

// ---- Socket ----

export interface SocketConstructorOpts {
    allowHalfOpen?: boolean;
    readable?: boolean;
    writable?: boolean;
    noDelay?: boolean;
    keepAlive?: boolean;
    keepAliveInitialDelay?: number;
}

// Node's TcpSocketConnectOpts | IpcSocketConnectOpts as one shape: `port`
// (and `host`) for TCP, `path` for a Unix-domain socket.
export interface NetConnectOpts {
    port?: number;
    host?: string;
    path?: string;
    family?: number;
    noDelay?: boolean;
    keepAlive?: boolean;
    keepAliveInitialDelay?: number;
    timeout?: number;
    allowHalfOpen?: boolean;
}

export type TcpSocketConnectOpts = NetConnectOpts;
export type IpcSocketConnectOpts = NetConnectOpts;

// kml:callable Socket — Node's is a function that constructs when called without `new`
export class Socket extends Duplex {
    connecting = false;
    _hadError = false;
    _handle: number = -1;
    _host: string | null = null;
    // The Server that accepted this socket, or null.
    server: Server | null = null;
    _server: Server | null = null;
    private reading = false;
    private noDelayWanted = false;
    private keepAliveWanted = false;
    private keepAliveDelay = 0;
    private timeoutMs = 0;
    private timeoutTimer: ReturnType<typeof setTimeout> | null = null;
    private bytesReadCount = 0;
    private bytesWrittenCount = 0;
    private peername: AddressInfo | null = null;
    private sockname: AddressInfo | null = null;
    private endedByPeer = false;

    constructor(options?: SocketConstructorOpts & { handle?: number; pauseOnCreate?: boolean }) {
        const dopts: DuplexOptions = {
            allowHalfOpen: options?.allowHalfOpen === true,
            // For backwards compat do not emit close on destroy: the handle's
            // close does.
            emitClose: false,
            autoDestroy: true,
            // Handle strings directly.
            decodeStrings: false,
        };
        super(dopts);
        this.noDelayWanted = options?.noDelay === true;
        this.keepAliveWanted = options?.keepAlive === true;
        this.keepAliveDelay = Math.floor((options?.keepAliveInitialDelay ?? 0) / 1000);
        // Shut down the socket when we're finished with it.
        this.on('end', () => { this.endedByPeer = true; });
        if (options?.handle !== undefined && options.handle >= 0) {
            this._handle = options.handle;
            if (options.readable !== false) {
                if (options.pauseOnCreate) {
                    this.reading = false;
                    this.pause();
                } else {
                    this.read(0);
                }
            }
        }
    }

    // The events, typed as @types/node declares them.
    on(event: 'close', listener: (hadError: boolean) => void): this;
    on(event: 'connect', listener: () => void): this;
    on(event: 'connectionAttempt', listener: (ip: string, port: number, family: number) => void): this;
    on(event: 'connectionAttemptFailed', listener: (ip: string, port: number, family: number, error: Error) => void): this;
    on(event: 'data', listener: (data: Buffer) => void): this;
    on(event: 'drain', listener: () => void): this;
    on(event: 'end', listener: () => void): this;
    on(event: 'error', listener: (err: Error) => void): this;
    on(event: 'lookup', listener: (err: Error, address: string, family: string | number, host: string) => void): this;
    on(event: 'ready', listener: () => void): this;
    on(event: 'timeout', listener: () => void): this;
    on(event: string | symbol, listener: (...args: any[]) => void): this;
    on(event: string | symbol, listener: (...args: any[]) => void): this {
        return super.on(event, listener);
    }

    once(event: 'close', listener: (hadError: boolean) => void): this;
    once(event: 'connect', listener: () => void): this;
    once(event: 'data', listener: (data: Buffer) => void): this;
    once(event: 'drain', listener: () => void): this;
    once(event: 'end', listener: () => void): this;
    once(event: 'error', listener: (err: Error) => void): this;
    once(event: 'ready', listener: () => void): this;
    once(event: 'timeout', listener: () => void): this;
    once(event: string | symbol, listener: (...args: any[]) => void): this;
    once(event: string | symbol, listener: (...args: any[]) => void): this {
        return super.once(event, listener);
    }

    get bytesRead(): number { return this.bytesReadCount; }
    get bytesWritten(): number { return this.bytesWrittenCount; }
    get pending(): boolean { return this._handle < 0 || this.connecting; }

    get readyState(): string {
        if (this.connecting) return 'opening';
        if (this.readable && this.writable) return 'open';
        if (this.readable && !this.writable) return 'readOnly';
        if (!this.readable && this.writable) return 'writeOnly';
        return 'closed';
    }

    private getpeername(): AddressInfo | null {
        if (this._handle < 0 || this.connecting) return this.peername;
        if (this.peername === null) this.peername = handleAddress(this._handle, true);
        return this.peername;
    }

    private getsockname(): AddressInfo | null {
        if (this._handle < 0) return this.sockname;
        if (this.sockname === null) this.sockname = handleAddress(this._handle, false);
        return this.sockname;
    }

    get remoteAddress(): string | undefined { return this.getpeername()?.address; }
    get remoteFamily(): string | undefined { return this.getpeername()?.family; }
    get remotePort(): number | undefined { return this.getpeername()?.port; }
    get localAddress(): string | undefined { return this.getsockname()?.address; }
    get localFamily(): string | undefined { return this.getsockname()?.family; }
    get localPort(): number | undefined { return this.getsockname()?.port; }

    address(): AddressInfo | {} {
        return this.getsockname() ?? {};
    }

    // Node's stream_base_commons onStreamRead, as the native handle calls it.
    private onRead(status: number, nread: number): void {
        this.refreshTimer();
        if (nread > 0 && !this.destroyed) {
            const buf = Buffer.allocUnsafe(nread);
            __kml_native.tcpTake(this._handle, buf);
            this.bytesReadCount += nread;
            if (!this.push(buf)) {
                this.reading = false;
                if (!this.destroyed) __kml_native.tcpReadStop(this._handle);
            }
            return;
        }
        if (status !== 0) {
            this.destroy(errnoException(status, 'read'));
            return;
        }
        // The end of the stream: push null before 'close' is possible, so
        // the order of events is 'end' -> 'close'.
        if (!this._readableState!.endEmitted) {
            this.push(null);
            this.read(0);
        }
    }

    private tryReadStart(): void {
        this.reading = true;
        __kml_native.tcpReadStart(this._handle, (status: number, nread: number) => { this.onRead(status, nread); });
    }

    _read(size: number): void {
        if (this.connecting || this._handle < 0) {
            this.once('connect', () => { this._read(size); });
        } else if (!this.reading) {
            this.tryReadStart();
        }
    }

    _write(data: any, encoding: BufferEncoding, cb: (error?: Error | null) => void): void {
        this.writeGeneric(data, encoding, cb);
    }

    private writeGeneric(data: any, encoding: BufferEncoding, cb: (error?: Error | null) => void): void {
        // Still connecting: buffer this one until the connect.
        if (this.connecting) {
            const onClose = () => {
                cb(new NodeError('ERR_SOCKET_CLOSED_BEFORE_CONNECTION', 'Socket closed before the connection was established'));
            };
            this.once('connect', () => {
                this.off('close', onClose);
                this.writeGeneric(data, encoding, cb);
            });
            this.once('close', onClose);
            return;
        }
        if (this._handle < 0) {
            cb(new NodeError('ERR_SOCKET_CLOSED', 'Socket is closed'));
            return;
        }
        this.refreshTimer();
        const buf: Buffer = typeof data === 'string' ? Buffer.from(data as string, encoding) : data as Buffer;
        const r = __kml_native.tcpWrite(this._handle, buf, 0, buf.length, (status: number, unused: number) => {
            if (status !== 0) {
                cb(errnoException(status, 'write'));
                return;
            }
            this.bytesWrittenCount += buf.length;
            this.refreshTimer();
            cb(null);
        });
        if (r < 0) {
            cb(errnoException(-r, 'write'));
        } else if (r === 1) {
            // The request completed synchronously.
            this.bytesWrittenCount += buf.length;
            cb();
        }
    }

    _final(cb: (error?: Error | null) => void): void {
        // Still connecting: defer handling `_final` until 'connect'.
        if (this.connecting) {
            this.once('connect', () => { this._final(cb); });
            return;
        }
        if (this._handle < 0) {
            cb();
            return;
        }
        // afterShutdown: the shutdown's own status is not reported.
        __kml_native.tcpShutdown(this._handle, (status: number, unused: number) => { cb(); });
    }

    _destroy(exception: Error | null, cb: (error?: Error | null) => void): void {
        this.connecting = false;
        this.clearTimer();
        if (this._handle >= 0) {
            const isException = exception ? true : false;
            const handle = this._handle;
            this._handle = -1;
            this.sockname = null;
            __kml_native.tcpClose(handle, (status: number, unused: number) => {
                this.emit('close', isException);
            });
            cb(exception);
        } else {
            cb(exception);
            process.nextTick(() => { this.emit('close', false); });
        }
        const server = this._server;
        if (server !== null) {
            server._connections--;
            server._emitCloseIfDrained();
        }
    }

    // Hand the handle to a socket that wraps it (a TLSSocket): this one lets
    // go of it without closing it, as Node's tls_wrap takes a socket's handle.
    _kmlReleaseHandle(): number {
        const h = this._handle;
        if (h >= 0) __kml_native.tcpReadStop(h);
        this.clearTimer();
        this._handle = -1;
        this.reading = false;
        this._server = null;
        return h;
    }

    destroySoon(): void {
        if (this.writable) this.end();
        if (this.writableFinished) this.destroy();
        else this.once('finish', () => { this.destroy(); });
    }

    setNoDelay(enable?: boolean): this {
        const on = enable === undefined ? true : enable;
        if (this._handle < 0) {
            this.noDelayWanted = on;
            return this;
        }
        if (on !== this.noDelayWanted) {
            this.noDelayWanted = on;
            __kml_native.tcpSetNoDelay(this._handle, on);
        }
        return this;
    }

    setKeepAlive(enable?: boolean, initialDelay?: number): this {
        const on = enable === true;
        const delay = Math.floor((initialDelay ?? 0) / 1000);
        if (this._handle < 0) {
            this.keepAliveWanted = on;
            this.keepAliveDelay = delay;
            return this;
        }
        if (on !== this.keepAliveWanted || (on && this.keepAliveDelay !== delay)) {
            this.keepAliveWanted = on;
            this.keepAliveDelay = delay;
            __kml_native.tcpSetKeepAlive(this._handle, on, delay);
        }
        return this;
    }

    ref(): this {
        if (this._handle >= 0) __kml_native.tcpRef(this._handle, true);
        return this;
    }

    unref(): this {
        if (this._handle >= 0) __kml_native.tcpRef(this._handle, false);
        return this;
    }

    // setStreamTimeout: 'timeout' after ms of inactivity (0 disables).
    setTimeout(ms: number, callback?: () => void): this {
        this.timeoutMs = ms;
        if (callback) {
            if (ms === 0) this.removeListener('timeout', callback);
            else this.once('timeout', callback);
        }
        this.refreshTimer();
        return this;
    }

    get timeout(): number | undefined {
        return this.timeoutMs > 0 ? this.timeoutMs : undefined;
    }

    private clearTimer(): void {
        if (this.timeoutTimer !== null) {
            clearTimeout(this.timeoutTimer);
            this.timeoutTimer = null;
        }
    }

    private refreshTimer(): void {
        this.clearTimer();
        if (this.timeoutMs > 0 && !this.destroyed) {
            // Node's socket timeouts are unref'd timers: they fire while the
            // socket keeps the loop, but do not keep it themselves.
            const t = setTimeout(() => {
                this.timeoutTimer = null;
                this.emit('timeout');
            }, this.timeoutMs);
            t.unref();
            this.timeoutTimer = t;
        }
    }

    connect(options: NetConnectOpts, connectionListener?: () => void): this;
    connect(port: number, host: string, connectionListener?: () => void): this;
    connect(port: number, connectionListener?: () => void): this;
    connect(arg0: NetConnectOpts | number, arg1?: string | Listener, arg2?: Listener): this {
        const [options, cb] = normalizeArgs(arg0, arg1, arg2);
        if (cb !== null) this.once('connect', cb);
        if (options.port === undefined && options.path === undefined) {
            throw new NodeTypeError('ERR_MISSING_ARGS', 'The "options" or "port" or "path" argument must be specified');
        }
        this.connecting = true;
        if (options.noDelay === true) this.noDelayWanted = true;
        if (options.keepAlive === true) {
            this.keepAliveWanted = true;
            this.keepAliveDelay = Math.floor((options.keepAliveInitialDelay ?? 0) / 1000);
        }
        if (options.timeout !== undefined) this.setTimeout(options.timeout);
        const path = options.path;
        if (path !== undefined) this.internalConnectPipe(path);
        else this.lookupAndConnect(options);
        return this;
    }

    private lookupAndConnect(options: NetConnectOpts): void {
        const host: string = options.host || 'localhost';
        const port = validatePort(options.port ?? 0);
        this._host = host;
        const addressType = isIP(host);
        if (addressType) {
            process.nextTick(() => {
                if (this.connecting) this.internalConnect(host, port, addressType);
            });
            return;
        }
        __kml_native.dnsLookup(host, options.family ?? 0, (status: number, family: number) => {
            const address = status === 0 ? __kml_native.lastString() : '';
            const err = status === 0 ? null : dnsException(status, host);
            this.emit('lookup', err, address, family, host);
            // It's possible we were destroyed while looking this up.
            if (!this.connecting) return;
            if (err !== null) {
                process.nextTick(() => { this.connecting = false; this.destroy(err); });
                return;
            }
            this.internalConnect(address, port, family);
        });
    }

    private internalConnectPipe(path: string): void {
        const h = __kml_native.pipeConnect(path, (status: number, unused: number) => {
            this.afterConnect(status, path, 0);
        });
        if (h < 0) {
            this.destroy(errnoException(-h, 'connect', path));
            return;
        }
        this._handle = h;
    }

    private internalConnect(address: string, port: number, addressType: number): void {
        this.emit('connectionAttempt', address, port, addressType);
        const h = __kml_native.tcpConnect(address, port, (status: number, unused: number) => {
            this.afterConnect(status, address, port);
        });
        if (h < 0) {
            this.destroy(errnoException(-h, 'connect', address, port));
            return;
        }
        this._handle = h;
    }

    private afterConnect(status: number, address: string, port: number): void {
        // The callback may come after a destroy.
        if (this.destroyed) return;
        this.connecting = false;
        this.sockname = null;
        if (status === 0) {
            this.refreshTimer();
            if (this.noDelayWanted) __kml_native.tcpSetNoDelay(this._handle, true);
            if (this.keepAliveWanted) __kml_native.tcpSetKeepAlive(this._handle, true, this.keepAliveDelay);
            this.emit('connect');
            this.emit('ready');
            // Start the first read, or get an immediate EOF.
            if (this.readable && !this.isPaused()) this.read(0);
        } else {
            const ex = errnoException(status, 'connect', address, port);
            this.emit('connectionAttemptFailed', address, port, isIP(address), ex);
            this.destroy(ex);
        }
    }
}

export { Socket as Stream };

function validatePort(port: number | string): number {
    const n = typeof port === 'string' ? (port.trim() !== '' ? Number(port) : NaN) : port;
    if (!Number.isInteger(n) || n < 0 || n > 65535) {
        throw new NodeRangeError('ERR_SOCKET_BAD_PORT', 'Port should be >= 0 and < 65536. Received ' + (typeof port === 'string' ? "type string ('" + port + "')" : 'type number (' + String(port) + ')') + '.');
    }
    return n;
}

type Listener = () => void;

// normalizeArgs: ([port][, host][, cb]) or (options[, cb]).
function normalizeArgs(arg0: NetConnectOpts | number | undefined, arg1?: string | Listener, arg2?: Listener): [NetConnectOpts, Listener | null] {
    let options: NetConnectOpts;
    let cb: Listener | null = null;
    if (typeof arg0 === 'number' || arg0 === undefined) {
        options = { port: arg0 as number };
        if (typeof arg1 === 'string') options.host = arg1;
    } else {
        options = arg0;
    }
    if (typeof arg2 === 'function') cb = arg2;
    else if (typeof arg1 === 'function') cb = arg1;
    return [options, cb];
}

// ---- Server ----

export interface ServerOpts {
    allowHalfOpen?: boolean;
    pauseOnConnect?: boolean;
    noDelay?: boolean;
    keepAlive?: boolean;
    keepAliveInitialDelay?: number;
    highWaterMark?: number;
}

export interface ListenOptions {
    port?: number;
    host?: string;
    path?: string;
    backlog?: number;
}

export interface DropArgument {
    localAddress?: string;
    localPort?: number;
    localFamily?: string;
    remoteAddress?: string;
    remotePort?: number;
    remoteFamily?: string;
}

// kml:callable Server — Node's is a function that constructs when called without `new`
export class Server extends EventEmitter {
    _connections = 0;
    _handle: number = -1;
    private listeningId = 1;
    private unrefd = false;
    private pipeName: string | null = null;
    allowHalfOpen: boolean;
    pauseOnConnect: boolean;
    noDelay: boolean;
    keepAlive: boolean;
    keepAliveInitialDelay: number;
    highWaterMark: number;
    maxConnections: number | undefined = undefined;

    constructor(connectionListener?: (socket: Socket) => void);
    constructor(options?: ServerOpts, connectionListener?: (socket: Socket) => void);
    constructor(options?: ServerOpts | ((socket: Socket) => void), connectionListener?: (socket: Socket) => void) {
        super();
        let opts: ServerOpts = {};
        if (typeof options === 'function') {
            this.on('connection', options);
        } else {
            if (options !== undefined) opts = options;
            if (typeof connectionListener === 'function') this.on('connection', connectionListener);
        }
        this.allowHalfOpen = opts.allowHalfOpen === true;
        this.pauseOnConnect = opts.pauseOnConnect === true;
        this.noDelay = opts.noDelay === true;
        this.keepAlive = opts.keepAlive === true;
        this.keepAliveInitialDelay = Math.floor((opts.keepAliveInitialDelay ?? 0) / 1000);
        this.highWaterMark = opts.highWaterMark ?? 64 * 1024;
    }

    // The events, typed as @types/node declares them.
    on(event: 'close', listener: () => void): this;
    on(event: 'connection', listener: (socket: Socket) => void): this;
    on(event: 'error', listener: (err: Error) => void): this;
    on(event: 'listening', listener: () => void): this;
    on(event: 'drop', listener: (data?: DropArgument) => void): this;
    on(event: string | symbol, listener: (...args: any[]) => void): this;
    on(event: string | symbol, listener: (...args: any[]) => void): this {
        return super.on(event, listener);
    }

    once(event: 'close', listener: () => void): this;
    once(event: 'connection', listener: (socket: Socket) => void): this;
    once(event: 'error', listener: (err: Error) => void): this;
    once(event: 'listening', listener: () => void): this;
    once(event: 'drop', listener: (data?: DropArgument) => void): this;
    once(event: string | symbol, listener: (...args: any[]) => void): this;
    once(event: string | symbol, listener: (...args: any[]) => void): this {
        return super.once(event, listener);
    }

    get listening(): boolean {
        return this._handle >= 0;
    }

    listen(port?: number, hostname?: string, backlog?: number, listeningListener?: () => void): this;
    listen(port?: number, hostname?: string, listeningListener?: () => void): this;
    listen(port?: number, backlog?: number, listeningListener?: () => void): this;
    listen(port?: number, listeningListener?: () => void): this;
    listen(path: string, backlog?: number, listeningListener?: () => void): this;
    listen(path: string, listeningListener?: () => void): this;
    listen(options: ListenOptions, listeningListener?: () => void): this;
    listen(arg0?: number | string | ListenOptions, arg1?: string | number | Listener, arg2?: number | Listener, arg3?: Listener): this {
        let options: ListenOptions;
        if (typeof arg0 === 'string') {
            options = { path: arg0 };
        } else if (typeof arg0 === 'number' || arg0 === undefined) {
            options = { port: arg0 };
            if (typeof arg1 === 'string') options.host = arg1;
        } else {
            options = arg0;
        }
        let cb: Listener | null = null;
        if (typeof arg3 === 'function') cb = arg3;
        else if (typeof arg2 === 'function') cb = arg2;
        else if (typeof arg1 === 'function') cb = arg1;
        if (this._handle >= 0) {
            throw new NodeError('ERR_SERVER_ALREADY_LISTEN', 'Listen method has been called more than once without closing.');
        }
        if (cb !== null) this.once('listening', cb);
        const backlog = (typeof arg1 === 'number' ? arg1 : 0) || (typeof arg2 === 'number' ? arg2 : 0) || options.backlog || 511;
        this.listeningId++;
        const path = options.path;
        if (path !== undefined) {
            const ph = __kml_native.pipeListen(path, backlog, (status: number, client: number) => {
                this.onConnection(status, client);
            });
            this.afterListen(ph, path, -1);
            return this;
        }
        const port = options.port === undefined ? 0 : validatePort(options.port);
        const host: string = options.host ?? '';
        const h = __kml_native.tcpListen(host, port, backlog, (status: number, client: number) => {
            this.onConnection(status, client);
        });
        this.afterListen(h, host === '' ? '::' : host, port);
        return this;
    }

    private afterListen(h: number, address: string, port: number): void {
        if (h < 0) {
            const error = uvExceptionWithHostPort(-h, 'listen', address, port);
            process.nextTick(() => { this.emit('error', error); });
            return;
        }
        this._handle = h;
        this.pipeName = port < 0 ? address : null;
        if (this.unrefd) __kml_native.tcpRef(h, false);
        process.nextTick(() => {
            // Ensure the handle hasn't closed.
            if (this._handle >= 0) this.emit('listening');
        });
    }

    private onConnection(status: number, client: number): void {
        if (status !== 0) {
            this.emit('error', errnoException(status, 'accept'));
            return;
        }
        if (this.maxConnections !== undefined && this._connections >= this.maxConnections) {
            const local = handleAddress(client, false);
            const remote = handleAddress(client, true);
            this.emit('drop', {
                localAddress: local?.address, localPort: local?.port, localFamily: local?.family,
                remoteAddress: remote?.address, remotePort: remote?.port, remoteFamily: remote?.family,
            });
            __kml_native.tcpClose(client, (s: number, u: number) => {});
            return;
        }
        const socket = new Socket({
            handle: client,
            allowHalfOpen: this.allowHalfOpen,
            pauseOnCreate: this.pauseOnConnect,
            readable: true,
            writable: true,
        });
        if (this.noDelay) socket.setNoDelay(true);
        if (this.keepAlive) socket.setKeepAlive(true, this.keepAliveInitialDelay * 1000);
        this._connections++;
        socket.server = this;
        socket._server = this;
        this.emit('connection', socket);
    }

    address(): AddressInfo | string | null {
        if (this._handle < 0) return null;
        if (this.pipeName !== null) return this.pipeName;
        const a = handleAddress(this._handle, false);
        if (a === null) throw errnoException(9, 'address');
        return a;
    }

    close(cb?: (err?: Error) => void): this {
        this.listeningId++;
        if (typeof cb === 'function') {
            if (this._handle < 0) {
                this.once('close', () => {
                    cb(new NodeError('ERR_SERVER_NOT_RUNNING', 'Server is not running.'));
                });
            } else {
                this.once('close', () => { cb(); });
            }
        }
        if (this._handle >= 0) {
            __kml_native.tcpClose(this._handle, (s: number, u: number) => {});
            this._handle = -1;
        }
        this._emitCloseIfDrained();
        return this;
    }

    _emitCloseIfDrained(): void {
        if (this._handle >= 0 || this._connections) return;
        process.nextTick(() => { this.emit('close'); });
    }

    getConnections(cb: (error: Error | null, count: number) => void): this {
        const n = this._connections;
        process.nextTick(() => { cb(null, n); });
        return this;
    }

    ref(): this {
        this.unrefd = false;
        if (this._handle >= 0) __kml_native.tcpRef(this._handle, true);
        return this;
    }

    unref(): this {
        this.unrefd = true;
        if (this._handle >= 0) __kml_native.tcpRef(this._handle, false);
        return this;
    }
}

export function createServer(connectionListener?: (socket: Socket) => void): Server;
export function createServer(options?: ServerOpts, connectionListener?: (socket: Socket) => void): Server;
export function createServer(options?: ServerOpts | ((socket: Socket) => void), connectionListener?: (socket: Socket) => void): Server {
    return new Server(options, connectionListener);
}

export function connect(options: NetConnectOpts, connectionListener?: () => void): Socket;
export function connect(port: number, host?: string, connectionListener?: () => void): Socket;
export function connect(arg0: NetConnectOpts | number, arg1?: string | Listener, arg2?: Listener): Socket {
    const [options, cb] = normalizeArgs(arg0, arg1, arg2);
    const socket = new Socket();
    if (options.timeout) socket.setTimeout(options.timeout);
    return cb !== null ? socket.connect(options, cb) : socket.connect(options);
}

export function createConnection(options: NetConnectOpts, connectionListener?: () => void): Socket;
export function createConnection(port: number, host?: string, connectionListener?: () => void): Socket;
export function createConnection(arg0: NetConnectOpts | number, arg1?: string | Listener, arg2?: Listener): Socket {
    if (typeof arg0 === 'number') {
        if (typeof arg1 === 'string') return arg2 !== undefined ? connect(arg0, arg1, arg2) : connect(arg0, arg1);
        return arg1 !== undefined ? connect(arg0, undefined, arg1) : connect(arg0);
    }
    return typeof arg1 === 'function' ? connect(arg0, arg1) : connect(arg0);
}
