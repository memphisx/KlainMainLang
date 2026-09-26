// Node's `https` server side, ported from Node v24's lib/https.js: an
// https.Server is a tls.Server whose secure connections run the http
// module's connection handling (_http_server's connectionListener). The
// client (request, get, Agent) is the https module's other part.
import * as tls from 'tls';
import * as net from 'net';
import { _kmlHttpServerCore, Agent as HttpAgent, ClientRequest, _kmlSetHttpsGlobalAgent } from './internal_http';
import type { IncomingMessage, ServerResponse, RequestListener, ServerOptions as HttpServerOptions,
    AgentOptions as HttpAgentOptions, AgentConnectOptions, RequestOptions as HttpRequestOptions, ResponseListener } from './internal_http';

export interface ServerOptions extends tls.TlsOptions, HttpServerOptions {}

// kml:callable Server — Node's is a function that constructs when called without `new`
export class Server extends tls.Server {
    core: _kmlHttpServerCore;

    constructor(requestListener?: RequestListener);
    constructor(options: ServerOptions, requestListener?: RequestListener);
    constructor(options?: ServerOptions | RequestListener, requestListener?: RequestListener) {
        let opts: ServerOptions = {};
        let listener: RequestListener | null = null;
        if (typeof options === 'function') {
            listener = options;
        } else {
            if (options !== undefined) opts = options;
            if (requestListener !== undefined) listener = requestListener;
        }
        super({
            cert: opts.cert, key: opts.key, ca: opts.ca,
            ALPNProtocols: opts.ALPNProtocols ?? ['http/1.1'],
            requestCert: opts.requestCert, rejectUnauthorized: opts.rejectUnauthorized,
            allowHalfOpen: true, noDelay: opts.noDelay ?? true, keepAlive: opts.keepAlive,
            keepAliveInitialDelay: opts.keepAliveInitialDelay, highWaterMark: opts.highWaterMark,
        });
        const core = new _kmlHttpServerCore(this, opts);
        this.core = core;
        if (listener !== null) this.on('request', listener);
        this.on('secureConnection', (socket: tls.TLSSocket) => { core.connectionListener(socket); });
        this.on('listening', () => { core.setupConnectionsTracking(); });
    }

    // The events, typed as @types/node declares them.
    on(event: 'request', listener: RequestListener): this;
    on(event: 'checkContinue', listener: RequestListener): this;
    on(event: 'clientError', listener: (err: Error, socket: net.Socket) => void): this;
    on(event: 'upgrade', listener: (req: IncomingMessage, socket: net.Socket, head: Buffer) => void): this;
    on(event: 'connect', listener: (req: IncomingMessage, socket: net.Socket, head: Buffer) => void): this;
    on(event: 'secureConnection', listener: (socket: tls.TLSSocket) => void): this;
    on(event: 'tlsClientError', listener: (err: Error, socket: tls.TLSSocket) => void): this;
    on(event: 'close', listener: () => void): this;
    on(event: 'error', listener: (err: Error) => void): this;
    on(event: 'listening', listener: () => void): this;
    on(event: string | symbol, listener: (...args: any[]) => void): this;
    on(event: string | symbol, listener: (...args: any[]) => void): this {
        return super.on(event, listener);
    }

    get timeout(): number { return this.core.timeout; }
    set timeout(v: number) { this.core.timeout = v; }
    get maxHeadersCount(): number | null { return this.core.maxHeadersCount; }
    set maxHeadersCount(v: number | null) { this.core.maxHeadersCount = v; }
    get headersTimeout(): number { return this.core.headersTimeout; }
    set headersTimeout(v: number) { this.core.headersTimeout = v; }
    get keepAliveTimeout(): number { return this.core.keepAliveTimeout; }
    set keepAliveTimeout(v: number) { this.core.keepAliveTimeout = v; }
    get requestTimeout(): number { return this.core.requestTimeout; }
    set requestTimeout(v: number) { this.core.requestTimeout = v; }

    close(cb?: (err?: Error) => void): this {
        this.core.stopConnectionsTracking();
        this.core.closeIdleConnections();
        super.close(cb);
        return this;
    }

    closeAllConnections(): void {
        this.core.closeAllConnections();
    }

    closeIdleConnections(): void {
        this.core.closeIdleConnections();
    }

    setTimeout(msecs?: number, callback?: () => void): this {
        this.core.timeout = msecs ?? 0;
        if (callback) this.on('timeout', callback);
        return this;
    }
}

export function createServer(requestListener?: RequestListener): Server;
export function createServer(options: ServerOptions, requestListener?: RequestListener): Server;
export function createServer(options?: ServerOptions | RequestListener, requestListener?: RequestListener): Server {
    if (typeof options === 'function') return new Server(options);
    return new Server(options ?? {}, requestListener);
}

// ---- the client: lib/https.js's Agent, request and get ----

export interface AgentOptions extends HttpAgentOptions, tls.ConnectionOptions {
    maxCachedSessions?: number;
}

// kml:callable Agent — Node's is a function that constructs when called without `new`
export class Agent extends HttpAgent {
    constructor(options?: AgentOptions) {
        super(options);
        this.defaultPort = 443;
        this.protocol = 'https:';
    }

    createConnection(options: AgentConnectOptions, cb?: (err: Error | null, socket: net.Socket) => void): net.Socket {
        return tls.connect({ port: options.port, host: options.host, servername: options.servername,
            rejectUnauthorized: options.rejectUnauthorized, ca: options.ca, cert: options.cert, key: options.key,
            family: options.family, path: options.socketPath });
    }

    getName(options?: AgentConnectOptions): string {
        let name = super.getName(options);
        const o = options ?? {};
        name += ':';
        if (o.ca) name += String(o.ca).length;
        name += ':';
        if (o.servername && o.servername !== o.host) name += o.servername;
        name += ':';
        if (o.rejectUnauthorized !== undefined) name += String(o.rejectUnauthorized);
        return name;
    }
}

export const globalAgent = new Agent({ keepAlive: true, scheduling: 'lifo', timeout: 5000 });
_kmlSetHttpsGlobalAgent(globalAgent);

export interface RequestOptions extends HttpRequestOptions {}

function httpsOptions(url: string | RequestOptions): RequestOptions {
    if (typeof url === 'string') {
        const u = new URL(url);
        const o: RequestOptions = { protocol: u.protocol, hostname: u.hostname.startsWith('[') ? u.hostname.slice(1, -1) : u.hostname,
            path: u.pathname + u.search, defaultPort: 443 };
        if (u.port !== '') o.port = Number(u.port);
        if (u.username || u.password) o.auth = decodeURIComponent(u.username) + ':' + decodeURIComponent(u.password);
        return o;
    }
    const o: RequestOptions = { protocol: url.protocol ?? 'https:', hostname: url.hostname, host: url.host, port: url.port,
        path: url.path, method: url.method, headers: url.headers, auth: url.auth, agent: url.agent,
        defaultPort: url.defaultPort ?? 443, family: url.family, localAddress: url.localAddress,
        socketPath: url.socketPath, setHost: url.setHost, timeout: url.timeout, servername: url.servername,
        rejectUnauthorized: url.rejectUnauthorized, ca: url.ca, cert: url.cert, key: url.key,
        maxHeaderSize: url.maxHeaderSize, joinDuplicateHeaders: url.joinDuplicateHeaders };
    return o;
}

export function request(url: string | RequestOptions, options?: RequestOptions | ResponseListener, cb?: ResponseListener): ClientRequest {
    const base = httpsOptions(url);
    if (typeof options === 'function') return new ClientRequest(base, options);
    if (options !== undefined) return new ClientRequest(base, httpsOptions(options), cb);
    return new ClientRequest(base, cb);
}

export function get(url: string | RequestOptions, options?: RequestOptions | ResponseListener, cb?: ResponseListener): ClientRequest {
    const req = request(url, options, cb);
    req.end();
    return req;
}
