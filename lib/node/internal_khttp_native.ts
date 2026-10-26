// klain:http's `native` backend (TDD-00255): TCP handles on the event loop,
// the HTTP/1 parser natives (one parser per connection), and HTTP/2 with
// prior knowledge (h2c) over the nghttp2 natives. klain_http.ts reaches it
// as ./internal_khttp_backend; another backend exports the same functions.
//
// A request goes to onRequest once its body is complete, or once the
// buffered part reaches the cap (the handler then reads the rest with
// stream()). Its response goes back through respond (whole) or
// respondStart/respondWrite/respondEnd (streamed).

// A body reaching this runs the handler in streaming state.
const BODY_CAP = 10 * 1024 * 1024;
const MAX_HEADER = 16 * 1024;

// The HTTP/2 connection preface a prior-knowledge client opens with.
const PREFACE = 'PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n';

const NGHTTP2_SESSION_SERVER = 0;
const NGHTTP2_FLAG_END_STREAM = 0x1;
const H2E_HEADERS = 1;
const H2E_DATA = 2;
const H2E_STREAM_END = 3;
const H2E_STREAM_CLOSE = 4;
const H2E_GOAWAY = 7;
const H2E_SESSION_ERROR = 13;

const REASONS: { [status: number]: string } = {
    100: 'Continue', 101: 'Switching Protocols', 200: 'OK', 201: 'Created', 202: 'Accepted',
    204: 'No Content', 206: 'Partial Content', 301: 'Moved Permanently', 302: 'Found',
    303: 'See Other', 304: 'Not Modified', 307: 'Temporary Redirect', 308: 'Permanent Redirect',
    400: 'Bad Request', 401: 'Unauthorized', 403: 'Forbidden', 404: 'Not Found',
    405: 'Method Not Allowed', 409: 'Conflict', 410: 'Gone', 413: 'Payload Too Large',
    415: 'Unsupported Media Type', 418: "I'm a Teapot", 422: 'Unprocessable Entity',
    429: 'Too Many Requests', 431: 'Request Header Fields Too Large',
    500: 'Internal Server Error', 501: 'Not Implemented', 502: 'Bad Gateway',
    503: 'Service Unavailable', 504: 'Gateway Timeout',
};

function reason(status: number): string {
    const r = REASONS[status];
    return r === undefined ? 'OK' : r;
}

// The server's side of a request (kml_http_request.ts): its body arriving.
interface RequestSink {
    _push(chunk: Uint8Array): boolean;
    _end(): void;
    _bufferedLength(): number;
    _setStreaming(): void;
}

function sink(req: HttpRequest): RequestSink {
    return req as unknown as RequestSink;
}

// One request and its response: on an HTTP/1 connection, or a stream of an
// HTTP/2 session.
export class Exchange {
    conn: Conn;
    req: HttpRequest;
    stream: number; // the HTTP/2 stream, 0 on HTTP/1
    started = false; // onRequest has run

    constructor(conn: Conn, req: HttpRequest, stream: number) {
        this.conn = conn;
        this.req = req;
        this.stream = stream;
    }
}

// One accepted connection. On HTTP/1 its requests are answered in order (a
// pipelined one waits for the response before it); on HTTP/2 each stream
// is its own exchange.
class Conn {
    handle: number;
    parser = -1;
    session = -1;            // the nghttp2 session, once the preface came
    sniff: Uint8Array | null = null; // bytes read before the protocol is known
    ex: Exchange | null = null;      // HTTP/1: the request being read or answered
    keepAlive = true;
    busy = false;            // HTTP/1: a response is being written
    closed = false;
    paused = false;
    pending: Uint8Array[] = [];       // HTTP/1: bytes read while a response was in flight
    streams = new Map<number, Exchange>(); // HTTP/2

    constructor(handle: number) {
        this.handle = handle;
    }

    resume(): void {
        if (this.paused && !this.closed) {
            this.paused = false;
            __kml_native.tcpReadStart(this.handle, (status: number, nread: number) => { onRead(this, status, nread); });
        }
    }

    pause(): void {
        if (!this.paused && !this.closed) {
            this.paused = true;
            __kml_native.tcpReadStop(this.handle);
        }
    }

    idle(): boolean {
        return this.session >= 0 ? this.streams.size === 0 : !this.busy && this.ex === null;
    }
}

let onRequest: ((ex: Exchange, req: HttpRequest) => void) | null = null;
let server = -1;
let listening = false;
const conns = new Set<Conn>();

function closeConn(c: Conn): void {
    if (c.closed) return;
    c.closed = true;
    conns.delete(c);
    if (c.session >= 0) {
        __kml_native.h2Free(c.session);
        c.session = -1;
    }
    __kml_native.tcpClose(c.handle, (s: number, u: number) => {});
}

function write(c: Conn, data: Uint8Array, done: ((status: number) => void) | null): void {
    if (c.closed) {
        if (done !== null) done(-1);
        return;
    }
    const r = __kml_native.tcpWrite(c.handle, data, 0, data.length, (status: number, unused: number) => {
        if (done !== null) done(status);
    });
    if (r === 1 && done !== null) done(0);
    else if (r < 0 && done !== null) done(r);
}

// The request is complete, or its buffered body reached the cap.
function start(ex: Exchange): void {
    if (ex.started) return;
    ex.started = true;
    if (ex.stream === 0) ex.conn.busy = true;
    onRequest!(ex, ex.req);
}

function bodyChunk(ex: Exchange, bytes: Uint8Array): void {
    if (!sink(ex.req)._push(bytes) && ex.stream === 0) ex.conn.pause();
    if (!ex.started && sink(ex.req)._bufferedLength() >= BODY_CAP) {
        sink(ex.req)._setStreaming();
        start(ex);
    }
}

function bodyEnd(ex: Exchange): void {
    sink(ex.req)._end();
    start(ex);
}

// ---- HTTP/1 ---------------------------------------------------------------

function closes(headers: string[]): boolean {
    for (let i = 0; i + 1 < headers.length; i += 2) {
        if (headers[i].toLowerCase() === 'connection' && headers[i + 1].toLowerCase() === 'close') return true;
    }
    return false;
}

function head1(c: Conn, status: number, headers: string[], length: number): Uint8Array {
    if (!listening || closes(headers)) c.keepAlive = false;
    let h = 'HTTP/1.1 ' + status + ' ' + reason(status) + '\r\nDate: ' + new Date().toUTCString() + '\r\n';
    h += length >= 0 ? 'Content-Length: ' + length + '\r\n' : 'Transfer-Encoding: chunked\r\n';
    h += c.keepAlive ? 'Connection: keep-alive\r\nKeep-Alive: timeout=5\r\n' : 'Connection: close\r\n';
    for (let i = 0; i + 1 < headers.length; i += 2) h += headers[i] + ': ' + headers[i + 1] + '\r\n';
    return Buffer.from(h + '\r\n', 'latin1');
}

// The response is out: the next request on the connection, or its close.
function finished1(c: Conn): void {
    c.ex = null;
    if (!c.keepAlive || !listening) {
        // Nothing more is read on it.
        __kml_native.tcpShutdown(c.handle, (s: number, u: number) => { closeConn(c); });
        return;
    }
    c.busy = false;
    const more = c.pending;
    c.pending = [];
    // In order, until a request waits on its response again (feed1 queued
    // the rest of its bytes); a streaming body's bytes go on to the parser.
    for (let i = 0; i < more.length; i++) {
        feed1(c, more[i]);
        if (c.closed) return;
        if (c.pending.length > 0) {
            c.pending = c.pending.concat(more.slice(i + 1));
            return;
        }
    }
}

// A repeated field joins the earlier value, as Node joins them.
function addHeader(headers: Map<string, string>, k: string, v: string): void {
    const prev = headers.get(k);
    headers.set(k, prev === undefined ? v : prev + ', ' + v);
}

function headerList(p: number): string[] {
    const n = __kml_native.httpParserInfo(p, 5);
    const out: string[] = [];
    for (let i = 0; i < n; i++) out.push(__kml_native.httpParserHeader(p, i));
    return out;
}

function feed1(c: Conn, data: Uint8Array): void {
    let off = 0;
    while (!c.closed) {
        // A pipelined request waits for the response before it.
        if (c.busy && c.ex === null) {
            c.pending.push(data.subarray(off));
            return;
        }
        const ev = __kml_native.httpParserExecute(c.parser, data, off, data.length - off);
        off += __kml_native.httpParserInfo(c.parser, 6);
        if (ev === 0) break;
        if (ev === 1) {
            const list = headerList(c.parser);
            const headers = new Map<string, string>();
            let expect = false;
            for (let i = 0; i + 1 < list.length; i += 2) {
                const k = list[i].toLowerCase();
                const v = list[i + 1].trim();
                addHeader(headers, k, v);
                if (k === 'expect' && v.toLowerCase() === '100-continue') expect = true;
            }
            c.keepAlive = __kml_native.httpParserInfo(c.parser, 4) === 1;
            const method = __kml_native.httpParserString(c.parser, 0);
            const url = __kml_native.httpParserString(c.parser, 1);
            c.ex = new Exchange(c, new HttpRequest(() => { c.resume(); }, method, url, headers), 0);
            if (expect) write(c, Buffer.from('HTTP/1.1 100 Continue\r\n\r\n', 'latin1'), null);
            // An upgrade nothing here takes goes on as HTTP.
            if (__kml_native.httpParserInfo(c.parser, 3) === 1) __kml_native.httpParserSet(c.parser, 2);
        } else if (ev === 2) {
            const start8 = __kml_native.httpParserInfo(c.parser, 7);
            const len = __kml_native.httpParserInfo(c.parser, 8);
            if (c.ex !== null) bodyChunk(c.ex, Buffer.from(data.subarray(start8, start8 + len)));
        } else if (ev === 3) {
            const ex = c.ex;
            if (ex === null) continue;
            bodyEnd(ex);
            // Reading goes on with the next request once this one's
            // response is out.
            if (c.busy && c.ex === ex) {
                c.pending.push(data.subarray(off));
                return;
            }
        } else if (ev === 4) {
            break;
        } else {
            // A parse error: 400, or 431 for headers past the limit, then close.
            const code = __kml_native.httpParserString(c.parser, 3);
            const status = code === 'HPE_HEADER_OVERFLOW' ? 431 : 400;
            c.keepAlive = false;
            c.busy = true;
            write(c, Buffer.from('HTTP/1.1 ' + status + ' ' + reason(status) + '\r\nConnection: close\r\n\r\n', 'latin1'), (s: number) => { closeConn(c); });
            return;
        }
    }
}

// ---- HTTP/2 (h2c, prior knowledge) -----------------------------------------

function flush2(c: Conn, done: ((status: number) => void) | null): void {
    if (c.closed || c.session < 0) {
        if (done !== null) done(-1);
        return;
    }
    const n = __kml_native.h2Flush(c.session);
    if (n > 0) {
        const out = Buffer.alloc(n);
        __kml_native.h2Take(c.session, out);
        write(c, out, done);
    } else if (done !== null) {
        done(0);
    }
    events2(c);
}

function events2(c: Conn): void {
    const h = c.session;
    while (!c.closed && c.session === h) {
        const type = __kml_native.h2Next(h);
        if (type === 0) break;
        const a = __kml_native.h2Event(h, 0);
        const b = __kml_native.h2Event(h, 1);
        const flags = __kml_native.h2Event(h, 2);
        if (type === H2E_HEADERS) {
            const text = __kml_native.lastString();
            if (c.streams.has(a) || !listening) continue; // trailers, or a stream past close
            const parts = text.split('\n');
            parts.pop();
            let method = 'GET';
            let path = '/';
            const headers = new Map<string, string>();
            for (let i = 0; i + 1 < parts.length; i += 2) {
                const k = parts[i];
                if (k === ':method') method = parts[i + 1];
                else if (k === ':path') path = parts[i + 1];
                else if (k === ':authority') headers.set('host', parts[i + 1]);
                else if (k.charAt(0) !== ':') addHeader(headers, k, parts[i + 1]);
            }
            const ex = new Exchange(c, new HttpRequest(() => {}, method, path, headers), a);
            c.streams.set(a, ex);
            if ((flags & NGHTTP2_FLAG_END_STREAM) !== 0) bodyEnd(ex);
        } else if (type === H2E_DATA) {
            const bytes = Buffer.alloc(__kml_native.h2Event(h, 3));
            __kml_native.h2EventBytes(h, bytes);
            const ex = c.streams.get(a);
            if (ex !== undefined) bodyChunk(ex, bytes);
        } else if (type === H2E_STREAM_END) {
            const ex = c.streams.get(a);
            if (ex !== undefined && !ex.started) bodyEnd(ex);
        } else if (type === H2E_STREAM_CLOSE) {
            c.streams.delete(a);
            if (!listening && c.streams.size === 0) closeConn(c);
        } else if (type === H2E_GOAWAY || type === H2E_SESSION_ERROR) {
            closeConn(c);
        }
    }
}

function feed2(c: Conn, data: Uint8Array): void {
    const n = __kml_native.h2Feed(c.session, data, 0, data.length);
    events2(c);
    if (n < 0) {
        closeConn(c);
        return;
    }
    flush2(c, null);
}

function headers2(status: number, headers: string[], length: number): string {
    let s = ':status\n' + status + '\n';
    for (let i = 0; i + 1 < headers.length; i += 2) {
        const k = headers[i].toLowerCase();
        // Connection-specific fields have no place in HTTP/2 (RFC 9113 8.2.2).
        if (k === 'connection' || k === 'keep-alive' || k === 'transfer-encoding' || k === 'upgrade') continue;
        s += k + '\n' + headers[i + 1] + '\n';
    }
    if (length >= 0) s += 'content-length\n' + length + '\n';
    return s;
}

// ---- the connection ----------------------------------------------------------

// The first bytes decide the protocol: the HTTP/2 preface, or HTTP/1.
function feed(c: Conn, data: Uint8Array): void {
    if (c.session >= 0) {
        feed2(c, data);
        return;
    }
    if (c.parser >= 0) {
        // Bytes queued behind a response go first; the body of a request
        // being answered (one past the cap, streaming) goes on to the parser.
        if (c.pending.length > 0) c.pending.push(data);
        else feed1(c, data);
        return;
    }
    const all = c.sniff === null ? data : Buffer.concat([c.sniff, data]);
    const n = Math.min(all.length, PREFACE.length);
    let h2 = true;
    for (let i = 0; i < n; i++) {
        if (all[i] !== PREFACE.charCodeAt(i)) {
            h2 = false;
            break;
        }
    }
    if (h2 && all.length < PREFACE.length) {
        c.sniff = all;
        return;
    }
    c.sniff = null;
    if (h2) {
        c.session = __kml_native.h2New(NGHTTP2_SESSION_SERVER, -1, -1, -1);
        __kml_native.h2Settings(c.session, new Uint8Array(0));
        feed2(c, all);
    } else {
        c.parser = __kml_native.httpParserNew(0, MAX_HEADER);
        feed1(c, all);
    }
}

function onRead(c: Conn, status: number, nread: number): void {
    if (c.closed) return;
    if (status !== 0 || nread < 0) {
        // The client's end (or an error): a request still being answered
        // finishes first.
        if (c.idle()) closeConn(c);
        else c.keepAlive = false;
        return;
    }
    const buf = Buffer.alloc(nread);
    __kml_native.tcpTake(c.handle, buf);
    feed(c, buf);
}

function onConnection(status: number, handle: number): void {
    if (status !== 0 || handle < 0) return;
    if (!listening) {
        __kml_native.tcpClose(handle, (s: number, u: number) => {});
        return;
    }
    const c = new Conn(handle);
    conns.add(c);
    __kml_native.tcpSetNoDelay(handle, true);
    __kml_native.tcpReadStart(handle, (st: number, nread: number) => { onRead(c, st, nread); });
}

// ---- the backend interface ---------------------------------------------------

// Listen on host:port (every address for ""): 0, or the negative errno.
export function serve(host: string, port: number, fn: (ex: Exchange, req: HttpRequest) => void): number {
    onRequest = fn;
    const id = __kml_native.tcpListen(host, port, 511, (status: number, handle: number) => { onConnection(status, handle); });
    if (id < 0) return id;
    server = id;
    listening = true;
    const a = __kml_native.tcpAddress(id, false);
    return a < 0 ? port : a % 65536;
}

// The whole response. headers are name, value pairs.
export function respond(ex: Exchange, status: number, headers: string[], body: Uint8Array): void {
    const c = ex.conn;
    if (c.closed) return;
    if (ex.stream !== 0) {
        if (c.session < 0) return;
        __kml_native.h2Respond(c.session, ex.stream, headers2(status, headers, body.length), body.length === 0 ? 1 : 0, 0);
        if (body.length > 0) __kml_native.h2Write(c.session, ex.stream, body, 0, body.length, 1);
        flush2(c, null);
        return;
    }
    const head = head1(c, status, headers, body.length);
    if (body.length < 16384) {
        write(c, body.length === 0 ? head : Buffer.concat([head, body]), (s: number) => { finished1(c); });
    } else {
        write(c, head, null);
        write(c, body, (s: number) => { finished1(c); });
    }
}

// A streamed response: its head, then each chunk (done once it is out, the
// backpressure signal), then its end.
export function respondStart(ex: Exchange, status: number, headers: string[]): void {
    const c = ex.conn;
    if (c.closed) return;
    if (ex.stream !== 0) {
        if (c.session < 0) return;
        __kml_native.h2Respond(c.session, ex.stream, headers2(status, headers, -1), 0, 0);
        flush2(c, null);
        return;
    }
    write(c, head1(c, status, headers, -1), null);
}

export function respondWrite(ex: Exchange, chunk: Uint8Array, done: (ok: boolean) => void): void {
    const c = ex.conn;
    if (c.closed) {
        done(false);
        return;
    }
    if (ex.stream !== 0) {
        if (c.session < 0) {
            done(false);
            return;
        }
        __kml_native.h2Write(c.session, ex.stream, chunk, 0, chunk.length, 0);
        flush2(c, (s: number) => { done(s === 0); });
        return;
    }
    const frame = Buffer.concat([Buffer.from(chunk.length.toString(16) + '\r\n', 'latin1'), chunk, Buffer.from('\r\n', 'latin1')]);
    write(c, frame, (s: number) => { done(s === 0); });
}

export function respondEnd(ex: Exchange): void {
    const c = ex.conn;
    if (c.closed) return;
    if (ex.stream !== 0) {
        if (c.session < 0) return;
        __kml_native.h2Write(c.session, ex.stream, new Uint8Array(0), 0, 0, 1);
        flush2(c, null);
        return;
    }
    write(c, Buffer.from('0\r\n\r\n', 'latin1'), (s: number) => { finished1(c); });
}

// The exchange failed mid-response: drop its connection (HTTP/1) or reset
// its stream (HTTP/2).
export function abort(ex: Exchange): void {
    const c = ex.conn;
    if (ex.stream !== 0 && c.session >= 0) {
        __kml_native.h2Rst(c.session, ex.stream, 2);
        flush2(c, null);
        return;
    }
    closeConn(c);
}

// Stop accepting: connections in flight finish, idle ones close.
export function close(): void {
    if (!listening) return;
    listening = false;
    if (server >= 0) {
        __kml_native.tcpClose(server, (s: number, u: number) => {});
        server = -1;
    }
    for (const c of Array.from(conns)) {
        if (c.idle()) closeConn(c);
    }
}

// Close every connection now, answered or not.
export function closeAll(): void {
    for (const c of Array.from(conns)) closeConn(c);
}
