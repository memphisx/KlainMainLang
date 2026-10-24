// kml:global
// HttpRequest — klain:http's request (TDD-00255): what a handler passed to
// klain:http's listen receives. The body arrives through _push/_end from the
// server's connection; a body past the buffer cap is read with stream(), as
// is a Node Readable view of it (on, pipe, for await).
import { Readable } from 'stream';

// A body larger than this runs the handler in streaming state: stream()
// reads it, bodyBytes() throws.
const BODY_CAP = 10 * 1024 * 1024;
const CHUNK = 65536;

function decode(s: string): string {
    try {
        return decodeURIComponent(s);
    } catch (e) {
        return s;
    }
}

function parseQuery(q: string): Map<string, string> {
    const m = new Map<string, string>();
    if (q.length === 0) return m;
    for (const pair of q.split('&')) {
        if (pair.length === 0) continue;
        const eq = pair.indexOf('=');
        if (eq < 0) m.set(decode(pair), '');
        else m.set(decode(pair.slice(0, eq)), decode(pair.slice(eq + 1)));
    }
    return m;
}

// The h2o backend builds instances in C (khttpH2oRequest), filling method
// and path; _adopt sets the rest.
/** @lowerType kml_khttp_req */
export class HttpRequest {
    method: string;
    path: string;
    query: Map<string, string>;
    headers: Map<string, string>;
    #text: string | null = null;
    #resume: () => void;
    #chunks: Uint8Array[] = [];
    #length = 0;
    #complete = false;
    #streaming = false; // the body passed the cap before the handler ran
    #disturbed = false;
    #controller: ReadableStreamDefaultController<Uint8Array> | null = null;
    #web: ReadableStream<Uint8Array> | null = null;
    #node: Readable | null = null;

    constructor(resume: () => void, method: string, url: string, headers: Map<string, string>) {
        this.#resume = resume;
        this.method = method;
        const q = url.indexOf('?');
        this.path = q < 0 ? url : url.slice(0, q);
        this.query = parseQuery(q < 0 ? '' : url.slice(q + 1));
        this.headers = headers;
    }

    // A C-built request (khttpH2oRequest): the parts C does not fill.
    _adopt(resume: () => void, query: string, headers: Map<string, string>): void {
        this.#resume = resume;
        this.query = parseQuery(query);
        this.headers = headers;
    }

    // The body as text. A body past the cap is read with stream() instead.
    get body(): string {
        if (this.#disturbed) throw new TypeError('Body is unusable: Body has already been read');
        if (this.#streaming) throw new RangeError('The request body is larger than ' + BODY_CAP + ' bytes: read it with stream()');
        if (this.#text === null) this.#text = Buffer.concat(this.#chunks, this.#length).toString('utf8');
        return this.#text;
    }

    bodyBytes(): ArrayBuffer {
        if (this.#disturbed) throw new TypeError('Body is unusable: Body has already been read');
        if (this.#streaming) throw new RangeError('The request body is larger than ' + BODY_CAP + ' bytes: read it with stream()');
        const all = Buffer.concat(this.#chunks, this.#length);
        const out = new ArrayBuffer(all.length);
        new Uint8Array(out).set(all);
        return out;
    }

    stream(): ReadableStream<Uint8Array> {
        if (this.#web !== null || this.#disturbed) throw new TypeError('ReadableStream is locked or disturbed');
        this.#disturbed = true;
        const self = this;
        this.#web = new ReadableStream<Uint8Array>({
            start(controller: ReadableStreamDefaultController<Uint8Array>) {
                // What arrived so far, in pieces as a socket hands them on.
                for (const c of self.#chunks) {
                    for (let off = 0; off < c.length; off += CHUNK) controller.enqueue(c.subarray(off, Math.min(c.length, off + CHUNK)));
                }
                self.#chunks = [];
                if (self.#complete) controller.close();
                else self.#controller = controller;
            },
            pull() {
                self.#resume();
            },
        }, { highWaterMark: 4 });
        return this.#web;
    }

    // The request as a Node Readable over its own body.
    #readable(): Readable {
        if (this.#node === null) this.#node = Readable.fromWeb(this.stream());
        return this.#node;
    }

    on(event: string, listener: (...args: any[]) => void): this {
        this.#readable().on(event, listener);
        return this;
    }

    pipe<T>(dest: T, options?: { end?: boolean }): T {
        return this.#readable().pipe(dest as any, options) as T;
    }

    [Symbol.asyncIterator](): AsyncIterator<Uint8Array> {
        return this.#readable()[Symbol.asyncIterator]();
    }

    // From the connection: a body chunk (a copy), and the body's end. False
    // when the reader is behind, to pause the socket.
    _push(chunk: Uint8Array): boolean {
        if (this.#controller !== null) {
            this.#controller.enqueue(chunk);
            return (this.#controller.desiredSize ?? 1) > 0;
        }
        this.#chunks.push(chunk);
        this.#length += chunk.length;
        return true;
    }

    _end(): void {
        this.#complete = true;
        if (this.#controller !== null) {
            this.#controller.close();
            this.#controller = null;
        }
    }

    _bufferedLength(): number { return this.#length; }
    _setStreaming(): void { this.#streaming = true; }
}

