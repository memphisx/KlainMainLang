// Node's `stream` module, ported from Node v24's lib/internal/streams/
// (readable.js, writable.js, duplex.js, transform.js, passthrough.js,
// destroy.js, end-of-stream.js, pipeline.js, from.js): the same states, the
// same order of events and callbacks (each process.nextTick where Node
// defers), the same defaults. Node keeps a state's flags in one bit field;
// here each is a named field. Web-stream adapters and compose are not
// ported.
//
// kml:default-namespace — `import stream from 'stream'` reads this module's
// exports (`stream.Readable`), as Node's default export carries them.
import { EventEmitter } from 'events';

export type Callback = (error?: Error | null) => void;
export type TransformCallback = (error?: Error | null, data?: any) => void;

export interface StreamOptions {
    highWaterMark?: number;
    objectMode?: boolean;
    autoDestroy?: boolean;
    emitClose?: boolean;
}

export interface ReadableOptions extends StreamOptions {
    encoding?: BufferEncoding;
    construct?: (this: Readable, callback: (error?: Error | null) => void) => void;
    read?: (this: Readable, size: number) => void;
    destroy?: (this: Readable, error: Error | null, callback: Callback) => void;
}

export interface WritableOptions extends StreamOptions {
    decodeStrings?: boolean;
    construct?: (this: Writable, callback: (error?: Error | null) => void) => void;
    defaultEncoding?: BufferEncoding;
    write?: (this: Writable, chunk: any, encoding: BufferEncoding, callback: Callback) => void;
    final?: (this: Writable, callback: Callback) => void;
    destroy?: (this: Writable, error: Error | null, callback: Callback) => void;
}

export interface DuplexOptions extends StreamOptions {
    encoding?: BufferEncoding;
    allowHalfOpen?: boolean;
    readable?: boolean;
    writable?: boolean;
    readableObjectMode?: boolean;
    writableObjectMode?: boolean;
    readableHighWaterMark?: number;
    writableHighWaterMark?: number;
    decodeStrings?: boolean;
    defaultEncoding?: BufferEncoding;
    construct?: (this: Duplex, callback: (error?: Error | null) => void) => void;
    read?: (this: Duplex, size: number) => void;
    write?: (this: Duplex, chunk: any, encoding: BufferEncoding, callback: Callback) => void;
    final?: (this: Duplex, callback: Callback) => void;
    destroy?: (this: Duplex, error: Error | null, callback: Callback) => void;
}

export interface TransformOptions extends DuplexOptions {
    transform?: (this: Transform, chunk: any, encoding: BufferEncoding, callback: TransformCallback) => void;
    flush?: (this: Transform, callback: TransformCallback) => void;
}

// A Node error with its `code` (ERR_STREAM_*).
class StreamError extends Error {
    code: string;
    constructor(code: string, message: string) {
        super(message);
        this.code = code;
    }
}

function nop(error?: Error | null): void {}

function defaultHighWaterMark(objectMode: boolean): number {
    if (objectMode) return 16;
    return process.platform === 'win32' ? 16 * 1024 : 64 * 1024;
}

function toBuffer(chunk: string, encoding: string): Buffer {
    switch (encoding) {
        case 'hex': return Buffer.from(chunk, 'hex');
        case 'base64': return Buffer.from(chunk, 'base64');
        case 'base64url': return Buffer.from(chunk, 'base64url');
        case 'latin1': return Buffer.from(chunk, 'latin1');
        case 'binary': return Buffer.from(chunk, 'binary');
        case 'ascii': return Buffer.from(chunk, 'ascii');
        case 'utf16le': return Buffer.from(chunk, 'utf16le');
        case 'utf-16le': return Buffer.from(chunk, 'utf16le');
        case 'ucs2': return Buffer.from(chunk, 'ucs2');
        case 'ucs-2': return Buffer.from(chunk, 'ucs2');
    }
    return Buffer.from(chunk);
}

function decodeBuffer(buf: Buffer, encoding: string): string {
    switch (encoding) {
        case 'hex': return buf.toString('hex');
        case 'base64': return buf.toString('base64');
        case 'base64url': return buf.toString('base64url');
        case 'latin1': return buf.toString('latin1');
        case 'binary': return buf.toString('binary');
        case 'ascii': return buf.toString('ascii');
        case 'utf16le': return buf.toString('utf16le');
        case 'utf-16le': return buf.toString('utf16le');
        case 'ucs2': return buf.toString('ucs2');
        case 'ucs-2': return buf.toString('ucs2');
    }
    return buf.toString();
}

// Node's string_decoder for setEncoding: UTF-8 keeps a character split
// across chunks for the next one; the other encodings decode per chunk.
class Decoder {
    encoding: string;
    private pending: number[] = [];
    constructor(encoding: string) {
        this.encoding = encoding === 'utf-8' ? 'utf8' : encoding;
    }
    write(buf: Buffer): string {
        const enc = this.encoding;
        if (enc === 'utf16le' || enc === 'utf-16le' || enc === 'ucs2' || enc === 'ucs-2') return this.writeUtf16(buf);
        if (enc === 'base64' || enc === 'base64url') return this.writeBase64(buf);
        if (enc !== 'utf8') return decodeBuffer(buf, enc);
        let bytes = buf;
        if (this.pending.length > 0) {
            bytes = Buffer.concat([Buffer.from(this.pending), buf]);
            this.pending = [];
        }
        let end = bytes.length;
        let i = end - 1;
        let back = 0;
        while (i >= 0 && back < 3 && (bytes[i] & 0xc0) === 0x80) {
            i--;
            back++;
        }
        if (i >= 0) {
            const lead = bytes[i];
            const need = lead >= 0xf0 ? 4 : lead >= 0xe0 ? 3 : lead >= 0xc0 ? 2 : 1;
            if (need > back + 1) {
                for (let k = i; k < end; k++) this.pending.push(bytes[k]);
                end = i;
            }
        }
        return bytes.subarray(0, end).toString();
    }
    // A UTF-16 code unit split across chunks, or a high surrogate without
    // its pair, waits for the next chunk.
    private writeUtf16(buf: Buffer): string {
        let bytes = buf;
        if (this.pending.length > 0) {
            bytes = Buffer.concat([Buffer.from(this.pending), buf]);
            this.pending = [];
        }
        let end = bytes.length - (bytes.length % 2);
        if (end >= 2) {
            const unit = bytes[end - 2] | (bytes[end - 1] << 8);
            if (unit >= 0xd800 && unit <= 0xdbff) end -= 2;
        }
        for (let k = end; k < bytes.length; k++) this.pending.push(bytes[k]);
        return decodeBuffer(bytes.subarray(0, end), this.encoding);
    }
    // Base64 encodes whole three-byte groups; the rest waits.
    private writeBase64(buf: Buffer): string {
        let bytes = buf;
        if (this.pending.length > 0) {
            bytes = Buffer.concat([Buffer.from(this.pending), buf]);
            this.pending = [];
        }
        const end = bytes.length - (bytes.length % 3);
        for (let k = end; k < bytes.length; k++) this.pending.push(bytes[k]);
        return decodeBuffer(bytes.subarray(0, end), this.encoding);
    }
    end(): string {
        if (this.pending.length === 0) return '';
        const rest = Buffer.from(this.pending);
        this.pending = [];
        if (this.encoding === 'utf8') return rest.toString();
        return decodeBuffer(rest, this.encoding);
    }
}

class ReadableState {
    objectMode: boolean;
    highWaterMark: number;
    buffer: any[] = [];
    length = 0;
    pipes: Stream[] = [];
    // flowing is readableFlowing: null until something sets it (hasFlowing).
    hasFlowing = false;
    flowing = false;
    ended = false;
    endEmitted = false;
    reading = false;
    constructed = true;
    sync = true;
    needReadable = false;
    emittedReadable = false;
    readableListening = false;
    resumeScheduled = false;
    errorEmitted = false;
    emitClose = true;
    autoDestroy = true;
    destroyed = false;
    errored: Error | null = null;
    closed = false;
    closeEmitted = false;
    defaultEncoding = 'utf8';
    awaitDrainWriters: Stream[] = [];
    multiAwaitDrain = false;
    readingMore = false;
    dataEmitted = false;
    dataListening = false;
    paused = false;
    hasPaused = false;
    readable = true;
    decoder: Decoder | null = null;
    encoding: string | null = null;
    constructor(options: DuplexOptions | undefined, isDuplex: boolean) {
        this.objectMode = (options?.objectMode ?? false) || (isDuplex && (options?.readableObjectMode ?? false));
        const hwm = options?.highWaterMark ?? (isDuplex ? options?.readableHighWaterMark : undefined);
        this.highWaterMark = hwm ?? defaultHighWaterMark(this.objectMode);
        if (options?.emitClose === false) this.emitClose = false;
        if (options?.autoDestroy === false) this.autoDestroy = false;
        if (options?.defaultEncoding) this.defaultEncoding = options.defaultEncoding;
        if (options?.encoding) {
            this.decoder = new Decoder(options.encoding);
            this.encoding = this.decoder.encoding;
        }
    }
}

interface BufferedWrite {
    chunk: any;
    encoding: string;
    callback: Callback;
}

class WritableState {
    objectMode: boolean;
    highWaterMark: number;
    decodeStrings = true;
    defaultEncoding = 'utf8';
    length = 0;
    corked = 0;
    writing = false;
    sync = true;
    bufferProcessing = false;
    writelen = 0;
    writecb: Callback | null = null;
    expectWriteCb = false;
    buffered: BufferedWrite[] = [];
    bufferedIndex = 0;
    pendingcb = 0;
    constructed = true;
    prefinished = false;
    errorEmitted = false;
    emitClose = true;
    autoDestroy = true;
    errored: Error | null = null;
    closed = false;
    closeEmitted = false;
    ending = false;
    ended = false;
    finished = false;
    finalCalled = false;
    destroyed = false;
    needDrain = false;
    afterWritePending = false;
    afterWriteTickInfo: AfterWriteTickInfo | null = null;
    onFinished: Callback[] = [];
    writable = true;
    constructor(options: DuplexOptions | undefined, isDuplex: boolean) {
        this.objectMode = (options?.objectMode ?? false) || (isDuplex && (options?.writableObjectMode ?? false));
        const hwm = options?.highWaterMark ?? (isDuplex ? options?.writableHighWaterMark : undefined);
        this.highWaterMark = hwm ?? defaultHighWaterMark(this.objectMode);
        if (options?.decodeStrings === false) this.decodeStrings = false;
        if (options?.emitClose === false) this.emitClose = false;
        if (options?.autoDestroy === false) this.autoDestroy = false;
        if (options?.defaultEncoding) this.defaultEncoding = options.defaultEncoding;
    }
}

interface AfterWriteTickInfo {
    count: number;
    cb: Callback;
}

// Node's legacy Stream: the base of every stream class. The hooks below are
// what the shared machinery calls on a stream whatever its class (a pipe
// writing to its destination, pipeline piping one stream into the next);
// each class overrides the ones it has.
export class Stream extends EventEmitter {
    _readableState: ReadableState | null = null;
    _writableState: WritableState | null = null;
    constructor(options?: StreamOptions) {
        super();
    }
    _read(size: number): void {}
    _write(chunk: any, encoding: BufferEncoding, callback: Callback): void {
        callback(new StreamError('ERR_METHOD_NOT_IMPLEMENTED', 'The _write() method is not implemented'));
    }
    // Node's streams have no `_final` unless the options or a subclass give
    // one; this stand-in marks its absence for prefinish.
    _final(callback: Callback): void {
        this._kmlNoFinal = true;
    }
    _kmlNoFinal = false;
    // Node's `_construct`: an asynchronous initialization (an fs stream opens
    // its file) that reading, writing and destroying wait for. A class
    // defines it, or the options give one (constructImpl).
    _construct?(callback: (error?: Error | null) => void): void;
    _kmlConstructImpl?: (this: Stream, callback: (error?: Error | null) => void) => void;
    // kConstruct's listeners, and destroy()'s wait while constructing
    // (Node's kConstruct and kDestroy events).
    _kmlOnConstructed: (() => void)[] = [];
    _kmlOnDestroyReady: ((error?: Error | null) => void) | null = null;
    // A Duplex constructs once, for both its sides.
    _kmlIsDuplex(): boolean { return false; }
    _destroy(error: Error | null, callback: Callback): void {
        callback(error);
    }
    destroy(error?: Error | null, callback?: Callback): this {
        destroyStream(this, error ?? null, callback);
        return this;
    }
    _kmlWrite(chunk: any): boolean { return true; }
    _kmlEnd(): void {}
    _kmlPipe(dest: Stream, end: boolean): void {}
    _kmlPause(): void {}
    _kmlResume(): void {}
    _kmlUnpipe(dest: Stream): void {}
    allowsHalfOpen(): boolean { return true; }
    pipe<T extends Stream>(destination: T, options?: { end?: boolean }): T {
        return destination;
    }
}

// ---- destroy.js ----

function destroyStream(stream: Stream, err: Error | null, cb: Callback | undefined): void {
    const r = stream._readableState;
    const w = stream._writableState;
    if ((w !== null && w.destroyed) || (r !== null && r.destroyed)) {
        if (cb) cb();
        return;
    }
    checkError(err, w, r);
    if (w !== null) w.destroyed = true;
    if (r !== null) r.destroyed = true;
    const constructed = w !== null ? w.constructed : r !== null && r.constructed;
    if (!constructed) {
        // Still constructing: destroy once construction ends.
        stream._kmlOnDestroyReady = (er?: Error | null) => {
            destroyNow(stream, aggregateTwoErrors(er ?? null, err), cb);
        };
        return;
    }
    destroyNow(stream, err, cb);
}

function aggregateTwoErrors(inner: Error | null, outer: Error | null): Error | null {
    if (inner && outer && inner !== outer) {
        const e = new AggregateError([outer, inner], outer.message);
        (e as any).code = (outer as any).code;
        return e;
    }
    return inner ?? outer;
}

function destroyNow(stream: Stream, err: Error | null, cb: Callback | undefined): void {
    const r = stream._readableState;
    const w = stream._writableState;
    let called = false;
    const onDestroy = (er?: Error | null) => {
        if (called) return;
        called = true;
        checkError(er ?? null, stream._writableState, stream._readableState);
        if (w !== null) w.closed = true;
        if (r !== null) r.closed = true;
        if (cb) cb(er);
        if (er) {
            process.nextTick(() => {
                emitErrorNT(stream, er);
                emitCloseNT(stream);
            });
        } else {
            process.nextTick(() => { emitCloseNT(stream); });
        }
    };
    try {
        stream._destroy(err, onDestroy);
    } catch (e) {
        onDestroy(e as Error);
    }
}

function checkError(err: Error | null, w: WritableState | null, r: ReadableState | null): void {
    if (err) {
        if (w !== null && !w.errored) w.errored = err;
        if (r !== null && !r.errored) r.errored = err;
    }
}

function emitCloseNT(stream: Stream): void {
    const r = stream._readableState;
    const w = stream._writableState;
    if (w !== null) w.closeEmitted = true;
    if (r !== null) r.closeEmitted = true;
    if ((w !== null && w.emitClose) || (r !== null && r.emitClose)) stream.emit('close');
}

function emitErrorNT(stream: Stream, err: Error): void {
    const r = stream._readableState;
    const w = stream._writableState;
    if ((w !== null && w.errorEmitted) || (r !== null && r.errorEmitted)) return;
    if (w !== null) w.errorEmitted = true;
    if (r !== null) r.errorEmitted = true;
    stream.emit('error', err);
}

// Node's construct: `_construct` runs on the next tick, and its callback's
// outcome lands on the one after.
function construct(stream: Stream, cb: () => void): void {
    if (stream._construct == null && stream._kmlConstructImpl === undefined) return;
    const r = stream._readableState;
    const w = stream._writableState;
    if (r !== null) r.constructed = false;
    if (w !== null) w.constructed = false;
    stream._kmlOnConstructed.push(cb);
    if (stream._kmlOnConstructed.length > 1) return;
    process.nextTick(() => { constructNT(stream); });
}

function constructNT(stream: Stream): void {
    let called = false;
    const onConstruct = (err?: Error | null) => {
        if (called) {
            errorOrDestroy(stream, err ?? new StreamError('ERR_MULTIPLE_CALLBACK', 'Callback called multiple times'), false);
            return;
        }
        called = true;
        const r = stream._readableState;
        const w = stream._writableState;
        if (r !== null) r.constructed = true;
        if (w !== null) w.constructed = true;
        const destroyed = w !== null ? w.destroyed : r !== null && r.destroyed;
        if (destroyed) {
            const ready = stream._kmlOnDestroyReady;
            stream._kmlOnDestroyReady = null;
            if (ready !== null) ready(err);
        } else if (err) {
            errorOrDestroy(stream, err, true);
        } else {
            const cbs = stream._kmlOnConstructed;
            stream._kmlOnConstructed = [];
            for (let i = 0; i < cbs.length; i++) cbs[i]();
        }
    };
    const done = (err?: Error | null) => {
        process.nextTick(() => { onConstruct(err); });
    };
    try {
        const impl = stream._kmlConstructImpl;
        if (impl !== undefined) impl.call(stream, done);
        else if (stream._construct) stream._construct(done);
    } catch (err) {
        process.nextTick(() => { onConstruct(err as Error); });
    }
}

function errorOrDestroy(stream: Stream, err: Error, sync: boolean): void {
    const r = stream._readableState;
    const w = stream._writableState;
    if ((w !== null && w.destroyed) || (r !== null && r.destroyed)) return;
    if ((r !== null && r.autoDestroy) || (w !== null && w.autoDestroy)) {
        stream.destroy(err);
        return;
    }
    if (w !== null && !w.errored) w.errored = err;
    if (r !== null && !r.errored) r.errored = err;
    if (sync) process.nextTick(() => { emitErrorNT(stream, err); });
    else emitErrorNT(stream, err);
}

// ---- readable.js ----

// kml:callable Readable — Node's is a function that constructs when called without `new`
export class Readable extends Stream {
    private readImpl?: (this: Readable, size: number) => void;
    private destroyImpl?: (this: Readable, error: Error | null, callback: Callback) => void;

    constructor(options?: ReadableOptions) {
        super(options);
        this._readableState = new ReadableState(options, false);
        this.readImpl = options?.read;
        this.destroyImpl = options?.destroy;
        if (options?.construct) this._kmlConstructImpl = options.construct as (this: Stream, callback: (error?: Error | null) => void) => void;
        if (!this._kmlIsDuplex()) {
            construct(this, () => {
                const s = this._readableState!;
                if (s.needReadable) maybeReadMore(this, s);
            });
        }
    }

    static from(iterable: any, options?: ReadableOptions): Readable {
        return readableFrom(iterable, options);
    }

    _read(size: number): void {
        if (this.readImpl) {
            this.readImpl.call(this, size);
            return;
        }
        throw new StreamError('ERR_METHOD_NOT_IMPLEMENTED', 'The _read() method is not implemented');
    }

    _destroy(error: Error | null, callback: Callback): void {
        if (this.destroyImpl) {
            this.destroyImpl.call(this, error, callback);
            return;
        }
        callback(error);
    }

    get readable(): boolean {
        const r = this._readableState!;
        return r.readable && !r.destroyed && !r.errorEmitted && !r.endEmitted;
    }
    get readableDidRead(): boolean { return this._readableState!.dataEmitted; }
    get readableHighWaterMark(): number { return this._readableState!.highWaterMark; }
    get readableLength(): number { return this._readableState!.length; }
    get readableObjectMode(): boolean { return this._readableState!.objectMode; }
    get readableEncoding(): string | null { return this._readableState!.encoding; }
    get readableEnded(): boolean { return this._readableState!.endEmitted; }
    get readableFlowing(): boolean | null {
        const r = this._readableState!;
        return r.hasFlowing ? r.flowing : null;
    }
    set readableFlowing(value: boolean | null) {
        const r = this._readableState!;
        if (value === null) {
            r.hasFlowing = false;
            r.flowing = false;
        } else {
            r.hasFlowing = true;
            r.flowing = value;
        }
    }
    get readableAborted(): boolean {
        const r = this._readableState!;
        return r.readable !== false && (r.destroyed || r.errored !== null) && !r.endEmitted;
    }
    get destroyed(): boolean { return this._readableState!.destroyed; }
    get closed(): boolean { return this._readableState!.closed; }
    get errored(): Error | null { return this._readableState!.errored; }

    push(chunk: any, encoding?: BufferEncoding): boolean {
        const s = this._readableState!;
        return s.objectMode ? readableAddChunkPushObjectMode(this, s, chunk) : readableAddChunkPushByteMode(this, s, chunk, encoding);
    }

    unshift(chunk: any, encoding?: BufferEncoding): boolean {
        const s = this._readableState!;
        return s.objectMode ? readableAddChunkUnshiftObjectMode(this, s, chunk) : readableAddChunkUnshiftByteMode(this, s, chunk, encoding);
    }

    isPaused(): boolean {
        const s = this._readableState!;
        return s.paused || (s.hasFlowing && !s.flowing);
    }

    setEncoding(encoding: BufferEncoding): this {
        const s = this._readableState!;
        const decoder = new Decoder(encoding);
        s.decoder = decoder;
        s.encoding = decoder.encoding;
        let content = '';
        for (let i = 0; i < s.buffer.length; i++) {
            const data = s.buffer[i];
            content += typeof data === 'string' ? data : decoder.write(data);
        }
        s.buffer = [];
        if (content !== '') s.buffer.push(content);
        s.length = content.length;
        return this;
    }

    read(n?: number): any {
        return readImpl(this, n);
    }

    pipe<T extends Stream>(destination: T, options?: { end?: boolean }): T {
        pipeTo(this, destination, options?.end !== false);
        return destination;
    }

    _kmlPipe(dest: Stream, end: boolean): void { pipeTo(this, dest, end); }
    _kmlPause(): void { this.pause(); }
    _kmlResume(): void { this.resume(); }
    _kmlUnpipe(dest: Stream): void { this.unpipe(dest); }

    unpipe(destination?: Stream): this {
        const s = this._readableState!;
        if (s.pipes.length === 0) return this;
        if (destination === undefined) {
            const dests = s.pipes;
            s.pipes = [];
            this.pause();
            for (const d of dests) d.emit('unpipe', this, { hasUnpiped: false });
            return this;
        }
        const index = s.pipes.indexOf(destination);
        if (index === -1) return this;
        s.pipes.splice(index, 1);
        if (s.pipes.length === 0) this.pause();
        destination.emit('unpipe', this, { hasUnpiped: false });
        return this;
    }

    on(event: string | symbol, listener: (...args: any[]) => void): this {
        super.on(event, listener);
        const s = this._readableState!;
        if (event === 'data') {
            s.dataListening = true;
            if (this.listenerCount('readable') > 0) s.readableListening = true;
            if (!(s.hasFlowing && !s.flowing)) this.resume();
        } else if (event === 'readable') {
            if (!s.endEmitted && !s.readableListening) {
                s.readableListening = true;
                s.needReadable = true;
                s.hasFlowing = true;
                s.flowing = false;
                s.emittedReadable = false;
                if (s.length > 0) emitReadable(this);
                else if (!s.reading) process.nextTick(() => { this.read(0); });
            }
        }
        return this;
    }

    addListener(event: string | symbol, listener: (...args: any[]) => void): this {
        return this.on(event, listener);
    }

    removeListener(event: string | symbol, listener: (...args: any[]) => void): this {
        super.removeListener(event, listener);
        const s = this._readableState!;
        if (event === 'readable') {
            process.nextTick(() => { updateReadableListening(this); });
        } else if (event === 'data' && this.listenerCount('data') === 0) {
            s.dataListening = false;
        }
        return this;
    }

    off(event: string | symbol, listener: (...args: any[]) => void): this {
        return this.removeListener(event, listener);
    }

    resume(): this {
        const s = this._readableState!;
        if (!s.flowing) {
            s.hasFlowing = true;
            s.flowing = !s.readableListening;
            if (!s.resumeScheduled) {
                s.resumeScheduled = true;
                process.nextTick(() => { resumeNT(this, s); });
            }
        }
        s.hasPaused = true;
        s.paused = false;
        return this;
    }

    pause(): this {
        const s = this._readableState!;
        if (!(s.hasFlowing && !s.flowing)) {
            s.hasFlowing = true;
            s.flowing = false;
            this.emit('pause');
        }
        s.hasPaused = true;
        s.paused = true;
        return this;
    }

    async *[Symbol.asyncIterator](): AsyncIterableIterator<any> {
        let callback: () => void = () => {};
        let woken = false;
        const next = () => {
            woken = true;
            const cb = callback;
            callback = () => {};
            cb();
        };
        this.on('readable', next);
        let error: Error | null | undefined = undefined;
        let done = false;
        const cleanup = finished(this, { writable: false }, (err?: Error | null) => {
            error = err ?? null;
            done = true;
            next();
        });
        try {
            while (true) {
                const chunk = this.destroyed ? null : this.read();
                if (chunk !== null) {
                    yield chunk;
                } else if (done && error) {
                    throw error;
                } else if (done) {
                    return;
                } else {
                    woken = false;
                    await new Promise<void>((resolve) => {
                        if (woken) resolve();
                        else callback = resolve;
                    });
                }
            }
        } finally {
            if (!done || error) this.destroy(null);
            else {
                this.off('readable', next);
                cleanup();
            }
        }
    }

    static toWeb(readable: Readable): ReadableStream<any> {
        return new ReadableStream<any>({
            start(controller) {
                readable.on('data', (chunk: any) => { controller.enqueue(chunk); });
                readable.on('end', () => { controller.close(); });
                readable.on('error', (err: Error) => { controller.error(err); });
            },
        });
    }

    static fromWeb(web: ReadableStream<any>, options?: ReadableOptions): Readable {
        const reader = web.getReader();
        const r = new Readable({
            objectMode: options?.objectMode ?? false,
            highWaterMark: options?.highWaterMark,
            read() {
                reader.read().then((res) => {
                    if (res.done) r.push(null);
                    else r.push(res.value);
                });
            },
        });
        return r;
    }
}

function canPushMore(s: ReadableState): boolean {
    return !s.ended && (s.length < s.highWaterMark || s.length === 0);
}

function readableAddChunkUnshiftByteMode(stream: Readable, s: ReadableState, chunk: any, encoding: BufferEncoding | undefined): boolean {
    if (chunk === null) {
        s.reading = false;
        onEofChunk(stream, s);
        return false;
    }
    if (typeof chunk === 'string') {
        const enc = encoding ?? s.defaultEncoding;
        if (s.encoding !== enc) {
            chunk = s.encoding !== null ? decodeBuffer(toBuffer(chunk, enc), s.encoding) : toBuffer(chunk, enc);
        }
    } else if (chunk !== undefined && !Buffer.isBuffer(chunk)) {
        if (!ArrayBuffer.isView(chunk)) {
            errorOrDestroy(stream, new StreamError('ERR_INVALID_ARG_TYPE', 'The "chunk" argument must be of type string or an instance of Buffer, TypedArray, or DataView.'), false);
            return false;
        }
        chunk = Buffer.from(chunk as Uint8Array); // Stream._uint8ArrayToBuffer
    }
    if (!(chunk && chunk.length > 0)) return canPushMore(s);
    return readableAddChunkUnshiftValue(stream, s, chunk);
}

function readableAddChunkUnshiftObjectMode(stream: Readable, s: ReadableState, chunk: any): boolean {
    if (chunk === null) {
        s.reading = false;
        onEofChunk(stream, s);
        return false;
    }
    return readableAddChunkUnshiftValue(stream, s, chunk);
}

function readableAddChunkUnshiftValue(stream: Readable, s: ReadableState, chunk: any): boolean {
    if (s.endEmitted) errorOrDestroy(stream, new StreamError('ERR_STREAM_UNSHIFT_AFTER_END_EVENT', 'stream.unshift() after end event'), false);
    else if (s.destroyed || s.errored) return false;
    else addChunk(stream, s, chunk, true);
    return canPushMore(s);
}

function readableAddChunkPushByteMode(stream: Readable, s: ReadableState, chunk: any, encoding: BufferEncoding | undefined): boolean {
    if (chunk === null) {
        s.reading = false;
        onEofChunk(stream, s);
        return false;
    }
    let enc: string = encoding ?? '';
    if (typeof chunk === 'string') {
        if (enc === '') enc = s.defaultEncoding;
        if (s.encoding !== enc) {
            chunk = toBuffer(chunk, enc);
            enc = '';
        }
    } else if (Buffer.isBuffer(chunk)) {
        enc = '';
    } else if (chunk !== undefined && ArrayBuffer.isView(chunk)) {
        chunk = Buffer.from(chunk as Uint8Array); // Stream._uint8ArrayToBuffer
        enc = '';
    } else if (chunk !== undefined) {
        errorOrDestroy(stream, new StreamError('ERR_INVALID_ARG_TYPE', 'The "chunk" argument must be of type string or an instance of Buffer, TypedArray, or DataView.'), false);
        return false;
    }
    if (!chunk || chunk.length <= 0) {
        s.reading = false;
        maybeReadMore(stream, s);
        return canPushMore(s);
    }
    if (s.ended) {
        errorOrDestroy(stream, new StreamError('ERR_STREAM_PUSH_AFTER_EOF', 'stream.push() after EOF'), false);
        return false;
    }
    if (s.destroyed || s.errored) return false;
    s.reading = false;
    if (s.decoder !== null && enc === '') {
        chunk = s.decoder.write(chunk);
        if (chunk.length === 0) {
            maybeReadMore(stream, s);
            return canPushMore(s);
        }
    }
    addChunk(stream, s, chunk, false);
    return canPushMore(s);
}

function readableAddChunkPushObjectMode(stream: Readable, s: ReadableState, chunk: any): boolean {
    if (chunk === null) {
        s.reading = false;
        onEofChunk(stream, s);
        return false;
    }
    if (s.ended) {
        errorOrDestroy(stream, new StreamError('ERR_STREAM_PUSH_AFTER_EOF', 'stream.push() after EOF'), false);
        return false;
    }
    if (s.destroyed || s.errored) return false;
    s.reading = false;
    addChunk(stream, s, chunk, false);
    return canPushMore(s);
}

function addChunk(stream: Readable, s: ReadableState, chunk: any, addToFront: boolean): void {
    if (s.flowing && !s.sync && s.dataListening && s.length === 0) {
        s.awaitDrainWriters = [];
        s.dataEmitted = true;
        stream.emit('data', chunk);
    } else {
        s.length += s.objectMode ? 1 : chunk.length;
        if (addToFront) s.buffer.unshift(chunk);
        else s.buffer.push(chunk);
        if (s.needReadable) emitReadable(stream);
    }
    maybeReadMore(stream, s);
}

function howMuchToRead(n: number, s: ReadableState): number {
    if (n <= 0 || (s.length === 0 && s.ended)) return 0;
    if (s.objectMode) return 1;
    if (Number.isNaN(n)) {
        if (s.flowing && s.length > 0) return s.buffer[0].length;
        return s.length;
    }
    if (n <= s.length) return n;
    return s.ended ? s.length : 0;
}

function computeNewHighWaterMark(n: number): number {
    let v = n - 1;
    let p = 1;
    while (p <= v) p = p * 2;
    return p;
}

function readImpl(stream: Readable, size: number | undefined): any {
    const s = stream._readableState!;
    let n = size === undefined ? NaN : Math.trunc(size);
    const nOrig = n;
    if (n > s.highWaterMark) s.highWaterMark = computeNewHighWaterMark(n);
    if (n !== 0) s.emittedReadable = false;
    if (n === 0 && s.needReadable && ((s.highWaterMark !== 0 ? s.length >= s.highWaterMark : s.length > 0) || s.ended)) {
        if (s.length === 0 && s.ended) endReadable(stream);
        else emitReadable(stream);
        return null;
    }
    n = howMuchToRead(n, s);
    if (n === 0 && s.ended) {
        if (s.length === 0) endReadable(stream);
        return null;
    }
    let doRead = s.needReadable;
    if (s.length === 0 || s.length - n < s.highWaterMark) doRead = true;
    if (s.reading || s.ended || s.destroyed || s.errored || !s.constructed) {
        doRead = false;
    } else if (doRead) {
        s.reading = true;
        s.sync = true;
        if (s.length === 0) s.needReadable = true;
        try {
            stream._read(s.highWaterMark);
        } catch (err) {
            errorOrDestroy(stream, err as Error, false);
        }
        s.sync = false;
        if (!s.reading) n = howMuchToRead(nOrig, s);
    }
    let ret: any = null;
    if (n > 0) ret = fromList(n, s);
    if (ret === null) {
        if (s.length <= s.highWaterMark) s.needReadable = true;
        n = 0;
    } else {
        s.length -= n;
        s.awaitDrainWriters = [];
    }
    if (s.length === 0) {
        if (!s.ended) s.needReadable = true;
        if (nOrig !== n && s.ended) endReadable(stream);
    }
    if (ret !== null && !s.errorEmitted && !s.closeEmitted) {
        s.dataEmitted = true;
        stream.emit('data', ret);
    }
    return ret;
}

function fromList(n: number, s: ReadableState): any {
    if (s.length === 0) return null;
    const buf = s.buffer;
    if (s.objectMode) return buf.shift();
    if (s.decoder !== null) {
        // Decoded strings.
        if (n >= s.length) {
            let all = '';
            for (const c of buf) all += c;
            s.buffer = [];
            return all;
        }
        const head: string = buf[0];
        if (n < head.length) {
            buf[0] = head.slice(n);
            return head.slice(0, n);
        }
        if (n === head.length) return buf.shift();
        let text = '';
        let want = n;
        while (buf.length > 0) {
            const str: string = buf[0];
            if (want > str.length) {
                text += str;
                want -= str.length;
                buf.shift();
            } else {
                if (want === str.length) {
                    text += str;
                    buf.shift();
                } else {
                    text += str.slice(0, want);
                    buf[0] = str.slice(want);
                }
                break;
            }
        }
        return text;
    }
    // Buffers.
    if (n >= s.length) {
        if (buf.length === 1) return buf.shift();
        const parts: Buffer[] = [];
        for (const c of buf) parts.push(c);
        s.buffer = [];
        return Buffer.concat(parts);
    }
    const first: Buffer = buf[0];
    if (n < first.length) {
        buf[0] = first.subarray(n);
        return first.subarray(0, n);
    }
    if (n === first.length) return buf.shift();
    const parts: Buffer[] = [];
    let want = n;
    while (buf.length > 0) {
        const data: Buffer = buf[0];
        if (want > data.length) {
            parts.push(data);
            want -= data.length;
            buf.shift();
        } else {
            if (want === data.length) {
                parts.push(data);
                buf.shift();
            } else {
                parts.push(data.subarray(0, want));
                buf[0] = data.subarray(want);
            }
            break;
        }
    }
    return Buffer.concat(parts);
}

function onEofChunk(stream: Readable, s: ReadableState): void {
    if (s.ended) return;
    if (s.decoder !== null) {
        const chunk = s.decoder.end();
        if (chunk.length > 0) {
            s.buffer.push(chunk);
            s.length += s.objectMode ? 1 : chunk.length;
        }
    }
    s.ended = true;
    if (s.sync) {
        emitReadable(stream);
    } else {
        s.needReadable = false;
        s.emittedReadable = true;
        emitReadableNT(stream);
    }
}

function emitReadable(stream: Readable): void {
    const s = stream._readableState!;
    s.needReadable = false;
    if (!s.emittedReadable) {
        s.emittedReadable = true;
        process.nextTick(() => { emitReadableNT(stream); });
    }
}

function emitReadableNT(stream: Readable): void {
    const s = stream._readableState!;
    if (!s.destroyed && !s.errored && (s.length > 0 || s.ended)) {
        stream.emit('readable');
        s.emittedReadable = false;
    }
    if (!s.flowing && !s.ended && s.length <= s.highWaterMark) s.needReadable = true;
    flow(stream);
}

function maybeReadMore(stream: Readable, s: ReadableState): void {
    if (!s.readingMore && !s.reading && s.constructed) {
        s.readingMore = true;
        process.nextTick(() => { maybeReadMoreNT(stream, s); });
    }
}

function maybeReadMoreNT(stream: Readable, s: ReadableState): void {
    while (!s.reading && !s.ended && (s.length < s.highWaterMark || (s.flowing && s.length === 0))) {
        const len = s.length;
        stream.read(0);
        if (len === s.length) break;
    }
    s.readingMore = false;
}

function updateReadableListening(stream: Readable): void {
    const s = stream._readableState!;
    s.readableListening = stream.listenerCount('readable') > 0;
    if (s.hasPaused && !s.paused && s.resumeScheduled) {
        s.hasFlowing = true;
        s.flowing = true;
    } else if (s.dataListening) {
        stream.resume();
    } else if (!s.readableListening) {
        s.hasFlowing = false;
        s.flowing = false;
    }
}

function resumeNT(stream: Readable, s: ReadableState): void {
    if (!s.reading) stream.read(0);
    s.resumeScheduled = false;
    stream.emit('resume');
    flow(stream);
    if (s.flowing && !s.reading) stream.read(0);
}

function flow(stream: Readable): void {
    const s = stream._readableState!;
    while (s.flowing && stream.read() !== null) {
        // read() emits 'data'
    }
}

function endReadable(stream: Readable): void {
    const s = stream._readableState!;
    if (!s.endEmitted) {
        s.ended = true;
        process.nextTick(() => { endReadableNT(s, stream); });
    }
}

function endReadableNT(s: ReadableState, stream: Readable): void {
    if (!s.errored && !s.closeEmitted && !s.endEmitted && s.length === 0) {
        s.endEmitted = true;
        stream.emit('end');
        const w = stream._writableState;
        if (w !== null && isWritableSide(stream) && !stream.allowsHalfOpen()) {
            process.nextTick(() => {
                const w2 = stream._writableState!;
                if (isWritableSide(stream) && !w2.ending && !w2.destroyed) stream._kmlEnd();
            });
        } else if (s.autoDestroy) {
            if (w === null || (w.autoDestroy && (w.finished || w.writable === false))) stream.destroy();
        }
    }
}

// Whether the stream's write side is open (Node's `stream.writable`).
function isWritableSide(stream: Stream): boolean {
    const w = stream._writableState;
    return w !== null && w.writable && !w.destroyed && !w.errored && !w.ending;
}

// Readable.prototype.pipe: src into dest, pausing on a full destination and
// resuming on its 'drain'. Node's closure web of listeners is this object;
// each listener is a field so that it can be removed.
function pipeTo(src: Readable, dest: Stream, end: boolean): void {
    const s = src._readableState!;
    if (s.pipes.length === 1) s.multiAwaitDrain = true;
    s.pipes.push(dest);
    const p = new Pipe(src, dest);
    const endFn = end && !isStdio(dest) ? p.onend : p.unpipe;
    if (s.endEmitted) process.nextTick(endFn);
    else src.once('end', endFn);
    dest.on('unpipe', p.onunpipe);
    src.on('data', p.ondata);
    dest.prependListener('error', p.onerror);
    dest.once('close', p.onclose);
    dest.once('finish', p.onfinish);
    dest.emit('pipe', src);
    const ws = dest._writableState;
    if (ws !== null && ws.needDrain) p.pause();
    else if (!s.flowing) src.resume();
}

class Pipe {
    src: Readable;
    dest: Stream;
    cleanedUp = false;
    ondrain: (() => void) | null = null;
    onend: () => void;
    unpipe: () => void;
    ondata: (chunk: any) => void;
    onerror: (er: Error) => void;
    onclose: () => void;
    onfinish: () => void;
    onunpipe: (readable: any, info: any) => void;
    constructor(src: Readable, dest: Stream) {
        this.src = src;
        this.dest = dest;
        this.onend = () => { dest._kmlEnd(); };
        this.unpipe = () => { src.unpipe(dest); };
        this.ondata = (chunk: any) => {
            const ret = dest._kmlWrite(chunk);
            if (ret === false) this.pause();
        };
        this.onerror = (er: Error) => {
            this.unpipe();
            dest.removeListener('error', this.onerror);
            if (dest.listenerCount('error') === 0) {
                const ds = dest._writableState;
                if (ds !== null && !ds.errorEmitted) errorOrDestroy(dest, er, false);
                else dest.emit('error', er);
            }
        };
        this.onclose = () => {
            dest.removeListener('finish', this.onfinish);
            this.unpipe();
        };
        this.onfinish = () => {
            dest.removeListener('close', this.onclose);
            this.unpipe();
        };
        this.onunpipe = (readable: any, info: any) => {
            if (readable === src && info && info.hasUnpiped === false) {
                info.hasUnpiped = true;
                this.cleanup();
            }
        };
    }
    cleanup(): void {
        const src = this.src;
        const dest = this.dest;
        dest.removeListener('close', this.onclose);
        dest.removeListener('finish', this.onfinish);
        const ondrain = this.ondrain;
        if (ondrain !== null) dest.removeListener('drain', ondrain);
        dest.removeListener('error', this.onerror);
        dest.removeListener('unpipe', this.onunpipe);
        src.removeListener('end', this.onend);
        src.removeListener('end', this.unpipe);
        src.removeListener('data', this.ondata);
        this.cleanedUp = true;
        const ws = dest._writableState;
        if (ondrain !== null && src._readableState!.awaitDrainWriters.length > 0 && (ws === null || ws.needDrain)) ondrain();
    }
    pause(): void {
        const src = this.src;
        const dest = this.dest;
        const s = src._readableState!;
        if (!this.cleanedUp) {
            if (s.pipes.length === 1 && s.pipes[0] === dest) {
                s.awaitDrainWriters = [dest];
                s.multiAwaitDrain = false;
            } else if (s.pipes.length > 1 && s.pipes.includes(dest) && !s.awaitDrainWriters.includes(dest)) {
                s.awaitDrainWriters.push(dest);
            }
            src.pause();
        }
        if (this.ondrain === null) {
            const ondrain = () => {
                const i = s.awaitDrainWriters.indexOf(dest);
                if (i !== -1) s.awaitDrainWriters.splice(i, 1);
                if (s.awaitDrainWriters.length === 0 && s.dataListening) src.resume();
            };
            this.ondrain = ondrain;
            dest.on('drain', ondrain);
        }
    }
}

function isStdio(dest: Stream): boolean {
    return false;
}

// ---- from.js ----

// Readable.from(iterable): a readable in object mode (unless the options say
// otherwise) over an array's elements, each pushed as the stream asks for
// it; a value that is a promise is awaited first. A string or a Buffer is
// one chunk.
function readableFrom(iterable: any, options?: ReadableOptions): Readable {
    if (typeof iterable === 'string' || Buffer.isBuffer(iterable)) {
        return new Readable({
            objectMode: options?.objectMode ?? true,
            highWaterMark: options?.highWaterMark,
            read() {
                this.push(iterable);
                this.push(null);
            },
        });
    }
    if (iterable === null || iterable === undefined || typeof iterable.length !== 'number') {
        throw new StreamError('ERR_INVALID_ARG_TYPE', 'The "iterable" argument must be an instance of Iterable. Received ' + String(iterable));
    }
    const n: number = iterable.length;
    let index = 0;
    let reading = false;
    let isAsyncValues = false;
    const readable = new Readable({
        objectMode: options?.objectMode ?? true,
        highWaterMark: options?.highWaterMark ?? 1,
        read() {
            if (!reading) {
                reading = true;
                if (isAsyncValues) nextWithAsyncValues();
                else nextSyncWithSyncValues();
            }
        },
    });
    const pushValue = (value: any): boolean => {
        if (value === null) {
            reading = false;
            readable.destroy(new StreamError('ERR_STREAM_NULL_VALUES', 'May not write null values to stream'));
            return false;
        }
        if (readable.push(value)) return true;
        reading = false;
        return false;
    };
    const nextSyncWithSyncValues = (): void => {
        while (true) {
            if (index >= n) {
                readable.push(null);
                return;
            }
            const value = iterable[index];
            index++;
            if (value && typeof value.then === 'function') {
                isAsyncValues = true;
                awaitValue(value);
                return;
            }
            if (!pushValue(value)) return;
        }
    };
    const awaitValue = (value: any): void => {
        Promise.resolve(value).then((res: any) => {
            if (pushValue(res)) nextWithAsyncValues();
        }, (err: any) => {
            readable.destroy(err as Error);
        });
    };
    const nextWithAsyncValues = (): void => {
        while (true) {
            if (index >= n) {
                readable.push(null);
                return;
            }
            const value = iterable[index];
            index++;
            if (value && typeof value.then === 'function') {
                awaitValue(value);
                return;
            }
            if (!pushValue(value)) return;
        }
    };
    return readable;
}

// ---- writable.js ----

// kml:callable Writable — Node's is a function that constructs when called without `new`
export class Writable extends Stream {
    private writeImpl?: (this: Writable, chunk: any, encoding: BufferEncoding, callback: Callback) => void;
    private finalImpl?: (this: Writable, callback: Callback) => void;
    private destroyImpl?: (this: Writable, error: Error | null, callback: Callback) => void;

    constructor(options?: WritableOptions) {
        super(options);
        this._writableState = new WritableState(options, false);
        this.writeImpl = options?.write;
        this.finalImpl = options?.final;
        this.destroyImpl = options?.destroy;
        if (options?.construct) this._kmlConstructImpl = options.construct as (this: Stream, callback: (error?: Error | null) => void) => void;
        construct(this, () => { writableConstructed(this, this._writableState!); });
    }

    _write(chunk: any, encoding: BufferEncoding, callback: Callback): void {
        if (this.writeImpl) {
            this.writeImpl.call(this, chunk, encoding, callback);
            return;
        }
        throw new StreamError('ERR_METHOD_NOT_IMPLEMENTED', 'The _write() method is not implemented');
    }

    _final(callback: Callback): void {
        if (this.finalImpl) {
            this.finalImpl.call(this, callback);
            return;
        }
        super._final(callback);
    }

    _destroy(error: Error | null, callback: Callback): void {
        if (this.destroyImpl) {
            this.destroyImpl.call(this, error, callback);
            return;
        }
        callback(error);
    }

    get writable(): boolean { return isWritableSide(this); }
    get writableFinished(): boolean { return this._writableState!.finished; }
    get writableObjectMode(): boolean { return this._writableState!.objectMode; }
    get writableHighWaterMark(): number { return this._writableState!.highWaterMark; }
    get writableEnded(): boolean { return this._writableState!.ending; }
    get writableNeedDrain(): boolean {
        const w = this._writableState!;
        return !w.destroyed && !w.ending && w.needDrain;
    }
    get writableLength(): number { return this._writableState!.length; }
    get writableCorked(): number { return this._writableState!.corked; }
    get writableAborted(): boolean {
        const w = this._writableState!;
        return w.writable !== false && (w.destroyed || w.errored !== null) && !w.finished;
    }
    get destroyed(): boolean { return this._writableState!.destroyed; }
    get closed(): boolean { return this._writableState!.closed; }
    get errored(): Error | null { return this._writableState!.errored; }

    write(chunk: any, encoding?: BufferEncoding | Callback, cb?: Callback): boolean {
        if (typeof encoding === 'function') return writeChunk(this, chunk, undefined, encoding) === true;
        return writeChunk(this, chunk, encoding, cb) === true;
    }

    end(chunk?: any, encoding?: BufferEncoding | Callback, cb?: Callback): this {
        endWritable(this, chunk, encoding, cb);
        return this;
    }

    cork(): void { this._writableState!.corked++; }
    uncork(): void { uncorkStream(this); }

    setDefaultEncoding(encoding: BufferEncoding): this {
        this._writableState!.defaultEncoding = encoding;
        return this;
    }

    _kmlWrite(chunk: any): boolean { return this.write(chunk); }
    _kmlEnd(): void { this.end(); }

    static toWeb(writable: Writable): WritableStream<any> {
        return new WritableStream<any>({
            write(chunk) {
                return new Promise<void>((resolve) => { writable.write(chunk, () => { resolve(); }); });
            },
            close() {
                return new Promise<void>((resolve) => { writable.end(() => { resolve(); }); });
            },
        });
    }
}

// Writable.prototype.write's body: `true` or `false` as write() returns, or
// the error it reported.
function writeChunk(stream: Stream, chunk: any, encoding: BufferEncoding | undefined, cb: Callback | undefined): boolean | Error {
    const s = stream._writableState!;
    const callback: Callback = cb ?? nop;
    if (chunk === null) {
        throw new StreamError('ERR_STREAM_NULL_VALUES', 'May not write null values to stream');
    }
    let enc: string = encoding ?? s.defaultEncoding;
    if (!s.objectMode) {
        if (typeof chunk === 'string') {
            if (s.decodeStrings) {
                chunk = toBuffer(chunk, enc);
                enc = 'buffer';
            }
        } else if (Buffer.isBuffer(chunk)) {
            enc = 'buffer';
        } else if (ArrayBuffer.isView(chunk)) {
            chunk = Buffer.from(chunk as Uint8Array); // Stream._uint8ArrayToBuffer
            enc = 'buffer';
        } else {
            throw new StreamError('ERR_INVALID_ARG_TYPE', 'The "chunk" argument must be of type string or an instance of Buffer, TypedArray, or DataView.');
        }
    }
    let err: Error | null = null;
    if (s.ending) err = new StreamError('ERR_STREAM_WRITE_AFTER_END', 'write after end');
    else if (s.destroyed) err = new StreamError('ERR_STREAM_DESTROYED', 'Cannot call write after a stream was destroyed');
    if (err !== null) {
        const e = err;
        process.nextTick(() => { callback(e); });
        errorOrDestroy(stream, e, true);
        return e;
    }
    s.pendingcb++;
    return writeOrBuffer(stream, s, chunk, enc, callback);
}

function writeOrBuffer(stream: Stream, s: WritableState, chunk: any, encoding: string, callback: Callback): boolean {
    const len = s.objectMode ? 1 : chunk.length;
    s.length += len;
    if (s.writing || s.errored || s.corked > 0 || !s.constructed) {
        s.buffered.push({ chunk: chunk, encoding: encoding, callback: callback });
    } else {
        s.writelen = len;
        s.writecb = callback === nop ? null : callback;
        s.writing = true;
        s.sync = true;
        s.expectWriteCb = true;
        stream._write(chunk, encoding as BufferEncoding, (er?: Error | null) => { onwrite(stream, er); });
        s.sync = false;
    }
    const ret = s.length < s.highWaterMark || s.length === 0;
    if (!ret) s.needDrain = true;
    return ret && !s.destroyed && !s.errored;
}

function doWrite(stream: Stream, s: WritableState, len: number, chunk: any, encoding: string, cb: Callback): void {
    s.writelen = len;
    s.writecb = cb === nop ? null : cb;
    s.writing = true;
    s.sync = true;
    s.expectWriteCb = true;
    if (s.destroyed) onwrite(stream, new StreamError('ERR_STREAM_DESTROYED', 'Cannot call write after a stream was destroyed'));
    else stream._write(chunk, encoding as BufferEncoding, (er?: Error | null) => { onwrite(stream, er); });
    s.sync = false;
}

function onwriteError(stream: Stream, s: WritableState, er: Error, cb: Callback): void {
    --s.pendingcb;
    cb(er);
    errorBuffer(s);
    errorOrDestroy(stream, er, false);
}

function onwrite(stream: Stream, er: Error | null | undefined): void {
    const s = stream._writableState!;
    if (!s.expectWriteCb) {
        errorOrDestroy(stream, new StreamError('ERR_MULTIPLE_CALLBACK', 'Callback called multiple times'), false);
        return;
    }
    const sync = s.sync;
    const cb: Callback = s.writecb ?? nop;
    s.writecb = null;
    s.writing = false;
    s.expectWriteCb = false;
    s.length -= s.writelen;
    s.writelen = 0;
    if (er) {
        const e = er;
        if (!s.errored) s.errored = e;
        const r = stream._readableState;
        if (r !== null && !r.errored) r.errored = e;
        if (sync) process.nextTick(() => { onwriteError(stream, s, e, cb); });
        else onwriteError(stream, s, e, cb);
        return;
    }
    if (s.buffered.length > s.bufferedIndex) clearBuffer(stream, s);
    if (sync) {
        const needDrain = s.needDrain && s.length === 0;
        // Node writes this test as `(state[kState] & kDestroyed !== 0)`,
        // which reads the state's bit 0, object mode: an object-mode write
        // always completes on a tick.
        const needTick = needDrain || s.objectMode || cb !== nop;
        if (cb === nop) {
            if (!s.afterWritePending && needTick) {
                s.afterWritePending = true;
                process.nextTick(() => { afterWrite(stream, s, 1, cb); });
            } else {
                s.pendingcb--;
                if (s.ending) finishMaybe(stream, s, true);
            }
        } else if (s.afterWriteTickInfo !== null && s.afterWriteTickInfo.cb === cb) {
            s.afterWriteTickInfo.count++;
        } else if (needTick) {
            const info: AfterWriteTickInfo = { count: 1, cb: cb };
            s.afterWriteTickInfo = info;
            s.afterWritePending = true;
            process.nextTick(() => {
                s.afterWriteTickInfo = null;
                afterWrite(stream, s, info.count, info.cb);
            });
        } else {
            s.pendingcb--;
            if (s.ending) finishMaybe(stream, s, true);
        }
    } else {
        afterWrite(stream, s, 1, cb);
    }
}

function afterWrite(stream: Stream, s: WritableState, count: number, cb: Callback): void {
    s.afterWritePending = false;
    const needDrain = !s.ending && !s.destroyed && s.needDrain && s.length === 0;
    if (needDrain) {
        s.needDrain = false;
        stream.emit('drain');
    }
    while (count-- > 0) {
        s.pendingcb--;
        cb(null);
    }
    if (s.destroyed) errorBuffer(s);
    if (s.ending) finishMaybe(stream, s, true);
}

function errorBuffer(s: WritableState): void {
    if (s.writing) return;
    for (let n = s.bufferedIndex; n < s.buffered.length; ++n) {
        const b = s.buffered[n];
        s.length -= s.objectMode ? 1 : b.chunk.length;
        b.callback(s.errored ?? new StreamError('ERR_STREAM_DESTROYED', 'Cannot call write after a stream was destroyed'));
    }
    callFinishedCallbacks(s, s.errored ?? new StreamError('ERR_STREAM_DESTROYED', 'Cannot call end after a stream was destroyed'));
    s.buffered = [];
    s.bufferedIndex = 0;
}

function clearBuffer(stream: Stream, s: WritableState): void {
    if (s.destroyed || s.bufferProcessing || s.corked > 0 || !s.constructed) return;
    if (s.buffered.length - s.bufferedIndex === 0) return;
    let i = s.bufferedIndex;
    s.bufferProcessing = true;
    do {
        const b = s.buffered[i++];
        doWrite(stream, s, s.objectMode ? 1 : b.chunk.length, b.chunk, b.encoding, b.callback);
    } while (i < s.buffered.length && !s.writing);
    if (i === s.buffered.length) {
        s.buffered = [];
        s.bufferedIndex = 0;
    } else {
        s.bufferedIndex = i;
    }
    s.bufferProcessing = false;
}

function uncorkStream(stream: Stream): void {
    const s = stream._writableState!;
    if (s.corked > 0) {
        s.corked--;
        if (s.corked === 0 && !s.writing) clearBuffer(stream, s);
    }
}

function endWritable(stream: Stream, chunk: any, encoding: BufferEncoding | Callback | undefined, cb: Callback | undefined): void {
    const s = stream._writableState!;
    let callback: Callback | undefined = cb;
    let enc: BufferEncoding | undefined = undefined;
    if (typeof chunk === 'function') {
        callback = chunk;
        chunk = null;
    } else if (typeof encoding === 'function') {
        callback = encoding;
    } else {
        enc = encoding;
    }
    let err: Error | null = null;
    if (chunk !== null && chunk !== undefined) {
        const ret = writeChunk(stream, chunk, enc, undefined);
        if (ret instanceof Error) err = ret;
    }
    if (s.corked > 0) {
        s.corked = 1;
        uncorkStream(stream);
    }
    if (err !== null) {
        // Nothing more: the write reported it.
    } else if (!s.ending && !s.errored) {
        s.ending = true;
        finishMaybe(stream, s, true);
        s.ended = true;
    } else if (s.finished) {
        err = new StreamError('ERR_STREAM_ALREADY_FINISHED', 'Cannot call end after a stream was finished');
    } else if (s.destroyed) {
        err = new StreamError('ERR_STREAM_DESTROYED', 'Cannot call end after a stream was destroyed');
    }
    if (callback) {
        const cbv = callback;
        if (err !== null) {
            const e = err;
            process.nextTick(() => { cbv(e); });
        } else if (s.errored) {
            const e = s.errored;
            process.nextTick(() => { cbv(e); });
        } else if (s.finished) {
            process.nextTick(() => { cbv(null); });
        } else {
            s.onFinished.push(cbv);
        }
    }
}

// WritableState's onConstructed.
function writableConstructed(stream: Stream, s: WritableState): void {
    if (!s.writing) clearBuffer(stream, s);
    if (s.ending) finishMaybe(stream, s, false);
}

function needFinish(s: WritableState): boolean {
    return s.ending && s.constructed && !s.destroyed && !s.finished && !s.writing && !s.errorEmitted &&
        !s.closeEmitted && !s.errored && s.buffered.length - s.bufferedIndex === 0 && s.length === 0;
}

function onFinish(stream: Stream, s: WritableState, err: Error | null | undefined): void {
    if (s.prefinished) {
        errorOrDestroy(stream, err ?? new StreamError('ERR_MULTIPLE_CALLBACK', 'Callback called multiple times'), false);
        return;
    }
    s.pendingcb--;
    if (err) {
        callFinishedCallbacks(s, err);
        errorOrDestroy(stream, err, s.sync);
    } else if (needFinish(s)) {
        s.prefinished = true;
        stream.emit('prefinish');
        s.pendingcb++;
        process.nextTick(() => { finish(stream, s); });
    }
}

function prefinish(stream: Stream, s: WritableState): void {
    if (s.prefinished || s.finalCalled) return;
    if (!s.destroyed) {
        s.finalCalled = true;
        s.sync = true;
        s.pendingcb++;
        stream._kmlNoFinal = false;
        try {
            stream._final((err?: Error | null) => { onFinish(stream, s, err); });
        } catch (err) {
            onFinish(stream, s, err as Error);
        }
        s.sync = false;
        if (stream._kmlNoFinal) {
            // No `_final` of its own: prefinish now.
            s.pendingcb--;
            s.prefinished = true;
            stream.emit('prefinish');
        }
    } else {
        s.finalCalled = true;
        s.prefinished = true;
        stream.emit('prefinish');
    }
}

function finishMaybe(stream: Stream, s: WritableState, sync: boolean): void {
    if (!needFinish(s)) return;
    prefinish(stream, s);
    if (s.pendingcb !== 0) return;
    if (sync) {
        s.pendingcb++;
        process.nextTick(() => {
            if (needFinish(s)) finish(stream, s);
            else s.pendingcb--;
        });
    } else if (needFinish(s)) {
        s.pendingcb++;
        finish(stream, s);
    }
}

function finish(stream: Stream, s: WritableState): void {
    s.pendingcb--;
    s.finished = true;
    callFinishedCallbacks(s, null);
    stream.emit('finish');
    if (s.autoDestroy) {
        const r = stream._readableState;
        if (r === null || (r.autoDestroy && (r.endEmitted || r.readable === false))) stream.destroy();
    }
}

function callFinishedCallbacks(s: WritableState, err: Error | null): void {
    const cbs = s.onFinished;
    s.onFinished = [];
    for (const cb of cbs) cb(err);
}

// ---- duplex.js ----

// kml:callable Duplex — Node's is a function that constructs when called without `new`
export class Duplex extends Readable {
    private dWriteImpl?: (this: Duplex, chunk: any, encoding: BufferEncoding, callback: Callback) => void;
    private dFinalImpl?: (this: Duplex, callback: Callback) => void;
    allowHalfOpen: boolean;

    constructor(options?: DuplexOptions) {
        super();
        this._readableState = new ReadableState(options, true);
        this._writableState = new WritableState(options, true);
        this.allowHalfOpen = options?.allowHalfOpen !== false;
        if (options?.readable === false) {
            const r = this._readableState!;
            r.readable = false;
            r.ended = true;
            r.endEmitted = true;
        }
        if (options?.writable === false) {
            const w = this._writableState!;
            w.writable = false;
            w.ending = true;
            w.ended = true;
            w.finished = true;
        }
        this.dReadImpl = options?.read;
        this.dDestroyImpl = options?.destroy;
        this.dWriteImpl = options?.write;
        this.dFinalImpl = options?.final;
        if (options?.construct) this._kmlConstructImpl = options.construct as (this: Stream, callback: (error?: Error | null) => void) => void;
        construct(this, () => {
            const r = this._readableState!;
            if (r.needReadable) maybeReadMore(this, r);
            writableConstructed(this, this._writableState!);
        });
    }

    _kmlIsDuplex(): boolean { return true; }

    private dReadImpl?: (this: Duplex, size: number) => void;
    private dDestroyImpl?: (this: Duplex, error: Error | null, callback: Callback) => void;

    _read(size: number): void {
        if (this.dReadImpl) {
            this.dReadImpl.call(this, size);
            return;
        }
        throw new StreamError('ERR_METHOD_NOT_IMPLEMENTED', 'The _read() method is not implemented');
    }

    _destroy(error: Error | null, callback: Callback): void {
        if (this.dDestroyImpl) {
            this.dDestroyImpl.call(this, error, callback);
            return;
        }
        callback(error);
    }

    _write(chunk: any, encoding: BufferEncoding, callback: Callback): void {
        if (this.dWriteImpl) {
            this.dWriteImpl.call(this, chunk, encoding, callback);
            return;
        }
        throw new StreamError('ERR_METHOD_NOT_IMPLEMENTED', 'The _write() method is not implemented');
    }

    _final(callback: Callback): void {
        if (this.dFinalImpl) {
            this.dFinalImpl.call(this, callback);
            return;
        }
        super._final(callback);
    }

    allowsHalfOpen(): boolean { return this.allowHalfOpen; }

    get writable(): boolean { return isWritableSide(this); }
    get writableFinished(): boolean { return this._writableState!.finished; }
    get writableObjectMode(): boolean { return this._writableState!.objectMode; }
    get writableHighWaterMark(): number { return this._writableState!.highWaterMark; }
    get writableEnded(): boolean { return this._writableState!.ending; }
    get writableNeedDrain(): boolean {
        const w = this._writableState!;
        return !w.destroyed && !w.ending && w.needDrain;
    }
    get writableLength(): number { return this._writableState!.length; }
    get writableCorked(): number { return this._writableState!.corked; }
    get destroyed(): boolean { return this._readableState!.destroyed && this._writableState!.destroyed; }

    write(chunk: any, encoding?: BufferEncoding | Callback, cb?: Callback): boolean {
        if (typeof encoding === 'function') return writeChunk(this, chunk, undefined, encoding) === true;
        return writeChunk(this, chunk, encoding, cb) === true;
    }

    end(chunk?: any, encoding?: BufferEncoding | Callback, cb?: Callback): this {
        endWritable(this, chunk, encoding, cb);
        return this;
    }

    cork(): void { this._writableState!.corked++; }
    uncork(): void { uncorkStream(this); }

    setDefaultEncoding(encoding: BufferEncoding): this {
        this._writableState!.defaultEncoding = encoding;
        return this;
    }

    _kmlWrite(chunk: any): boolean { return this.write(chunk); }
    _kmlEnd(): void { this.end(); }
}

// ---- transform.js, passthrough.js ----

// kml:callable Transform — Node's is a function that constructs when called without `new`
export class Transform extends Duplex {
    private transformImpl?: (this: Transform, chunk: any, encoding: BufferEncoding, callback: TransformCallback) => void;
    private flushImpl?: (this: Transform, callback: TransformCallback) => void;
    private pendingCallback: Callback | null = null;

    constructor(options?: TransformOptions) {
        super(options);
        this._readableState!.sync = false;
        this.transformImpl = options?.transform;
        this.flushImpl = options?.flush;
    }

    _transform(chunk: any, encoding: BufferEncoding, callback: TransformCallback): void {
        if (this.transformImpl) {
            this.transformImpl.call(this, chunk, encoding, callback);
            return;
        }
        throw new StreamError('ERR_METHOD_NOT_IMPLEMENTED', 'The _transform() method is not implemented');
    }

    _flush(callback: TransformCallback): void {
        if (this.flushImpl) {
            this.flushImpl.call(this, callback);
            return;
        }
        callback();
    }

    _final(callback: Callback): void {
        if (!this.destroyed) {
            this._flush((er?: Error | null, data?: any) => {
                if (er) {
                    callback(er);
                    return;
                }
                if (data !== undefined && data !== null) this.push(data);
                this.push(null);
                callback();
            });
        } else {
            this.push(null);
            callback();
        }
    }

    _write(chunk: any, encoding: BufferEncoding, callback: Callback): void {
        const r = this._readableState!;
        const w = this._writableState!;
        const length = r.length;
        this._transform(chunk, encoding, (err?: Error | null, val?: any) => {
            if (err) {
                callback(err);
                return;
            }
            if (val !== undefined && val !== null) this.push(val);
            if (r.ended) {
                process.nextTick(() => { callback(); });
            } else if (w.ended || length === r.length || r.length < r.highWaterMark) {
                callback();
            } else {
                this.pendingCallback = callback;
            }
        });
    }

    _read(size: number): void {
        const cb = this.pendingCallback;
        if (cb !== null) {
            this.pendingCallback = null;
            cb();
        }
    }
}

// kml:callable PassThrough — Node's is a function that constructs when called without `new`
export class PassThrough extends Transform {
    constructor(options?: TransformOptions) {
        super(options);
    }
    _transform(chunk: any, encoding: BufferEncoding, callback: TransformCallback): void {
        callback(null, chunk);
    }
}

// ---- end-of-stream.js ----

export interface FinishedOptions {
    error?: boolean;
    readable?: boolean;
    writable?: boolean;
}

// finished(stream[, options], callback): the callback runs once when the
// stream has ended (its read side), finished (its write side) and, when it
// will emit one, closed; or with the error it hit or `Premature close`.
// Returns a function removing its listeners.
export function finished(stream: Stream, options: FinishedOptions | ((err?: Error | null) => void), callback?: (err?: Error | null) => void): () => void {
    let cb: (err?: Error | null) => void = nop;
    let opts: FinishedOptions = {};
    if (typeof options === 'function') cb = options;
    else {
        opts = options;
        if (callback) cb = callback;
    }
    let called = false;
    const call = (err?: Error | null) => {
        if (called) return;
        called = true;
        if (err) cb(err);
        else cb();
    };
    const r = stream._readableState;
    const w = stream._writableState;
    const readable = opts.readable ?? isReadableNodeStream(stream);
    const writable = opts.writable ?? isWritableNodeStream(stream);
    let willEmitClose = willEmitCloseOf(stream) && isReadableNodeStream(stream) === readable && isWritableNodeStream(stream) === writable;
    let writableFinished = isWritableFinished(stream, false);
    let readableFinished = isReadableFinished(stream, false);
    const onfinish = () => {
        writableFinished = true;
        if (streamDestroyed(stream)) willEmitClose = false;
        if (willEmitClose && (!isReadableSide(stream) || readable)) return;
        if (!readable || readableFinished) call();
    };
    const onend = () => {
        readableFinished = true;
        if (streamDestroyed(stream)) willEmitClose = false;
        if (willEmitClose && (!isWritableSide(stream) || writable)) return;
        if (!writable || writableFinished) call();
    };
    const onerror = (err: Error) => { call(err); };
    let closed = (w !== null && w.closed) || (r !== null && r.closed);
    const onclose = () => {
        closed = true;
        const errored = (w !== null ? w.errored : null) ?? (r !== null ? r.errored : null);
        if (errored) {
            call(errored);
            return;
        }
        if (readable && !readableFinished && isReadableNodeStream(stream)) {
            if (!isReadableFinished(stream, false)) {
                call(new StreamError('ERR_STREAM_PREMATURE_CLOSE', 'Premature close'));
                return;
            }
        }
        if (writable && !writableFinished) {
            if (!isWritableFinished(stream, false)) {
                call(new StreamError('ERR_STREAM_PREMATURE_CLOSE', 'Premature close'));
                return;
            }
        }
        call();
    };
    const onclosed = () => {
        closed = true;
        const errored = (w !== null ? w.errored : null) ?? (r !== null ? r.errored : null);
        if (errored) {
            call(errored);
            return;
        }
        call();
    };
    stream.on('end', onend);
    stream.on('finish', onfinish);
    if (opts.error !== false) stream.on('error', onerror);
    stream.on('close', onclose);
    if (closed) {
        process.nextTick(onclose);
    } else if ((w !== null && w.errorEmitted) || (r !== null && r.errorEmitted)) {
        if (!willEmitClose) process.nextTick(onclosed);
    } else if (!readable && (!willEmitClose || isReadableSide(stream)) && (writableFinished || !isWritableSide(stream)) && (w === null || w.pendingcb === 0)) {
        process.nextTick(onclosed);
    } else if (!writable && (!willEmitClose || isWritableSide(stream)) && (readableFinished || !isReadableSide(stream))) {
        process.nextTick(onclosed);
    }
    return () => {
        cb = nop;
        stream.removeListener('finish', onfinish);
        stream.removeListener('end', onend);
        stream.removeListener('error', onerror);
        stream.removeListener('close', onclose);
    };
}

function isReadableNodeStream(stream: Stream): boolean {
    const r = stream._readableState;
    return r !== null && (stream._writableState === null || r.readable !== false);
}

function isWritableNodeStream(stream: Stream): boolean {
    const w = stream._writableState;
    return w !== null && (stream._readableState === null || w.writable !== false);
}

function isReadableFinished(stream: Stream, strict: boolean): boolean {
    const r = stream._readableState;
    if (r === null || r.errored) return false;
    return r.endEmitted || (!strict && r.ended && r.length === 0);
}

function isWritableFinished(stream: Stream, strict: boolean): boolean {
    const w = stream._writableState;
    if (w === null || w.errored) return false;
    return w.finished || (!strict && w.ended && w.length === 0);
}

function willEmitCloseOf(stream: Stream): boolean {
    const w = stream._writableState;
    if (w !== null) return w.autoDestroy && w.emitClose && !w.closed;
    const r = stream._readableState;
    return r !== null && r.autoDestroy && r.emitClose && !r.closed;
}

function streamDestroyed(stream: Stream): boolean {
    const w = stream._writableState;
    const r = stream._readableState;
    if (w !== null && r !== null) return w.destroyed && r.destroyed;
    return (w !== null && w.destroyed) || (r !== null && r.destroyed);
}

// Whether the read side is open (Node's `isReadable`).
function isReadableSide(stream: Stream): boolean {
    const r = stream._readableState;
    if (r === null || streamDestroyed(stream)) return false;
    return isReadableNodeStream(stream) && r.readable && !r.destroyed && !r.errorEmitted && !r.endEmitted && !isReadableFinished(stream, true);
}

// ---- pipeline.js ----

// pipeline(source, ...transforms, destination, callback): pipes each stream
// into the next; the callback runs once the last has finished, or with the
// first error, every stream then destroyed. Returns the last stream.
export function pipeline(...args: any[]): Stream {
    const callback = args[args.length - 1] as (err?: Error | null) => void;
    const streams: Stream[] = [];
    for (let i = 0; i < args.length - 1; i++) streams.push(args[i] as Stream);
    return pipelineImpl(streams, callback);
}

function pipelineImpl(streams: Stream[], callback: (err?: Error | null) => void): Stream {
    if (streams.length < 2) throw new StreamError('ERR_MISSING_ARGS', 'The "streams" argument must be specified');
    let error: Error | null = null;
    const destroys: ((err: Error | null) => void)[] = [];
    let finishCount = 0;
    const finishImpl = (err: Error | null | undefined, final: boolean) => {
        if (err && (error === null || (error as StreamError).code === 'ERR_STREAM_PREMATURE_CLOSE')) error = err;
        if (error === null && !final) return;
        while (destroys.length > 0) destroys.shift()!(error);
        if (final) {
            const e = error;
            process.nextTick(() => {
                if (e) callback(e);
                else callback();
            });
        }
    };
    const finish = (err?: Error | null) => { finishImpl(err, --finishCount === 0); };
    const finishOnlyHandleError = (err?: Error | null) => { finishImpl(err, false); };
    let ret: Stream = streams[0];
    for (let i = 0; i < streams.length; i++) {
        const stream = streams[i];
        const reading = i < streams.length - 1;
        const writing = i > 0;
        destroys.push(destroyer(stream, reading, writing));
        stream.on('error', (err: Error) => {
            if (err && (err as StreamError).code !== 'ERR_STREAM_PREMATURE_CLOSE') finishOnlyHandleError(err);
        });
        if (i > 0) {
            finishCount += 2;
            pipeWithFinish(ret, stream, finish, finishOnlyHandleError);
            ret = stream;
        }
    }
    return ret;
}

function destroyer(stream: Stream, reading: boolean, writing: boolean): (err: Error | null) => void {
    let done = false;
    stream.on('close', () => { done = true; });
    finished(stream, { readable: reading, writable: writing }, (err?: Error | null) => { done = !err; });
    return (err: Error | null) => {
        if (done) return;
        done = true;
        stream.destroy(err ?? new StreamError('ERR_STREAM_DESTROYED', 'Cannot call pipe after a stream was destroyed'));
    };
}

function pipeWithFinish(src: Stream, dst: Stream, finish: (err?: Error | null) => void, finishOnlyHandleError: (err?: Error | null) => void): void {
    let ended = false;
    dst.on('close', () => {
        if (!ended) finishOnlyHandleError(new StreamError('ERR_STREAM_PREMATURE_CLOSE', 'Premature close'));
    });
    src._kmlPipe(dst, false);
    const endFn = () => {
        ended = true;
        dst._kmlEnd();
    };
    if (isReadableFinished(src, true)) process.nextTick(endFn);
    else src.once('end', endFn);
    finished(src, { readable: true, writable: false }, (err?: Error | null) => {
        const r = src._readableState;
        if (err && (err as StreamError).code === 'ERR_STREAM_PREMATURE_CLOSE' && r !== null && r.ended && !r.errored && !r.errorEmitted) {
            src.once('end', () => { finish(); });
            src.once('error', (e: Error) => { finish(e); });
        } else {
            finish(err);
        }
    });
    finished(dst, { readable: false, writable: true }, finish);
}

export function duplexPair(options?: DuplexOptions): [Duplex, Duplex] {
    const a = new PairSide(options);
    const b = new PairSide(options);
    a.peer = b;
    b.peer = a;
    return [a, b];
}

// One side of duplexPair: what is written to it is readable from its peer.
// A write completes when the peer reads it, and the side finishes when the
// peer has ended, as Node's DuplexSide.
class PairSide extends Duplex {
    peer: PairSide | null = null;
    pendingCallback: Callback | null = null;
    _read(size: number): void {
        const cb = this.pendingCallback;
        if (cb !== null) {
            this.pendingCallback = null;
            cb();
        }
    }
    _write(chunk: any, encoding: BufferEncoding, callback: Callback): void {
        const peer = this.peer!;
        if (chunk.length === 0) {
            process.nextTick(() => { callback(); });
        } else {
            peer.push(chunk);
            peer.pendingCallback = callback;
        }
    }
    _final(callback: Callback): void {
        const peer = this.peer!;
        peer.on('end', () => { callback(); });
        peer.push(null);
    }
}

// stream/promises.
export const promises = {
    pipeline(...streams: Stream[]): Promise<void> {
        return new Promise<void>((resolve, reject) => {
            pipelineImpl(streams, (err?: Error | null) => {
                if (err) reject(err);
                else resolve();
            });
        });
    },
    finished(stream: Stream, options?: FinishedOptions): Promise<void> {
        return new Promise<void>((resolve, reject) => {
            finished(stream, options ?? {}, (err?: Error | null) => {
                if (err) reject(err);
                else resolve();
            });
        });
    },
};
