// Node's `http` server side, ported from Node v24's lib/_http_common.js,
// lib/_http_incoming.js, lib/_http_outgoing.js and lib/_http_server.js: an
// IncomingMessage is a Readable a native HTTP/1 parser (llhttp's rules)
// fills from a net.Socket, a ServerResponse an OutgoingMessage writing to
// it, and the Server a net.Server whose connections each run that parser.
// The client (ClientRequest, Agent) is the http module's other part.
// Diagnostics channels, async hooks and the connections checking interval's
// timers-per-server list are not ported: each connection keeps its own
// headers/request timers.
import { EventEmitter } from 'events';
import { Readable, Stream, finished } from 'stream';
import * as net from 'net';

// ---- errors ----

class HttpError extends Error {
    code: string;
    constructor(code: string, message: string) {
        super(message);
        this.code = code;
    }
}

class HttpTypeError extends TypeError {
    code: string;
    constructor(code: string, message: string) {
        super(message);
        this.code = code;
    }
}

class HttpRangeError extends RangeError {
    code: string;
    constructor(code: string, message: string) {
        super(message);
        this.code = code;
    }
}

function received(value: any): string {
    if (value === null) return ' Received null';
    if (value === undefined) return ' Received undefined';
    if (typeof value === 'function') return ' Received function ' + (value.name || '');
    if (typeof value === 'object') return ' Received an instance of Object';
    let shown = String(value);
    if (typeof value === 'string') {
        if (shown.length > 28) shown = shown.slice(0, 25) + '...';
        shown = "'" + shown + "'";
    }
    return ' Received type ' + typeof value + ' (' + shown + ')';
}

// ---- lib/_http_common.js ----

export const METHODS: string[] = [
    'ACL', 'BIND', 'CHECKOUT', 'CONNECT', 'COPY', 'DELETE', 'GET', 'HEAD', 'LINK', 'LOCK', 'M-SEARCH',
    'MERGE', 'MKACTIVITY', 'MKCALENDAR', 'MKCOL', 'MOVE', 'NOTIFY', 'OPTIONS', 'PATCH', 'POST', 'PROPFIND',
    'PROPPATCH', 'PURGE', 'PUT', 'QUERY', 'REBIND', 'REPORT', 'SEARCH', 'SOURCE', 'SUBSCRIBE', 'TRACE',
    'UNBIND', 'UNLINK', 'UNLOCK', 'UNSUBSCRIBE',
];

export const STATUS_CODES: { [errorCode: string]: string | undefined } = {
    '100': 'Continue', '101': 'Switching Protocols', '102': 'Processing', '103': 'Early Hints',
    '200': 'OK', '201': 'Created', '202': 'Accepted', '203': 'Non-Authoritative Information',
    '204': 'No Content', '205': 'Reset Content', '206': 'Partial Content', '207': 'Multi-Status',
    '208': 'Already Reported', '226': 'IM Used', '300': 'Multiple Choices', '301': 'Moved Permanently',
    '302': 'Found', '303': 'See Other', '304': 'Not Modified', '305': 'Use Proxy', '307': 'Temporary Redirect',
    '308': 'Permanent Redirect', '400': 'Bad Request', '401': 'Unauthorized', '402': 'Payment Required',
    '403': 'Forbidden', '404': 'Not Found', '405': 'Method Not Allowed', '406': 'Not Acceptable',
    '407': 'Proxy Authentication Required', '408': 'Request Timeout', '409': 'Conflict', '410': 'Gone',
    '411': 'Length Required', '412': 'Precondition Failed', '413': 'Payload Too Large', '414': 'URI Too Long',
    '415': 'Unsupported Media Type', '416': 'Range Not Satisfiable', '417': 'Expectation Failed',
    '418': "I'm a Teapot", '421': 'Misdirected Request', '422': 'Unprocessable Entity', '423': 'Locked',
    '424': 'Failed Dependency', '425': 'Too Early', '426': 'Upgrade Required', '428': 'Precondition Required',
    '429': 'Too Many Requests', '431': 'Request Header Fields Too Large', '451': 'Unavailable For Legal Reasons',
    '500': 'Internal Server Error', '501': 'Not Implemented', '502': 'Bad Gateway', '503': 'Service Unavailable',
    '504': 'Gateway Timeout', '505': 'HTTP Version Not Supported', '506': 'Variant Also Negotiates',
    '507': 'Insufficient Storage', '508': 'Loop Detected', '509': 'Bandwidth Limit Exceeded',
    '510': 'Not Extended', '511': 'Network Authentication Required',
};

export const maxHeaderSize = 16384;

const tokenRegExp = /^[\^_`a-zA-Z\-0-9!#$%&'*+.|~]+$/;
// Verifies that the given val is a valid HTTP token per RFC 7230.
function checkIsHttpToken(val: string): boolean {
    return tokenRegExp.test(val);
}

const headerCharRegex = /[^\t\x20-\x7e\x80-\xff]/;
// True if val contains an invalid field-vchar.
function checkInvalidHeaderChar(val: string): boolean {
    return headerCharRegex.test(val);
}

const chunkExpression = /(?:^|\W)chunked(?:$|\W)/i;
const continueExpression = /(?:^|\W)100-continue(?:$|\W)/i;

export function validateHeaderName(name: string, label?: string): void {
    if (typeof name !== 'string' || !name || !checkIsHttpToken(name)) {
        throw new HttpTypeError('ERR_INVALID_HTTP_TOKEN', (label || 'Header name') + ' must be a valid HTTP token ["' + name + '"]');
    }
}

export function validateHeaderValue(name: string, value: any): void {
    if (value === undefined) {
        throw new HttpTypeError('ERR_HTTP_INVALID_HEADER_VALUE', 'Invalid value "' + value + '" for header "' + name + '"');
    }
    if (checkInvalidHeaderChar(String(value))) {
        throw new HttpTypeError('ERR_INVALID_CHAR', 'Invalid character in header content ["' + name + '"]');
    }
}

// The native parser, as Node's HTTPParser binding: execute() runs the
// parser over a chunk and calls the handlers below as it goes.
const REQUEST = 0;
const RESPONSE = 1;

class HTTPParser {
    id: number;
    socket: net.Socket | null = null;
    incoming: IncomingMessage | null = null;
    outgoing: OutgoingMessage | null = null;
    maxHeaderPairs = 2000;
    type = REQUEST;
    onIncoming: ((incoming: IncomingMessage, shouldKeepAlive: boolean) => number) | null = null;
    currentBuffer: Buffer | null = null;

    constructor(type: number, maxHeaderSize: number) {
        this.type = type;
        this.id = __kml_native.httpParserNew(type, maxHeaderSize);
    }

    initialize(type: number, maxHeaderSize: number): void {
        this.type = type;
        __kml_native.httpParserInit(this.id, type, maxHeaderSize);
    }

    private headerList(): string[] {
        const n = __kml_native.httpParserInfo(this.id, 5);
        const out: string[] = [];
        for (let i = 0; i < n; i++) out.push(__kml_native.httpParserHeader(this.id, i));
        return out;
    }

    private parseError(consumed: number): Error {
        const code = __kml_native.httpParserString(this.id, 3);
        const reason = __kml_native.httpParserString(this.id, 4);
        const err: any = new Error(reason ? 'Parse Error: ' + reason : 'Parse Error');
        err.bytesParsed = consumed;
        err.code = code;
        err.reason = reason;
        return err;
    }

    // The bytes parsed, or the parse error; at an upgrade, the bytes before
    // the other protocol's.
    execute(data: Buffer): number | Error {
        this.currentBuffer = data;
        let off = 0;
        while (true) {
            const ev = __kml_native.httpParserExecute(this.id, data, off, data.length - off);
            off += __kml_native.httpParserInfo(this.id, 6);
            if (ev === 0) break;
            if (ev === 1) {
                const major = __kml_native.httpParserInfo(this.id, 0);
                const minor = __kml_native.httpParserInfo(this.id, 1);
                const upgrade = __kml_native.httpParserInfo(this.id, 3) === 1;
                const keepAlive = __kml_native.httpParserInfo(this.id, 4) === 1;
                const ret = this.onHeadersComplete(major, minor, this.headerList(),
                    __kml_native.httpParserString(this.id, 0), __kml_native.httpParserString(this.id, 1),
                    __kml_native.httpParserInfo(this.id, 2), __kml_native.httpParserString(this.id, 2),
                    upgrade, keepAlive);
                if (ret === 1) __kml_native.httpParserSet(this.id, 1);
                // An upgrade nothing takes goes on as HTTP.
                if (upgrade && this.incoming !== null && !this.incoming.upgrade) __kml_native.httpParserSet(this.id, 2);
            } else if (ev === 2) {
                const start = __kml_native.httpParserInfo(this.id, 7);
                const len = __kml_native.httpParserInfo(this.id, 8);
                this.onBody(data.subarray(start, start + len));
            } else if (ev === 3) {
                this.onMessageComplete(this.headerList());
            } else if (ev === 4) {
                this.currentBuffer = null;
                return off;
            } else {
                this.currentBuffer = null;
                return this.parseError(off);
            }
        }
        this.currentBuffer = null;
        return data.length;
    }

    finish(): number | Error {
        const ev = __kml_native.httpParserFinish(this.id);
        if (ev === 3) {
            this.onMessageComplete([]);
            return 0;
        }
        if (ev < 0) return this.parseError(0);
        return 0;
    }

    // parserOnHeadersComplete
    onHeadersComplete(versionMajor: number, versionMinor: number, headers: string[], method: string,
        url: string, statusCode: number, statusMessage: string, upgrade: boolean, shouldKeepAlive: boolean): number {
        const socket = this.socket;
        if (socket === null) return 0;
        const st = httpOf(socket).state;
        if (st !== null && st.messageStart === 0) st.messageStart = Date.now();
        const incoming = new IncomingMessage(socket);
        this.incoming = incoming;
        incoming.httpVersionMajor = versionMajor;
        incoming.httpVersionMinor = versionMinor;
        incoming.httpVersion = versionMajor + '.' + versionMinor;
        incoming.url = url;
        incoming.upgrade = upgrade;
        let n = headers.length;
        // If parser.maxHeaderPairs <= 0 assume that there's no limit.
        if (this.maxHeaderPairs > 0) n = Math.min(n, this.maxHeaderPairs);
        incoming._addHeaderLines(headers, n);
        if (this.type === REQUEST) {
            incoming.method = method;
        } else {
            incoming.statusCode = statusCode;
            incoming.statusMessage = statusMessage;
        }
        const on = this.onIncoming;
        return on !== null ? on(incoming, shouldKeepAlive) : 0;
    }

    // parserOnBody
    onBody(b: Buffer): void {
        const stream = this.incoming;
        // If the stream has already been removed, then drop it.
        if (stream === null) return;
        // Pretend this was the result of a stream._read call.
        if (!stream._dumped) {
            const ret = stream.push(b);
            if (!ret && this.socket !== null) readStop(this.socket);
        }
    }

    // parserOnMessageComplete
    onMessageComplete(trailers: string[]): void {
        if (this.socket !== null) {
            const st = httpOf(this.socket).state;
            if (st !== null) st.messageStart = 0;
        }
        const stream = this.incoming;
        if (stream !== null) {
            stream.complete = true;
            if (trailers.length > 0) stream._addHeaderLines(trailers, trailers.length);
            // For emit end event
            stream.push(null);
        }
        // Force to read the next incoming message
        if (this.socket !== null) readStart(this.socket);
    }

    free(): void {
        this.socket = null;
        this.incoming = null;
        this.outgoing = null;
        this.onIncoming = null;
    }
}

// What Node keeps on a socket an HTTP server or client drives: its parser,
// its in-flight message, the connection's state, whether it was paused for
// backpressure.
class SocketHttp {
    parser: HTTPParser | null = null;
    httpMessage: OutgoingMessage | null = null;
    state: ConnectionState | null = null;
    paused = false;
    onClose: (() => void) | null = null;
    // A client request's socket listeners (tickOnSocket), for removal.
    clientListeners: ClientSocketListeners | null = null;
}

const socketHttp = new Map<net.Socket, SocketHttp>();

function httpOf(socket: net.Socket): SocketHttp {
    let h = socketHttp.get(socket);
    if (h === undefined) {
        h = new SocketHttp();
        socketHttp.set(socket, h);
        socket.once('close', () => { socketHttp.delete(socket); });
    }
    return h;
}

function freeParser(parser: HTTPParser, req: IncomingMessage | null, socket: net.Socket | null): void {
    parser.free();
    if (socket !== null) httpOf(socket).parser = null;
}

// ---- lib/_http_incoming.js ----

export interface IncomingHttpHeaders {
    [key: string]: string | string[] | undefined;
    accept?: string;
    'accept-encoding'?: string;
    'accept-language'?: string;
    authorization?: string;
    connection?: string;
    'content-length'?: string;
    'content-type'?: string;
    cookie?: string;
    expect?: string;
    host?: string;
    origin?: string;
    referer?: string;
    'set-cookie'?: string[];
    'transfer-encoding'?: string;
    upgrade?: string;
    'user-agent'?: string;
}

function readStart(socket: net.Socket): void {
    if (socket.readableFlowing === false || httpOf(socket).paused) return;
    if (socket.readableFlowing === null || !socket.destroyed) socket.resume();
}

function readStop(socket: net.Socket): void {
    socket.pause();
}

// Add the given (field, value) pair to the message: a known field is
// kept once (the first wins), set-cookie gathers an array, cookie joins
// with '; ', any other joins with ', '.
function matchKnownFields(field: string): string {
    switch (field.length) {
        case 3:
            if (field === 'Age' || field === 'age') return 'age';
            break;
        case 4:
            if (field === 'Host' || field === 'host') return 'host';
            if (field === 'From' || field === 'from') return 'from';
            if (field === 'ETag' || field === 'etag') return 'etag';
            if (field === 'Date' || field === 'date') return '\u0000date';
            if (field === 'Vary' || field === 'vary') return '\u0000vary';
            break;
        case 6:
            if (field === 'Server' || field === 'server') return 'server';
            if (field === 'Cookie' || field === 'cookie') return '\u0002cookie';
            if (field === 'Origin' || field === 'origin') return '\u0000origin';
            if (field === 'Expect' || field === 'expect') return '\u0000expect';
            if (field === 'Accept' || field === 'accept') return '\u0000accept';
            break;
        case 7:
            if (field === 'Referer' || field === 'referer') return 'referer';
            if (field === 'Expires' || field === 'expires') return 'expires';
            if (field === 'Upgrade' || field === 'upgrade') return '\u0000upgrade';
            break;
        case 8:
            if (field === 'Location' || field === 'location') return 'location';
            if (field === 'If-Match' || field === 'if-match') return '\u0000if-match';
            break;
        case 10:
            if (field === 'User-Agent' || field === 'user-agent') return 'user-agent';
            if (field === 'Set-Cookie' || field === 'set-cookie') return '\u0001';
            if (field === 'Connection' || field === 'connection') return '\u0000connection';
            break;
        case 11:
            if (field === 'Retry-After' || field === 'retry-after') return 'retry-after';
            break;
        case 12:
            if (field === 'Content-Type' || field === 'content-type') return 'content-type';
            if (field === 'Max-Forwards' || field === 'max-forwards') return 'max-forwards';
            break;
        case 13:
            if (field === 'Authorization' || field === 'authorization') return 'authorization';
            if (field === 'Last-Modified' || field === 'last-modified') return 'last-modified';
            if (field === 'Cache-Control' || field === 'cache-control') return '\u0000cache-control';
            if (field === 'If-None-Match' || field === 'if-none-match') return '\u0000if-none-match';
            break;
        case 14:
            if (field === 'Content-Length' || field === 'content-length') return 'content-length';
            break;
        case 15:
            if (field === 'Accept-Encoding' || field === 'accept-encoding') return '\u0000accept-encoding';
            if (field === 'Accept-Language' || field === 'accept-language') return '\u0000accept-language';
            if (field === 'X-Forwarded-For' || field === 'x-forwarded-for') return '\u0000x-forwarded-for';
            break;
        case 16:
            if (field === 'X-Forwarded-Host' || field === 'x-forwarded-host') return '\u0000x-forwarded-host';
            break;
        case 17:
            if (field === 'If-Modified-Since' || field === 'if-modified-since') return 'if-modified-since';
            if (field === 'Transfer-Encoding' || field === 'transfer-encoding') return '\u0000transfer-encoding';
            if (field === 'X-Forwarded-Proto' || field === 'x-forwarded-proto') return '\u0000x-forwarded-proto';
            break;
        case 19:
            if (field === 'Proxy-Authorization' || field === 'proxy-authorization') return 'proxy-authorization';
            if (field === 'If-Unmodified-Since' || field === 'if-unmodified-since') return 'if-unmodified-since';
            break;
    }
    const lowered = field.toLowerCase();
    if (lowered !== field) return matchKnownFields(lowered);
    // Unknown header: joined.
    return '\u0000' + field;
}

export class IncomingMessage extends Readable {
    aborted = false;
    httpVersionMajor = 0;
    httpVersionMinor = 0;
    httpVersion = '';
    complete = false;
    rawHeaders: string[] = [];
    rawTrailers: string[] = [];
    joinDuplicateHeaders = false;
    upgrade = false;
    url = '';
    method: string | undefined = undefined;
    statusCode: number | undefined = undefined;
    statusMessage: string | undefined = undefined;
    socket: net.Socket;
    // The client's request this response answers.
    req: ClientRequest | null = null;
    // A kept-alive client socket handed back to its agent is no longer this
    // response's to destroy.
    detachedFromSocket = false;
    _consuming = false;
    // Flag for when we decide that this message cannot possibly be
    // read by the user, so there's no point continuing to handle it.
    _dumped = false;
    private headersCache: IncomingHttpHeaders | null = null;
    private headersCount = 0;
    private trailersCache: { [key: string]: string | undefined } | null = null;
    private trailersCount = 0;

    constructor(socket: net.Socket) {
        super({ highWaterMark: socket.readableHighWaterMark });
        this.socket = socket;
        this._readableState!.readingMore = true;
    }

    get connection(): net.Socket {
        return this.socket;
    }

    get headers(): IncomingHttpHeaders {
        let dst = this.headersCache;
        if (dst === null) {
            dst = {};
            this.headersCache = dst;
            const src = this.rawHeaders;
            for (let n = 0; n < this.headersCount; n += 2) this._addHeaderLine(src[n], src[n + 1], dst);
        }
        return dst;
    }

    set headers(val: IncomingHttpHeaders) {
        this.headersCache = val;
    }

    get headersDistinct(): { [key: string]: string[] } {
        const out: { [key: string]: string[] } = {};
        const src = this.rawHeaders;
        for (let n = 0; n < this.headersCount; n += 2) {
            const key = src[n].toLowerCase();
            const cur = out[key];
            if (cur !== undefined) cur.push(src[n + 1]);
            else out[key] = [src[n + 1]];
        }
        return out;
    }

    get trailers(): { [key: string]: string | undefined } {
        let dst = this.trailersCache;
        if (dst === null) {
            const d: { [key: string]: string | undefined } = {};
            this.trailersCache = d;
            const src = this.rawTrailers;
            for (let n = 0; n < this.trailersCount; n += 2) {
                const field = matchKnownFields(src[n]);
                const key = field.charCodeAt(0) <= 2 ? field.slice(1) : field;
                const cur = d[key];
                if (cur === undefined) d[key] = src[n + 1];
                else if (field.charCodeAt(0) === 0) d[key] = cur + ', ' + src[n + 1];
            }
            dst = d;
        }
        return dst;
    }

    setTimeout(msecs: number, callback?: () => void): this {
        if (callback) this.on('timeout', callback);
        this.socket.setTimeout(msecs);
        return this;
    }

    _read(n: number): void {
        if (!this._consuming) {
            this._readableState!.readingMore = false;
            this._consuming = true;
        }
        // We actually do almost nothing here, because the parserOnBody
        // function fills up our internal buffer directly.  However, we
        // do need to unpause the underlying socket so that it flows.
        if (this.socket.readable) readStart(this.socket);
    }

    // It's possible that the socket will be destroyed, and removed from
    // any messages, before ever calling this.  In that case, just skip
    // it, since something else is destroying this connection anyway.
    _destroy(err: Error | null, cb: (error?: Error | null) => void): void {
        if (!this.readableEnded || !this.complete) {
            this.aborted = true;
            this.emit('aborted');
        }
        // If aborted and the underlying socket is not already destroyed,
        // destroy it.
        if (!this.detachedFromSocket && !this.socket.destroyed && this.aborted) {
            this.socket.destroy(err ?? undefined);
            const cleanup = finished(this.socket, (e?: Error | null) => {
                let ex: Error | null | undefined = e;
                if (ex && (ex as HttpError).code === 'ERR_STREAM_PREMATURE_CLOSE') ex = null;
                cleanup();
                const final: Error | null = ex ?? err;
                process.nextTick(() => { cb(final); });
            });
        } else {
            process.nextTick(() => { cb(err); });
        }
    }

    _addHeaderLines(headers: string[], n: number): void {
        if (headers.length === 0) return;
        if (this.complete) {
            this.rawTrailers = headers;
            this.trailersCount = n;
            this.trailersCache = null;
        } else {
            this.rawHeaders = headers;
            this.headersCount = n;
            this.headersCache = null;
        }
    }

    // Add the given (field, value) pair to the message
    //
    // Per RFC2616, section 4.2 it is acceptable to join multiple instances of
    // the same header with a ', ' if the header in question supports
    // specification of multiple values this way. The one exception to this
    // is the Set-Cookie header, which has an array of values; and Cookie,
    // joined with '; '.
    _addHeaderLine(field: string, value: string, dest: IncomingHttpHeaders): void {
        const matched = matchKnownFields(field);
        const flag = matched.charCodeAt(0);
        if (flag === 0 || flag === 2) {
            const key = matched.slice(1);
            // Make a delimited list
            const cur = dest[key];
            if (typeof cur === 'string') dest[key] = cur + (flag === 0 ? ', ' : '; ') + value;
            else dest[key] = value;
        } else if (flag === 1) {
            // Array header -- only Set-Cookie at the moment
            const sc = dest['set-cookie'];
            if (sc !== undefined) sc.push(value);
            else dest['set-cookie'] = [value];
        } else if (this.joinDuplicateHeaders) {
            const cur = dest[matched];
            if (cur === undefined) dest[matched] = value;
            else if (typeof cur === 'string') dest[matched] = cur + ', ' + value;
        } else if (dest[matched] === undefined) {
            // Drop duplicates
            dest[matched] = value;
        }
    }

    // Call this instead of resume() if we want to just
    // dump all the data to /dev/null
    _dump(): void {
        if (!this._dumped) {
            this._dumped = true;
            // If there is buffered data, it may trigger 'data' events.
            // Remove 'data' event listeners explicitly.
            this.removeAllListeners('data');
            this.resume();
        }
    }
}

// ---- lib/_http_outgoing.js ----

export type OutgoingHttpHeader = number | string | string[];

export interface OutgoingHttpHeaders {
    [key: string]: OutgoingHttpHeader | undefined;
}

const crlf_buf: Buffer = Buffer.from('\r\n');
const kCorked = 0;

function isCookieField(s: string): boolean {
    return s.length === 6 && s.toLowerCase() === 'cookie';
}

function isContentDispositionField(s: string): boolean {
    return s.length === 19 && s.toLowerCase() === 'content-disposition';
}

// A header as setHeader stored it: its name as given, and its value.
interface HeaderEntry {
    name: string;
    value: OutgoingHttpHeader;
}

interface OutputChunk {
    data: Buffer;
    callback: ((err?: Error | null) => void) | null;
}

function utcDate(): string {
    return new Date().toUTCString();
}

export class OutgoingMessage extends Stream {
    // Queue that holds all currently pending data, until the response will
    // be assigned to the socket (until it will its turn in the HTTP pipeline).
    outputData: OutputChunk[] = [];
    // `outputSize` is an approximate measure of how much data is queued on
    // this response. `_onPendingData` will be invoked to update similar
    // global per-connection counter. That counter will be used to pause/unpause
    // the TCP socket and HTTP Parser and thus handle the backpressure.
    outputSize = 0;
    writable = true;
    destroyed = false;
    _last = false;
    chunkedEncoding = false;
    shouldKeepAlive = true;
    maxRequestsOnConnectionReached = false;
    _defaultKeepAlive = true;
    useChunkedEncodingByDefault = true;
    sendDate = false;
    _removedConnection = false;
    _removedContLen = false;
    _removedTE = false;
    strictContentLength = false;
    _contentLength: number | null = null;
    _hasBody = true;
    _trailer = '';
    finished = false;
    _headerSent = false;
    _closed = false;
    _header: string | null = null;
    _keepAliveTimeout = 0;
    _maxRequestsPerSocket = 0;
    socket: net.Socket | null = null;
    req: IncomingMessage | null = null;
    // The header map: lowercased name -> as set.
    protected headersMap: { [key: string]: HeaderEntry | undefined } | null = null;
    private headerOrder: string[] = [];
    private corked = 0;
    private chunkedEncodingPending: OutputChunk[] = [];
    private errored: Error | null = null;
    _onPendingData: (amount: number) => void = (amount: number) => {};

    constructor(options?: { highWaterMark?: number; rejectNonStandardBodyWrites?: boolean }) {
        super();
        this.highWaterMark = options?.highWaterMark ?? 16 * 1024;
    }

    highWaterMark: number;

    get writableFinished(): boolean {
        return this.finished && this.outputSize === 0 && (this.socket === null || this.socket.writableLength === 0);
    }

    get writableObjectMode(): boolean {
        return false;
    }

    get writableLength(): number {
        return this.outputSize + this.corked * 0 + (this.socket !== null ? this.socket.writableLength : 0);
    }

    get writableHighWaterMark(): number {
        return this.socket !== null ? this.socket.writableHighWaterMark : this.highWaterMark;
    }

    get writableCorked(): number {
        return this.corked;
    }

    get writableEnded(): boolean {
        return this.finished;
    }

    get writableNeedDrain(): boolean {
        return !this.destroyed && !this.finished && this._needDrain;
    }

    _needDrain = false;

    get connection(): net.Socket | null {
        return this.socket;
    }

    get headersSent(): boolean {
        return this._header !== null;
    }

    get errored_(): Error | null {
        return this.errored;
    }

    cork(): void {
        this.corked++;
        if (this.socket !== null) this.socket.cork();
    }

    uncork(): void {
        if (this.corked > 0) this.corked--;
        if (this.socket !== null) this.socket.uncork();
        if (this.corked === 0 && this.chunkedEncodingPending.length > 0) {
            const pending = this.chunkedEncodingPending;
            this.chunkedEncodingPending = [];
            let len = 0;
            for (const c of pending) len += c.data.length;
            if (!this._header) this._implicitHeader();
            const head = Buffer.from(len.toString(16) + '\r\n', 'latin1');
            const parts: Buffer[] = [head];
            const callbacks: ((err?: Error | null) => void)[] = [];
            for (const c of pending) {
                parts.push(c.data);
                if (c.callback !== null) callbacks.push(c.callback);
            }
            parts.push(crlf_buf);
            this._send(Buffer.concat(parts), null, callbacks.length > 0 ? (err?: Error | null) => {
                for (const cb of callbacks) cb(err);
            } : null);
        }
    }

    setTimeout(msecs: number, callback?: () => void): this {
        if (callback) this.on('timeout', callback);
        const socket = this.socket;
        if (socket === null) {
            this.once('socket', (s: net.Socket) => { s.setTimeout(msecs); });
        } else {
            socket.setTimeout(msecs);
        }
        return this;
    }

    // It's possible that the socket will be destroyed, and removed from
    // any messages, before ever calling this.  In that case, just skip
    // it, since something else is destroying this connection anyway.
    destroy(error?: Error): this {
        if (this.destroyed) return this;
        this.destroyed = true;
        if (error) this.errored = error;
        const socket = this.socket;
        if (socket !== null) {
            socket.destroy(error);
        } else {
            this.once('socket', (s: net.Socket) => { s.destroy(error); });
        }
        return this;
    }

    // This abstract either writing directly to the socket or buffering it.
    _send(data: Buffer, encoding: BufferEncoding | null, callback: ((err?: Error | null) => void) | null, byteLength?: number): boolean {
        // This is a shameful hack to get the headers and first body chunk onto
        // the same packet. Future versions of Node are going to take care of
        // this at a lower level and in a more general way.
        if (!this._headerSent && this._header !== null) {
            const header = Buffer.from(this._header, 'latin1');
            data = Buffer.concat([header, data]);
            this._headerSent = true;
        }
        return this._writeRaw(data, callback);
    }

    _writeRaw(data: Buffer, callback: ((err?: Error | null) => void) | null): boolean {
        const conn = this.socket;
        if (conn !== null && conn.destroyed) {
            // The socket was destroyed. If we're still trying to write to it,
            // then we haven't gotten the 'close' event yet.
            return false;
        }
        if (conn !== null && httpOf(conn).httpMessage === this && conn.writable) {
            // There might be pending data in the this.output buffer.
            if (this.outputData.length > 0) this._flushOutput(conn);
            // Directly write to socket.
            return callback !== null ? conn.write(data, callback) : conn.write(data);
        }
        // Buffer, as long as we're not destroyed.
        this.outputData.push({ data: data, callback: callback });
        this.outputSize += data.length;
        this._onPendingData(data.length);
        return this.outputSize < this.highWaterMark;
    }

    _storeHeader(firstLine: string, headers: HeaderEntry[] | null): void {
        // firstLine in the case of request is: 'GET /index.html HTTP/1.1\r\n'
        // in the case of response it is: 'HTTP/1.1 200 OK\r\n'
        const state = {
            connection: false,
            contLen: false,
            te: false,
            date: false,
            expect: false,
            trailer: false,
            header: firstLine,
        };
        if (headers !== null) {
            for (const h of headers) this.processHeader(state, h.name, h.value, true);
        }
        let header = state.header;
        // Date header
        if (this.sendDate && !state.date) header += 'Date: ' + utcDate() + '\r\n';
        // Force the connection to close when the response is a 204 No Content or
        // a 304 Not Modified and the user has set a "Transfer-Encoding: chunked"
        // header.
        //
        // RFC 2616 mandates that 204 and 304 responses MUST NOT have a body but
        // node.js used to send out a zero chunk anyway to accommodate clients
        // that don't have special handling for those responses.
        //
        // It was pointed out that this might confuse reverse proxies to the point
        // of creating security liabilities, so suppress the zero chunk and force
        // the connection to close.
        if (this.chunkedEncoding && (this.statusCodeForHeader === 204 || this.statusCodeForHeader === 304)) {
            this._last = true;
            this.shouldKeepAlive = false;
        }
        // keep-alive logic
        if (this._removedConnection) {
            // shouldKeepAlive is generally true for HTTP/1.1. In that common case,
            // even if the connection header isn't sent, we still persist by default.
            this._last = !this.shouldKeepAlive;
        } else if (!state.connection) {
            const shouldSendKeepAlive = this.shouldKeepAlive &&
                (state.contLen || this.useChunkedEncodingByDefault || this.agentKeepAlive);
            if (shouldSendKeepAlive && this.maxRequestsOnConnectionReached) {
                header += 'Connection: close\r\n';
            } else if (shouldSendKeepAlive) {
                header += 'Connection: keep-alive\r\n';
                if (this._keepAliveTimeout && this._defaultKeepAlive) {
                    const timeoutSeconds = Math.floor(this._keepAliveTimeout / 1000);
                    let max = '';
                    if (this._maxRequestsPerSocket > 0) max = ', max=' + this._maxRequestsPerSocket;
                    header += 'Keep-Alive: timeout=' + timeoutSeconds + max + '\r\n';
                }
            } else {
                this._last = true;
                header += 'Connection: close\r\n';
            }
        }
        if (!state.contLen && !state.te) {
            if (!this._hasBody) {
                // Make sure we don't end the 0\r\n\r\n at the end of the message.
                this.chunkedEncoding = false;
            } else if (!this.useChunkedEncodingByDefault) {
                this._last = true;
            } else if (!state.trailer && !this._removedContLen && typeof this._contentLength === 'number') {
                header += 'Content-Length: ' + this._contentLength + '\r\n';
            } else if (!this._removedTE) {
                header += 'Transfer-Encoding: chunked\r\n';
                this.chunkedEncoding = true;
            } else {
                // We should only be able to get here if both Content-Length and
                // Transfer-Encoding are removed by the user.
                // See: test/parallel/test-http-remove-header-stays-removed.js
                this._last = true;
            }
        }
        // Test non-chunked message does not have trailer header set,
        // message will be terminated by the first empty line after the
        // header fields, regardless of the header fields present in the
        // message, and thus cannot contain a message body or 'trailers'.
        if (this.chunkedEncoding !== true && state.trailer) {
            throw new HttpError('ERR_HTTP_TRAILER_INVALID', 'Trailers are invalid with this transfer encoding');
        }
        this._header = header + '\r\n';
        this._headerSent = false;
        // Wait until the first body chunk, or close(), is sent to flush,
        // UNLESS we're sending Expect: 100-continue.
        if (state.expect) this._send(Buffer.alloc(0), null, null);
    }

    // The response's status code, for _storeHeader's 204/304 rule.
    statusCodeForHeader = 0;
    // Whether an Agent keeps this request's socket alive (the client's).
    agentKeepAlive = false;

    private processHeader(state: { connection: boolean; contLen: boolean; te: boolean; date: boolean; expect: boolean; trailer: boolean; header: string },
        key: string, value: OutgoingHttpHeader, validate: boolean): void {
        if (validate) validateHeaderName(key);
        // If key is content-disposition and there is content-length
        // encode the value in latin1
        // https://www.rfc-editor.org/rfc/rfc6266#section-4.3
        // Refs: https://github.com/nodejs/node/pull/46528
        if (typeof value === 'string' || typeof value === 'number') {
            this.storeHeader(state, key, String(value), validate);
            return;
        }
        if (value.length < 2 || !isCookieField(key)) {
            // Retain for(;;) loop for performance reasons
            // Refs: https://github.com/nodejs/node/pull/30958
            for (let i = 0; i < value.length; i++) this.storeHeader(state, key, value[i], validate);
            return;
        }
        this.storeHeader(state, key, value.join('; '), validate);
    }

    private storeHeader(state: { connection: boolean; contLen: boolean; te: boolean; date: boolean; expect: boolean; trailer: boolean; header: string },
        key: string, value: string, validate: boolean): void {
        if (validate) validateHeaderValue(key, value);
        state.header += key + ': ' + value + '\r\n';
        this.matchHeader(state, key, value);
    }

    private matchHeader(state: { connection: boolean; contLen: boolean; te: boolean; date: boolean; expect: boolean; trailer: boolean; header: string },
        field: string, value: string): void {
        if (field.length < 4 || field.length > 17) return;
        const lower = field.toLowerCase();
        switch (lower) {
            case 'connection':
                state.connection = true;
                this._removedConnection = false;
                if (/(?:^|\W)close(?:$|\W)/i.test(value)) this._last = true;
                else this.shouldKeepAlive = true;
                break;
            case 'transfer-encoding':
                state.te = true;
                this._removedTE = false;
                if (chunkExpression.test(value)) this.chunkedEncoding = true;
                break;
            case 'content-length':
                state.contLen = true;
                this._contentLength = +value;
                this._removedContLen = false;
                break;
            case 'date':
            case 'expect':
            case 'trailer':
                if (lower === 'date') state.date = true;
                else if (lower === 'expect') state.expect = true;
                else state.trailer = true;
                break;
            case 'keep-alive':
                this._defaultKeepAlive = false;
                break;
        }
    }

    setHeader(name: string, value: number | string | readonly string[]): this {
        if (this._header) {
            throw new HttpError('ERR_HTTP_HEADERS_SENT', 'Cannot set headers after they are sent to the client');
        }
        validateHeaderName(name);
        if (value === undefined) {
            throw new HttpTypeError('ERR_HTTP_INVALID_HEADER_VALUE', 'Invalid value "undefined" for header "' + name + '"');
        }
        const v: OutgoingHttpHeader = typeof value === 'string' || typeof value === 'number' ? value : (value as string[]).slice();
        if (typeof v === 'string' || typeof v === 'number') validateHeaderValue(name, v);
        else for (const one of v) validateHeaderValue(name, one);
        let headers = this.headersMap;
        if (headers === null) {
            headers = {};
            this.headersMap = headers;
        }
        const key = name.toLowerCase();
        if (headers[key] === undefined) this.headerOrder.push(key);
        headers[key] = { name: name, value: v };
        return this;
    }

    setHeaders(headers: Map<string, number | string | readonly string[]>): this {
        if (this._header) {
            throw new HttpError('ERR_HTTP_HEADERS_SENT', 'Cannot set headers after they are sent to the client');
        }
        for (const [k, v] of headers) this.setHeader(k, v);
        return this;
    }

    appendHeader(name: string, value: number | string | readonly string[]): this {
        if (this._header) {
            throw new HttpError('ERR_HTTP_HEADERS_SENT', 'Cannot append headers after they are sent to the client');
        }
        validateHeaderName(name);
        const key = name.toLowerCase();
        const existing = this.headersMap !== null ? this.headersMap[key] : undefined;
        if (existing === undefined) return this.setHeader(name, value);
        const prev: string[] = typeof existing.value === 'object' ? existing.value.slice() : [String(existing.value)];
        if (typeof value === 'string' || typeof value === 'number') {
            validateHeaderValue(name, value);
            prev.push(String(value));
        } else {
            for (const one of value as string[]) {
                validateHeaderValue(name, one);
                prev.push(one);
            }
        }
        existing.value = prev;
        return this;
    }

    getHeader(name: string): OutgoingHttpHeader | undefined {
        if (typeof name !== 'string') {
            throw new HttpTypeError('ERR_INVALID_ARG_TYPE', 'The "name" argument must be of type string.' + received(name));
        }
        const headers = this.headersMap;
        if (headers === null) return undefined;
        const entry = headers[name.toLowerCase()];
        return entry !== undefined ? entry.value : undefined;
    }

    // Returns an array of the names of the current outgoing headers.
    getHeaderNames(): string[] {
        return this.headersMap !== null ? this.headerOrder.filter((k: string) => this.headersMap![k] !== undefined) : [];
    }

    // Returns an array of the names of the current outgoing raw headers.
    getRawHeaderNames(): string[] {
        const headers = this.headersMap;
        if (headers === null) return [];
        const out: string[] = [];
        for (const k of this.headerOrder) {
            const e = headers[k];
            if (e !== undefined) out.push(e.name);
        }
        return out;
    }

    // Returns a shallow copy of the current outgoing headers.
    getHeaders(): OutgoingHttpHeaders {
        const ret: OutgoingHttpHeaders = {};
        const headers = this.headersMap;
        if (headers !== null) {
            for (const k of this.headerOrder) {
                const e = headers[k];
                if (e !== undefined) ret[k] = e.value;
            }
        }
        return ret;
    }

    hasHeader(name: string): boolean {
        if (typeof name !== 'string') {
            throw new HttpTypeError('ERR_INVALID_ARG_TYPE', 'The "name" argument must be of type string.' + received(name));
        }
        return this.headersMap !== null && this.headersMap[name.toLowerCase()] !== undefined;
    }

    removeHeader(name: string): void {
        if (typeof name !== 'string') {
            throw new HttpTypeError('ERR_INVALID_ARG_TYPE', 'The "name" argument must be of type string.' + received(name));
        }
        if (this._header) {
            throw new HttpError('ERR_HTTP_HEADERS_SENT', 'Cannot remove headers after they are sent to the client');
        }
        const key = name.toLowerCase();
        switch (key) {
            case 'connection':
                this._removedConnection = true;
                break;
            case 'content-length':
                this._removedContLen = true;
                break;
            case 'transfer-encoding':
                this._removedTE = true;
                break;
            case 'date':
                this.sendDate = false;
                break;
        }
        if (this.headersMap !== null && this.headersMap[key] !== undefined) {
            this.headersMap[key] = undefined;
            this.headerOrder = this.headerOrder.filter((k: string) => k !== key);
        }
    }

    // The headers as _storeHeader lists them, in the order they were set.
    protected headerEntries(): HeaderEntry[] | null {
        const headers = this.headersMap;
        if (headers === null) return null;
        const out: HeaderEntry[] = [];
        for (const k of this.headerOrder) {
            const e = headers[k];
            if (e !== undefined) out.push(e);
        }
        return out;
    }

    _implicitHeader(): void {
        throw new HttpError('ERR_METHOD_NOT_IMPLEMENTED', 'The _implicitHeader() method is not implemented');
    }

    write(chunk: string | Buffer | Uint8Array, encoding?: BufferEncoding | ((error?: Error | null) => void), callback?: (error?: Error | null) => void): boolean {
        let enc: BufferEncoding | null = null;
        let cb: ((error?: Error | null) => void) | null = null;
        if (typeof encoding === 'function') cb = encoding;
        else {
            if (encoding !== undefined) enc = encoding;
            if (callback !== undefined) cb = callback;
        }
        const ret = this.write_(chunk, enc, cb, false);
        if (!ret) this._needDrain = true;
        return ret;
    }

    private writeAfterEnd(cb: ((error?: Error | null) => void) | null): void {
        const err = new HttpError('ERR_STREAM_WRITE_AFTER_END', 'write after end');
        process.nextTick(() => {
            this.emit('error', err);
            if (cb !== null) cb(err);
        });
    }

    private write_(chunk: string | Buffer | Uint8Array, encoding: BufferEncoding | null, callback: ((error?: Error | null) => void) | null, fromEnd: boolean): boolean {
        let err: Error | null = null;
        if (this.finished) {
            err = new HttpError('ERR_STREAM_WRITE_AFTER_END', 'write after end');
        } else if (this.destroyed) {
            err = new HttpError('ERR_STREAM_DESTROYED', 'Cannot call write after a stream was destroyed');
        }
        if (err !== null) {
            const e = err;
            if (!this.destroyed) {
                process.nextTick(() => { this.emit('error', e); });
            }
            if (callback !== null) process.nextTick(() => { callback(e); });
            return false;
        }
        if (chunk === null) {
            throw new HttpTypeError('ERR_STREAM_NULL_VALUES', 'May not write null values to stream');
        }
        const data: Buffer = typeof chunk === 'string' ? Buffer.from(chunk, encoding ?? 'utf8') :
            Buffer.isBuffer(chunk) ? chunk : Buffer.from(chunk);
        if (!this._header) {
            if (fromEnd) this._contentLength = data.length;
            this._implicitHeader();
        }
        if (!this._hasBody) {
            if (this.rejectNonStandardBodyWrites) {
                throw new HttpError('ERR_HTTP_BODY_NOT_ALLOWED', 'Adding content for this request method or response status is not allowed.');
            }
            if (callback !== null) process.nextTick(() => { callback(null); });
            return true;
        }
        if (!fromEnd && this.socket !== null && !this.socket.writableCorked) {
            this.socket.cork();
            const s = this.socket;
            process.nextTick(() => { s.uncork(); });
        }
        if (this.strictContentLength && this._contentLength !== null) {
            // checked by Node against the running total; not tracked here
        }
        if (this.chunkedEncoding && data.length !== 0) {
            if (this.corked > 0) {
                this.chunkedEncodingPending.push({ data: data, callback: callback });
                return this.outputSize < this.highWaterMark;
            }
            const head = Buffer.from(data.length.toString(16) + '\r\n', 'latin1');
            return this._send(Buffer.concat([head, data, crlf_buf]), null, callback);
        }
        return this._send(data, encoding, callback);
    }

    rejectNonStandardBodyWrites = false;

    addTrailers(headers: OutgoingHttpHeaders | [string, string][]): void {
        this._trailer = '';
        const add = (field: string, value: string): void => {
            if (typeof field !== 'string' || !field || !checkIsHttpToken(field)) {
                throw new HttpTypeError('ERR_INVALID_HTTP_TOKEN', 'Trailer name must be a valid HTTP token ["' + field + '"]');
            }
            if (checkInvalidHeaderChar(value)) {
                throw new HttpTypeError('ERR_INVALID_CHAR', 'Invalid character in trailer content ["' + field + '"]');
            }
            this._trailer += field + ': ' + value + '\r\n';
        };
        if (Array.isArray(headers)) {
            for (const [k, v] of headers as [string, string][]) add(k, v);
        } else {
            const h = headers as OutgoingHttpHeaders;
            for (const k in h) {
                const v = h[k];
                if (v !== undefined) add(k, String(v));
            }
        }
    }

    end(chunk?: string | Buffer | Uint8Array | (() => void), encoding?: BufferEncoding | (() => void), callback?: () => void): this {
        let data: string | Buffer | Uint8Array | null = null;
        let enc: BufferEncoding | null = null;
        let cb: (() => void) | null = null;
        if (typeof chunk === 'function') {
            cb = chunk;
        } else {
            if (chunk !== undefined && chunk !== null) data = chunk;
            if (typeof encoding === 'function') cb = encoding;
            else {
                if (encoding !== undefined) enc = encoding;
                if (callback !== undefined) cb = callback;
            }
        }
        if (data !== null) {
            if (this.finished) {
                this.writeAfterEnd(cb !== null ? (e?: Error | null) => { cb!(); } : null);
                return this;
            }
            if (this.socket !== null) this.socket.cork();
            this.write_(data, enc, null, true);
            if (this.socket !== null) this.socket.uncork();
        } else if (this.finished) {
            if (cb !== null) {
                if (!this.writableFinished) {
                    this.on('finish', cb);
                } else {
                    const f = cb;
                    process.nextTick(() => { f(); });
                }
            }
            return this;
        } else if (!this._header) {
            if (this.socket !== null) this.socket.cork();
            this._contentLength = 0;
            this._implicitHeader();
            if (this.socket !== null) this.socket.uncork();
        }
        if (cb !== null) this.once('finish', cb);
        const finish = (err?: Error | null): void => { this.onFinish(); };
        if (this._hasBody && this.chunkedEncoding) {
            this._send(Buffer.from('0\r\n' + this._trailer + '\r\n', 'latin1'), null, finish);
        } else if (!this._headerSent || this.writableLength || data !== null) {
            this._send(Buffer.alloc(0), null, finish);
        } else {
            process.nextTick(() => { finish(null); });
        }
        if (this.socket !== null) {
            // Fully uncork connection on end().
            this.socket.uncork();
        }
        this.corked = 0;
        this.finished = true;
        // There is the first message on the outgoing queue, and we've sent
        // everything to the socket.
        if (this.outputData.length === 0 && this.socket !== null && httpOf(this.socket).httpMessage === this) {
            this._finish();
        }
        return this;
    }

    private onFinish(): void {
        this.emit('finish');
    }

    _finish(): void {
        this.emit('prefinish');
    }

    // This logic is probably a bit confusing. Let me explain a bit:
    //
    // In both HTTP servers and clients it is possible to queue up several
    // outgoing messages. This is easiest to imagine in the case of a client.
    // Take the following situation:
    //
    //    req1 = client.request('GET', '/');
    //    req2 = client.request('POST', '/');
    //
    // When the user does
    //
    //   req2.write('hello world\n');
    //
    // it's possible that the first request has not been completely flushed to
    // the socket yet. Thus the outgoing messages need to be prepared to queue
    // up data internally before sending it on further to the socket's queue.
    //
    // This function, _flush(), is called by both the Server and Client
    // to attempt to flush any pending messages out to the socket.
    _flush(): void {
        const socket = this.socket;
        if (socket !== null && socket.writable) {
            // There might be remaining data in this.output; write it out
            const ret = this._flushOutput(socket);
            if (this.finished) {
                // This is a queue to the server or client to bring in the next this.
                this._finish();
            } else if (ret && this._needDrain) {
                this._needDrain = false;
                this.emit('drain');
            }
        }
    }

    _flushOutput(socket: net.Socket): boolean {
        while (this.corked > 0) {
            this.corked--;
            socket.cork();
        }
        const outputLength = this.outputData.length;
        if (outputLength <= 0) return true;
        const outputData = this.outputData;
        socket.cork();
        let ret = true;
        for (let i = 0; i < outputLength; i++) {
            const { data, callback } = outputData[i];
            ret = callback !== null ? socket.write(data, callback) : socket.write(data);
        }
        socket.uncork();
        this.outputData = [];
        this._onPendingData(-this.outputSize);
        this.outputSize = 0;
        return ret;
    }

    flushHeaders(): void {
        if (!this._header) this._implicitHeader();
        // Force-flush the headers.
        this._send(Buffer.alloc(0), null, null);
    }

    // What a Readable piping into this calls (Node calls write/end by name).
    _kmlWrite(chunk: any): boolean { return this.write(chunk); }
    _kmlEnd(): void { this.end(); }

    pipe<T extends Stream>(destination: T, options?: { end?: boolean }): T {
        // OutgoingMessage should be write-only. Piping from it is disabled.
        this.emit('error', new HttpError('ERR_STREAM_CANNOT_PIPE', 'Cannot pipe, not readable'));
        return destination;
    }
}

// ---- lib/_http_server.js ----

const kServerResponse = 1;

export class ServerResponse extends OutgoingMessage {
    statusCode = 200;
    statusMessage: string = '';
    _sent100 = false;
    _expect_continue = false;
    private writeHeadCalled = false;
    _upgrading = false;

    constructor(req: IncomingMessage, options?: { highWaterMark?: number; rejectNonStandardBodyWrites?: boolean }) {
        super(options);
        if (req.method === 'HEAD') this._hasBody = false;
        this.req = req;
        this.sendDate = true;
        if (req.httpVersionMajor < 1 || req.httpVersionMinor < 1) {
            this.useChunkedEncodingByDefault = chunkExpression.test(String(req.headers.te ?? ''));
            this.shouldKeepAlive = false;
        }
        if (options?.rejectNonStandardBodyWrites) this.rejectNonStandardBodyWrites = true;
    }

    _finish(): void {
        super._finish();
    }

    assignSocket(socket: net.Socket): void {
        const h = httpOf(socket);
        if (h.httpMessage !== null) {
            throw new HttpError('ERR_HTTP_SOCKET_ASSIGNED', 'ServerResponse has an already assigned socket');
        }
        h.httpMessage = this;
        const onClose = () => { onServerResponseClose(socket); };
        h.onClose = onClose;
        socket.on('close', onClose);
        this.socket = socket;
        this.emit('socket', socket);
        this._flush();
    }

    detachSocket(socket: net.Socket): void {
        const h = httpOf(socket);
        if (h.onClose !== null) socket.removeListener('close', h.onClose);
        h.onClose = null;
        h.httpMessage = null;
        this.socket = null;
    }

    writeContinue(cb?: () => void): void {
        this._writeRaw(Buffer.from('HTTP/1.1 100 Continue\r\n\r\n', 'latin1'), cb !== undefined ? (e?: Error | null) => { cb(); } : null);
        this._sent100 = true;
    }

    writeProcessing(cb?: () => void): void {
        this._writeRaw(Buffer.from('HTTP/1.1 102 Processing\r\n\r\n', 'latin1'), cb !== undefined ? (e?: Error | null) => { cb(); } : null);
    }

    writeEarlyHints(hints: { [key: string]: string | string[] | undefined }, cb?: () => void): void {
        let head = 'HTTP/1.1 103 Early Hints\r\n';
        const link = hints.link;
        if (link === undefined) {
            throw new HttpTypeError('ERR_INVALID_ARG_VALUE', "The argument 'hints' is invalid.");
        }
        head += 'Link: ' + (typeof link === 'string' ? link : link.join(', ')) + '\r\n';
        for (const key in hints) {
            if (key !== 'link') {
                const v = hints[key];
                if (v !== undefined) head += key + ': ' + String(v) + '\r\n';
            }
        }
        head += '\r\n';
        this._writeRaw(Buffer.from(head, 'latin1'), cb !== undefined ? (e?: Error | null) => { cb(); } : null);
    }

    _implicitHeader(): void {
        this.writeHead(this.statusCode);
    }

    writeHead(statusCode: number, reason?: string | OutgoingHttpHeaders | OutgoingHttpHeader[], obj?: OutgoingHttpHeaders | OutgoingHttpHeader[]): this {
        if (this._header) {
            throw new HttpError('ERR_HTTP_HEADERS_SENT', 'Cannot write headers after they are sent to the client');
        }
        const originalStatusCode = statusCode;
        statusCode |= 0;
        if (statusCode < 100 || statusCode > 999) {
            throw new HttpRangeError('ERR_HTTP_INVALID_STATUS_CODE', 'Invalid status code: ' + originalStatusCode);
        }
        let headersArg: OutgoingHttpHeaders | OutgoingHttpHeader[] | null = null;
        if (typeof reason === 'string') {
            // writeHead(statusCode, reasonPhrase[, headers])
            this.statusMessage = reason;
            if (obj !== undefined) headersArg = obj;
        } else {
            // writeHead(statusCode[, headers])
            this.statusMessage ||= STATUS_CODES[String(statusCode)] ?? 'unknown';
            if (reason !== undefined) headersArg = reason;
            else if (obj !== undefined) headersArg = obj;
        }
        this.statusCode = statusCode;
        let headers: HeaderEntry[] | null;
        if (this.headersMap !== null) {
            // Slow-case: when progressive API and header fields are passed.
            if (headersArg !== null) {
                if (Array.isArray(headersArg)) {
                    const arr = headersArg as OutgoingHttpHeader[];
                    if (arr.length % 2 !== 0) {
                        throw new HttpTypeError('ERR_INVALID_ARG_VALUE', "The argument 'headers' is invalid.");
                    }
                    // Headers in obj should override previous headers but still
                    // allow explicit duplicates. To do so, we first remove any
                    // existing conflicts, then use appendHeader.
                    for (let n = 0; n < arr.length; n += 2) this.removeHeader(String(arr[n]));
                    for (let n = 0; n < arr.length; n += 2) {
                        const v = arr[n + 1];
                        if (v !== undefined) this.appendHeader(String(arr[n]), v);
                    }
                } else {
                    const o = headersArg as OutgoingHttpHeaders;
                    for (const k in o) {
                        const v = o[k];
                        if (v !== undefined) this.setHeader(k, v);
                    }
                }
            }
            // Only progressive api is used
            headers = this.headerEntries();
        } else {
            // Only writeHead() called
            headers = [];
            if (headersArg !== null) {
                if (Array.isArray(headersArg)) {
                    const arr = headersArg as OutgoingHttpHeader[];
                    if (arr.length % 2 !== 0) {
                        throw new HttpTypeError('ERR_INVALID_ARG_VALUE', "The argument 'headers' is invalid.");
                    }
                    for (let n = 0; n < arr.length; n += 2) {
                        headers.push({ name: String(arr[n]), value: arr[n + 1] });
                    }
                } else {
                    const o = headersArg as OutgoingHttpHeaders;
                    for (const k in o) {
                        const v = o[k];
                        if (v !== undefined) headers.push({ name: k, value: v });
                    }
                }
            }
        }
        if (checkInvalidHeaderChar(this.statusMessage)) {
            throw new HttpError('ERR_INVALID_CHAR', 'Invalid character in statusMessage');
        }
        const statusLine = 'HTTP/1.1 ' + statusCode + ' ' + this.statusMessage + '\r\n';
        if (statusCode === 204 || statusCode === 304 || (statusCode >= 100 && statusCode <= 199)) {
            // RFC 2616, 10.2.5:
            // The 204 response MUST NOT include a message-body, and thus is always
            // terminated by the first empty line after the header fields.
            // RFC 2616, 10.3.5:
            // The 304 response MUST NOT contain a message-body, and thus is always
            // terminated by the first empty line after the header fields.
            // RFC 2616, 10.1 Informational 1xx:
            // This class of status code indicates a provisional response,
            // consisting only of the Status-Line and optional headers, and is
            // terminated by an empty line.
            this._hasBody = false;
        }
        // Don't keep alive connections where the client expects 100 Continue
        // but we sent a final status; they may put extra bytes on the wire.
        if (this._expect_continue && !this._sent100) this.shouldKeepAlive = false;
        this.statusCodeForHeader = statusCode;
        this._storeHeader(statusLine, headers);
        return this;
    }

    writeHeader(statusCode: number, reason?: string | OutgoingHttpHeaders, obj?: OutgoingHttpHeaders): this {
        return this.writeHead(statusCode, reason, obj);
    }
}

function onServerResponseClose(socket: net.Socket): void {
    // EventEmitter.emit makes a copy of the 'close' listeners array before
    // calling the listeners. detachSocket() unregisters onServerResponseClose
    // but if detachSocket() is called, directly or indirectly, by a 'close'
    // listener, onServerResponseClose is still in that copy of the listeners
    // array. That is, in the example below, b still gets called even though
    // it's been removed by a:
    //
    //   const EventEmitter = require('events');
    //   const obj = new EventEmitter();
    //   obj.on('event', a);
    //   obj.on('event', b);
    //   function a() { obj.removeListener('event', b) }
    //   function b() { throw "BAM!" }
    //   obj.emit('event');  // throws
    //
    // Ergo, we need to deal with stale 'close' events and handle the case
    // where the ServerResponse object has already been deconstructed.
    // Fortunately, that requires only a single if check. :-)
    const msg = httpOf(socket).httpMessage;
    if (msg !== null) {
        msg.destroyed = true;
        msg._closed = true;
        msg.emit('close');
    }
}

export type RequestListener = (req: IncomingMessage, res: ServerResponse) => void;

export interface ServerOptions {
    requestTimeout?: number;
    joinDuplicateHeaders?: boolean;
    keepAliveTimeout?: number;
    keepAliveTimeoutBuffer?: number;
    connectionsCheckingInterval?: number;
    highWaterMark?: number;
    insecureHTTPParser?: boolean;
    maxHeaderSize?: number;
    noDelay?: boolean;
    requireHostHeader?: boolean;
    keepAlive?: boolean;
    keepAliveInitialDelay?: number;
    uniqueHeaders?: Array<string | string[]>;
    rejectNonStandardBodyWrites?: boolean;
    headersTimeout?: number;
    maxRequestsPerSocket?: number;
}

const badRequestResponse = 'HTTP/1.1 400 Bad Request\r\nConnection: close\r\n\r\n';
const requestTimeoutResponse = 'HTTP/1.1 408 Request Timeout\r\nConnection: close\r\n\r\n';
const requestHeaderFieldsTooLargeResponse = 'HTTP/1.1 431 Request Header Fields Too Large\r\nConnection: close\r\n\r\n';

// A connection's parse state (Node's connectionListenerInternal locals).
class ConnectionState {
    onData: ((d: Buffer) => void) | null = null;
    onEnd: (() => void) | null = null;
    onClose: (() => void) | null = null;
    onDrain: (() => void) | null = null;
    incoming: IncomingMessage[] = [];
    outgoing: ServerResponse[] = [];
    // `outgoingData` is an approximate amount of bytes queued through all
    // inactive responses. If more data than the high watermark is queued - we
    // need to pause TCP socket/HTTP parser, and wait until the data will be
    // sent to the client.
    outgoingData = 0;
    requestsCount = 0;
    keepAliveTimeoutSet = false;
    // The current message's start (Date.now()), 0 between messages, and
    // whether its headers are complete: what the connections check reads.
    messageStart = 0;
    headersDone = false;
}

// A server's HTTP side (Node's _http_server connectionListener and its
// helpers, with the options storeHTTPOptions keeps): http.Server and
// https.Server each drive their connections through one. (`_kml`: shared
// with the https module, not an export of http's.)
export class _kmlHttpServerCore {
    server: net.Server;
    timeout = 0;
    maxHeadersCount: number | null = null;
    maxRequestsPerSocket = 0;
    headersTimeout = 60000;
    keepAliveTimeout = 5000;
    requestTimeout = 300000;
    connectionsCheckingInterval = 30000;
    keepAliveTimeoutBuffer = 1000;
    private checkTimer: ReturnType<typeof setInterval> | null = null;
    httpAllowHalfOpen = false;
    requireHostHeader = true;
    joinDuplicateHeaders = false;
    rejectNonStandardBodyWrites = false;
    maxHeaderSize_: number = maxHeaderSize;
    idleConnections: net.Socket[] = [];
    activeConnections: net.Socket[] = [];
    serverHighWaterMark = 16 * 1024;

    constructor(server: net.Server, opts: ServerOptions) {
        this.server = server;
        if (opts.requestTimeout !== undefined) {
            if (opts.requestTimeout < 0) throw new HttpRangeError('ERR_OUT_OF_RANGE', 'The value of "options.requestTimeout" is out of range. It must be >= 0. Received ' + opts.requestTimeout);
            this.requestTimeout = opts.requestTimeout;
        }
        if (opts.headersTimeout !== undefined) this.headersTimeout = opts.headersTimeout;
        if (opts.keepAliveTimeout !== undefined) this.keepAliveTimeout = opts.keepAliveTimeout;
        if (opts.keepAliveTimeoutBuffer !== undefined) this.keepAliveTimeoutBuffer = opts.keepAliveTimeoutBuffer;
        if (opts.connectionsCheckingInterval !== undefined) this.connectionsCheckingInterval = opts.connectionsCheckingInterval;
        if (opts.maxRequestsPerSocket !== undefined) this.maxRequestsPerSocket = opts.maxRequestsPerSocket;
        if (opts.requireHostHeader !== undefined) this.requireHostHeader = opts.requireHostHeader;
        if (opts.joinDuplicateHeaders !== undefined) this.joinDuplicateHeaders = opts.joinDuplicateHeaders;
        if (opts.rejectNonStandardBodyWrites !== undefined) this.rejectNonStandardBodyWrites = opts.rejectNonStandardBodyWrites;
        this.maxHeaderSize_ = opts.maxHeaderSize ?? maxHeaderSize;
        this.serverHighWaterMark = opts.highWaterMark ?? 16 * 1024;
    }

    // Node's connections checking: an unref'd interval expires a connection
    // whose request headers (headersTimeout) or whole request
    // (requestTimeout) take too long.
    setupConnectionsTracking(): void {
        if (this.checkTimer !== null) clearInterval(this.checkTimer);
        const t = setInterval(() => { this.checkConnections(); }, this.connectionsCheckingInterval);
        t.unref();
        this.checkTimer = t;
    }

    private checkConnections(): void {
        if (this.headersTimeout === 0 && this.requestTimeout === 0) return;
        const now = Date.now();
        // Every connection with a message in progress (Node's active list).
        const all = this.activeConnections.concat(this.idleConnections);
        for (const socket of all) {
            const state = httpOf(socket).state;
            if (state === null || state.messageStart === 0) continue;
            const age = now - state.messageStart;
            if ((!state.headersDone && this.headersTimeout > 0 && age > this.headersTimeout) ||
                (this.requestTimeout > 0 && age > this.requestTimeout)) {
                state.messageStart = 0;
                this.socketOnError(socket, new HttpError('ERR_HTTP_REQUEST_TIMEOUT', 'Request timeout'));
            }
        }
    }

    stopConnectionsTracking(): void {
        if (this.checkTimer !== null) {
            clearInterval(this.checkTimer);
            this.checkTimer = null;
        }
    }

    closeAllConnections(): void {
        const all = this.activeConnections.concat(this.idleConnections);
        for (const s of all) s.destroy();
    }

    closeIdleConnections(): void {
        const idle = this.idleConnections.slice();
        for (const s of idle) {
            const state = httpOf(s).state;
            if (state !== null && state.outgoing.length === 0 && state.incoming.length === 0) s.destroy();
        }
    }


    private trackActive(socket: net.Socket): void {
        const i = this.idleConnections.indexOf(socket);
        if (i >= 0) this.idleConnections.splice(i, 1);
        if (this.activeConnections.indexOf(socket) < 0) this.activeConnections.push(socket);
    }

    private trackIdle(socket: net.Socket): void {
        const i = this.activeConnections.indexOf(socket);
        if (i >= 0) this.activeConnections.splice(i, 1);
        if (this.idleConnections.indexOf(socket) < 0) this.idleConnections.push(socket);
    }

    private untrack(socket: net.Socket): void {
        let i = this.activeConnections.indexOf(socket);
        if (i >= 0) this.activeConnections.splice(i, 1);
        i = this.idleConnections.indexOf(socket);
        if (i >= 0) this.idleConnections.splice(i, 1);
    }

    connectionListener(socket: net.Socket): void {
        // Ensure that the server property of the socket is correctly set.
        // See https://github.com/nodejs/node/issues/13435
        socket.server = this.server;
        // If the user has added a listener to the server,
        // request, or response, then it's their responsibility.
        // otherwise, destroy on timeout by default
        if (this.timeout && typeof socket.setTimeout === 'function') socket.setTimeout(this.timeout);
        socket.on('timeout', () => { this.socketOnTimeout(socket); });
        const parser = new HTTPParser(REQUEST, this.maxHeaderSize_);
        parser.socket = socket;
        const h = httpOf(socket);
        h.parser = parser;
        this.trackIdle(socket);
        // Propagate headers limit from server instance to parser
        if (typeof this.maxHeadersCount === 'number') parser.maxHeaderPairs = this.maxHeadersCount << 1;
        const state = new ConnectionState();
        h.state = state;
        state.onData = (d: Buffer) => { this.socketOnData(socket, parser, state, d); };
        state.onEnd = () => { this.socketOnEnd(socket, parser, state); };
        state.onClose = () => { this.socketOnClose(socket, state); };
        state.onDrain = () => { this.socketOnDrain(socket, state); };
        socket.on('data', state.onData);
        socket.on('error', (e: Error) => { this.socketOnError(socket, e); });
        socket.on('end', state.onEnd);
        socket.on('close', state.onClose);
        socket.on('drain', state.onDrain);
        parser.onIncoming = (req: IncomingMessage, keepAlive: boolean): number => this.parserOnIncoming(socket, parser, state, req, keepAlive);
        // We are consuming socket, so it won't get any actual data
        socket.on('resume', () => { this.onSocketResume(socket); });
        socket.on('pause', () => { this.onSocketPause(socket); });
        h.paused = false;
    }

    private socketOnTimeout(socket: net.Socket): void {
        const h = httpOf(socket);
        const req = h.parser !== null ? h.parser.incoming : null;
        const reqTimeout = req !== null && !req.complete && req.emit('timeout', socket);
        const res = h.httpMessage;
        const resTimeout = res !== null && res.emit('timeout', socket);
        const serverTimeout = this.server.emit('timeout', socket);
        if (!reqTimeout && !resTimeout && !serverTimeout) socket.destroy();
    }

    private socketOnClose(socket: net.Socket, state: ConnectionState): void {
        state.messageStart = 0;
        const parser = httpOf(socket).parser;
        if (parser !== null) {
            parser.finish();
            freeParser(parser, null, socket);
        }
        this.untrack(socket);
        this.abortIncoming(state.incoming);
    }

    private abortIncoming(incoming: IncomingMessage[]): void {
        while (incoming.length) {
            const req = incoming.shift()!;
            req.destroy(connResetException('aborted'));
        }
        // Abort socket._httpMessage ?
    }

    private socketOnEnd(socket: net.Socket, parser: HTTPParser, state: ConnectionState): void {
        const ret = parser.finish();
        if (ret instanceof Error) {
            this.socketOnError(socket, ret);
        } else if (!this.httpAllowHalfOpen) {
            socket.end();
        } else if (state.outgoing.length) {
            state.outgoing[state.outgoing.length - 1]._last = true;
        } else if (httpOf(socket).httpMessage !== null) {
            httpOf(socket).httpMessage!._last = true;
        } else {
            socket.end();
        }
    }

    private socketOnData(socket: net.Socket, parser: HTTPParser, state: ConnectionState, d: Buffer): void {
        if (state.messageStart === 0) {
            state.messageStart = Date.now();
            state.headersDone = false;
        }
        const ret = parser.execute(d);
        this.onParserExecuteCommon(socket, parser, state, ret, d);
    }

    private onParserExecuteCommon(socket: net.Socket, parser: HTTPParser, state: ConnectionState, ret: number | Error, d: Buffer): void {
        if (ret instanceof Error) {
            const e: any = ret;
            e.rawPacket = d;
            this.socketOnError(socket, ret);
            return;
        }
        const incoming = parser.incoming;
        if (incoming !== null && incoming.upgrade) {
            // Upgrade or CONNECT
            const req = incoming;
            const eventName = req.method === 'CONNECT' ? 'connect' : 'upgrade';
            if (eventName === 'upgrade' || this.server.listenerCount(eventName) > 0) {
                const bodyHead = d.subarray(ret, d.length);
                socket.readableFlowing = null;
                state.messageStart = 0;
                this.unconsume(socket, parser, state);
                parser.finish();
                freeParser(parser, req, socket);
                this.untrack(socket);
                httpOf(socket).state = null;
                this.server.emit(eventName, req, socket, bodyHead);
            } else {
                // Got CONNECT method, but have no handler.
                socket.destroy();
            }
        } else if (parser.incoming !== null && parser.incoming.method === 'PRI') {
            this.socketOnError(socket, new HttpError('ERR_HTTP2_NOT_SUPPORTED', 'HTTP/2 is not supported'));
        }

    }

    private unconsume(socket: net.Socket, parser: HTTPParser, state: ConnectionState): void {
        if (state.onData !== null) socket.removeListener('data', state.onData);
        if (state.onEnd !== null) socket.removeListener('end', state.onEnd);
        if (state.onClose !== null) socket.removeListener('close', state.onClose);
        if (state.onDrain !== null) socket.removeListener('drain', state.onDrain);
        socket.removeAllListeners('timeout');
        socket.removeAllListeners('resume');
        socket.removeAllListeners('pause');
        socket.removeAllListeners('error');
    }

    socketOnError(socket: net.Socket, e: Error): void {
        // Ignore further errors
        socket.removeAllListeners('error');
        socket.on('error', (err: Error) => {});
        if (socket.timeout) socket.setTimeout(0);
        if (!this.server.emit('clientError', e, socket)) {
            // Caution must be taken to avoid corrupting the remote peer.
            // Reply an error segment if there is no in-flight `ServerResponse`,
            // or no data of the in-flight one has been written yet to this socket.
            const msg = httpOf(socket).httpMessage;
            if (socket.writable && (msg === null || !msg._headerSent)) {
                let response: string;
                const code = (e as HttpError).code;
                switch (code) {
                    case 'HPE_HEADER_OVERFLOW':
                        response = requestHeaderFieldsTooLargeResponse;
                        break;
                    case 'ERR_HTTP_REQUEST_TIMEOUT':
                        response = requestTimeoutResponse;
                        break;
                    default:
                        response = badRequestResponse;
                        break;
                }
                socket.write(Buffer.from(response, 'latin1'));
            }
            socket.destroy(e);
        }
    }

    private socketOnDrain(socket: net.Socket, state: ConnectionState): void {
        const needPause = state.outgoingData > socket.writableHighWaterMark;
        // If we previously paused, then start reading again.
        const h = httpOf(socket);
        if (h.paused && !needPause) {
            h.paused = false;
            socket.resume();
        }
        const msg = h.httpMessage;
        if (msg !== null && !msg.finished && msg._needDrain) {
            msg._needDrain = false;
            msg.emit('drain');
        }
    }

    private onSocketResume(socket: net.Socket): void {}

    private onSocketPause(socket: net.Socket): void {}

    private updateOutgoingData(socket: net.Socket, state: ConnectionState, delta: number): void {
        state.outgoingData += delta;
        this.socketOnDrain(socket, state);
    }

    private resOnFinish(req: IncomingMessage, res: ServerResponse, socket: net.Socket, state: ConnectionState): void {
        // Usually the first incoming element should be our request.  it may
        // be that in the case abortIncoming() was called that the incoming
        // array will be empty.
        const i = state.incoming.indexOf(req);
        if (i >= 0) state.incoming.splice(i, 1);
        // If the user never called req.read(), and didn't pipe() or
        // .resume() or .on('data'), then we call req._dump() so that the
        // bytes will be pulled off the wire.
        if (!req._consuming && !req._readableState!.resumeScheduled) req._dump();
        res.detachSocket(socket);
        process.nextTick(() => {
            res.emit('close');
        });
        if (res._last) {
            if (typeof socket.destroySoon === 'function') socket.destroySoon();
            else socket.end();
        } else if (state.outgoing.length === 0) {
            this.trackIdle(socket);
            if (this.keepAliveTimeout && typeof socket.setTimeout === 'function') {
                // Increase the internal timeout wrt the advertised value to reduce
                // the likelihood of ECONNRESET errors.
                socket.setTimeout(this.keepAliveTimeout + this.keepAliveTimeoutBuffer);
                state.keepAliveTimeoutSet = true;
            }
        } else {
            // Start sending the next message
            const m = state.outgoing.shift()!;
            if (m) m.assignSocket(socket);
        }
    }

    private parserOnIncoming(socket: net.Socket, parser: HTTPParser, state: ConnectionState, req: IncomingMessage, keepAlive: boolean): number {
        state.headersDone = true;
        this.trackActive(socket);
        if (req.upgrade) {
            req.upgrade = req.method === 'CONNECT' || this.server.listenerCount('upgrade') > 0;
            if (req.upgrade) return 0;
        }
        state.incoming.push(req);
        // If the writable end isn't consuming, then stop reading
        // so that we don't become overwhelmed by a flood of
        // pipelined requests that may never be resolved.
        const h = httpOf(socket);
        if (!h.paused) {
            if (socket.writableNeedDrain || state.outgoingData >= socket.writableHighWaterMark) {
                h.paused = true;
                // We also need to pause the parser, but don't do that until after
                // the call to execute, because we may still be processing the last
                // chunk.
                socket.pause();
            }
        }
        if (state.keepAliveTimeoutSet) {
            socket.setTimeout(this.timeout);
            state.keepAliveTimeoutSet = false;
        }
        req.joinDuplicateHeaders = this.joinDuplicateHeaders;
        const res = new ServerResponse(req, { highWaterMark: this.serverHighWaterMark, rejectNonStandardBodyWrites: this.rejectNonStandardBodyWrites });
        res._keepAliveTimeout = this.keepAliveTimeout;
        res._maxRequestsPerSocket = this.maxRequestsPerSocket;
        res._onPendingData = (delta: number) => { this.updateOutgoingData(socket, state, delta); };
        res.shouldKeepAlive = keepAlive;
        res._upgrading = false;
        if (h.httpMessage !== null) {
            // There are already pending outgoing res, append.
            state.outgoing.push(res);
        } else {
            res.assignSocket(socket);
        }
        // When we're finished writing the response, check if this is the last
        // response, if so destroy the socket.
        res.on('finish', () => { this.resOnFinish(req, res, socket, state); });
        state.requestsCount++;
        if (this.maxRequestsPerSocket > 0 && state.requestsCount > this.maxRequestsPerSocket) {
            // Drop the request: over the limit.
            res.writeHead(503);
            res.end();
            this.server.emit('dropRequest', req, socket);
            return 0;
        }
        if (this.maxRequestsPerSocket > 0 && state.requestsCount === this.maxRequestsPerSocket) {
            res.maxRequestsOnConnectionReached = true;
        }
        let handled = false;
        if (req.httpVersionMajor === 1 && req.httpVersionMinor === 1) {
            // From RFC 7230 5.4 https://datatracker.ietf.org/doc/html/rfc7230#section-5.4
            // A server MUST respond with a 400 (Bad Request) status code to any
            // HTTP/1.1 request message that lacks a Host header field
            if (this.requireHostHeader && req.headers.host === undefined) {
                res.writeHead(400, ['Connection', 'close']);
                res.end();
                return 0;
            }
            const isRequestsLimitSet = typeof this.maxRequestsPerSocket === 'number' && this.maxRequestsPerSocket > 0;
            const expect = req.headers.expect;
            if (expect !== undefined) {
                if (continueExpression.test(expect)) {
                    res._expect_continue = true;
                    if (this.server.listenerCount('checkContinue') > 0) {
                        this.server.emit('checkContinue', req, res);
                    } else {
                        res.writeContinue();
                        this.server.emit('request', req, res);
                    }
                } else if (this.server.listenerCount('checkExpectation') > 0) {
                    this.server.emit('checkExpectation', req, res);
                } else {
                    res.writeHead(417);
                    res.end();
                }
                handled = true;
            }
            if (isRequestsLimitSet) {}
        }
        if (!handled) {
            this.server.emit('request', req, res);
        }
        return 0; // No special treatment.
    }
}

// kml:callable Server — Node's is a function that constructs when called without `new`
export class Server extends net.Server {
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
        super({ allowHalfOpen: true, noDelay: opts.noDelay ?? true, keepAlive: opts.keepAlive, keepAliveInitialDelay: opts.keepAliveInitialDelay, highWaterMark: opts.highWaterMark });
        const core = new _kmlHttpServerCore(this, opts);
        this.core = core;
        if (listener !== null) this.on('request', listener);
        this.on('connection', (socket: net.Socket) => { core.connectionListener(socket); });
        this.on('listening', () => { core.setupConnectionsTracking(); });
    }

    // The options, as properties (Node keeps them on the server).
    get timeout(): number { return this.core.timeout; }
    set timeout(v: number) { this.core.timeout = v; }
    get maxHeadersCount(): number | null { return this.core.maxHeadersCount; }
    set maxHeadersCount(v: number | null) { this.core.maxHeadersCount = v; }
    get maxRequestsPerSocket(): number { return this.core.maxRequestsPerSocket; }
    set maxRequestsPerSocket(v: number) { this.core.maxRequestsPerSocket = v; }
    get headersTimeout(): number { return this.core.headersTimeout; }
    set headersTimeout(v: number) { this.core.headersTimeout = v; }
    get keepAliveTimeout(): number { return this.core.keepAliveTimeout; }
    set keepAliveTimeout(v: number) { this.core.keepAliveTimeout = v; }
    get requestTimeout(): number { return this.core.requestTimeout; }
    set requestTimeout(v: number) { this.core.requestTimeout = v; }
    get maxHeaderSize_(): number { return this.core.maxHeaderSize_; }

    // The events, typed as @types/node declares them.
    on(event: 'close', listener: () => void): this;
    on(event: 'connection', listener: (socket: net.Socket) => void): this;
    on(event: 'error', listener: (err: Error) => void): this;
    on(event: 'listening', listener: () => void): this;
    on(event: 'checkContinue', listener: RequestListener): this;
    on(event: 'checkExpectation', listener: RequestListener): this;
    on(event: 'clientError', listener: (err: Error, socket: net.Socket) => void): this;
    on(event: 'connect', listener: (req: IncomingMessage, socket: net.Socket, head: Buffer) => void): this;
    on(event: 'dropRequest', listener: (req: IncomingMessage, socket: net.Socket) => void): this;
    on(event: 'request', listener: RequestListener): this;
    on(event: 'upgrade', listener: (req: IncomingMessage, socket: net.Socket, head: Buffer) => void): this;
    on(event: string | symbol, listener: (...args: any[]) => void): this;
    on(event: string | symbol, listener: (...args: any[]) => void): this {
        return super.on(event, listener);
    }

    once(event: 'close', listener: () => void): this;
    once(event: 'connection', listener: (socket: net.Socket) => void): this;
    once(event: 'error', listener: (err: Error) => void): this;
    once(event: 'listening', listener: () => void): this;
    once(event: 'checkContinue', listener: RequestListener): this;
    once(event: 'checkExpectation', listener: RequestListener): this;
    once(event: 'clientError', listener: (err: Error, socket: net.Socket) => void): this;
    once(event: 'connect', listener: (req: IncomingMessage, socket: net.Socket, head: Buffer) => void): this;
    once(event: 'dropRequest', listener: (req: IncomingMessage, socket: net.Socket) => void): this;
    once(event: 'request', listener: RequestListener): this;
    once(event: 'upgrade', listener: (req: IncomingMessage, socket: net.Socket, head: Buffer) => void): this;
    once(event: string | symbol, listener: (...args: any[]) => void): this;
    once(event: string | symbol, listener: (...args: any[]) => void): this {
        return super.once(event, listener);
    }


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

function connResetException(msg: string): Error {
    const ex: any = new Error(msg);
    ex.code = 'ECONNRESET';
    return ex;
}

export function createServer(requestListener?: RequestListener): Server;
export function createServer(options: ServerOptions, requestListener?: RequestListener): Server;
export function createServer(options?: ServerOptions | RequestListener, requestListener?: RequestListener): Server {
    if (typeof options === 'function') return new Server(options);
    return new Server(options ?? {}, requestListener);
}

// ---- lib/_http_agent.js ----

export interface AgentOptions {
    keepAlive?: boolean;
    keepAliveMsecs?: number;
    maxSockets?: number;
    maxTotalSockets?: number;
    maxFreeSockets?: number;
    timeout?: number;
    scheduling?: 'fifo' | 'lifo';
    noDelay?: boolean;
}

// What an Agent connects with: a request's resolved options.
export interface AgentConnectOptions {
    host?: string;
    port?: number;
    localAddress?: string;
    family?: number;
    socketPath?: string;
    servername?: string;
    rejectUnauthorized?: boolean;
    ca?: string | Buffer | Array<string | Buffer>;
    cert?: string | Buffer | Array<string | Buffer>;
    key?: string | Buffer | Array<string | Buffer>;
    timeout?: number;
    keepAlive?: boolean;
    keepAliveInitialDelay?: number;
    noDelay?: boolean;
}

function freeSocketErrorListener(err: Error): void {
    // The socket is idle in an agent's pool: its error only removes it.
}

// kml:callable Agent — Node's is a function that constructs when called without `new`
export class Agent extends EventEmitter {
    defaultPort = 80;
    protocol = 'http:';
    options: AgentOptions & AgentConnectOptions;
    requests: { [key: string]: ClientRequest[] } = {};
    sockets: { [key: string]: net.Socket[] } = {};
    freeSockets: { [key: string]: net.Socket[] } = {};
    keepAliveMsecs: number;
    keepAlive: boolean;
    maxSockets: number;
    maxFreeSockets: number;
    maxTotalSockets: number;
    totalSocketCount = 0;
    scheduling: string;
    static defaultMaxSockets = Infinity;

    constructor(options?: AgentOptions) {
        super();
        const o = options ?? {};
        this.options = { keepAlive: o.keepAlive, keepAliveMsecs: o.keepAliveMsecs, maxSockets: o.maxSockets,
            maxTotalSockets: o.maxTotalSockets, maxFreeSockets: o.maxFreeSockets, timeout: o.timeout,
            scheduling: o.scheduling, noDelay: o.noDelay ?? true };
        this.keepAliveMsecs = o.keepAliveMsecs || 1000;
        this.keepAlive = o.keepAlive || false;
        this.maxSockets = o.maxSockets || Agent.defaultMaxSockets;
        this.maxFreeSockets = o.maxFreeSockets || 256;
        this.scheduling = o.scheduling || 'lifo';
        this.maxTotalSockets = o.maxTotalSockets ?? Infinity;
        this.on('free', (socket: net.Socket, opts: AgentConnectOptions) => { this.onFree(socket, opts); });
    }

    private onFree(socket: net.Socket, options: AgentConnectOptions): void {
        const name = this.getName(options);
        if (!socket.writable) {
            socket.destroy();
            return;
        }
        const requests = this.requests[name];
        if (requests !== undefined && requests.length > 0) {
            const req = requests.shift()!;
            this.setRequestSocket(req, socket);
            if (requests.length === 0) delete this.requests[name];
            return;
        }
        // If there are no pending requests, then put it in the freeSockets
        // pool, but only if we're allowed to do so.
        const req = httpOf(socket).httpMessage;
        if (req === null || !req.shouldKeepAlive || !this.keepAlive) {
            socket.destroy();
            return;
        }
        const freeSockets = this.freeSockets[name] ?? [];
        const freeLen = freeSockets.length;
        let count = freeLen;
        const active = this.sockets[name];
        if (active !== undefined) count += active.length;
        if (this.totalSocketCount > this.maxTotalSockets || count > this.maxSockets ||
            freeLen >= this.maxFreeSockets || !this.keepSocketAlive(socket)) {
            socket.destroy();
            return;
        }
        this.freeSockets[name] = freeSockets;
        httpOf(socket).httpMessage = null;
        this.removeSocket(socket, options);
        socket.once('error', freeSocketErrorListener);
        freeSockets.push(socket);
    }

    getName(options?: AgentConnectOptions): string {
        const o = options ?? {};
        let name = o.host || 'localhost';
        name += ':';
        if (o.port) name += o.port;
        name += ':';
        if (o.localAddress) name += o.localAddress;
        // Pacify parallel/test-http-agent-getname by only appending
        // the ':' when options.family is set.
        if (o.family === 4 || o.family === 6) name += ':' + o.family;
        if (o.socketPath) name += ':' + o.socketPath;
        return name;
    }

    addRequest(req: ClientRequest, options: AgentConnectOptions): void {
        const name = this.getName(options);
        if (this.sockets[name] === undefined) this.sockets[name] = [];
        const freeSockets = this.freeSockets[name];
        let socket: net.Socket | undefined = undefined;
        if (freeSockets !== undefined) {
            while (freeSockets.length > 0 && freeSockets[0].destroyed) freeSockets.shift();
            socket = this.scheduling === 'fifo' ? freeSockets.shift() : freeSockets.pop();
            if (freeSockets.length === 0) delete this.freeSockets[name];
        }
        const freeLen = freeSockets !== undefined ? freeSockets.length : 0;
        const sockLen = freeLen + this.sockets[name]!.length;
        if (socket !== undefined) {
            this.reuseSocket(socket, req);
            this.setRequestSocket(req, socket);
            this.sockets[name]!.push(socket);
        } else if (sockLen < this.maxSockets && this.totalSocketCount < this.maxTotalSockets) {
            // If we are under maxSockets create a new one.
            this.createSocket(req, options, (err: Error | null, s: net.Socket | null) => {
                if (err !== null || s === null) req.onSocket(s, err);
                else this.setRequestSocket(req, s);
            });
        } else {
            // We are over limit so we'll add it to the queue.
            if (this.requests[name] === undefined) this.requests[name] = [];
            req.agentOptions = options;
            this.requests[name]!.push(req);
        }
    }

    createConnection(options: AgentConnectOptions, cb?: (err: Error | null, socket: net.Socket) => void): net.Socket {
        if (options.socketPath !== undefined) return net.createConnection({ path: options.socketPath });
        return net.createConnection({ port: options.port, host: options.host, family: options.family,
            keepAlive: options.keepAlive, keepAliveInitialDelay: options.keepAliveInitialDelay, noDelay: options.noDelay });
    }

    createSocket(req: ClientRequest, options: AgentConnectOptions, cb: (err: Error | null, socket: net.Socket | null) => void): void {
        const name = this.getName(options);
        const opts: AgentConnectOptions = { host: options.host, port: options.port, localAddress: options.localAddress,
            family: options.family, socketPath: options.socketPath, servername: options.servername,
            rejectUnauthorized: options.rejectUnauthorized, ca: options.ca, cert: options.cert, key: options.key,
            timeout: options.timeout, noDelay: this.options.noDelay };
        // When keepAlive is true, pass the related options to createConnection
        if (this.keepAlive) {
            opts.keepAlive = this.keepAlive;
            opts.keepAliveInitialDelay = this.keepAliveMsecs;
        }
        const s = this.createConnection(opts);
        if (this.sockets[name] === undefined) this.sockets[name] = [];
        this.sockets[name]!.push(s);
        this.totalSocketCount++;
        this.installListeners(s, options);
        cb(null, s);
    }

    private installListeners(s: net.Socket, options: AgentConnectOptions): void {
        const onFree = () => { this.emit('free', s, options); };
        s.on('free', onFree);
        const onClose = () => {
            this.totalSocketCount--;
            this.removeSocket(s, options);
        };
        s.on('close', onClose);
        const onTimeout = () => {
            // Destroy if in free list.
            for (const name in this.freeSockets) {
                const list = this.freeSockets[name];
                if (list !== undefined && list.indexOf(s) >= 0) {
                    s.destroy();
                    return;
                }
            }
        };
        s.on('timeout', onTimeout);
        const onRemove = () => {
            // We need this function for cases like HTTP 'upgrade'
            // (defined by WebSockets) where we need to remove a socket from the
            // pool because it'll be locked up indefinitely
            this.totalSocketCount--;
            this.removeSocket(s, options);
            s.removeListener('close', onClose);
            s.removeListener('free', onFree);
            s.removeListener('timeout', onTimeout);
        };
        s.once('agentRemove', onRemove);
    }

    removeSocket(s: net.Socket, options: AgentConnectOptions): void {
        const name = this.getName(options);
        const remove = (sockets: { [key: string]: net.Socket[] }) => {
            const list = sockets[name];
            if (list !== undefined) {
                const index = list.indexOf(s);
                if (index !== -1) {
                    list.splice(index, 1);
                    // Don't leak
                    if (list.length === 0) delete sockets[name];
                }
            }
        };
        remove(this.sockets);
        // If the socket was destroyed, remove it from the free buffers too.
        if (!s.writable) remove(this.freeSockets);
        const pending = this.requests[name];
        if (pending !== undefined && pending.length > 0) {
            const req = pending[0];
            const reqOptions = req.agentOptions;
            if (reqOptions !== null) {
                req.agentOptions = null;
                // If we have pending requests and a socket gets closed make a new one
                this.createSocket(req, reqOptions, (err: Error | null, socket: net.Socket | null) => {
                    if (err !== null || socket === null) req.onSocket(socket, err);
                    else socket.emit('free');
                });
            }
        }
    }

    keepSocketAlive(socket: net.Socket): boolean {
        socket.setKeepAlive(true, this.keepAliveMsecs);
        socket.unref();
        const agentTimeout = this.options.timeout || 0;
        if (socket.timeout !== (agentTimeout > 0 ? agentTimeout : undefined)) socket.setTimeout(agentTimeout);
        return true;
    }

    reuseSocket(socket: net.Socket, req: ClientRequest): void {
        socket.removeListener('error', freeSocketErrorListener);
        req.reusedSocket = true;
        socket.ref();
    }

    private setRequestSocket(req: ClientRequest, socket: net.Socket): void {
        req.onSocket(socket, null);
        const agentTimeout = this.options.timeout || 0;
        if (req.timeout === undefined || req.timeout === agentTimeout) return;
        socket.setTimeout(req.timeout);
    }

    destroy(): void {
        for (const set of [this.freeSockets, this.sockets]) {
            for (const name in set) {
                const list = set[name];
                if (list !== undefined) for (const s of list.slice()) s.destroy();
            }
        }
    }
}

export const globalAgent = new Agent({ keepAlive: true, scheduling: 'lifo', timeout: 5000 });

// ---- lib/_http_client.js ----

export interface RequestOptions extends AgentConnectOptions {
    protocol?: string;
    hostname?: string;
    defaultPort?: number;
    method?: string;
    path?: string;
    headers?: OutgoingHttpHeaders;
    auth?: string;
    agent?: Agent | boolean;
    setHost?: boolean;
    maxHeaderSize?: number;
    joinDuplicateHeaders?: boolean;
    createConnection?: (options: AgentConnectOptions, oncreate: (err: Error | null, socket?: net.Socket) => void) => net.Socket | undefined | void;
}

interface ClientSocketListeners {
    onData: (d: Buffer) => void;
    onEnd: () => void;
    onClose: () => void;
    onError: (err: Error) => void;
    onDrain: () => void;
}

const INVALID_PATH_REGEX = /[^!-ÿ]/;

function connResetClient(msg: string): Error {
    const e = new HttpError('ECONNRESET', msg);
    return e;
}

function statusIsInformational(status: number): boolean {
    return status < 200 && status >= 100 && status !== 101;
}

export type ResponseListener = (res: IncomingMessage) => void;

export class ClientRequest extends OutgoingMessage {
    agent: Agent | null;
    method: string;
    path: string;
    host: string;
    protocol: string;
    port: number;
    timeout: number | undefined = undefined;
    aborted = false;
    res: IncomingMessage | null = null;
    reusedSocket = false;
    maxHeadersCount: number | null = null;
    agentOptions: AgentConnectOptions | null = null;
    _ended = false;
    upgradeOrConnect = false;
    private parser: HTTPParser | null = null;
    private timeoutCb: (() => void) | null = null;
    private maxHeaderSizeOpt: number;
    private error: Error | null = null;

    constructor(url: string | RequestOptions, options?: RequestOptions | ResponseListener, cb?: ResponseListener) {
        super();
        let opts: RequestOptions = {};
        let listener: ResponseListener | null = null;
        if (typeof url === 'string') {
            opts = urlToHttpOptions(new URL(url));
        } else {
            opts = url;
        }
        if (typeof options === 'function') {
            listener = options;
        } else if (options !== undefined) {
            opts = mergeRequestOptions(opts, options);
            if (cb !== undefined) listener = cb;
        } else if (cb !== undefined) {
            listener = cb;
        }
        let agent: Agent | null = null;
        const defaultAgent = opts.protocol === 'https:' || opts.defaultPort === 443 ? httpsGlobalAgent() : globalAgent;
        const a = opts.agent;
        if (a === false) agent = new Agent();
        else if (a === undefined || a === true) {
            // No agent when createConnection is given: it makes the socket.
            if (typeof opts.createConnection !== 'function') agent = defaultAgent;
        } else agent = a;
        const createConnection = opts.createConnection;
        this.agent = agent;
        const protocol = opts.protocol || defaultAgent.protocol;
        const expectedProtocol = agent !== null ? agent.protocol : defaultAgent.protocol;
        if (opts.path !== undefined && INVALID_PATH_REGEX.test(opts.path)) {
            throw new HttpTypeError('ERR_UNESCAPED_CHARACTERS', 'Request path contains unescaped characters');
        }
        if (protocol !== expectedProtocol) {
            throw new HttpTypeError('ERR_INVALID_PROTOCOL', 'Protocol "' + protocol + '" not supported. Expected "' + expectedProtocol + '"');
        }
        const defaultPort = opts.defaultPort || (agent !== null ? agent.defaultPort : 80);
        this.port = opts.port || defaultPort || 80;
        const host = opts.hostname || opts.host || 'localhost';
        this.host = host;
        const setHost = opts.setHost === undefined || opts.setHost;
        if (opts.timeout !== undefined) this.timeout = opts.timeout;
        let method = opts.method;
        if (method !== undefined && method !== '') {
            if (!checkIsHttpToken(method)) {
                throw new HttpTypeError('ERR_INVALID_HTTP_TOKEN', 'Method must be a valid HTTP token ["' + method + '"]');
            }
            method = method.toUpperCase();
        } else {
            method = 'GET';
        }
        this.method = method;
        this.maxHeaderSizeOpt = opts.maxHeaderSize ?? maxHeaderSize;
        this.path = opts.path || '/';
        if (listener !== null) this.once('response', listener);
        if (method === 'GET' || method === 'HEAD' || method === 'DELETE' || method === 'OPTIONS' ||
            method === 'TRACE' || method === 'CONNECT') {
            this.useChunkedEncodingByDefault = false;
        } else {
            this.useChunkedEncodingByDefault = true;
        }
        this.protocol = protocol;
        if (agent !== null) {
            // If there is an agent we should default to Connection:keep-alive,
            // but only if the Agent will actually reuse the connection!
            // If it's not a keepAlive agent, and the maxSockets==Infinity, then
            // there's never a case where this socket will actually be reused
            if (!agent.keepAlive && !Number.isFinite(agent.maxSockets)) {
                this._last = true;
                this.shouldKeepAlive = false;
            } else {
                this._last = false;
                this.shouldKeepAlive = true;
            }
        }
        this.agentKeepAlive = agent !== null && agent.keepAlive;
        const headers = opts.headers;
        if (headers !== undefined) {
            for (const key in headers) {
                const v = headers[key];
                if (v !== undefined) this.setHeader(key, v);
            }
        }
        if (host && !this.getHeader('host') && setHost) {
            let hostHeader = host;
            // For the Host header, ensure that IPv6 addresses are enclosed
            // in square brackets, as defined by URI formatting
            // https://tools.ietf.org/html/rfc3986#section-3.2.2
            const posColon = hostHeader.indexOf(':');
            if (posColon !== -1 && hostHeader.indexOf(':', posColon + 1) !== -1 && hostHeader.charCodeAt(0) !== 91) {
                hostHeader = '[' + hostHeader + ']';
            }
            if (this.port && this.port !== defaultPort) hostHeader += ':' + this.port;
            this.setHeader('Host', hostHeader);
        }
        if (opts.auth !== undefined && !this.getHeader('Authorization')) {
            this.setHeader('Authorization', 'Basic ' + Buffer.from(opts.auth).toString('base64'));
        }
        if (this.getHeader('expect')) {
            this._storeHeader(this.method + ' ' + this.path + ' HTTP/1.1\r\n', this.headerEntries());
        }
        // initiate connection
        const connectOpts: AgentConnectOptions = { host: host, port: this.port, localAddress: opts.localAddress,
            family: opts.family, socketPath: opts.socketPath, servername: opts.servername ?? (net.isIP(host) === 0 ? host : undefined),
            rejectUnauthorized: opts.rejectUnauthorized, ca: opts.ca, cert: opts.cert, key: opts.key, timeout: opts.timeout };
        if (agent !== null) {
            agent.addRequest(this, connectOpts);
        } else {
            // No agent: Connection: close, over createConnection's socket or a
            // new connection (_http_client.js).
            this._last = true;
            this.shouldKeepAlive = false;
            if (typeof createConnection === 'function') {
                let called = false;
                const oncreate = (err: Error | null, socket?: net.Socket): void => {
                    if (called) return;
                    called = true;
                    if (err !== null) {
                        process.nextTick(() => { this.emit('error', err); });
                    } else if (socket !== undefined) {
                        this.onSocket(socket, null);
                    }
                };
                try {
                    const newSocket = createConnection(connectOpts, oncreate);
                    if (newSocket) oncreate(null, newSocket);
                } catch (e) {
                    oncreate(e as Error);
                }
            } else {
                const sock = connectOpts.socketPath !== undefined
                    ? net.createConnection({ path: connectOpts.socketPath })
                    : net.createConnection({ port: connectOpts.port, host: connectOpts.host, family: connectOpts.family });
                this.onSocket(sock, null);
            }
        }
    }

    // The events, typed as @types/node declares them.
    on(event: 'response', listener: (res: IncomingMessage) => void): this;
    on(event: 'socket', listener: (socket: net.Socket) => void): this;
    on(event: 'upgrade', listener: (res: IncomingMessage, socket: net.Socket, head: Buffer) => void): this;
    on(event: 'connect', listener: (res: IncomingMessage, socket: net.Socket, head: Buffer) => void): this;
    on(event: 'information', listener: (info: InformationEvent) => void): this;
    on(event: 'error', listener: (err: Error) => void): this;
    on(event: string | symbol, listener: (...args: any[]) => void): this;
    on(event: string | symbol, listener: (...args: any[]) => void): this {
        return super.on(event, listener);
    }

    once(event: 'response', listener: (res: IncomingMessage) => void): this;
    once(event: 'socket', listener: (socket: net.Socket) => void): this;
    once(event: 'error', listener: (err: Error) => void): this;
    once(event: string | symbol, listener: (...args: any[]) => void): this;
    once(event: string | symbol, listener: (...args: any[]) => void): this {
        return super.once(event, listener);
    }

    _implicitHeader(): void {
        if (this._header) {
            throw new HttpError('ERR_HTTP_HEADERS_SENT', 'Cannot render headers after they are sent to the client');
        }
        this._storeHeader(this.method + ' ' + this.path + ' HTTP/1.1\r\n', this.headerEntries());
    }

    abort(): void {
        if (this.aborted) return;
        this.aborted = true;
        process.nextTick(() => { this.emit('abort'); });
        this.destroy();
    }

    destroy(err?: Error): this {
        if (this.destroyed) return this;
        this.destroyed = true;
        // If we're aborting, we don't care about any more response data.
        const res = this.res;
        if (res !== null) res._dump();
        this.error = err ?? null;
        const socket = this.socket;
        if (socket !== null) socket.destroy(err);
        return this;
    }

    setTimeout(msecs: number, callback?: () => void): this {
        if (callback) this.once('timeout', callback);
        const socket = this.socket;
        if (socket !== null) socket.setTimeout(msecs);
        else this.once('socket', (s: net.Socket) => { s.setTimeout(msecs); });
        this.timeout = msecs;
        return this;
    }

    setNoDelay(noDelay?: boolean): void {
        const socket = this.socket;
        if (socket !== null) socket.setNoDelay(noDelay);
        else this.once('socket', (s: net.Socket) => { s.setNoDelay(noDelay); });
    }

    setSocketKeepAlive(enable?: boolean, initialDelay?: number): void {
        const socket = this.socket;
        if (socket !== null) socket.setKeepAlive(enable, initialDelay);
        else this.once('socket', (s: net.Socket) => { s.setKeepAlive(enable, initialDelay); });
    }

    onSocket(socket: net.Socket | null, err: Error | null): void {
        process.nextTick(() => { this.onSocketNT(socket, err); });
    }

    private onSocketNT(socket: net.Socket | null, err: Error | null): void {
        if (this.destroyed || err !== null) {
            this.destroyed = true;
            const done = (e: Error | null) => {
                let ex = e;
                if (!this.aborted && ex === null) ex = connResetClient('socket hang up');
                if (ex !== null) this.emit('error', ex);
                this._closed = true;
                this.emit('close');
            };
            if (socket !== null) {
                if (err === null && this.agent !== null && !socket.destroyed) {
                    socket.emit('free');
                } else {
                    socket.destroy(err ?? this.error ?? undefined);
                    finished(socket, (er?: Error | null) => {
                        let e2: Error | null = er ?? null;
                        if (e2 !== null && (e2 as HttpError).code === 'ERR_STREAM_PREMATURE_CLOSE') e2 = null;
                        done(e2 ?? err);
                    });
                    return;
                }
            }
            done(err ?? this.error);
            return;
        }
        this.tickOnSocket(socket!);
        this._flush();
    }

    private tickOnSocket(socket: net.Socket): void {
        const parser = new HTTPParser(RESPONSE, this.maxHeaderSizeOpt);
        this.socket = socket;
        parser.socket = socket;
        parser.outgoing = this;
        this.parser = parser;
        const h = httpOf(socket);
        h.parser = parser;
        h.httpMessage = this;
        if (typeof this.maxHeadersCount === 'number') parser.maxHeaderPairs = this.maxHeadersCount << 1;
        parser.onIncoming = (res: IncomingMessage, keepAlive: boolean): number => this.parserOnIncomingClient(socket, res, keepAlive);
        const l: ClientSocketListeners = {
            onData: (d: Buffer) => { this.socketOnData(socket, d); },
            onEnd: () => { this.socketOnEnd(socket); },
            onClose: () => { this.socketCloseListener(socket); },
            onError: (e: Error) => { this.socketErrorListener(socket, e); },
            onDrain: () => {
                if (!this.finished && this._needDrain) {
                    this._needDrain = false;
                    this.emit('drain');
                }
            },
        };
        h.clientListeners = l;
        socket.on('error', l.onError);
        socket.on('data', l.onData);
        socket.on('end', l.onEnd);
        socket.on('close', l.onClose);
        socket.on('drain', l.onDrain);
        if (this.timeout !== undefined || (this.agent !== null && this.agent.options.timeout)) this.listenSocketTimeout();
        this.emit('socket', socket);
    }

    private listenSocketTimeout(): void {
        if (this.timeoutCb !== null) return;
        const cb = () => { this.emit('timeout'); };
        this.timeoutCb = cb;
        // Delegate socket timeout event.
        if (this.socket !== null) this.socket.once('timeout', cb);
        else this.on('socket', (s: net.Socket) => { s.once('timeout', cb); });
    }

    private detachListeners(socket: net.Socket): void {
        const l = httpOf(socket).clientListeners;
        if (l === null) return;
        socket.removeListener('data', l.onData);
        socket.removeListener('end', l.onEnd);
        socket.removeListener('drain', l.onDrain);
    }

    private socketCloseListener(socket: net.Socket): void {
        const parser = httpOf(socket).parser;
        const res = this.res;
        this.destroyed = true;
        if (res !== null) {
            // Socket closed before we emitted 'end' below.
            if (!res.complete) res.destroy(connResetClient('aborted'));
            this._closed = true;
            this.emit('close');
            if (!res.aborted && res.readable) res.push(null);
        } else {
            if (!socket._hadError) {
                // This socket error fired before we started to
                // receive a response. The error needs to
                // fire on the request.
                socket._hadError = true;
                this.emit('error', connResetClient('socket hang up'));
            }
            this._closed = true;
            this.emit('close');
        }
        this.outputData = [];
        if (parser !== null) {
            parser.finish();
            freeParser(parser, null, socket);
        }
    }

    private socketErrorListener(socket: net.Socket, err: Error): void {
        // For Safety. Some additional errors might fire later on
        // and we need to make sure we don't double-fire the error event.
        socket._hadError = true;
        this.emit('error', err);
        const parser = httpOf(socket).parser;
        if (parser !== null) {
            parser.finish();
            freeParser(parser, null, socket);
        }
        // Ensure that no further data will come out of the socket
        this.detachListeners(socket);
        socket.destroy();
    }

    private socketOnEnd(socket: net.Socket): void {
        if (this.res === null && !socket._hadError) {
            // If we don't have a response then we know that the socket
            // ended prematurely and we need to emit an error on the request.
            socket._hadError = true;
            this.emit('error', connResetClient('socket hang up'));
        }
        const parser = httpOf(socket).parser;
        if (parser !== null) {
            parser.finish();
            freeParser(parser, null, socket);
        }
        socket.destroy();
    }

    private socketOnData(socket: net.Socket, d: Buffer): void {
        const parser = httpOf(socket).parser;
        if (parser === null) return;
        const ret = parser.execute(d);
        if (ret instanceof Error) {
            const e: any = ret;
            e.rawPacket = d;
            freeParser(parser, null, socket);
            this.detachListeners(socket);
            socket.destroy();
            socket._hadError = true;
            this.emit('error', ret);
            return;
        }
        const incoming = parser.incoming;
        if (incoming !== null && incoming.upgrade) {
            // Upgrade (if status code 101) or CONNECT
            const res = incoming;
            this.res = res;
            this.detachListeners(socket);
            if (this.timeoutCb !== null) socket.removeListener('timeout', this.timeoutCb);
            parser.finish();
            freeParser(parser, null, socket);
            const bodyHead = d.subarray(ret, d.length);
            const eventName = this.method === 'CONNECT' ? 'connect' : 'upgrade';
            if (this.listenerCount(eventName) > 0) {
                this.upgradeOrConnect = true;
                // detach the socket
                socket.emit('agentRemove');
                const l = httpOf(socket).clientListeners;
                if (l !== null) {
                    socket.removeListener('close', l.onClose);
                    socket.removeListener('error', l.onError);
                }
                httpOf(socket).httpMessage = null;
                socket.readableFlowing = null;
                this.emit(eventName, res, socket, bodyHead);
                this.destroyed = true;
                this._closed = true;
                this.emit('close');
            } else {
                // Requested Upgrade or used CONNECT method, but have no handler.
                socket.destroy();
            }
        } else if (incoming !== null && incoming.complete &&
            // When the status code is informational (100, 102-199),
            // the server will send a final response after this client
            // sends a request body, so we must not free the parser.
            // 101 (Switching Protocols) and all other status codes
            // should be processed normally.
            !statusIsInformational(incoming.statusCode ?? 0)) {
            this.detachListeners(socket);
            freeParser(parser, null, socket);
        }
    }

    // parserOnIncomingClient
    private parserOnIncomingClient(socket: net.Socket, res: IncomingMessage, shouldKeepAlive: boolean): number {
        if (this.res !== null) {
            // We already have a response object, this means the server
            // sent a double response.
            socket.destroy();
            return 0; // No special treatment.
        }
        this.res = res;
        // Skip body and treat as Upgrade.
        if (res.upgrade) return 2;
        // Responses to CONNECT request is handled as Upgrade.
        const method = this.method;
        if (method === 'CONNECT') {
            res.upgrade = true;
            return 2; // Skip body and treat as Upgrade.
        }
        const status = res.statusCode ?? 0;
        if (statusIsInformational(status)) {
            // Restart the parser, as this is a 1xx informational message.
            this.res = null; // Clear res so that we don't hit double-responses.
            // Maintain compatibility by sending 100-specific events
            if (status === 100) this.emit('continue');
            // Send information events to all 1xx responses except 101 Upgrade.
            this.emit('information', {
                statusCode: status, statusMessage: res.statusMessage ?? '', httpVersion: res.httpVersion,
                httpVersionMajor: res.httpVersionMajor, httpVersionMinor: res.httpVersionMinor,
                headers: res.headers, rawHeaders: res.rawHeaders,
            });
            return 1; // Skip body but don't treat as Upgrade.
        }
        if (this.shouldKeepAlive && !shouldKeepAlive && !this.upgradeOrConnect) {
            // Server MUST respond with Connection:keep-alive for us to enable it.
            // If we've been upgraded (via WebSockets) we also shouldn't try to
            // keep the connection open.
            this.shouldKeepAlive = false;
        }
        res.req = this;
        // Add our listener first, so that we guarantee socket cleanup
        res.on('end', () => { this.responseOnEnd(); });
        this.on('finish', () => { this.requestOnFinish(); });
        socket.on('timeout', () => {
            const r = this.res;
            if (r !== null) r.emit('timeout');
        });
        // If the user did not listen for the 'response' event, then they
        // can't possibly read the data, so we ._dump() it into the void
        // so that the socket doesn't hang there in a paused state.
        if (this.aborted || !this.emit('response', res)) res._dump();
        if (method === 'HEAD') return 1; // Skip body but don't treat as Upgrade.
        if (status === 304) {
            res.complete = true;
            return 1; // Skip body as there won't be any
        }
        return 0; // No special treatment.
    }

    private responseKeepAlive(): void {
        const socket = this.socket;
        if (socket === null) return;
        if (this.timeoutCb !== null) {
            socket.setTimeout(0);
            socket.removeListener('timeout', this.timeoutCb);
            this.timeoutCb = null;
        }
        const l = httpOf(socket).clientListeners;
        if (l !== null) {
            socket.removeListener('close', l.onClose);
            socket.removeListener('error', l.onError);
            socket.removeListener('data', l.onData);
            socket.removeListener('end', l.onEnd);
        }
        // Mark this socket as available, AFTER user-added end handlers have
        // a chance to run.
        process.nextTick(() => {
            this._closed = true;
            this.emit('close');
            socket.emit('free');
        });
        this.destroyed = true;
        const res = this.res;
        if (res !== null) {
            // Detach socket from IncomingMessage to avoid destroying the freed
            // socket in IncomingMessage.destroy().
            res.detachedFromSocket = true;
        }
    }

    private responseOnEnd(): void {
        const socket = this.socket;
        if (socket !== null && this.timeoutCb !== null) socket.removeListener('timeout', this.timeoutCb);
        this._ended = true;
        if (!this.shouldKeepAlive) {
            if (socket !== null && socket.writable) socket.destroySoon();
        } else if (this.writableFinished && !this.aborted) {
            // We can assume `req.finished` means all data has been written since:
            // - `'responseOnEnd'` means we have been assigned a socket.
            // - when we have a socket we write directly to it without buffering.
            // - `req.finished` means `end()` has been called and no further data.
            //   can be written
            // In addition, `req.writableFinished` means all data has been flushed
            this.responseKeepAlive();
        }
    }

    // This function is necessary in the case where we receive the entire response
    // from the server before we finish sending out the request.
    private requestOnFinish(): void {
        if (this.shouldKeepAlive && this._ended) this.responseKeepAlive();
    }
}

export interface InformationEvent {
    statusCode: number;
    statusMessage: string;
    httpVersion: string;
    httpVersionMajor: number;
    httpVersionMinor: number;
    headers: IncomingHttpHeaders;
    rawHeaders: string[];
}

// url.urlToHttpOptions
function urlToHttpOptions(url: URL): RequestOptions {
    const hostname = url.hostname.startsWith('[') ? url.hostname.slice(1, -1) : url.hostname;
    const options: RequestOptions = {
        protocol: url.protocol,
        hostname: hostname,
        path: url.pathname + url.search,
    };
    if (url.port !== '') options.port = Number(url.port);
    if (url.username || url.password) {
        options.auth = decodeURIComponent(url.username) + ':' + decodeURIComponent(url.password);
    }
    return options;
}

function mergeRequestOptions(base: RequestOptions, o: RequestOptions): RequestOptions {
    const out: RequestOptions = {
        protocol: o.protocol ?? base.protocol, hostname: o.hostname ?? base.hostname, host: o.host ?? base.host,
        port: o.port ?? base.port, path: o.path ?? base.path, method: o.method ?? base.method,
        headers: o.headers ?? base.headers, auth: o.auth ?? base.auth, agent: o.agent ?? base.agent,
        defaultPort: o.defaultPort ?? base.defaultPort, family: o.family ?? base.family,
        localAddress: o.localAddress ?? base.localAddress, socketPath: o.socketPath ?? base.socketPath,
        setHost: o.setHost ?? base.setHost, timeout: o.timeout ?? base.timeout,
        servername: o.servername ?? base.servername, rejectUnauthorized: o.rejectUnauthorized ?? base.rejectUnauthorized,
        ca: o.ca ?? base.ca, cert: o.cert ?? base.cert, key: o.key ?? base.key,
        maxHeaderSize: o.maxHeaderSize ?? base.maxHeaderSize, joinDuplicateHeaders: o.joinDuplicateHeaders ?? base.joinDuplicateHeaders,
        createConnection: o.createConnection ?? base.createConnection,
    };
    return out;
}

// The https module's global agent, set by it (an Agent over tls.connect).
let httpsAgent: Agent | null = null;

export function _kmlSetHttpsGlobalAgent(agent: Agent): void {
    httpsAgent = agent;
}

function httpsGlobalAgent(): Agent {
    return httpsAgent ?? globalAgent;
}

export function request(url: string | RequestOptions, options?: RequestOptions | ResponseListener, cb?: ResponseListener): ClientRequest {
    return new ClientRequest(url, options, cb);
}

export function get(url: string | RequestOptions, options?: RequestOptions | ResponseListener, cb?: ResponseListener): ClientRequest {
    const req = request(url, options, cb);
    req.end();
    return req;
}
