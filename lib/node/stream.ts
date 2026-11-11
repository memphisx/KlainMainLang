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
import { NodeError, NodeTypeError, NodeRangeError } from './internal_errors';

export type Callback = (error?: any) => void;
export type TransformCallback = (error?: any, data?: any) => void;

export interface StreamOptions {
    signal?: AbortSignal;
    highWaterMark?: number;
    objectMode?: boolean;
    autoDestroy?: boolean;
    emitClose?: boolean;
}

export interface ReadableOptions extends StreamOptions {
    encoding?: BufferEncoding;
    construct?: (this: Readable, callback: (error?: any) => void) => void;
    read?: (this: Readable, size: number) => void;
    destroy?: (this: Readable, error: any, callback: Callback) => void;
}

export interface WritableOptions extends StreamOptions {
    decodeStrings?: boolean;
    construct?: (this: Writable, callback: (error?: any) => void) => void;
    defaultEncoding?: BufferEncoding;
    write?: (this: Writable, chunk: any, encoding: BufferEncoding, callback: Callback) => void;
    writev?: (this: Writable, chunks: any[], callback: Callback) => void;
    final?: (this: Writable, callback: Callback) => void;
    destroy?: (this: Writable, error: any, callback: Callback) => void;
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
    construct?: (this: Duplex, callback: (error?: any) => void) => void;
    read?: (this: Duplex, size: number) => void;
    write?: (this: Duplex, chunk: any, encoding: BufferEncoding, callback: Callback) => void;
    final?: (this: Duplex, callback: Callback) => void;
    destroy?: (this: Duplex, error: any, callback: Callback) => void;
}

export interface TransformOptions extends DuplexOptions {
    transform?: (this: Transform, chunk: any, encoding: BufferEncoding, callback: TransformCallback) => void;
    flush?: (this: Transform, callback: TransformCallback) => void;
}

// A Node error with its `code` (ERR_STREAM_*).
// Node's coded errors (internal/errors): each code's own class.
const streamTypeErrors = ['ERR_INVALID_ARG_TYPE', 'ERR_INVALID_ARG_VALUE', 'ERR_INVALID_RETURN_VALUE', 'ERR_MISSING_ARGS', 'ERR_STREAM_NULL_VALUES'];
function streamError(code: string, message: string): any {
    if (code === 'ERR_OUT_OF_RANGE') return NodeRangeError(code, message);
    if (streamTypeErrors.includes(code)) return NodeTypeError(code, message);
    return NodeError(code, message);
}

// Node's AbortError (internal/errors): what an aborted stream operation
// rejects or errors with.
class AbortError extends Error {
    code: string;
    cause: any;
    constructor(message: string = 'The operation was aborted', cause?: any) {
        super(message);
        this.name = 'AbortError';
        this.code = 'ABORT_ERR';
        this.cause = cause;
    }
}

export interface OperatorOptions {
    signal?: AbortSignal;
    concurrency?: number;
    highWaterMark?: number;
}

function nop(error?: any): void {}

let defaultObjectHWM = 16;
let defaultByteHWM = -1;

function defaultHighWaterMark(objectMode: boolean): number {
    if (objectMode) return defaultObjectHWM;
    if (defaultByteHWM < 0) defaultByteHWM = process.platform === 'win32' ? 16 * 1024 : 64 * 1024;
    return defaultByteHWM;
}

export function getDefaultHighWaterMark(objectMode: boolean): number {
    return defaultHighWaterMark(objectMode);
}

// Node's validateInteger(value, 'value', 0).
export function setDefaultHighWaterMark(objectMode: boolean, value: number): void {
    const v: any = value;
    if (typeof v !== 'number') {
        throw streamError('ERR_INVALID_ARG_TYPE', 'The "value" argument must be of type number.' + describeReceived(v));
    }
    if (!Number.isInteger(v) || v < 0) {
        const e: any = new RangeError('The value of "value" is out of range. It must be ' +
            (Number.isInteger(v) ? '>= 0 && <= 9007199254740991' : 'an integer') + '. Received ' + String(v));
        e.code = 'ERR_OUT_OF_RANGE';
        throw e;
    }
    if (objectMode) defaultObjectHWM = v;
    else defaultByteHWM = v;
}

// Node's stream utils: null for a value that is not a stream of that kind.
export function isReadable(stream: any): boolean | null {
    if (stream === null || typeof stream !== 'object' || typeof stream._readableState !== 'object' || stream._readableState === null) return null;
    if (stream.destroyed) return false;
    const st = stream._readableState;
    return stream.readable !== false && !(st.endEmitted || (st.ended && st.length === 0));
}

export function isWritable(stream: any): boolean | null {
    if (stream === null || typeof stream !== 'object' || typeof stream._writableState !== 'object' || stream._writableState === null) return null;
    if (stream.destroyed) return false;
    if (stream._writableState.ending) return false;
    return stream.writable !== false;
}

export function isErrored(stream: any): boolean {
    if (stream === null || typeof stream !== 'object') return false;
    const r = stream._readableState, w = stream._writableState;
    return !!(stream.readableErrored ?? stream.writableErrored ?? r?.errorEmitted ?? w?.errorEmitted ?? r?.errored ?? w?.errored);
}

export function isDisturbed(stream: any): boolean {
    if (stream === null || typeof stream !== 'object') return false;
    return !!(stream.readableDidRead || stream.readableAborted);
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
    errored: any = null;
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
    errored: any = null;
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
        callback(streamError('ERR_METHOD_NOT_IMPLEMENTED', 'The _write() method is not implemented'));
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
    _construct?(callback: (error?: any) => void): void;
    // Node's Writable.prototype._writev is null; a subclass or the
    // options (writev) give one, taking the buffered chunks at once.
    _writev?(chunks: any[], callback: Callback): void;
    _kmlWritevImpl: ((chunks: any[], callback: Callback) => void) | null = null;
    _kmlConstructImpl?: (this: Stream, callback: (error?: any) => void) => void;
    // kConstruct's listeners, and destroy()'s wait while constructing
    // (Node's kConstruct and kDestroy events).
    _kmlOnConstructed: (() => void)[] = [];
    _kmlOnDestroyReady: ((error?: any) => void) | null = null;
    // A Duplex constructs once, for both its sides.
    _kmlIsDuplex(): boolean { return false; }
    _destroy(error: any, callback: Callback): void {
        callback(error);
    }
    destroy(error?: any, callback?: Callback): this {
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

function destroyStream(stream: Stream, err: any, cb: Callback | undefined): void {
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
        stream._kmlOnDestroyReady = (er?: any) => {
            destroyNow(stream, aggregateTwoErrors(er ?? null, err), cb);
        };
        return;
    }
    destroyNow(stream, err, cb);
}

function aggregateTwoErrors(inner: any, outer: any): any {
    if (inner && outer && inner !== outer) {
        const e = new AggregateError([outer, inner], outer.message);
        (e as any).code = (outer as any).code;
        return e;
    }
    return inner ?? outer;
}

function destroyNow(stream: Stream, err: any, cb: Callback | undefined): void {
    const r = stream._readableState;
    const w = stream._writableState;
    let called = false;
    const onDestroy = (er?: any) => {
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
        onDestroy(e);
    }
}

function checkError(err: any, w: WritableState | null, r: ReadableState | null): void {
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

function emitErrorNT(stream: Stream, err: any): void {
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
    const onConstruct = (err?: any) => {
        if (called) {
            errorOrDestroy(stream, err ?? streamError('ERR_MULTIPLE_CALLBACK', 'Callback called multiple times'), false);
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
    const done = (err?: any) => {
        process.nextTick(() => { onConstruct(err); });
    };
    try {
        const impl = stream._kmlConstructImpl;
        if (impl !== undefined) impl.call(stream, done);
        else if (stream._construct) stream._construct(done);
    } catch (err) {
        process.nextTick(() => { onConstruct(err); });
    }
}

function errorOrDestroy(stream: Stream, err: any, sync: boolean): void {
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
    private destroyImpl?: (this: Readable, error: any, callback: Callback) => void;

    constructor(options?: ReadableOptions) {
        super(options);
        this._readableState = new ReadableState(options, false);
        this.readImpl = options?.read;
        this.destroyImpl = options?.destroy;
        if (options?.construct) this._kmlConstructImpl = options.construct as (this: Stream, callback: (error?: any) => void) => void;
        if (options?.signal) addAbortSignal(options.signal, this);
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

    // ---- operators.js: the stream-returning operators wrap their async
    // generator in Readable.from, as Node's Readable.prototype versions do.
    // Each operator as @types/node declares it; the implementation takes
    // any options, checked at run time as Node checks them.
    map(fn: any, options?: OperatorOptions): Readable;
    map(fn: any, options?: any): Readable {
        return Readable.from(mapOperator(this, fn, options));
    }
    filter(fn: any, options?: OperatorOptions): Readable;
    filter(fn: any, options?: any): Readable {
        return Readable.from(mapOperator(this, filterFn(fn), options));
    }
    flatMap(fn: any, options?: OperatorOptions): Readable;
    flatMap(fn: any, options?: any): Readable {
        return Readable.from(flatMapGen(mapOperator(this, fn, options)));
    }
    drop(number: any, options?: OperatorOptions): Readable;
    drop(number: any, options?: any): Readable {
        validateOperatorOptions(options);
        return Readable.from(dropGen(this, toIntegerOrInfinity(number), options));
    }
    take(number: any, options?: OperatorOptions): Readable;
    take(number: any, options?: any): Readable {
        validateOperatorOptions(options);
        return Readable.from(takeGen(this, toIntegerOrInfinity(number), options));
    }
    forEach(fn: any, options?: OperatorOptions): Promise<void>;
    async forEach(fn: any, options?: any): Promise<void> {
        validateFunction(fn, 'fn');
        const each = async (value: any, opts: any): Promise<any> => {
            await fn(value, opts);
            return kEmpty;
        };
        for await (const unused of mapOperator(this, each, options)) {
            // drained for its side effects
        }
    }
    toArray(options?: OperatorOptions): Promise<any[]>;
    async toArray(options?: any): Promise<any[]> {
        validateOperatorOptions(options);
        const result: any[] = [];
        for await (const val of this) {
            if (options?.signal?.aborted) throw new AbortError(undefined, options.signal.reason);
            result.push(val);
        }
        return result;
    }
    some(fn: any, options?: OperatorOptions): Promise<boolean>;
    async some(fn: any, options?: any): Promise<boolean> {
        for await (const unused of mapOperator(this, filterFn(fn), options)) {
            return true;
        }
        return false;
    }
    every(fn: any, options?: OperatorOptions): Promise<boolean>;
    async every(fn: any, options?: any): Promise<boolean> {
        validateFunction(fn, 'fn');
        const not = async (value: any, opts: any): Promise<boolean> => !(await fn(value, opts));
        return !(await this.some(not, options));
    }
    find(fn: any, options?: OperatorOptions): Promise<any>;
    async find(fn: any, options?: any): Promise<any> {
        for await (const result of mapOperator(this, filterFn(fn), options)) {
            return result;
        }
        return undefined;
    }
    async reduce(reducer: any, ...rest: any[]): Promise<any> {
        validateFunction(reducer, 'reducer');
        const options: OperatorOptions | undefined = rest.length > 1 ? rest[1] : undefined;
        validateOperatorOptions(options);
        let hasInitialValue = rest.length > 0;
        let initialValue: any = hasInitialValue ? rest[0] : undefined;
        if (options?.signal?.aborted) {
            const err = new AbortError(undefined, options.signal.reason);
            this.once('error', () => {});
            this.destroy(err);
            throw err;
        }
        const ac = new AbortController();
        const signal = ac.signal;
        if (options?.signal) {
            options.signal.addEventListener('abort', () => { ac.abort(); }, { once: true });
        }
        let gotAnyItemFromStream = false;
        try {
            for await (const value of this) {
                gotAnyItemFromStream = true;
                if (options?.signal?.aborted) throw new AbortError();
                if (!hasInitialValue) {
                    initialValue = value;
                    hasInitialValue = true;
                } else {
                    initialValue = await reducer(initialValue, value, { signal });
                }
            }
            if (!gotAnyItemFromStream && !hasInitialValue) {
                throw streamError('ERR_MISSING_ARGS', 'Reduce of an empty stream requires an initial value');
            }
        } finally {
            ac.abort();
        }
        return initialValue;
    }

    _kmlWrapRead: (() => void) | null = null;

    _read(size: number): void {
        if (this.readImpl) {
            this.readImpl.call(this, size);
            return;
        }
        if (this._kmlWrapRead !== null) {
            this._kmlWrapRead();
            return;
        }
        throw streamError('ERR_METHOD_NOT_IMPLEMENTED', 'The _read() method is not implemented');
    }

    _destroy(error: any, callback: Callback): void {
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
    get readableEncoding(): BufferEncoding | null { return this._readableState!.encoding as BufferEncoding | null; }
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
    get errored(): any { return this._readableState!.errored; }

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
        let error: any = undefined;
        let done = false;
        const cleanup = finished(this, { writable: false }, (err?: any) => {
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

    // Node's newReadableStreamFromStreamReadable: chunks queue under the
    // stream's high-water mark (counted, or by byte length), pausing the
    // readable when the queue is full and resuming it on pull; its end
    // closes the web stream, its error (a premature close as an
    // AbortError) errors it, and a cancel destroys it.
    static toWeb(readable: Readable, options?: any): ReadableStream<any> {
        const src: any = readable;
        if (src === null || typeof src !== 'object' || typeof src._readableState !== 'object') {
            throw streamError('ERR_INVALID_ARG_TYPE', 'The "streamReadable" argument must be an instance of Readable.' + describeReceived(src));
        }
        if (readable.destroyed || !readable.readable) {
            const dead = new ReadableStream<any>();
            dead.cancel();
            return dead;
        }
        const objectMode = readable.readableObjectMode;
        const highWaterMark = readable.readableHighWaterMark;
        const strategy: any = options?.strategy ?? (objectMode
            ? { highWaterMark, size: (): number => 1 }
            : { highWaterMark, size: (chunk: any): number => chunk.byteLength });
        let controller: any = null;
        const onData = (chunk: any): void => {
            const c = Buffer.isBuffer(chunk) && !objectMode ? new Uint8Array(chunk) : chunk;
            controller.enqueue(c);
            if (controller.desiredSize <= 0) readable.pause();
        };
        const cleanup = finished(readable, (error?: any) => {
            let err = error;
            if (err && err.code === 'ERR_STREAM_PREMATURE_CLOSE') err = new AbortError(undefined, err);
            cleanup();
            readable.removeListener('data', onData);
            if (err) {
                controller.error(err);
                return;
            }
            controller.close();
        });
        readable.on('data', onData);
        return new ReadableStream<any>({
            start(c) { controller = c; },
            pull() { readable.resume(); },
            cancel(reason) { readable.destroy(reason); },
        }, strategy);
    }

    // `await using`: destroyed with an AbortError unless it has ended,
    // settling once it is finished.
    [Symbol.asyncDispose](): Promise<void> {
        let error: any = undefined;
        if (!this.destroyed) {
            error = this.readableEnded ? null : new AbortError();
            this.destroy(error);
        }
        return new Promise<void>((resolve, reject) => {
            finished(this, (err?: any) => { if (err && err !== error) reject(err); else resolve(); });
        });
    }

    // Node's Readable.prototype.wrap: an old-style stream's data, end,
    // error and close events feed this one; reading resumes it, a full
    // buffer pauses it, and destroy destroys it.
    wrap(stream: any): this {
        let paused = false;
        stream.on('data', (chunk: any) => {
            if (!this.push(chunk) && typeof stream.pause === 'function') {
                paused = true;
                stream.pause();
            }
        });
        stream.on('end', () => { this.push(null); });
        stream.on('error', (err: any) => { errorOrDestroy(this, err, false); });
        stream.on('close', () => { this.destroy(); });
        stream.on('destroy', () => { this.destroy(); });
        this._kmlWrapRead = (): void => {
            if (paused && typeof stream.resume === 'function') {
                paused = false;
                stream.resume();
            }
        };
        const keys = Object.keys(stream);
        for (const k of keys) {
            if ((this as any)[k] === undefined && typeof stream[k] === 'function') {
                (this as any)[k] = stream[k].bind(stream);
            }
        }
        return this;
    }

    // Node's newStreamReadableFromReadableStream: each read takes the
    // reader's next chunk, a failed read destroys the stream, destroy
    // cancels the reader, and the web stream's error destroys it.
    static fromWeb(web: ReadableStream<any>, options?: ReadableOptions): Readable {
        const o: any = options ?? {};
        const reader: any = web.getReader();
        let closed = false;
        let r: Readable | null = null;
        r = new Readable({
            objectMode: o.objectMode ?? false,
            highWaterMark: o.highWaterMark,
            encoding: o.encoding,
            signal: o.signal,
            read() {
                reader.read().then((res: any) => {
                    if (res.done) r!.push(null);
                    else r!.push(res.value);
                }, (error: any) => { r!.destroy(error); });
            },
            destroy(error: any, callback: Callback): void {
                const done = (): void => {
                    try { callback(error); } catch (err) { process.nextTick(() => { throw err; }); }
                };
                if (!closed) {
                    reader.cancel(error).then(done, done);
                    return;
                }
                done();
            },
        });
        reader.closed.then(() => { closed = true; }, (error: any) => {
            closed = true;
            r!.destroy(error);
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
            errorOrDestroy(stream, streamError('ERR_INVALID_ARG_TYPE', 'The "chunk" argument must be of type string or an instance of Buffer, TypedArray, or DataView.'), false);
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
    if (s.endEmitted) errorOrDestroy(stream, streamError('ERR_STREAM_UNSHIFT_AFTER_END_EVENT', 'stream.unshift() after end event'), false);
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
        errorOrDestroy(stream, streamError('ERR_INVALID_ARG_TYPE', 'The "chunk" argument must be of type string or an instance of Buffer, TypedArray, or DataView.'), false);
        return false;
    }
    if (!chunk || chunk.length <= 0) {
        s.reading = false;
        maybeReadMore(stream, s);
        return canPushMore(s);
    }
    if (s.ended) {
        errorOrDestroy(stream, streamError('ERR_STREAM_PUSH_AFTER_EOF', 'stream.push() after EOF'), false);
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
        errorOrDestroy(stream, streamError('ERR_STREAM_PUSH_AFTER_EOF', 'stream.push() after EOF'), false);
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
            errorOrDestroy(stream, err, false);
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
    onerror: (er: any) => void;
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
        this.onerror = (er: any) => {
            this.unpipe();
            dest.removeListener('error', this.onerror);
            if (dest.listenerCount('error') === 0) {
                const ws = dest._writableState;
                const rs = dest._readableState;
                const emitted = ws !== null ? ws.errorEmitted : rs !== null ? rs.errorEmitted : true;
                if (!emitted) errorOrDestroy(dest, er, false);
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
// ---- operators.js ----

const kEmpty: any = Symbol('kEmpty');
const kEof: any = Symbol('kEof');

function validateFunction(fn: any, name: string): void {
    if (typeof fn !== 'function') {
        throw streamError('ERR_INVALID_ARG_TYPE', 'The "' + name + '" argument must be of type function. Received ' + describeReceived(fn));
    }
}

function validateOperatorOptions(options: any): void {
    if (options !== null && options !== undefined && typeof options !== 'object') {
        throw streamError('ERR_INVALID_ARG_TYPE', 'The "options" argument must be of type object. Received ' + describeReceived(options));
    }
    // validateAbortSignal(options.signal, 'options.signal').
    const signal: any = options?.signal;
    if (signal !== undefined && (signal === null || typeof signal !== 'object' || !('aborted' in signal))) {
        throw streamError('ERR_INVALID_ARG_TYPE', 'The "options.signal" property must be an instance of AbortSignal. Received ' + describeReceived(signal));
    }
}

function toIntegerOrInfinity(number: any): number {
    const n = Number(number);
    if (Number.isNaN(n)) return 0;
    if (n < 0) {
        throw streamError('ERR_OUT_OF_RANGE', 'The value of "number" is out of range. It must be >= 0. Received ' + String(n));
    }
    return n;
}

function filterFn(fn: any): any {
    validateFunction(fn, 'fn');
    return async (value: any, opts: any): Promise<any> => {
        if (await fn(value, opts)) return value;
        return kEmpty;
    };
}

// mapOperator validates map's arguments eagerly (a bad concurrency throws at
// the call, as Node's does) and returns the async generator that runs fn
// over the stream with up to `concurrency` calls in flight.
function mapOperator(stream: Readable, fn: any, options: any): AsyncGenerator<any> {
    validateFunction(fn, 'fn');
    validateOperatorOptions(options);
    let concurrency = 1;
    if (options?.concurrency !== undefined && options?.concurrency !== null) concurrency = Math.floor(options.concurrency);
    let highWaterMark = concurrency - 1;
    if (options?.highWaterMark !== undefined && options?.highWaterMark !== null) highWaterMark = Math.floor(options.highWaterMark);
    if (!(concurrency >= 1)) {
        throw streamError('ERR_OUT_OF_RANGE', 'The value of "options.concurrency" is out of range. It must be >= 1 && <= 4294967295. Received ' + String(concurrency));
    }
    if (!(highWaterMark >= 0)) {
        throw streamError('ERR_OUT_OF_RANGE', 'The value of "options.highWaterMark" is out of range. It must be >= 0 && <= 4294967295. Received ' + String(highWaterMark));
    }
    highWaterMark += concurrency;
    return mapGen(stream, fn, options, concurrency, highWaterMark);
}

async function* mapGen(stream: Readable, fn: any, options: any, concurrency: number, highWaterMark: number): AsyncGenerator<any> {
    const signal: AbortSignal = options?.signal ?? new AbortController().signal;
    const queue: any[] = [];
    const signalOpt = { signal };
    let next: any = null;
    let resume: any = null;
    let done = false;
    let cnt = 0;

    const maybeResume = (): void => {
        if (resume !== null && !done && cnt < concurrency && queue.length < highWaterMark) {
            const r = resume;
            resume = null;
            r();
        }
    };
    const afterItemProcessed = (): void => {
        cnt -= 1;
        maybeResume();
    };
    const onCatch = (): void => {
        done = true;
        afterItemProcessed();
    };
    const wakeNext = (): void => {
        if (next !== null) {
            const n = next;
            next = null;
            n();
        }
    };

    const pump = async (): Promise<void> => {
        try {
            for await (const item of stream) {
                if (done) return;
                if (signal.aborted) throw new AbortError();
                let val: any;
                try {
                    val = fn(item, signalOpt);
                    if (val === kEmpty) continue;
                    val = Promise.resolve(val);
                } catch (err) {
                    val = Promise.reject(err);
                }
                cnt += 1;
                val.then(afterItemProcessed, onCatch);
                queue.push(val);
                wakeNext();
                if (!done && (queue.length >= highWaterMark || cnt >= concurrency)) {
                    await new Promise<void>((resolve) => { resume = resolve; });
                }
            }
            queue.push(kEof);
        } catch (err) {
            const val: any = Promise.reject(err);
            val.then(afterItemProcessed, onCatch);
            queue.push(val);
        } finally {
            done = true;
            wakeNext();
        }
    };

    pump();

    try {
        while (true) {
            while (queue.length > 0) {
                const val = await queue[0];
                if (val === kEof) return;
                if (signal.aborted) throw new AbortError();
                if (val !== kEmpty) yield val;
                queue.shift();
                maybeResume();
            }
            await new Promise<void>((resolve) => { next = resolve; });
        }
    } finally {
        done = true;
        if (resume !== null) {
            const r = resume;
            resume = null;
            r();
        }
    }
}

async function* flatMapGen(values: any): AsyncGenerator<any> {
    for await (const val of values) {
        const inner: any = val;
        for await (const v of inner) yield v;
    }
}

async function* dropGen(stream: Readable, number: number, options: any): AsyncGenerator<any> {
    if (options?.signal?.aborted) throw new AbortError();
    let n = number;
    for await (const val of stream) {
        if (options?.signal?.aborted) throw new AbortError();
        if (n-- <= 0) yield val;
    }
}

async function* takeGen(stream: Readable, number: number, options: any): AsyncGenerator<any> {
    if (options?.signal?.aborted) throw new AbortError();
    let n = number;
    for await (const val of stream) {
        if (options?.signal?.aborted) throw new AbortError();
        if (n-- > 0) yield val;
        // Don't take another item once the count is reached.
        if (n <= 0) return;
    }
}

// FromDuplexOptions: from(Duplexify, iterable, opts) — the writable half of
// a duplex whose readable half the iterable feeds (duplexify.js).
interface FromDuplexOptions {
    writable?: boolean;
    write?: (chunk: any, encoding: BufferEncoding, callback: Callback) => void;
    final?: (callback: Callback) => void;
    destroy?: (error: any, callback: Callback) => void;
}

function readableFrom(iterable: any, options?: ReadableOptions, duplex?: FromDuplexOptions): Readable {
    if (typeof iterable === 'string' || Buffer.isBuffer(iterable)) {
        return new Readable({
            objectMode: options?.objectMode ?? true,
            highWaterMark: options?.highWaterMark,
            encoding: options?.encoding,
            autoDestroy: options?.autoDestroy,
            emitClose: options?.emitClose,
            read() {
                this.push(iterable);
                this.push(null);
            },
        });
    }

    let isAsync = false;
    let iterator: any;
    if (iterable?.[Symbol.asyncIterator]) {
        isAsync = true;
        iterator = iterable[Symbol.asyncIterator]();
    } else if (iterable?.[Symbol.iterator]) {
        isAsync = false;
        iterator = iterable[Symbol.iterator]();
    } else {
        throw streamError('ERR_INVALID_ARG_TYPE', 'The "iterable" argument must be an instance of Iterable. Received ' + describeReceived(iterable));
    }

    // Flag to protect against _read being called before the last iteration
    // completes.
    let reading = false;
    let isAsyncValues = false;

    const close = async (error: any): Promise<void> => {
        const hadError = error !== undefined && error !== null;
        const hasThrow = typeof iterator.throw === 'function';
        if (hadError && hasThrow) {
            const res = await iterator.throw(error);
            await res.value;
            if (res.done) return;
        }
        if (typeof iterator.return === 'function') {
            const res = await iterator.return();
            await res.value;
        }
    };

    const read = (): void => {
        if (!reading) {
            reading = true;
            if (isAsync) nextAsync();
            else if (isAsyncValues) nextSyncWithAsyncValues();
            else nextSyncWithSyncValues();
        }
    };
    // Node replaces _destroy with one that runs the original first, then
    // closes the iterator.
    const destroyWith = (error: any, cb: Callback): void => {
        const afterOriginal = (destroyError?: any): void => {
            const combined = destroyError || error;
            close(combined ?? null).then(
                () => { process.nextTick(() => { cb(combined); }); },
                (closeError: any) => { process.nextTick(() => { cb(aggregateTwoErrors(combined ?? null, closeError)); }); },
            );
        };
        if (duplex?.destroy) duplex.destroy(error, afterOriginal);
        else afterOriginal(error);
    };
    const readable: Readable = duplex !== undefined
        ? new Duplex({
            objectMode: options?.objectMode ?? true,
            highWaterMark: options?.highWaterMark ?? 1,
            writable: duplex.writable,
            read() { read(); },
            write(chunk: any, encoding: BufferEncoding, cb: Callback) { duplex.write!(chunk, encoding, cb); },
            final(cb: Callback) { duplex.final!(cb); },
            destroy(error: any, cb: Callback) { destroyWith(error, cb); },
        })
        : new Readable({
            objectMode: options?.objectMode ?? true,
            highWaterMark: options?.highWaterMark ?? 1,
            encoding: options?.encoding,
            autoDestroy: options?.autoDestroy,
            emitClose: options?.emitClose,
            read() { read(); },
            destroy(error: any, cb: Callback) { destroyWith(error, cb); },
        });

    const nullValues = (): Error => streamError('ERR_STREAM_NULL_VALUES', 'May not write null values to stream');

    // The duplication below is Node's, on purpose: no await where none is
    // needed.
    const nextSyncWithSyncValues = (): void => {
        for (;;) {
            try {
                const res = iterator.next();
                if (res.done) {
                    readable.push(null);
                    return;
                }
                const value = res.value;
                if (value && typeof value.then === 'function') {
                    changeToAsyncValues(value);
                    return;
                }
                if (value === null) {
                    reading = false;
                    throw nullValues();
                }
                if (readable.push(value)) continue;
                reading = false;
            } catch (err) {
                readable.destroy(err);
            }
            break;
        }
    };

    const changeToAsyncValues = async (value: any): Promise<void> => {
        isAsyncValues = true;
        try {
            const res = await value;
            if (res === null) {
                reading = false;
                throw nullValues();
            }
            if (readable.push(res)) {
                nextSyncWithAsyncValues();
                return;
            }
            reading = false;
        } catch (err) {
            readable.destroy(err);
        }
    };

    const nextSyncWithAsyncValues = async (): Promise<void> => {
        for (;;) {
            try {
                const r = iterator.next();
                if (r.done) {
                    readable.push(null);
                    return;
                }
                const value = r.value;
                const res = value && typeof value.then === 'function' ? await value : value;
                if (res === null) {
                    reading = false;
                    throw nullValues();
                }
                if (readable.push(res)) continue;
                reading = false;
            } catch (err) {
                readable.destroy(err);
            }
            break;
        }
    };

    const nextAsync = async (): Promise<void> => {
        for (;;) {
            try {
                const r = await iterator.next();
                if (r.done) {
                    readable.push(null);
                    return;
                }
                const value = r.value;
                if (value === null) {
                    reading = false;
                    throw nullValues();
                }
                if (readable.push(value)) continue;
                reading = false;
            } catch (err) {
                readable.destroy(err);
            }
            break;
        }
    };
    return readable;
}

// describeReceived is the "Received …" tail of Node's ERR_INVALID_ARG_TYPE.
function describeReceived(value: any): string {
    if (value === null) return 'null';
    if (value === undefined) return 'undefined';
    if (typeof value === 'function') return 'function ' + value.name;
    if (typeof value === 'object') return 'an instance of Object';
    let s = String(value);
    if (s.length > 28) s = s.slice(0, 25) + '...';
    if (typeof value === 'string') return "type string ('" + s + "')";
    return 'type ' + typeof value + ' (' + s + ')';
}

// ---- writable.js ----

// kml:callable Writable — Node's is a function that constructs when called without `new`
export class Writable extends Stream {
    private writeImpl?: (this: Writable, chunk: any, encoding: BufferEncoding, callback: Callback) => void;
    private finalImpl?: (this: Writable, callback: Callback) => void;
    private destroyImpl?: (this: Writable, error: any, callback: Callback) => void;

    constructor(options?: WritableOptions) {
        super(options);
        this._writableState = new WritableState(options, false);
        this.writeImpl = options?.write;
        if (typeof options?.writev === 'function') this._kmlWritevImpl = options.writev as (chunks: any[], callback: Callback) => void;
        this.finalImpl = options?.final;
        this.destroyImpl = options?.destroy;
        if (options?.construct) this._kmlConstructImpl = options.construct as (this: Stream, callback: (error?: any) => void) => void;
        if (options?.signal) addAbortSignal(options.signal, this);
        construct(this, () => { writableConstructed(this, this._writableState!); });
    }

    _write(chunk: any, encoding: BufferEncoding, callback: Callback): void {
        if (this.writeImpl) {
            this.writeImpl.call(this, chunk, encoding, callback);
            return;
        }
        if (writevOf(this)) {
            callWritev(this, [{ chunk, encoding }], callback);
            return;
        }
        throw streamError('ERR_METHOD_NOT_IMPLEMENTED', 'The _write() method is not implemented');
    }

    _final(callback: Callback): void {
        if (this.finalImpl) {
            this.finalImpl.call(this, callback);
            return;
        }
        super._final(callback);
    }

    _destroy(error: any, callback: Callback): void {
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
    get errored(): any { return this._writableState!.errored; }

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

    // Node's newStreamWritableFromWritableStream: each write waits for
    // the writer's readiness, end closes the stream, destroy aborts it.
    [Symbol.asyncDispose](): Promise<void> {
        let error: any = undefined;
        if (!this.destroyed) {
            error = this.writableFinished ? null : new AbortError();
            this.destroy(error);
        }
        return new Promise<void>((resolve, reject) => {
            finished(this, (err?: any) => { if (err && err !== error) reject(err); else resolve(); });
        });
    }

    static fromWeb(web: WritableStream<any>, options?: any): Writable {
        const o: any = options ?? {};
        const objectMode = o.objectMode ?? false;
        const decodeStrings = o.decodeStrings ?? true;
        const writer: any = web.getWriter();
        let closed = false;
        let w: Writable | null = null;
        const encodeChunk = (chunk: any, encoding: BufferEncoding): any => {
            if (typeof chunk === 'string' && decodeStrings && !objectMode) {
                const b = Buffer.from(chunk, encoding);
                return new Uint8Array(b.buffer, b.byteOffset, b.byteLength);
            }
            return chunk;
        };
        w = new Writable({
            highWaterMark: o.highWaterMark,
            objectMode,
            decodeStrings,
            signal: o.signal,
            write(chunk: any, encoding: BufferEncoding, callback: Callback): void {
                const done = (error?: any): void => {
                    try { callback(error); } catch (err) { w!.destroy(err); }
                };
                const data = encodeChunk(chunk, encoding);
                writer.ready.then(() => writer.write(data).then(() => done(), done), done);
            },
            final(callback: Callback): void {
                const done = (error?: any): void => {
                    try { callback(error); } catch (err) { process.nextTick(() => w!.destroy(err)); }
                };
                if (!closed) writer.close().then(() => done(), done);
            },
            destroy(error: any, callback: Callback): void {
                const done = (): void => {
                    try { callback(error); } catch (err) { process.nextTick(() => { throw err; }); }
                };
                if (!closed) {
                    if (error !== null && error !== undefined) writer.abort(error).then(done, done);
                    else writer.close().then(done, done);
                    return;
                }
                done();
            },
        });
        writer.closed.then(() => {
            closed = true;
            if (!w!.writableEnded) w!.destroy(streamError('ERR_STREAM_PREMATURE_CLOSE', 'Premature close'));
        }, (error: any) => {
            closed = true;
            w!.destroy(error);
        });
        return w;
    }

    // Node's newWritableStreamFromStreamWritable: a write waits for
    // 'drain' when the stream needs it, close ends it and settles when it
    // finishes, abort destroys it, and its error errors the web stream.
    static toWeb(writable: Writable): WritableStream<any> {
        const dst: any = writable;
        if (dst === null || typeof dst !== 'object' || typeof dst._writableState !== 'object') {
            throw streamError('ERR_INVALID_ARG_TYPE', 'The "streamWritable" argument must be an instance of Writable.' + describeReceived(dst));
        }
        if (writable.destroyed || !writable.writable) {
            const dead = new WritableStream<any>();
            dead.close();
            return dead;
        }
        const highWaterMark = writable.writableHighWaterMark;
        const strategy: any = writable.writableObjectMode ? { highWaterMark, size: (): number => 1 } : { highWaterMark };
        let controller: any = null;
        let backpressure: { resolve: () => void; reject: (e: any) => void } | null = null;
        let closed: { resolve: () => void; reject: (e: any) => void } | null = null;
        const onDrain = (): void => { if (backpressure !== null) backpressure.resolve(); };
        const cleanup = finished(writable, (error?: any) => {
            let err = error;
            if (err && err.code === 'ERR_STREAM_PREMATURE_CLOSE') err = new AbortError(undefined, err);
            cleanup();
            writable.on('error', () => {});
            if (err !== null && err !== undefined) {
                if (backpressure !== null) backpressure.reject(err);
                if (closed !== null) { closed.reject(err); closed = null; }
                controller.error(err);
                return;
            }
            if (closed !== null) { closed.resolve(); closed = null; return; }
            controller.error(new AbortError());
        });
        writable.on('drain', onDrain);
        return new WritableStream<any>({
            start(c) { controller = c; },
            write(chunk): any {
                if (writable.writableNeedDrain || !writable.write(chunk)) {
                    return new Promise<void>((resolve, reject) => {
                        backpressure = { resolve, reject };
                    }).finally(() => { backpressure = null; });
                }
                return undefined;
            },
            abort(reason) { writable.destroy(reason); },
            close(): any {
                if (closed === null && !writable.writableEnded) {
                    const p = new Promise<void>((resolve, reject) => { closed = { resolve, reject }; });
                    writable.end();
                    return p;
                }
                return Promise.resolve();
            },
        }, strategy);
    }
}

// Writable.prototype.write's body: `true` or `false` as write() returns, or
// the error it reported.
function writeChunk(stream: Stream, chunk: any, encoding: BufferEncoding | undefined, cb: Callback | undefined): boolean | Error {
    const s = stream._writableState!;
    const callback: Callback = cb ?? nop;
    if (chunk === null) {
        throw streamError('ERR_STREAM_NULL_VALUES', 'May not write null values to stream');
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
            throw streamError('ERR_INVALID_ARG_TYPE', 'The "chunk" argument must be of type string or an instance of Buffer, TypedArray, or DataView.');
        }
    }
    let err: any = null;
    if (s.ending) err = streamError('ERR_STREAM_WRITE_AFTER_END', 'write after end');
    else if (s.destroyed) err = streamError('ERR_STREAM_DESTROYED', 'Cannot call write after a stream was destroyed');
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
        const onwriteCb = (er?: any): void => { onwrite(stream, er); };
        stream._write(chunk, encoding as BufferEncoding, onwriteCb);
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
    if (s.destroyed) onwrite(stream, streamError('ERR_STREAM_DESTROYED', 'Cannot call write after a stream was destroyed'));
    else {
        const onwriteCb = (er?: any): void => { onwrite(stream, er); };
        stream._write(chunk, encoding as BufferEncoding, onwriteCb);
    }
    s.sync = false;
}

function onwriteError(stream: Stream, s: WritableState, er: any, cb: Callback): void {
    --s.pendingcb;
    cb(er);
    errorBuffer(s);
    errorOrDestroy(stream, er, false);
}

function onwrite(stream: Stream, er: any): void {
    const s = stream._writableState!;
    if (!s.expectWriteCb) {
        errorOrDestroy(stream, streamError('ERR_MULTIPLE_CALLBACK', 'Callback called multiple times'), false);
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
        b.callback(s.errored ?? streamError('ERR_STREAM_DESTROYED', 'Cannot call write after a stream was destroyed'));
    }
    callFinishedCallbacks(s, s.errored ?? streamError('ERR_STREAM_DESTROYED', 'Cannot call end after a stream was destroyed'));
    s.buffered = [];
    s.bufferedIndex = 0;
}

function clearBuffer(stream: Stream, s: WritableState): void {
    if (s.destroyed || s.bufferProcessing || s.corked > 0 || !s.constructed) return;
    if (s.buffered.length - s.bufferedIndex === 0) return;
    let i = s.bufferedIndex;
    s.bufferProcessing = true;
    const bufferedLength = s.buffered.length - i;
    if (bufferedLength > 1 && writevOf(stream)) {
        // Node's: every buffered chunk in one _writev, one write callback
        // calling each chunk's.
        s.pendingcb -= bufferedLength - 1;
        const entries = s.buffered.slice(i);
        const callback = (err?: any): void => {
            for (const b of entries) b.callback(err);
        };
        const chunks: any = entries.map((b: any) => ({ chunk: b.chunk, encoding: b.encoding }));
        chunks.allBuffers = entries.every((b: any) => b.encoding === 'buffer');
        s.writelen = s.length;
        s.writecb = callback;
        s.writing = true;
        s.sync = true;
        s.expectWriteCb = true;
        if (s.destroyed) onwrite(stream, streamError('ERR_STREAM_DESTROYED', 'Cannot call write after a stream was destroyed'));
        else callWritev(stream, chunks, (er?: any): void => { onwrite(stream, er); });
        s.sync = false;
        s.buffered = [];
        s.bufferedIndex = 0;
        s.bufferProcessing = false;
        return;
    }
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

// The stream's _writev, from its options or its class; null without one.
function writevOf(stream: Stream): ((chunks: any[], callback: Callback) => void) | null {
    if (stream._kmlWritevImpl !== null) return stream._kmlWritevImpl;
    const m: any = (stream as any)._writev;
    return typeof m === 'function' ? m : null;
}

function callWritev(stream: Stream, chunks: any[], callback: Callback): void {
    const f: any = writevOf(stream);
    f.call(stream, chunks, callback);
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
    let err: any = null;
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
        err = streamError('ERR_STREAM_ALREADY_FINISHED', 'Cannot call end after a stream was finished');
    } else if (s.destroyed) {
        err = streamError('ERR_STREAM_DESTROYED', 'Cannot call end after a stream was destroyed');
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

function onFinish(stream: Stream, s: WritableState, err: any): void {
    if (s.prefinished) {
        errorOrDestroy(stream, err ?? streamError('ERR_MULTIPLE_CALLBACK', 'Callback called multiple times'), false);
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
            const onFinal = (err?: any): void => { onFinish(stream, s, err); };
            stream._final(onFinal);
        } catch (err) {
            onFinish(stream, s, err);
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

function callFinishedCallbacks(s: WritableState, err: any): void {
    const cbs = s.onFinished;
    s.onFinished = [];
    for (const cb of cbs) cb(err);
}

// ---- duplex.js ----

// kml:callable Duplex — Node's is a function that constructs when called without `new`
export class Duplex extends Readable {
    // Duplex.from (duplexify.js): a Duplex from a stream, an iterable, an
    // async generator function, a promise or a { readable, writable } pair.
    // Node's newReadableWritablePairFromDuplex: the readable side through
    // Readable.toWeb, the writable side through Writable.toWeb.
    static toWeb(duplex: Duplex): { readable: ReadableStream<any>; writable: WritableStream<any> } {
        return { readable: Readable.toWeb(duplex), writable: Writable.toWeb(duplex as any) };
    }

    // Node's newStreamDuplexFromReadableWritablePair.
    static fromWeb(pair: { readable: ReadableStream<any>; writable: WritableStream<any> }, options?: any): Duplex {
        const p: any = pair;
        if (p === null || typeof p !== 'object') {
            throw streamError('ERR_INVALID_ARG_TYPE', 'The "pair" argument must be of type object.' + describeReceived(p));
        }
        const d = duplexifyPair(Readable.fromWeb(p.readable, options), Writable.fromWeb(p.writable, options));
        if (typeof options?.encoding === 'string') d.setEncoding(options.encoding);
        return d;
    }

    static from(body: any, options?: ReadableOptions): Duplex {
        return duplexify(body, 'body');
    }

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
        if (typeof (options as any)?.writev === 'function') this._kmlWritevImpl = (options as any).writev;
        this.dFinalImpl = options?.final;
        if (options?.construct) this._kmlConstructImpl = options.construct as (this: Stream, callback: (error?: any) => void) => void;
        if (options?.signal) addAbortSignal(options.signal, this);
        construct(this, () => {
            const r = this._readableState!;
            if (r.needReadable) maybeReadMore(this, r);
            writableConstructed(this, this._writableState!);
        });
    }

    _kmlIsDuplex(): boolean { return true; }

    private dReadImpl?: (this: Duplex, size: number) => void;
    private dDestroyImpl?: (this: Duplex, error: any, callback: Callback) => void;

    _read(size: number): void {
        if (this.dReadImpl) {
            this.dReadImpl.call(this, size);
            return;
        }
        throw streamError('ERR_METHOD_NOT_IMPLEMENTED', 'The _read() method is not implemented');
    }

    _destroy(error: any, callback: Callback): void {
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
        if (writevOf(this)) {
            callWritev(this, [{ chunk, encoding }], callback);
            return;
        }
        throw streamError('ERR_METHOD_NOT_IMPLEMENTED', 'The _write() method is not implemented');
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

    // Whether Transform's own `_final` ran; a subclass that overrides it
    // (zlib's) has the flush run at 'prefinish' instead, as Node's does.
    private ownFinalRan = false;

    constructor(options?: TransformOptions) {
        super(options);
        this._readableState!.sync = false;
        this.transformImpl = options?.transform;
        this.flushImpl = options?.flush;
        this.on('prefinish', () => {
            if (!this.ownFinalRan) this.transformFinal(undefined);
        });
    }

    _transform(chunk: any, encoding: BufferEncoding, callback: TransformCallback): void {
        if (this.transformImpl) {
            this.transformImpl.call(this, chunk, encoding, callback);
            return;
        }
        throw streamError('ERR_METHOD_NOT_IMPLEMENTED', 'The _transform() method is not implemented');
    }

    _flush(callback: TransformCallback): void {
        if (this.flushImpl) {
            this.flushImpl.call(this, callback);
            return;
        }
        callback();
    }

    _final(callback: Callback): void {
        this.ownFinalRan = true;
        this.transformFinal(callback);
    }

    private transformFinal(callback: Callback | undefined): void {
        if (!this.destroyed) {
            const onFlush = (er?: any, data?: any): void => {
                if (er) {
                    if (callback) callback(er);
                    else this.destroy(er);
                    return;
                }
                if (data !== undefined && data !== null) this.push(data);
                this.push(null);
                if (callback) callback();
            };
            this._flush(onFlush);
        } else {
            this.push(null);
            if (callback) callback();
        }
    }

    _write(chunk: any, encoding: BufferEncoding, callback: Callback): void {
        const r = this._readableState!;
        const w = this._writableState!;
        const length = r.length;
        const onTransform = (err?: any, val?: any): void => {
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
        };
        this._transform(chunk, encoding, onTransform);
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
export function finished(stream: Stream, options: FinishedOptions | ((err?: any) => void), callback?: (err?: any) => void): () => void {
    let cb: (err?: any) => void = nop;
    let opts: FinishedOptions = {};
    if (typeof options === 'function') cb = options;
    else {
        opts = options;
        if (callback) cb = callback;
    }
    let called = false;
    const call = (err?: any) => {
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
    const onerror = (err: any) => { call(err); };
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
                call(streamError('ERR_STREAM_PREMATURE_CLOSE', 'Premature close'));
                return;
            }
        }
        if (writable && !writableFinished) {
            if (!isWritableFinished(stream, false)) {
                call(streamError('ERR_STREAM_PREMATURE_CLOSE', 'Premature close'));
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
// _kmlFromWeb is Readable.fromWeb for code generation: klain:http's request
// body (a web stream of Uint8Array chunks) as a Node Readable.
export function _kmlFromWeb(web: ReadableStream<Uint8Array>): any {
    const reader = web.getReader();
    const r: Readable = new Readable({
        read() {
            reader.read().then((res) => {
                if (res.done) r.push(null);
                else r.push(res.value);
            }, (err: any) => { r.destroy(err); });
        },
    });
    return r;
}

// ---- add-abort-signal.js ----

// addAbortSignal destroys stream with an AbortError when signal aborts
// (at once when it already has).
export function addAbortSignal(signal: any, stream: any): any {
    if (signal === null || typeof signal !== 'object' || signal.aborted === undefined) {
        throw streamError('ERR_INVALID_ARG_TYPE', 'The "signal" argument must be an instance of AbortSignal. Received ' + describeReceived(signal));
    }
    if (!(stream instanceof Stream)) {
        throw streamError('ERR_INVALID_ARG_TYPE', 'The "stream" argument must be an instance of ReadableStream, WritableStream, or Stream. Received ' + describeReceived(stream));
    }
    return addAbortSignalNoValidate(signal, stream);
}

function addAbortSignalNoValidate(signal: AbortSignal, stream: Stream): Stream {
    const onAbort = (): void => {
        stream.destroy(new AbortError(undefined, signal.reason));
    };
    if (signal.aborted) {
        onAbort();
    } else {
        signal.addEventListener('abort', onAbort, { once: true });
        finished(stream, () => { signal.removeEventListener('abort', onAbort); });
    }
    return stream;
}

// ---- pipeline.js ----

// isStreamValue / isIterableValue / isReadableValue / isWritableValue are
// Node's internal/streams/utils predicates over any value.
function isStreamValue(v: any): boolean {
    return v instanceof Stream;
}

function isIterableValue(v: any, asyncOnly?: boolean): boolean {
    if (v === null || v === undefined || typeof v !== 'object' && typeof v !== 'function' && typeof v !== 'string') return false;
    if (asyncOnly === true) return typeof v[Symbol.asyncIterator] === 'function';
    return typeof v[Symbol.asyncIterator] === 'function' || typeof v[Symbol.iterator] === 'function';
}

function isReadableValue(v: any): boolean {
    if (!isStreamValue(v)) return false;
    const s: Stream = v;
    if (streamDestroyed(s)) return false;
    return isReadableNodeStream(s) && s._readableState!.readable && !isReadableFinished(s, false);
}

function isWritableValue(v: any): boolean {
    if (!isStreamValue(v)) return false;
    const s: Stream = v;
    if (streamDestroyed(s)) return false;
    const w = s._writableState;
    return isWritableNodeStream(s) && w !== null && w.writable && !w.ending;
}

// destroyStreamWith is destroy.js's destroyer: destroy a stream with err.
function destroyStreamWith(stream: any, err: any): void {
    if (stream === null || stream === undefined) return;
    if (isStreamValue(stream)) {
        const s: Stream = stream;
        s.destroy(err);
    }
}

interface StreamDestroyer {
    destroy: (err: any) => void;
    cleanup: () => void;
}

function destroyer(stream: Stream, reading: boolean, writing: boolean): StreamDestroyer {
    let done = false;
    stream.on('close', () => { done = true; });
    const cleanup = finished(stream, { readable: reading, writable: writing }, (err?: any) => { done = !err; });
    return {
        destroy: (err: any) => {
            if (done) return;
            done = true;
            stream.destroy(err ?? streamError('ERR_STREAM_DESTROYED', 'Cannot call pipe after a stream was destroyed'));
        },
        cleanup,
    };
}

async function* fromReadableGen(val: Readable): AsyncGenerator<any> {
    for await (const chunk of val) yield chunk;
}

function makeAsyncIterable(val: any): any {
    if (isIterableValue(val)) return val;
    if (isStreamValue(val) && isReadableNodeStream(val)) return fromReadableGen(val);
    throw streamError('ERR_INVALID_ARG_TYPE', 'The "val" argument must be an instance of Readable, Iterable, or AsyncIterable. Received ' + describeReceived(val));
}

async function pumpToNode(iterable: any, writable: Writable, finish: (err?: any) => void, end: boolean): Promise<void> {
    let error: any = null;
    let onresolve: (() => void) | null = null;
    const resume = (err?: any): void => {
        if (err) error = err;
        if (onresolve !== null) {
            const callback = onresolve;
            onresolve = null;
            callback();
        }
    };
    const wait = (): Promise<void> => new Promise<void>((resolve, reject) => {
        if (error) {
            reject(error);
        } else {
            onresolve = () => {
                if (error) reject(error);
                else resolve();
            };
        }
    });
    writable.on('drain', resume);
    const cleanup = finished(writable, { readable: false }, resume);
    try {
        if (writable.writableNeedDrain) await wait();
        for await (const chunk of iterable) {
            if (!writable.write(chunk)) await wait();
        }
        if (end) {
            writable.end();
            await wait();
        }
        finish();
    } catch (err) {
        const e = err;
        finish(error !== e ? aggregateTwoErrors(error, e) : e);
    } finally {
        cleanup();
        writable.off('drain', resume);
    }
}

export interface PipelineOptions {
    signal?: AbortSignal;
    end?: boolean;
}

// pipeline(...streams, callback): each stage a stream, an (async) iterable, an
// async generator function, or — last — a function returning a promise.
export function pipeline(...args: any[]): any {
    const callback = args[args.length - 1];
    validateFunction(callback, 'streams[stream.length - 1]');
    let called = false;
    const once = (err?: any, value?: any): void => {
        if (called) return;
        called = true;
        callback(err, value);
    };
    return pipelineImpl(args.slice(0, args.length - 1), once, undefined);
}

function pipelineImpl(streamsIn: any[], callback: (err?: any, value?: any) => void, opts: PipelineOptions | undefined): any {
    let streams: any[] = streamsIn;
    if (streams.length === 1 && Array.isArray(streams[0])) streams = streams[0];
    if (streams.length < 2) throw streamError('ERR_MISSING_ARGS', 'The "streams" argument must be specified');
    const ac = new AbortController();
    const signal = ac.signal;
    const outerSignal = opts?.signal;
    const lastStreamCleanup: (() => void)[] = [];
    let error: any = undefined;
    let value: any = undefined;
    const destroys: ((err: any) => void)[] = [];
    let finishCount = 0;

    const finishImpl = (err: any, final: boolean): void => {
        if (err && (!error || (error as any).code === 'ERR_STREAM_PREMATURE_CLOSE' || error.name === 'AbortError')) error = err;
        if (!error && !final) return;
        while (destroys.length > 0) destroys.shift()!(error);
        if (outerSignal) outerSignal.removeEventListener('abort', abort);
        ac.abort();
        if (final) {
            if (!error) for (const fn of lastStreamCleanup) fn();
            const e = error;
            const v = value;
            process.nextTick(() => { callback(e, v); });
        }
    };
    const finish = (err?: any): void => { finishImpl(err, --finishCount === 0); };
    const finishOnlyHandleError = (err?: any): void => { finishImpl(err, false); };
    const abort = (): void => { finishImpl(new AbortError(undefined, outerSignal?.reason), false); };
    if (outerSignal) outerSignal.addEventListener('abort', abort, { once: true });

    let ret: any = undefined;
    for (let i = 0; i < streams.length; i++) {
        const stream: any = streams[i];
        const reading = i < streams.length - 1;
        const writing = i > 0;
        const next: any = i + 1 < streams.length ? streams[i + 1] : null;
        const end = reading || opts?.end !== false;
        const isLastStream = i === streams.length - 1;

        if (isStreamValue(stream)) {
            const st: Stream = stream;
            if (next !== null && isStreamValue(next) && (streamDestroyed(next) || (next as Stream)._readableState?.closed || (next as Stream)._writableState?.closed)) {
                throw streamError('ERR_STREAM_UNABLE_TO_PIPE', 'Cannot pipe to a closed or destroyed stream');
            }
            if (end) {
                const d = destroyer(st, reading, writing);
                destroys.push(d.destroy);
                if (isReadableValue(st) && isLastStream) lastStreamCleanup.push(d.cleanup);
            }
            // Stream errors after the pipe has completed.
            const onError = (err: any): void => {
                if (err && err.name !== 'AbortError' && err.code !== 'ERR_STREAM_PREMATURE_CLOSE') finishOnlyHandleError(err);
            };
            st.on('error', onError);
            if (isReadableValue(st) && isLastStream) lastStreamCleanup.push(() => { st.removeListener('error', onError); });
        }

        if (i === 0) {
            if (typeof stream === 'function') {
                ret = stream({ signal });
                if (!isIterableValue(ret)) {
                    throw streamError('ERR_INVALID_RETURN_VALUE', 'Expected Iterable, AsyncIterable or Stream to be returned from the "source" function.');
                }
            } else if (isIterableValue(stream) || (isStreamValue(stream) && isReadableNodeStream(stream))) {
                ret = stream;
            } else {
                ret = Duplex.from(stream);
            }
        } else if (typeof stream === 'function') {
            ret = makeAsyncIterable(ret);
            ret = stream(ret, { signal });
            if (reading) {
                if (!isIterableValue(ret, true)) {
                    throw streamError('ERR_INVALID_RETURN_VALUE', 'Expected AsyncIterable to be returned from the "transform[' + String(i - 1) + ']" function.');
                }
            } else {
                // The last stage is not a stream: a proxy stream, so pipeline()
                // always returns a stream.
                const pt = new PassThrough({ objectMode: true });
                const then = ret?.then;
                if (typeof then === 'function') {
                    finishCount++;
                    const endPt = end;
                    ret.then((val: any) => {
                        value = val;
                        if (val !== null && val !== undefined) pt.write(val);
                        if (endPt) pt.end();
                        process.nextTick(() => { finish(); });
                    }, (err: any) => {
                        pt.destroy(err);
                        process.nextTick(() => { finish(err); });
                    });
                } else if (isIterableValue(ret, true)) {
                    finishCount++;
                    pumpToNode(ret, pt, finish, end);
                } else {
                    throw streamError('ERR_INVALID_RETURN_VALUE', 'Expected AsyncIterable or Promise to be returned from the "destination" function.');
                }
                ret = pt;
                const d = destroyer(pt, false, true);
                destroys.push(d.destroy);
                if (isLastStream) lastStreamCleanup.push(d.cleanup);
            }
        } else if (isStreamValue(stream)) {
            const dst: Stream = stream;
            if (isStreamValue(ret) && isReadableNodeStream(ret)) {
                finishCount += 2;
                const cleanup = pipeWithFinish(ret, dst, finish, finishOnlyHandleError, end);
                if (isReadableValue(dst) && isLastStream) lastStreamCleanup.push(cleanup);
            } else if (isIterableValue(ret)) {
                finishCount++;
                pumpToNode(ret, dst as Writable, finish, end);
            } else {
                throw streamError('ERR_INVALID_ARG_TYPE', 'The "val" argument must be an instance of Readable, Iterable, AsyncIterable, ReadableStream, or TransformStream. Received ' + describeReceived(ret));
            }
            ret = dst;
        } else {
            ret = Duplex.from(stream);
        }
    }
    if (signal.aborted || outerSignal?.aborted) process.nextTick(abort);
    return ret;
}

function pipeWithFinish(src: Stream, dst: Stream, finish: (err?: any) => void, finishOnlyHandleError: (err?: any) => void, end: boolean): () => void {
    let ended = false;
    dst.on('close', () => {
        if (!ended) finishOnlyHandleError(streamError('ERR_STREAM_PREMATURE_CLOSE', 'Premature close'));
    });
    src._kmlPipe(dst, false);
    if (end) {
        const endFn = () => {
            ended = true;
            dst._kmlEnd();
        };
        if (isReadableFinished(src, true)) process.nextTick(endFn);
        else src.once('end', endFn);
    } else {
        finish();
    }
    finished(src, { readable: true, writable: false }, (err?: any) => {
        const r = src._readableState;
        if (err && (err as any).code === 'ERR_STREAM_PREMATURE_CLOSE' && r !== null && r.ended && !r.errored && !r.errorEmitted) {
            src.once('end', () => { finish(); });
            src.once('error', (e: any) => { finish(e); });
        } else {
            finish(err);
        }
    });
    return finished(dst, { readable: false, writable: true }, finish);
}

// ---- duplexify.js ----

// A duplexify/from promise-with-resolvers state: the feed between a
// Duplex's writable half and the async generator a function body consumes.
class AsyncGenFeed {
    promise: Promise<any> | null = null;
    resolve: ((v: any) => void) | null = null;
    ac: AbortController = new AbortController();
    constructor() { this.reset(); }
    reset(): void {
        this.promise = new Promise<any>((resolve) => { this.resolve = resolve; });
    }
    send(msg: any): void {
        const r = this.resolve;
        this.resolve = null;
        if (r !== null) r(msg);
    }
}

async function* feedSource(feed: AsyncGenFeed): AsyncGenerator<any> {
    while (true) {
        const p = feed.promise;
        feed.promise = null;
        const msg: any = await p;
        const cb: any = msg.cb;
        process.nextTick(() => { cb(); });
        if (msg.done) return;
        if (feed.ac.signal.aborted) throw new AbortError(undefined, feed.ac.signal.reason);
        feed.reset();
        yield msg.chunk;
    }
}

// A WHATWG WritableStream, by its own methods.
function isWritableWebStream(v: any): boolean {
    return v !== null && typeof v === 'object' && typeof v.getWriter === 'function' && typeof v.abort === 'function' && typeof v.pipeTo !== 'function';
}

// A Blob (or File), told apart as Node's isBlob does without naming the
// class, which would bring it into every program using streams.
function isBlobValue(v: any): boolean {
    if (v === null || typeof v !== 'object' || typeof v.arrayBuffer !== 'function' || typeof v.stream !== 'function') return false;
    const tag = Object.prototype.toString.call(v);
    return tag === '[object Blob]' || tag === '[object File]';
}

export function duplexify(body: any, name: string): Duplex {
    if (body instanceof Duplex) return body;
    if (isBlobValue(body)) return duplexify(body.arrayBuffer(), name);
    if (isWritableWebStream(body)) return duplexifyPair(undefined, Writable.fromWeb(body));
    if (isStreamValue(body) && isReadableNodeStream(body)) return duplexifyPair(body, undefined);
    if (isStreamValue(body) && isWritableNodeStream(body)) return duplexifyPair(undefined, body);
    if (isStreamValue(body)) return new Duplex({ writable: false, readable: false });
    if (typeof body === 'function') {
        const feed = new AsyncGenFeed();
        const value: any = body(feedSource(feed), { signal: feed.ac.signal });
        const write = (chunk: any, encoding: BufferEncoding, cb: Callback): void => { feed.send({ chunk, done: false, cb }); };
        const final = (cb: Callback): void => { feed.send({ done: true, cb }); };
        const destroy = (err: any, cb: Callback): void => {
            feed.ac.abort(err);
            // Unblock a source waiting for the next write, so the readable
            // side sees the abort.
            if (feed.resolve !== null) feed.send({ done: true, cb: () => {} });
            cb(err);
        };
        if (value instanceof Duplex) return value;
        if (isIterableValue(value)) {
            return readableFrom(value, { objectMode: true }, { write, final, destroy }) as Duplex;
        }
        const then = value?.then;
        if (typeof then === 'function') {
            let d: Duplex | null = null;
            const promise: Promise<any> = value.then((val: any) => {
                if (val !== null && val !== undefined) {
                    throw streamError('ERR_INVALID_RETURN_VALUE', 'Expected nully to be returned from the "' + name + '" function.');
                }
            }, (err: any) => {
                destroyStreamWith(d, err);
            });
            d = new Duplex({
                objectMode: true,
                readable: false,
                write(chunk: any, encoding: BufferEncoding, cb: Callback) { write(chunk, encoding, cb); },
                final(cb: Callback) {
                    final(async () => {
                        try {
                            await promise;
                            process.nextTick(() => { cb(null); });
                        } catch (err) {
                            process.nextTick(() => { cb(err); });
                        }
                    });
                },
                destroy(err: any, cb: Callback) { destroy(err, cb); },
            });
            return d;
        }
        throw streamError('ERR_INVALID_RETURN_VALUE', 'Expected Iterable, AsyncIterable or AsyncFunction to be returned from the "' + name + '" function.');
    }
    if (isIterableValue(body)) {
        return readableFrom(body, { objectMode: true }, { writable: false }) as Duplex;
    }
    if (body !== null && body !== undefined && (typeof body.writable === 'object' || typeof body.readable === 'object')) {
        const readable = body.readable ? (isStreamValue(body.readable) && isReadableNodeStream(body.readable) ? body.readable : duplexify(body.readable, name)) : undefined;
        const writable = body.writable ? (isStreamValue(body.writable) && isWritableNodeStream(body.writable) ? body.writable : duplexify(body.writable, name)) : undefined;
        return duplexifyPair(readable, writable);
    }
    const then = body?.then;
    if (typeof then === 'function') {
        let d: Duplex | null = null;
        body.then((val: any) => {
            if (val !== null && val !== undefined) d!.push(val);
            d!.push(null);
        }, (err: any) => {
            destroyStreamWith(d, err);
        });
        d = new Duplex({ objectMode: true, writable: false, read() {} });
        return d;
    }
    throw streamError('ERR_INVALID_ARG_TYPE', 'The "' + name + '" argument must be of type Blob, ReadableStream, WritableStream, Stream, Iterable, AsyncIterable, Function, { readable, writable } pair, or Promise. Received ' + describeReceived(body));
}

function duplexifyPair(rIn: any, wIn: any): Duplex {
    const r: any = rIn;
    const w: any = wIn;
    let readable = isReadableValue(r);
    let writable = isWritableValue(w);
    let ondrain: Callback | null = null;
    let onfinish: Callback | null = null;
    let onreadable: (() => void) | null = null;
    let onclose: Callback | null = null;
    let d: Duplex | null = null;
    const onfinished = (err?: any): void => {
        const cb = onclose;
        onclose = null;
        if (cb !== null) cb(err);
        else if (err) d!.destroy(err);
    };
    const doRead = (): void => {
        while (true) {
            const buf = (r as Readable).read();
            if (buf === null) {
                onreadable = doRead;
                return;
            }
            if (!d!.push(buf)) return;
        }
    };
    d = new Duplex({
        readableObjectMode: !!r?.readableObjectMode,
        writableObjectMode: !!w?.writableObjectMode,
        readable,
        writable,
        write(chunk: any, encoding: BufferEncoding, callback: Callback) {
            if ((w as Writable).write(chunk, encoding)) callback();
            else ondrain = callback;
        },
        final(callback: Callback) {
            (w as Writable).end();
            onfinish = callback;
        },
        read() { doRead(); },
        destroy(err: any, callback: Callback) {
            let e = err;
            if (!e && onclose !== null) e = new AbortError();
            onreadable = null;
            ondrain = null;
            onfinish = null;
            if (onclose === null) {
                callback(e);
            } else {
                onclose = callback;
                destroyStreamWith(w, e);
                destroyStreamWith(r, e);
            }
        },
    });
    if (writable) {
        finished(w as Stream, (err?: any) => {
            writable = false;
            if (err) destroyStreamWith(r, err);
            onfinished(err);
        });
        (w as Stream).on('drain', () => {
            if (ondrain !== null) {
                const cb = ondrain;
                ondrain = null;
                cb();
            }
        });
        (w as Stream).on('finish', () => {
            if (onfinish !== null) {
                const cb = onfinish;
                onfinish = null;
                cb();
            }
        });
    }
    if (readable) {
        finished(r as Stream, (err?: any) => {
            readable = false;
            if (err) destroyStreamWith(w, err);
            onfinished(err);
        });
        (r as Stream).on('readable', () => {
            if (onreadable !== null) {
                const cb = onreadable;
                onreadable = null;
                cb();
            }
        });
        (r as Stream).on('end', () => { d!.push(null); });
    }
    return d;
}

// ---- compose.js ----

// compose(...streams): one Duplex writing into the first stage and reading
// from the last, the stages piped together.
export function compose(...streamsIn: any[]): Duplex {
    if (streamsIn.length === 0) throw streamError('ERR_MISSING_ARGS', 'The "streams" argument must be specified');
    if (streamsIn.length === 1) return Duplex.from(streamsIn[0]);
    const orgStreams = streamsIn.slice();
    const streams: any[] = streamsIn.slice();
    if (typeof streams[0] === 'function') streams[0] = Duplex.from(streams[0]);
    if (typeof streams[streams.length - 1] === 'function') streams[streams.length - 1] = Duplex.from(streams[streams.length - 1]);
    for (let n = 0; n < streams.length; ++n) {
        if (!isStreamValue(streams[n])) continue;
        if (n < streams.length - 1 && !isReadableValue(streams[n])) {
            throw streamError('ERR_INVALID_ARG_VALUE', "The argument 'streams[" + String(n) + "]' must be readable. Received " + describeReceived(orgStreams[n]));
        }
        if (n > 0 && !isWritableValue(streams[n])) {
            throw streamError('ERR_INVALID_ARG_VALUE', "The argument 'streams[" + String(n) + "]' must be writable. Received " + describeReceived(orgStreams[n]));
        }
    }
    let ondrain: Callback | null = null;
    let onfinish: Callback | null = null;
    let onclose: Callback | null = null;
    let d: Duplex | null = null;
    const head: any = streams[0];
    let readable = false;
    let writable = false;
    const onfinished = (err?: any): void => {
        const cb = onclose;
        onclose = null;
        if (cb !== null) cb(err);
        else if (err) d!.destroy(err);
        else if (!readable && !writable) d!.destroy(null);
    };
    const tail: any = pipelineImpl(streams, onfinished, undefined);
    writable = isWritableValue(head);
    readable = isReadableValue(tail);
    d = new Duplex({
        writableObjectMode: !!head?.writableObjectMode,
        readableObjectMode: !!tail?.readableObjectMode,
        writable,
        readable,
        write(chunk: any, encoding: BufferEncoding, callback: Callback) {
            if ((head as Writable).write(chunk, encoding)) callback();
            else ondrain = callback;
        },
        final(callback: Callback) {
            (head as Writable).end();
            onfinish = callback;
        },
        read() { (tail as Readable).resume(); },
        destroy(err: any, callback: Callback) {
            let e = err;
            if (!e && onclose !== null) e = new AbortError();
            ondrain = null;
            onfinish = null;
            if (isStreamValue(tail)) destroyStreamWith(tail, e);
            if (onclose === null) callback(e);
            else onclose = callback;
        },
    });
    if (writable) {
        (head as Stream).on('drain', () => {
            if (ondrain !== null) {
                const cb = ondrain;
                ondrain = null;
                cb();
            }
        });
        finished(tail as Stream, () => {
            if (onfinish !== null) {
                const cb = onfinish;
                onfinish = null;
                cb();
            }
        });
    }
    if (readable) {
        (tail as Stream).on('data', (chunk: any) => {
            if (!d!.push(chunk)) (tail as Readable).pause();
        });
        (tail as Stream).on('end', () => { d!.push(null); });
    }
    return d;
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
    pipeline(...streams: any[]): Promise<any> {
        return new Promise<any>((resolve, reject) => {
            let opts: PipelineOptions | undefined = undefined;
            const last: any = streams[streams.length - 1];
            if (last !== null && last !== undefined && typeof last === 'object' && !isStreamValue(last) && !isIterableValue(last)) {
                opts = streams.pop();
            }
            pipelineImpl(streams, (err?: any, value?: any) => {
                if (err) reject(err);
                else resolve(value);
            }, opts);
        });
    },
    finished(stream: Stream, options?: FinishedOptions): Promise<void> {
        return new Promise<void>((resolve, reject) => {
            finished(stream, options ?? {}, (err?: any) => {
                if (err) reject(err);
                else resolve();
            });
        });
    },
};
