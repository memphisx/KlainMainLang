// klain:http — this project's own HTTP server (TDD-00255): a handler that
// takes a request and returns its response, instead of Node's (req, res).
//
//	import http from 'klain:http'
//	http.listen(8080, (req: HttpRequest) => ({ status: 200, body: 'hi ' + req.path }))
//
// listen blocks until close(): top-level code goes on once the server has
// closed and the loop has nothing else. The connections, the protocol and
// the request's body are the backend's (./internal_khttp_backend, chosen at
// build time); this module runs the handler and writes back its result, or
// the value its promise settles to.
import * as backend from './internal_khttp_backend';

export interface ListenOptions {
    workers?: number;
    host?: string;
    // Called once the server is bound, with its port: the one the system
    // chose when port is 0. In the primary only, before workers fork.
    onListening?: (port: number) => void;
}

export interface HttpResponse {
    status: number;
    body: string | ReadableStream<string> | ReadableStream<Uint8Array>;
    bodyBytes?: ArrayBuffer;
    headers?: Map<string, string>;
}

let handler: ((req: HttpRequest) => HttpResponse | Promise<HttpResponse>) | null = null;
let listening = false;
let clusterTimer: any = null;

function headerPairs(res: HttpResponse): string[] {
    const out: string[] = [];
    if (res.headers !== undefined && res.headers !== null) {
        res.headers.forEach((v: string, k: string) => { out.push(k, v); });
    }
    return out;
}

function respond(ex: backend.Exchange, res: HttpResponse): void {
    const headers = headerPairs(res);
    if (res.bodyBytes !== undefined && res.bodyBytes !== null) {
        backend.respond(ex, res.status, headers, new Uint8Array(res.bodyBytes));
        return;
    }
    const body: any = res.body;
    if (body === undefined || body === null || typeof body === 'string') {
        backend.respond(ex, res.status, headers, Buffer.from(body === undefined || body === null ? '' : (body as string), 'utf8'));
        return;
    }
    // A ReadableStream body: each chunk written once the one before is out.
    backend.respondStart(ex, res.status, headers);
    const reader = (body as ReadableStream<any>).getReader();
    const next = (): void => {
        reader.read().then((r: ReadableStreamReadResult<any>) => {
            if (r.done) {
                backend.respondEnd(ex);
                return;
            }
            const v: any = r.value;
            const bytes: Uint8Array = typeof v === 'string' ? Buffer.from(v, 'utf8') : v;
            if (bytes.length === 0) {
                next();
                return;
            }
            backend.respondWrite(ex, bytes, (ok: boolean) => {
                if (ok) next();
                else reader.cancel();
            });
        }, (e: any) => {
            backend.abort(ex);
        });
    };
    next();
}

function onRequest(ex: backend.Exchange, req: HttpRequest): void {
    const out: any = handler!(req);
    if (out instanceof Promise) {
        (out as Promise<HttpResponse>).then((res: HttpResponse) => { respond(ex, res); });
    } else {
        respond(ex, out as HttpResponse);
    }
}

function closeLocal(): void {
    if (!listening) return;
    listening = false;
    backend.close();
    if (clusterTimer !== null) {
        clearInterval(clusterTimer);
        clusterTimer = null;
    }
}

// Serve handler on port (0 for one the system picks; on every address unless
// opts.host names one) until close(); returns once the server has closed and
// nothing else holds the loop.
export function listen(port: number, fn: (req: HttpRequest) => HttpResponse | Promise<HttpResponse>, opts?: ListenOptions): void {
    if (listening) throw new Error('klain:http is already listening');
    handler = fn;
    let host = opts !== undefined && opts.host !== undefined ? opts.host : '';
    if (host === 'localhost') host = '127.0.0.1';
    const r = backend.serve(host, port, onRequest);
    if (r < 0) {
        const e: any = new Error('listen ' + errnoName(-r) + ' ' + (host === '' ? '0.0.0.0' : host) + ':' + port);
        e.code = errnoName(-r);
        e.errno = r;
        e.syscall = 'listen';
        throw e;
    }
    listening = true;
    if (opts !== undefined && opts.onListening !== undefined) opts.onListening(r);
    // `workers`: the primary forks after the bind, each process serving
    // the shared socket.
    __kml_native.khttpFork(opts !== undefined && opts.workers !== undefined ? opts.workers : 1);
    if (__kml_native.khttpClustered() !== 0) {
        // A sibling's close() or closeAllConnections() reaches this process
        // through the shared flags.
        clusterTimer = setInterval(() => {
            if (__kml_native.khttpFlag(2) !== 0) process.exit(0);
            if (__kml_native.khttpFlag(1) !== 0) backend.closeAll();
            if (__kml_native.khttpFlag(0) !== 0) closeLocal();
        }, 200);
        clusterTimer.unref();
    }
    __kml_native.runLoop();
}

// Stop accepting: connections in flight finish, idle ones close.
export function close(): void {
    __kml_native.khttpSetFlag(0);
    closeLocal();
}

// Close every connection now, answered or not.
export function closeAllConnections(): void {
    __kml_native.khttpSetFlag(1);
    backend.closeAll();
}

function errnoName(n: number): string {
    const names: { [n: number]: string } = { 13: 'EACCES', 48: 'EADDRINUSE', 98: 'EADDRINUSE', 49: 'EADDRNOTAVAIL', 99: 'EADDRNOTAVAIL', 22: 'EINVAL' };
    const s = names[n];
    return s === undefined ? 'E' + n : s;
}

export default { listen, close, closeAllConnections };
