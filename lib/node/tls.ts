// Node's `tls`, ported from Node v24's lib/_tls_wrap.js and
// lib/_tls_common.js: a TLSSocket is a net.Socket whose TCP handle carries a
// TLS session (OpenSSL, lib/native.d.ts), a tls.Server a net.Server that
// wraps each connection in one. Session resumption, OCSP, renegotiation,
// PSK and the certificate-object APIs (getPeerCertificate's fields) are not
// ported.
//
// kml:default-namespace — `import tls from 'tls'` reads this module's
// exports, as Node's default export carries them.
import * as net from 'net';

class TlsError extends Error {
    code: string;
    constructor(code: string, message: string) {
        super(message);
        this.code = code;
    }
}

export const DEFAULT_MIN_VERSION = 'TLSv1.2';
export const DEFAULT_MAX_VERSION = 'TLSv1.3';
export const DEFAULT_ECDH_CURVE = 'auto';

type PemInput = string | Buffer | Array<string | Buffer>;

function pem(input: PemInput | undefined): string {
    if (input === undefined) return '';
    if (typeof input === 'string') return input;
    if (Buffer.isBuffer(input)) return input.toString();
    let out = '';
    for (const one of input as Array<string | Buffer>) {
        out += typeof one === 'string' ? one : one.toString();
        if (!out.endsWith('\n')) out += '\n';
    }
    return out;
}

export interface SecureContextOptions {
    ca?: PemInput;
    cert?: PemInput;
    key?: PemInput;
    ALPNProtocols?: string[];
    minVersion?: string;
    maxVersion?: string;
    ciphers?: string;
}

export class SecureContext {
    context: number;
    constructor(context: number) {
        this.context = context;
    }
}

function contextFor(server: boolean, options: SecureContextOptions): SecureContext {
    const alpn = options.ALPNProtocols !== undefined ? options.ALPNProtocols.join(',') : '';
    const id = __kml_native.tlsContext(server, pem(options.cert), pem(options.key), pem(options.ca), alpn);
    if (id < 0) throw new TlsError('ERR_OSSL_PEM_BAD_BASE64_DECODE', __kml_native.tlsLastError());
    return new SecureContext(id);
}

export function createSecureContext(options?: SecureContextOptions): SecureContext {
    return contextFor(false, options ?? {});
}

export interface TLSSocketOptions extends SecureContextOptions {
    isServer?: boolean;
    server?: net.Server;
    secureContext?: SecureContext;
    rejectUnauthorized?: boolean;
    requestCert?: boolean;
    servername?: string;
}

export interface ConnectionOptions extends SecureContextOptions {
    host?: string;
    port?: number;
    path?: string;
    socket?: net.Socket;
    servername?: string;
    rejectUnauthorized?: boolean;
    secureContext?: SecureContext;
    timeout?: number;
    family?: number;
}

// The net.Socket options a TLSSocket over socket's handle starts from.
function wrapOptions(socket: net.Socket | null | undefined): { handle?: number; allowHalfOpen?: boolean } {
    if (socket === null || socket === undefined) return {};
    return { handle: socket._kmlReleaseHandle(), allowHalfOpen: socket.allowHalfOpen };
}

export class TLSSocket extends net.Socket {
    encrypted = true;
    authorized = false;
    authorizationError: string | undefined = undefined;
    alpnProtocol: string | false = false;
    servername: string | false = false;
    private isServer: boolean;
    private context: SecureContext | null = null;
    private rejectUnauthorized: boolean;
    private requestedServername: string;
    private secureStarted = false;
    _secureEstablished = false;

    constructor(socket?: net.Socket | null, options?: TLSSocketOptions) {
        super(wrapOptions(socket));
        const opts = options ?? {};
        this.isServer = opts.isServer === true;
        this.rejectUnauthorized = opts.rejectUnauthorized !== false;
        this.requestedServername = opts.servername ?? '';
        if (opts.server !== undefined) {
            this.server = opts.server;
            this._server = opts.server;
        }
        this.context = opts.secureContext ?? null;
        if (this.context === null) this.context = contextFor(this.isServer, opts);
        if (this.isServer) {
            // onSocketTLSError: an error before the connection is handed to
            // the program is the server's 'tlsClientError'.
            this.on('error', (err: Error) => {
                if (!this._secureEstablished && this.server !== null) this.server.emit('tlsClientError', err, this);
            });
        }
        if (this._handle >= 0) this.startTls();
        else this.once('connect', () => { this.startTls(); });
    }

    // The events, typed as @types/node declares them.
    on(event: 'secureConnect', listener: () => void): this;
    on(event: 'session', listener: (session: Buffer) => void): this;
    on(event: string | symbol, listener: (...args: any[]) => void): this;
    on(event: string | symbol, listener: (...args: any[]) => void): this {
        return super.on(event, listener);
    }

    private startTls(): void {
        if (this.secureStarted || this._handle < 0 || this.context === null) return;
        this.secureStarted = true;
        const r = __kml_native.tlsStart(this._handle, this.context.context, this.isServer, this.requestedServername,
            (status: number, unused: number) => { this.onHandshake(status); });
        if (r < 0) this.destroy(new TlsError('ERR_TLS_INVALID_STATE', 'TLS socket connection could not start'));
    }

    // onhandshakedone / onConnectSecure
    private onHandshake(status: number): void {
        if (this.destroyed) return;
        if (status !== 0) {
            __kml_native.tlsInfo(this._handle, 3);
            const reason = __kml_native.lastString();
            const err = reason.startsWith('Client network socket disconnected')
                ? new TlsError('ECONNRESET', reason)
                : new TlsError('ERR_SSL_' + reason.toUpperCase().replace(/ /g, '_'), reason);
            if (this.isServer && this.server !== null) {
                this.server.emit('tlsClientError', err, this);
                this.destroy();
            } else {
                this.destroy(err);
            }
            return;
        }
        this._secureEstablished = true;
        if (__kml_native.tlsInfo(this._handle, 4) > 0) this.alpnProtocol = __kml_native.lastString();
        if (this.isServer) {
            if (this.server !== null) this.server.emit('secureConnection', this);
            return;
        }
        this.servername = this.requestedServername === '' ? false : this.requestedServername;
        const ok = __kml_native.tlsInfo(this._handle, 0) === 1;
        if (!ok) {
            __kml_native.tlsInfo(this._handle, 1);
            const code = __kml_native.lastString();
            __kml_native.tlsInfo(this._handle, 2);
            const message = __kml_native.lastString();
            this.authorizationError = code;
            if (this.rejectUnauthorized) {
                this.destroy(new TlsError(code, code === 'ERR_TLS_CERT_ALTNAME_INVALID'
                    ? "Hostname/IP does not match certificate's altnames: Host: " + this.requestedServername + '.'
                    : message));
                return;
            }
        } else {
            this.authorized = true;
        }
        this.emit('secureConnect');
    }

    getProtocol(): string | null {
        if (this._handle < 0) return null;
        __kml_native.tlsInfo(this._handle, 5);
        return __kml_native.lastString();
    }

    getCipher(): { name: string; standardName: string; version: string } {
        __kml_native.tlsInfo(this._handle, 6);
        const name = __kml_native.lastString();
        return { name: name, standardName: name, version: this.getProtocol() ?? '' };
    }

    get alpn(): string | false {
        return this.alpnProtocol;
    }
}

export interface TlsOptions extends SecureContextOptions, net.ServerOpts {
    requestCert?: boolean;
    rejectUnauthorized?: boolean;
    handshakeTimeout?: number;
}

// kml:callable Server — Node's is a function that constructs when called without `new`
export class Server extends net.Server {
    private context: SecureContext;
    private rejectUnauthorized: boolean;

    constructor(secureConnectionListener?: (socket: TLSSocket) => void);
    constructor(options: TlsOptions, secureConnectionListener?: (socket: TLSSocket) => void);
    constructor(options?: TlsOptions | ((socket: TLSSocket) => void), secureConnectionListener?: (socket: TLSSocket) => void) {
        let opts: TlsOptions = {};
        let listener: ((socket: TLSSocket) => void) | null = null;
        if (typeof options === 'function') {
            listener = options;
        } else {
            if (options !== undefined) opts = options;
            if (secureConnectionListener !== undefined) listener = secureConnectionListener;
        }
        super({ allowHalfOpen: opts.allowHalfOpen, pauseOnConnect: opts.pauseOnConnect, noDelay: opts.noDelay, keepAlive: opts.keepAlive, keepAliveInitialDelay: opts.keepAliveInitialDelay, highWaterMark: opts.highWaterMark });
        this.context = contextFor(true, opts);
        this.rejectUnauthorized = opts.rejectUnauthorized !== false;
        if (listener !== null) this.on('secureConnection', listener);
        // tlsConnectionListener
        this.on('connection', (raw: net.Socket) => {
            new TLSSocket(raw, { isServer: true, server: this, secureContext: this.context, rejectUnauthorized: this.rejectUnauthorized });
        });
    }

    // The events, typed as @types/node declares them.
    on(event: 'secureConnection', listener: (socket: TLSSocket) => void): this;
    on(event: 'tlsClientError', listener: (err: Error, socket: TLSSocket) => void): this;
    on(event: 'connection', listener: (socket: net.Socket) => void): this;
    on(event: 'close', listener: () => void): this;
    on(event: 'error', listener: (err: Error) => void): this;
    on(event: 'listening', listener: () => void): this;
    on(event: string | symbol, listener: (...args: any[]) => void): this;
    on(event: string | symbol, listener: (...args: any[]) => void): this {
        return super.on(event, listener);
    }

    setSecureContext(options: SecureContextOptions): void {
        this.context = contextFor(true, options);
    }
}

export function createServer(secureConnectionListener?: (socket: TLSSocket) => void): Server;
export function createServer(options: TlsOptions, secureConnectionListener?: (socket: TLSSocket) => void): Server;
export function createServer(options?: TlsOptions | ((socket: TLSSocket) => void), secureConnectionListener?: (socket: TLSSocket) => void): Server {
    if (typeof options === 'function') return new Server(options);
    return new Server(options ?? {}, secureConnectionListener);
}

type SecureListener = () => void;

export function connect(options: ConnectionOptions, secureConnectListener?: SecureListener): TLSSocket;
export function connect(port: number, host?: string, options?: ConnectionOptions, secureConnectListener?: SecureListener): TLSSocket;
export function connect(port: number, options?: ConnectionOptions, secureConnectListener?: SecureListener): TLSSocket;
export function connect(arg0: ConnectionOptions | number, arg1?: string | ConnectionOptions | SecureListener,
    arg2?: ConnectionOptions | SecureListener, arg3?: SecureListener): TLSSocket {
    let options: ConnectionOptions = {};
    let cb: SecureListener | null = null;
    if (typeof arg0 === 'number') {
        options.port = arg0;
        if (typeof arg1 === 'string') {
            options.host = arg1;
            if (typeof arg2 === 'object') options = mergeOptions(arg2, arg0, arg1);
            if (typeof arg2 === 'function') cb = arg2;
            else if (arg3 !== undefined) cb = arg3;
        } else if (typeof arg1 === 'object') {
            options = mergeOptions(arg1, arg0, undefined);
            if (typeof arg2 === 'function') cb = arg2;
        } else if (typeof arg1 === 'function') {
            cb = arg1;
        }
    } else {
        options = arg0;
        if (typeof arg1 === 'function') cb = arg1;
    }
    const host = options.host ?? 'localhost';
    const servername = options.servername ?? (net.isIP(host) === 0 ? host : '');
    const socket = new TLSSocket(options.socket ?? null, {
        isServer: false,
        secureContext: options.secureContext,
        rejectUnauthorized: options.rejectUnauthorized,
        servername: servername,
        ca: options.ca, cert: options.cert, key: options.key, ALPNProtocols: options.ALPNProtocols,
    });
    if (cb !== null) socket.once('secureConnect', cb);
    if (options.timeout !== undefined) socket.setTimeout(options.timeout);
    if (options.socket === undefined) {
        if (options.path !== undefined) socket.connect({ path: options.path });
        else socket.connect({ port: options.port ?? 443, host: host, family: options.family });
    }
    return socket;
}

function mergeOptions(o: ConnectionOptions, port: number, host: string | undefined): ConnectionOptions {
    const out: ConnectionOptions = {
        port: port, host: host ?? o.host, path: o.path, socket: o.socket, servername: o.servername,
        rejectUnauthorized: o.rejectUnauthorized, secureContext: o.secureContext, timeout: o.timeout,
        family: o.family, ca: o.ca, cert: o.cert, key: o.key, ALPNProtocols: o.ALPNProtocols,
    };
    return out;
}

export function checkServerIdentity(hostname: string, cert: any): Error | undefined {
    return undefined;
}

export function getCiphers(): string[] {
    return ['aes128-gcm-sha256', 'aes256-gcm-sha384', 'chacha20-poly1305'];
}
