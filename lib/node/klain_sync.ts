// klain:sync — Go's concurrency model: goroutines on an M:N scheduler over
// OS threads (preempted at safepoints the compiler inserts), CSP channels and
// select. Memory is shared, as in Go.
//
//	const ch = new Channel<number>()
//	go(() => { for (let i = 0; i < 3; i++) ch.send(i); ch.close() })
//	for (const v of ch) console.log(v)

// Runs fn on a new goroutine.
export function go(fn: () => void): void {
    __kml_native.syncGo(fn);
}

// One case of a select: a receive or a send on a channel, or the default.
export class SelectCase {
    readonly ch: number;
    // 0 receive, 1 send, 2 default.
    readonly dir: number;
    readonly value: any;
    readonly handler: (value: any) => void;

    constructor(ch: number, dir: number, value: any, handler: (value: any) => void) {
        this.ch = ch;
        this.dir = dir;
        this.value = value;
        this.handler = handler;
    }
}

// A channel of T: unbuffered (a send waits for its receive) by default, or
// buffered with capacity slots.
export class Channel<T> {
    private readonly h: number;

    constructor(capacity?: number) {
        this.h = __kml_native.syncChanNew(capacity === undefined ? 0 : capacity);
    }

    // Sends value, waiting for a receiver (or a free slot).
    send(value: T): void {
        __kml_native.syncChanSend(this.h, value);
    }

    // The next value, waiting for one; undefined once the channel is closed
    // and drained.
    receive(): T {
        const v: any = __kml_native.syncChanRecv(this.h);
        if (__kml_native.syncLastOk() === 0) return undefined as any;
        return v;
    }

    // Closes the channel: receivers drain what is buffered, then get
    // undefined.
    close(): void {
        __kml_native.syncChanClose(this.h);
    }

    // A select case receiving from this channel into handler.
    recvCase(handler: (value: T) => void): SelectCase {
        return new SelectCase(this.h, 0, undefined, handler);
    }

    // A select case sending value on this channel, then running handler.
    sendCase(value: T, handler: () => void): SelectCase {
        return new SelectCase(this.h, 1, value, handler);
    }

    // Receives until the channel is closed and drained.
    [Symbol.iterator](): ChannelIterator<T> {
        return new ChannelIterator<T>(this.h);
    }
}

// A channel's values as they are received, until it is closed and drained.
export class ChannelIterator<T> {
    private readonly h: number;

    constructor(h: number) {
        this.h = h;
    }

    next(): IteratorResult<T> {
        const v: any = __kml_native.syncChanRecv(this.h);
        if (__kml_native.syncLastOk() === 0) return { value: undefined as any, done: true };
        return { value: v, done: false };
    }
}

// The case select runs when no other is ready.
export function defaultCase(handler: () => void): SelectCase {
    return new SelectCase(0, 2, undefined, handler);
}

// Waits until one case can proceed (a ready one at random when several are,
// the default at once when none is) and runs its handler.
export function select(...cases: SelectCase[]): void {
    __kml_native.syncSelectBegin();
    const picked: SelectCase[] = [];
    let fallback: SelectCase | undefined = undefined;
    for (const c of cases) {
        if (c.dir === 2) {
            if (fallback !== undefined) throw new Error('select has more than one defaultCase');
            fallback = c;
            continue;
        }
        if (__kml_native.syncSelectAdd(c.ch, c.dir, c.value) < 0) {
            throw new RangeError('select has more than 64 cases');
        }
        picked.push(c);
    }
    const i = __kml_native.syncSelectRun(fallback !== undefined);
    if (i < 0) {
        if (fallback !== undefined) fallback.handler(undefined);
        return;
    }
    const c = picked[i];
    if (c.dir === 0) {
        const v: any = __kml_native.syncSelectValue(i);
        c.handler(__kml_native.syncLastOk() === 0 ? undefined : v);
        return;
    }
    c.handler(undefined);
}
