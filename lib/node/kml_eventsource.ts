// kml:global
// EventSource — server-sent events as undici's lib/web/eventsource has them
// (TDD-00232 Stage 2), over fetch. Node ships it behind
// --experimental-eventsource; the web platform's global.

export interface EventSourceInit {
    withCredentials?: boolean;
}

const CONNECTING = 0;
const OPEN = 1;
const CLOSED = 2;

export class EventSource extends EventTarget {
    static readonly CONNECTING = 0;
    static readonly OPEN = 1;
    static readonly CLOSED = 2;
    readonly CONNECTING = 0;
    readonly OPEN = 1;
    readonly CLOSED = 2;

    #url: string;
    #withCredentials: boolean;
    #readyState = CONNECTING;
    #lastEventId = '';
    #reconnectionTime = 3000;
    #controller: AbortController | null = null;
    #onopen: any = null;
    #onmessage: any = null;
    #onerror: any = null;
    #handlers = new Set<string>();
    // The record being parsed.
    #data = '';
    #eventType = '';
    #pending = Buffer.alloc(0);
    #first = true;
    #sawCR = false;

    constructor(url: string | URL, init?: EventSourceInit) {
        super();
        let parsed: URL;
        try {
            parsed = new URL(`${url}`);
        } catch (e) {
            throw new DOMException(`${e}`, 'SyntaxError');
        }
        this.#url = parsed.href;
        this.#withCredentials = init?.withCredentials === true;
        this.#connect();
    }

    get url(): string { return this.#url; }
    get withCredentials(): boolean { return this.#withCredentials; }
    get readyState(): number { return this.#readyState; }

    get onopen(): any { return this.#onopen; }
    set onopen(h: any) { this.#onopen = this.#handler('open', h); }
    get onmessage(): any { return this.#onmessage; }
    set onmessage(h: any) { this.#onmessage = this.#handler('message', h); }
    get onerror(): any { return this.#onerror; }
    set onerror(h: any) { this.#onerror = this.#handler('error', h); }

    // An event handler attribute: registered once, at its first assignment.
    #handler(type: string, h: any): any {
        const fn = typeof h === 'function' ? h : null;
        if (fn !== null && !this.#handlers.has(type)) {
            this.#handlers.add(type);
            this.addEventListener(type, (ev: Event) => {
                const cur: any = type === 'open' ? this.#onopen : type === 'message' ? this.#onmessage : this.#onerror;
                if (cur !== null) cur.call(this, ev);
            });
        }
        return fn;
    }

    close(): void {
        if (this.#readyState === CLOSED) return;
        this.#readyState = CLOSED;
        if (this.#controller !== null) this.#controller.abort();
    }

    #connect(): void {
        const controller = new AbortController();
        this.#controller = controller;
        const headers: Record<string, string> = { 'accept': 'text/event-stream', 'cache-control': 'no-cache' };
        if (this.#lastEventId !== '') headers['last-event-id'] = this.#lastEventId;
        fetch(this.#url, { headers, signal: controller.signal }).then(
            (res: Response) => { this.#response(res); },
            () => { this.#reconnect(); },
        );
    }

    #response(res: Response): void {
        if (this.#readyState === CLOSED) return;
        const type = res.headers.get('content-type') ?? '';
        if (res.status !== 200 || !type.toLowerCase().startsWith('text/event-stream') || res.body === null) {
            this.#fail();
            return;
        }
        this.#readyState = OPEN;
        // Each connection's stream is parsed afresh.
        this.#pending = Buffer.alloc(0);
        this.#first = true;
        this.#sawCR = false;
        this.#data = '';
        this.#eventType = '';
        this.dispatchEvent(new Event('open'));
        this.#read(res);
    }

    // Reads the stream until it ends (or the connection is closed), then
    // reconnects.
    async #read(res: Response): Promise<void> {
        const reader = res.body!.getReader();
        try {
            while (this.#readyState !== CLOSED) {
                const { done, value } = await reader.read();
                if (done) break;
                this.#parse(value);
            }
        } catch (e) {
            // A network error: reconnect, as at the stream's end.
        }
        this.#reconnect();
    }

    // The event stream interpretation (HTML §9.2.6), over bytes as undici's
    // EventSourceStream: lines end at CRLF, LF or CR, each decoded as UTF-8
    // (a leading BOM dropped); a blank line dispatches the record.
    #parse(chunk: Uint8Array): void {
        const bytes = Buffer.from(chunk);
        const buf = this.#pending.length === 0 ? bytes : Buffer.concat([this.#pending, bytes]);
        let start = 0;
        for (let i = 0; i < buf.length; i++) {
            const c = buf[i];
            if (this.#sawCR) {
                this.#sawCR = false;
                if (c === 10) {
                    start = i + 1;
                    continue;
                }
            }
            if (c === 13 || c === 10) {
                this.#sawCR = c === 13;
                let line = buf.subarray(start, i).toString('utf8');
                if (this.#first) {
                    this.#first = false;
                    if (line.startsWith('\uFEFF')) line = line.substring(1);
                }
                this.#line(line);
                start = i + 1;
            }
        }
        this.#pending = buf.subarray(start);
    }

    #line(line: string): void {
        if (line === '') {
            this.#dispatch();
            return;
        }
        if (line.startsWith(':')) return;
        const colon = line.indexOf(':');
        let field = line;
        let value = '';
        if (colon >= 0) {
            field = line.substring(0, colon);
            value = line.substring(colon + 1);
            if (value.startsWith(' ')) value = value.substring(1);
        }
        switch (field) {
            case 'data':
                this.#data += value + '\n';
                break;
            case 'event':
                this.#eventType = value;
                break;
            case 'id':
                if (value.indexOf('\0') < 0) this.#lastEventId = value;
                break;
            case 'retry':
                if (/^[0-9]+$/.test(value)) this.#reconnectionTime = Number(value);
                break;
        }
    }

    #dispatch(): void {
        const type = this.#eventType === '' ? 'message' : this.#eventType;
        this.#eventType = '';
        if (this.#data === '') return;
        const data = this.#data.endsWith('\n') ? this.#data.substring(0, this.#data.length - 1) : this.#data;
        this.#data = '';
        if (this.#readyState === CLOSED) return;
        this.dispatchEvent(new MessageEvent(type, { data, lastEventId: this.#lastEventId, origin: new URL(this.#url).origin }));
    }

    // A network error or the stream's end: reconnect after the reconnection
    // time, announcing it with an error event.
    #reconnect(): void {
        if (this.#readyState === CLOSED) return;
        this.#readyState = CONNECTING;
        this.dispatchEvent(new Event('error'));
        setTimeout(() => {
            if (this.#readyState === CONNECTING) this.#connect();
        }, this.#reconnectionTime);
    }

    // A response of the wrong shape: the connection fails for good.
    #fail(): void {
        if (this.#readyState === CLOSED) return;
        this.#readyState = CLOSED;
        this.dispatchEvent(new Event('error'));
    }
}
