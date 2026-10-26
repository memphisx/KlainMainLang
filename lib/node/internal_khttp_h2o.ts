// klain:http's `h2o` backend (TDD-00255 Stage 2): libh2o on the program's
// event loop (codegen/llvm/h2osrc/klainh2o.c), which parses HTTP/1.1 and
// HTTP/2 (h2c by prior knowledge or upgrade) and frames the responses.
// klain_http.ts reaches it as ./internal_khttp_backend under
// -http-backend=h2o; it exports the same functions as the native backend.
//
// libh2o 0.13 buffers a request's whole body before the handler runs, so a
// body past the cap runs the handler once complete: stream() yields all of
// it, and req.body throws as on the native backend.

// A body reaching this runs the handler in streaming state.
const BODY_CAP = 10 * 1024 * 1024;

// The server's side of a request (kml_http_request.ts): its body arriving.
interface RequestSink {
    _push(chunk: Uint8Array): boolean;
    _end(): void;
    _setStreaming(): void;
}

function sink(req: HttpRequest): RequestSink {
    return req as unknown as RequestSink;
}

// One request and its response, by h2o's exchange id.
export class Exchange {
    id: number;
    req: HttpRequest;

    constructor(id: number, req: HttpRequest) {
        this.id = id;
        this.req = req;
    }
}

let listening = false;

function headerText(headers: string[]): string {
    let s = '';
    for (let i = 0; i + 1 < headers.length; i += 2) s += headers[i] + '\n' + headers[i + 1] + '\n';
    return s;
}

function onExchange(id: number, onRequest: (ex: Exchange, req: HttpRequest) => void): void {
    const text = __kml_native.khttpH2oReqString(id, 2);
    const headers = new Map<string, string>();
    const parts = text.split('\n');
    for (let i = 0; i + 1 < parts.length; i += 2) {
        // Repeated fields join as Node joins them.
        const prev = headers.get(parts[i]);
        headers.set(parts[i], prev === undefined ? parts[i + 1] : prev + ', ' + parts[i + 1]);
    }
    const req = __kml_native.khttpH2oRequest(id);
    req._adopt(() => {}, __kml_native.khttpH2oReqString(id, 3), headers);
    const n = __kml_native.khttpH2oBodyLength(id);
    if (n > 0) {
        const body = Buffer.alloc(n);
        __kml_native.khttpH2oBody(id, body);
        if (n >= BODY_CAP) sink(req)._setStreaming();
        sink(req)._push(body);
    }
    sink(req)._end();
    onRequest(new Exchange(id, req), req);
}

// Listen on host:port (every address for ""): 0, or the negative errno.
export function serve(host: string, port: number, fn: (ex: Exchange, req: HttpRequest) => void): number {
    const r = __kml_native.khttpH2oServe(host, port, (id: number, unused: number) => { onExchange(id, fn); });
    if (r < 0) return r;
    listening = true;
    return r;
}

// The whole response. headers are name, value pairs.
export function respond(ex: Exchange, status: number, headers: string[], body: Uint8Array): void {
    __kml_native.khttpH2oRespond(ex.id, status, headerText(headers), body);
}

// A streamed response: its head, then each chunk (done once h2o took it,
// the backpressure signal), then its end.
export function respondStart(ex: Exchange, status: number, headers: string[]): void {
    __kml_native.khttpH2oRespondStart(ex.id, status, headerText(headers));
}

export function respondWrite(ex: Exchange, chunk: Uint8Array, done: (ok: boolean) => void): void {
    __kml_native.khttpH2oRespondWrite(ex.id, chunk, (status: number, unused: number) => { done(status === 0); });
}

export function respondEnd(ex: Exchange): void {
    __kml_native.khttpH2oRespondEnd(ex.id);
}

// The exchange failed mid-response: h2o cuts it off.
export function abort(ex: Exchange): void {
    __kml_native.khttpH2oAbort(ex.id);
}

// Stop accepting: connections in flight finish, idle ones close.
export function close(): void {
    if (!listening) return;
    listening = false;
    __kml_native.khttpH2oClose();
}

// Close every connection now, answered or not.
export function closeAll(): void {
    __kml_native.khttpH2oCloseAll();
}
