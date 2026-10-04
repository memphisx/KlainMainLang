// Node's `dgram`, ported from Node v24's lib/dgram.js over a datagram
// handle (lib/native.d.ts udpSocket & co., Node's udp_wrap): bind resolves
// its address and emits 'listening', sends before the bind completes wait
// in its queue, 'message' carries a Buffer and { address, family, port,
// size }, connect/disconnect set a default destination. A datagram is sent
// synchronously (a full send buffer is waited out), so the send queue is
// always empty. Cluster-shared sockets and handle passing are not ported.
//
// kml:default-namespace — `import dgram from 'dgram'` reads this module's
// exports, as Node's default export carries them.

import { EventEmitter } from 'events';
import { isIP } from 'net';
import { NodeError, NodeTypeError, NodeRangeError } from './internal_errors';

// ---- errors (lib/internal/errors.js) ----

function received(value: any): string {
    if (value === null || value === undefined) return ' Received ' + String(value);
    if (typeof value === 'function') return ' Received function';
    if (typeof value === 'object') return ' Received an instance of Object';
    let shown = String(value);
    if (shown.length > 28) shown = shown.slice(0, 25) + '...';
    if (typeof value === 'string') shown = "'" + shown + "'";
    return ' Received type ' + typeof value + ' (' + shown + ')';
}

// ErrnoException: `${syscall} ${code}`; ExceptionWithHostPort adds
// ` ${address}:${port}`.
function errnoException(errnoIn: number, syscall: string, address?: string, port?: number): Error {
    const errno = errnoIn < 0 ? -errnoIn : errnoIn;
    const code = __kml_native.errnoName(errno);
    let message = syscall + ' ' + code;
    if (address !== undefined && address !== null) message += ' ' + address + (port !== undefined && port > 0 ? ':' + port : '');
    const e: any = new Error(message);
    e.errno = __kml_native.uvErrno(errno);
    e.code = code;
    e.syscall = syscall;
    if (address !== undefined && address !== null) e.address = address;
    if (port !== undefined && port > 0) e.port = port;
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

function validatePort(port: any, name: string, allowZero: boolean): number {
    const ok = (typeof port === 'number' || typeof port === 'string') &&
        !(typeof port === 'string' && (port as string).trim().length === 0) &&
        +port === (+port >>> 0) && +port <= 0xFFFF && (allowZero || +port !== 0);
    if (!ok) {
        const shown = typeof port === 'string' ? "'" + port + "'" : String(port);
        throw new NodeRangeError('ERR_SOCKET_BAD_PORT', name + ' should be ' + (allowZero ? '>=' : '>') +
            ' 0 and < 65536. Received type ' + typeof port + ' (' + shown + ').');
    }
    return +port | 0;
}

function validateString(value: any, name: string): void {
    if (typeof value !== 'string') {
        throw new NodeTypeError('ERR_INVALID_ARG_TYPE', 'The "' + name + '" argument must be of type string.' + received(value));
    }
}

function validateNumber(value: any, name: string): void {
    if (typeof value !== 'number') {
        throw new NodeTypeError('ERR_INVALID_ARG_TYPE', 'The "' + name + '" argument must be of type number.' + received(value));
    }
}

// ---- types (@types/node's dgram) ----

export type SocketType = 'udp4' | 'udp6';

export interface RemoteInfo {
    address: string;
    family: 'IPv4' | 'IPv6';
    port: number;
    size: number;
}

export interface BindOptions {
    port?: number | undefined;
    address?: string | undefined;
    exclusive?: boolean | undefined;
    fd?: number | undefined;
}

export interface SocketOptions {
    type: SocketType;
    reuseAddr?: boolean | undefined;
    reusePort?: boolean | undefined;
    ipv6Only?: boolean | undefined;
    recvBufferSize?: number | undefined;
    sendBufferSize?: number | undefined;
    signal?: AbortSignal | undefined;
}

const BIND_STATE_UNBOUND = 0;
const BIND_STATE_BINDING = 1;
const BIND_STATE_BOUND = 2;

const CONNECT_STATE_DISCONNECTED = 0;
const CONNECT_STATE_CONNECTING = 1;
const CONNECT_STATE_CONNECTED = 2;

const OPT_BROADCAST = 0;
const OPT_TTL = 1;
const OPT_MULTICAST_TTL = 2;
const OPT_MULTICAST_LOOP = 3;
const OPT_RCVBUF = 4;
const OPT_SNDBUF = 5;

function toBuffer(chunk: any): Buffer {
    if (typeof chunk === 'string') return Buffer.from(chunk as string);
    if (Buffer.isBuffer(chunk)) return chunk as Buffer;
    if (ArrayBuffer.isView(chunk)) {
        const v: any = chunk;
        const bytes = new Uint8Array(v.buffer as ArrayBuffer, v.byteOffset as number, v.byteLength as number);
        return Buffer.from(bytes);
    }
    throw new NodeTypeError('ERR_INVALID_ARG_TYPE', 'The "buffer" argument must be of type string or an instance of Buffer, TypedArray, or DataView.' + received(chunk));
}

function sliceBuffer(buffer: any, offset: any, length: any): Buffer {
    const buf = toBuffer(buffer);
    const off = offset >>> 0;
    const len = length >>> 0;
    if (off > buf.byteLength) {
        throw new NodeRangeError('ERR_BUFFER_OUT_OF_BOUNDS', '"offset" is outside of buffer bounds');
    }
    if (off + len > buf.byteLength) {
        throw new NodeRangeError('ERR_BUFFER_OUT_OF_BOUNDS', '"length" is outside of buffer bounds');
    }
    return buf.subarray(off, off + len);
}

export class Socket extends EventEmitter {
    type: SocketType;
    // The datagram socket, made at bind (as libuv makes it), -1 before.
    private handle: number = -1;
    private closed = false;
    private refd = true;
    private receiving = false;
    private bindState = BIND_STATE_UNBOUND;
    private connectState = CONNECT_STATE_DISCONNECTED;
    private queue: any = null;
    private reuseAddr = false;
    private ipv6Only = false;
    private recvBufferSize = 0;
    private sendBufferSize = 0;

    constructor(type: SocketType | SocketOptions, listener?: (msg: Buffer, rinfo: RemoteInfo) => void) {
        super();
        let t: any = type;
        let options: any = undefined;
        if (t !== null && typeof t === 'object') {
            options = t;
            t = options.type;
            if (options.recvBufferSize) this.recvBufferSize = options.recvBufferSize;
            if (options.sendBufferSize) this.sendBufferSize = options.sendBufferSize;
            this.reuseAddr = options.reuseAddr === true;
            this.ipv6Only = options.ipv6Only === true;
        }
        if (t !== 'udp4' && t !== 'udp6') {
            throw new NodeTypeError('ERR_SOCKET_BAD_TYPE', 'Bad socket type specified. Valid types are: udp4, udp6');
        }
        this.type = t;
        if (typeof listener === 'function') this.on('message', listener);
        if (options !== undefined && options.signal !== undefined) {
            const signal: any = options.signal;
            const onAborted = (): void => {
                if (!this.closed) this.close();
            };
            if (signal.aborted) onAborted();
            else signal.addEventListener('abort', onAborted, { once: true });
        }
    }

    private healthCheck(): void {
        if (this.closed) throw new NodeError('ERR_SOCKET_DGRAM_NOT_RUNNING', 'Not running');
    }

    // The bound socket's handle; before the bind, an option or address read
    // finds no descriptor (EBADF), as in Node.
    private boundHandle(syscall: string): number {
        this.healthCheck();
        if (this.handle < 0) throw errnoException(9, syscall);
        return this.handle;
    }

    // lookup4/lookup6: an IP literal answers on the next tick, a name
    // through getaddrinfo.
    private lookup(address: any, callback: (err: Error | null, ip: string) => void): void {
        const host: string = address || (this.type === 'udp4' ? '127.0.0.1' : '::1');
        const family = this.type === 'udp4' ? 4 : 6;
        if (isIP(host) !== 0) {
            process.nextTick(() => { callback(null, host); });
            return;
        }
        __kml_native.dnsLookup(host, family, (status: number, fam: number) => {
            if (status !== 0) {
                callback(dnsException(status, host), '');
                return;
            }
            callback(null, __kml_native.lastString());
        });
    }

    private enqueue(toEnqueue: () => void): void {
        if (this.queue === null) {
            this.queue = [];
            const onListenError = (err: any): void => {
                this.removeListener('listening', onListenSuccess);
                this.queue = null;
            };
            const onListenSuccess = (): void => {
                this.removeListener(EventEmitter.errorMonitor, onListenError);
                const queue: Array<() => void> = this.queue;
                this.queue = null;
                if (queue !== null) {
                    for (const entry of queue) entry();
                }
            };
            this.once(EventEmitter.errorMonitor, onListenError);
            this.once('listening', onListenSuccess);
        }
        this.queue.push(toEnqueue);
    }

    bind(port?: any, address?: any, callback?: () => void): this {
        this.healthCheck();
        if (this.bindState !== BIND_STATE_UNBOUND) {
            throw new NodeError('ERR_SOCKET_ALREADY_BOUND', 'Socket is already bound');
        }
        this.bindState = BIND_STATE_BINDING;
        let cb: any = undefined;
        if (typeof callback === 'function') cb = callback;
        else if (typeof address === 'function') cb = address;
        else if (typeof port === 'function') cb = port;
        if (cb !== undefined) {
            const self = this;
            const removeListeners = (): void => {
                self.removeListener('error', removeListeners);
                self.removeListener('listening', onListening);
            };
            const onListening = (): void => {
                removeListeners();
                cb.call(self);
            };
            this.on('error', removeListeners);
            this.on('listening', onListening);
        }
        let p: any = port;
        let addr: any;
        if (p !== null && typeof p === 'object') {
            addr = p.address || '';
            p = p.port;
        } else {
            addr = typeof address === 'function' ? '' : address;
        }
        if (typeof p === 'function' || p === undefined || p === null) p = 0;
        if (!addr) addr = this.type === 'udp4' ? '0.0.0.0' : '::';
        this.lookup(addr, (err: Error | null, ip: string) => {
            if (this.closed) return; // closed in the mean time
            if (err !== null) {
                this.bindState = BIND_STATE_UNBOUND;
                this.emit('error', err);
                return;
            }
            if (this.handle < 0) {
                const h = __kml_native.udpSocket(this.type === 'udp6' ? 6 : 4);
                if (h < 0) {
                    this.bindState = BIND_STATE_UNBOUND;
                    this.emit('error', errnoException(h, 'bind', ip, +p || 0));
                    return;
                }
                this.handle = h;
                if (!this.refd) __kml_native.tcpRef(h, false);
            }
            const flags = (this.reuseAddr ? 1 : 0) | (this.ipv6Only ? 2 : 0);
            const rc = __kml_native.udpBind(this.handle, ip, +p || 0, flags);
            if (rc < 0) {
                this.bindState = BIND_STATE_UNBOUND;
                this.emit('error', errnoException(rc, 'bind', ip, +p || 0));
                return;
            }
            this.startListening();
        });
        return this;
    }

    private startListening(): void {
        const h = this.handle;
        __kml_native.tcpReadStart(h, (status: number, nread: number) => {
            if (status !== 0) {
                this.emit('error', errnoException(status, 'recvmsg'));
                return;
            }
            const buf = Buffer.alloc(nread);
            __kml_native.tcpTake(h, buf);
            const who = __kml_native.udpSender(h);
            const family = Math.floor(who / 65536);
            const rinfo: RemoteInfo = {
                address: __kml_native.lastString(),
                family: family === 6 ? 'IPv6' : 'IPv4',
                port: who % 65536,
                size: nread,
            };
            this.emit('message', buf, rinfo);
        });
        this.receiving = true;
        this.bindState = BIND_STATE_BOUND;
        if (this.recvBufferSize) this.bufferSize(this.recvBufferSize, true);
        if (this.sendBufferSize) this.bufferSize(this.sendBufferSize, false);
        this.emit('listening');
    }

    connect(port: number, address?: any, callback?: () => void): void {
        const p = validatePort(port, 'Port', false);
        let addr: any = address;
        let cb: any = callback;
        if (typeof addr === 'function') {
            cb = addr;
            addr = '';
        } else if (addr === undefined) {
            addr = '';
        }
        validateString(addr, 'address');
        if (this.connectState !== CONNECT_STATE_DISCONNECTED) {
            throw new NodeError('ERR_SOCKET_DGRAM_IS_CONNECTED', 'Already connected');
        }
        this.connectState = CONNECT_STATE_CONNECTING;
        if (this.bindState === BIND_STATE_UNBOUND) this.bind({ port: 0, exclusive: true });
        if (this.bindState !== BIND_STATE_BOUND) {
            this.enqueue(() => { this.doConnectLookup(p, addr, cb); });
            return;
        }
        this.doConnectLookup(p, addr, cb);
    }

    private doConnectLookup(port: number, address: string, callback: any): void {
        if (callback) this.once('connect', callback);
        this.lookup(address, (ex: Error | null, ip: string) => {
            if (this.closed) return;
            let err: any = ex;
            if (err === null) {
                const rc = __kml_native.udpConnect(this.handle, ip, port);
                if (rc < 0) err = errnoException(rc, 'connect', address, port);
            }
            if (err !== null) {
                this.connectState = CONNECT_STATE_DISCONNECTED;
                process.nextTick(() => {
                    if (callback) {
                        this.removeListener('connect', callback);
                        callback(err);
                    } else {
                        this.emit('error', err);
                    }
                });
                return;
            }
            this.connectState = CONNECT_STATE_CONNECTED;
            process.nextTick(() => { this.emit('connect'); });
        });
    }

    disconnect(): void {
        if (this.connectState !== CONNECT_STATE_CONNECTED) {
            throw new NodeError('ERR_SOCKET_DGRAM_NOT_CONNECTED', 'Not connected');
        }
        const rc = __kml_native.udpConnect(this.handle, '', 0);
        if (rc < 0) throw errnoException(rc, 'connect');
        this.connectState = CONNECT_STATE_DISCONNECTED;
    }

    send(msg: any, offset?: any, length?: any, port?: any, address?: any, callback?: any): void {
        let buffer: any = msg;
        let off: any = offset;
        let len: any = length;
        let p: any = port;
        let addr: any = address;
        let cb: any = callback;
        const connected = this.connectState === CONNECT_STATE_CONNECTED;
        if (!connected) {
            if (addr || (p && typeof p !== 'function')) {
                buffer = sliceBuffer(buffer, off, len);
            } else {
                cb = p;
                p = off;
                addr = len;
            }
        } else {
            if (typeof len === 'number') {
                buffer = sliceBuffer(buffer, off, len);
                if (typeof p === 'function') {
                    cb = p;
                    p = null;
                } else {
                    cb = addr;
                }
            } else {
                cb = off;
            }
            if (p || addr) throw new NodeError('ERR_SOCKET_DGRAM_IS_CONNECTED', 'Already connected');
        }
        let list: Buffer[];
        if (Array.isArray(buffer)) {
            list = [];
            for (const chunk of buffer as any[]) list.push(toBuffer(chunk));
        } else {
            list = [toBuffer(buffer)];
        }
        if (!connected) p = validatePort(p, 'Port', false);
        if (typeof cb !== 'function') cb = undefined;
        if (typeof addr === 'function') {
            cb = addr;
            addr = undefined;
        } else if (addr !== null && addr !== undefined) {
            validateString(addr, 'address');
        }
        this.healthCheck();
        if (this.bindState === BIND_STATE_UNBOUND) this.bind({ port: 0, exclusive: true });
        if (list.length === 0) list.push(Buffer.alloc(0));
        if (this.bindState !== BIND_STATE_BOUND) {
            const sendList = list;
            const sendPort = p;
            const sendAddr = addr;
            const sendCb = cb;
            this.enqueue(() => { this.send(sendList, sendPort, sendAddr, sendCb); });
            return;
        }
        const afterDns = (ex: Error | null, ip: string | null): void => {
            this.doSend(ex, ip, list, addr, p, cb);
        };
        if (!connected) this.lookup(addr, afterDns);
        else afterDns(null, null);
    }

    private doSend(ex: Error | null, ip: string | null, list: Buffer[], address: any, port: any, callback: any): void {
        if (ex !== null) {
            if (typeof callback === 'function') {
                process.nextTick(() => { callback(ex); });
                return;
            }
            process.nextTick(() => { this.emit('error', ex); });
            return;
        }
        if (this.closed) return;
        const data = list.length === 1 ? list[0] : Buffer.concat(list);
        const rc = __kml_native.udpSend(this.handle, data, 0, data.length, ip !== null ? ip : '', port ? +port : 0);
        if (rc >= 0) {
            if (callback) process.nextTick(() => { callback(null, rc); });
            return;
        }
        if (callback) {
            // Not emitted as 'error': Node's legacy compatibility.
            const err = errnoException(rc, 'send', address, port ? +port : 0);
            process.nextTick(() => { callback(err); });
        }
    }

    close(callback?: () => void): this {
        if (typeof callback === 'function') this.on('close', callback);
        if (this.queue !== null) {
            this.queue.push(() => { this.close(); });
            return this;
        }
        this.healthCheck();
        this.receiving = false;
        this.closed = true;
        const h = this.handle;
        this.handle = -1;
        if (h >= 0) __kml_native.tcpClose(h, (status: number, unused: number) => {});
        process.nextTick(() => { this.emit('close'); });
        return this;
    }

    address(): { address: string; family: string; port: number } {
        const r = __kml_native.tcpAddress(this.boundHandle('getsockname'), false);
        if (r < 0) throw errnoException(r, 'getsockname');
        const fam = Math.floor(r / 65536);
        return { address: __kml_native.lastString(), family: fam === 6 ? 'IPv6' : 'IPv4', port: r % 65536 };
    }

    remoteAddress(): { address: string; family: string; port: number } {
        this.healthCheck();
        if (this.connectState !== CONNECT_STATE_CONNECTED) {
            throw new NodeError('ERR_SOCKET_DGRAM_NOT_CONNECTED', 'Not connected');
        }
        const r = __kml_native.tcpAddress(this.handle, true);
        if (r < 0) throw errnoException(r, 'getpeername');
        const fam = Math.floor(r / 65536);
        return { address: __kml_native.lastString(), family: fam === 6 ? 'IPv6' : 'IPv4', port: r % 65536 };
    }

    private option(which: number, value: number, syscall: string): number {
        const rc = __kml_native.udpOption(this.boundHandle(syscall), which, value);
        if (rc < 0) throw errnoException(rc, syscall);
        return rc;
    }

    setBroadcast(flag: boolean): void {
        this.option(OPT_BROADCAST, flag ? 1 : 0, 'setBroadcast');
    }

    setTTL(ttl: number): number {
        validateNumber(ttl, 'ttl');
        this.option(OPT_TTL, ttl, 'setTTL');
        return ttl;
    }

    setMulticastTTL(ttl: number): number {
        validateNumber(ttl, 'ttl');
        this.option(OPT_MULTICAST_TTL, ttl, 'setMulticastTTL');
        return ttl;
    }

    setMulticastLoopback(flag: boolean): boolean {
        this.option(OPT_MULTICAST_LOOP, flag ? 1 : 0, 'setMulticastLoopback');
        return flag;
    }

    setMulticastInterface(interfaceAddress: string): void {
        validateString(interfaceAddress, 'interfaceAddress');
        const rc = __kml_native.udpMulticastInterface(this.boundHandle('setMulticastInterface'), interfaceAddress);
        if (rc < 0) throw errnoException(rc, 'setMulticastInterface');
    }

    addMembership(multicastAddress: string, interfaceAddress?: string): void {
        this.healthCheck();
        if (!multicastAddress) {
            throw new NodeTypeError('ERR_MISSING_ARGS', 'The "multicastAddress" argument must be specified');
        }
        const rc = __kml_native.udpMembership(this.boundHandle('addMembership'), multicastAddress, interfaceAddress ?? '', true);
        if (rc < 0) throw errnoException(rc, 'addMembership');
    }

    dropMembership(multicastAddress: string, interfaceAddress?: string): void {
        this.healthCheck();
        if (!multicastAddress) {
            throw new NodeTypeError('ERR_MISSING_ARGS', 'The "multicastAddress" argument must be specified');
        }
        const rc = __kml_native.udpMembership(this.boundHandle('dropMembership'), multicastAddress, interfaceAddress ?? '', false);
        if (rc < 0) throw errnoException(rc, 'dropMembership');
    }

    private bufferSize(size: number, recv: boolean): number {
        if (size >>> 0 !== size) {
            throw new NodeTypeError('ERR_SOCKET_BAD_BUFFER_SIZE', 'Buffer size must be a positive integer');
        }
        const rc = this.handle < 0 ? -9 : __kml_native.udpOption(this.handle, recv ? OPT_RCVBUF : OPT_SNDBUF, size === 0 ? -1 : size);
        if (rc < 0) {
            const e: any = new NodeError('ERR_SOCKET_BUFFER_SIZE', 'Could not get or set buffer size: ' +
                (recv ? 'uv_recv_buffer_size' : 'uv_send_buffer_size') + ' returned ' + __kml_native.errnoName(-rc));
            throw e;
        }
        return size === 0 ? rc : size;
    }

    setRecvBufferSize(size: number): void {
        this.bufferSize(size, true);
    }

    setSendBufferSize(size: number): void {
        this.bufferSize(size, false);
    }

    getRecvBufferSize(): number {
        return this.bufferSize(0, true);
    }

    getSendBufferSize(): number {
        return this.bufferSize(0, false);
    }

    getSendQueueSize(): number {
        return 0;
    }

    getSendQueueCount(): number {
        return 0;
    }

    ref(): this {
        this.refd = true;
        if (this.handle >= 0) __kml_native.tcpRef(this.handle, true);
        return this;
    }

    unref(): this {
        this.refd = false;
        if (this.handle >= 0) __kml_native.tcpRef(this.handle, false);
        return this;
    }

    [Symbol.asyncDispose](): Promise<void> {
        if (this.closed) return Promise.resolve();
        return new Promise<void>((resolve) => { this.close(() => { resolve(); }); });
    }
}

export function createSocket(type: SocketType | SocketOptions, listener?: (msg: Buffer, rinfo: RemoteInfo) => void): Socket {
    return new Socket(type, listener);
}
