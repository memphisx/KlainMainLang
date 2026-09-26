// The part of Node's `fs` written in TypeScript: the callback file
// operations (open, close, read, write, writev, fsync), ported from Node
// v24's lib/fs.js, and the file streams (ReadStream, WriteStream,
// createReadStream, createWriteStream), ported from lib/internal/fs/
// streams.js. The rest of `fs` is the compiler's own; a program importing
// `fs` imports this module too, and `fs.X` names its export X when it has
// one. The operations run on the thread pool (lib/native.d.ts) and call
// back on the loop thread, as libuv's do.
import { Readable, Writable, finished } from 'stream';
import type { ReadableOptions, WritableOptions } from 'stream';

type PathLike = string | Buffer | URL;
type OpenMode = number | string;
type Mode = number | string;
type ErrnoCallback = (err: NodeJS.ErrnoException | null) => void;

// A Node error with its `code` (ERR_*).
class NodeError extends Error {
    code: string;
    constructor(code: string, message: string) {
        super(message);
        this.code = code;
    }
}

class NodeTypeError extends TypeError {
    code: string;
    constructor(code: string, message: string) {
        super(message);
        this.code = code;
    }
}

class NodeRangeError extends RangeError {
    code: string;
    constructor(code: string, message: string) {
        super(message);
        this.code = code;
    }
}

// How Node's errors describe the value they received.
function received(value: any): string {
    if (value === null) return 'Received null';
    if (value === undefined) return 'Received undefined';
    if (typeof value === 'function') return 'Received function ' + (value.name || '<anonymous>');
    if (typeof value === 'object') {
        if (Array.isArray(value)) return 'Received an instance of Array';
        if (Buffer.isBuffer(value)) return 'Received an instance of Buffer';
        return 'Received an instance of Object';
    }
    let shown = String(value);
    if (typeof value === 'string') {
        if (shown.length > 28) shown = shown.slice(0, 25) + '...';
        shown = "'" + shown + "'";
    }
    return 'Received type ' + typeof value + ' (' + shown + ')';
}

function invalidArgType(name: string, expected: string, value: any): NodeTypeError {
    return new NodeTypeError('ERR_INVALID_ARG_TYPE', 'The "' + name + '" argument must be ' + expected + '. ' + received(value));
}

function validateFunction(value: any, name: string): void {
    if (typeof value !== 'function') throw invalidArgType(name, 'of type function', value);
}

function validateInteger(value: any, name: string, min: number): void {
    if (typeof value !== 'number') throw invalidArgType(name, 'of type number', value);
    if (!Number.isInteger(value)) throw outOfRange(name, 'an integer', value);
    if (value < min || value > Number.MAX_SAFE_INTEGER) throw outOfRange(name, '>= ' + min + ' && <= ' + Number.MAX_SAFE_INTEGER, value);
}

function outOfRange(name: string, range: string, value: any): NodeRangeError {
    return new NodeRangeError('ERR_OUT_OF_RANGE', 'The value of "' + name + '" is out of range. It must be ' + range + '. Received ' + String(value));
}

function validateInt32(value: any, name: string, min: number): void {
    if (typeof value !== 'number') throw invalidArgType(name, 'of type number', value);
    if (!Number.isInteger(value)) throw outOfRange(name, 'an integer', value);
    if (value < min || value > 2147483647) throw outOfRange(name, '>= ' + min + ' && <= 2147483647', value);
}

function getValidatedFd(fd: any): number {
    validateInt32(fd, 'fd', 0);
    return fd;
}

function validateOffsetLengthRead(offset: number, length: number, bufferLength: number): void {
    if (offset < 0) throw outOfRange('offset', '>= 0', offset);
    if (length < 0) throw outOfRange('length', '>= 0', length);
    if (offset + length > bufferLength) throw outOfRange('length', '<= ' + (bufferLength - offset), length);
}

function validateOffsetLengthWrite(offset: number, length: number, byteLength: number): void {
    if (offset > byteLength) throw outOfRange('offset', '<= ' + byteLength, offset);
    if (length > byteLength - offset) throw outOfRange('length', '<= ' + (byteLength - offset), length);
    if (length < 0) throw outOfRange('length', '>= 0', length);
    validateInt32(length, 'length', 0);
}

function getValidatedPath(path: any, name: string): string {
    if (typeof path === 'string') return path;
    if (Buffer.isBuffer(path)) return path.toString();
    // Node's isURL (internal/url.js).
    if (path !== null && path !== undefined && path.href && path.protocol && path.auth === undefined && path.path === undefined) {
        if (path.protocol !== 'file:') throw new NodeTypeError('ERR_INVALID_URL_SCHEME', 'The URL must be of scheme file');
        return decodeURIComponent(path.pathname);
    }
    throw invalidArgType(name, 'of type string or an instance of Buffer or URL', path);
}

function stringToFlags(flags: any): number {
    if (typeof flags === 'number') return flags;
    if (flags === null || flags === undefined) return __kml_native.fsFlags('r');
    const n = typeof flags === 'string' ? __kml_native.fsFlags(flags) : -1;
    if (n < 0) {
        throw new NodeTypeError('ERR_INVALID_ARG_VALUE', "The argument 'flags' is invalid. Received " + (typeof flags === 'string' ? "'" + flags + "'" : String(flags)));
    }
    return n;
}

function parseFileMode(value: any, name: string, def: number): number {
    if (value === null || value === undefined) return def;
    if (typeof value === 'string') {
        if (!/^[0-7]+$/.test(value)) {
            throw new NodeTypeError('ERR_INVALID_ARG_VALUE', "The argument '" + name + "' must be a 32-bit unsigned integer or an octal string. Received '" + value + "'");
        }
        return parseInt(value, 8);
    }
    validateInteger(value, name, 0);
    return value;
}

function uvError(errno: number, syscall: string, path?: string): NodeJS.ErrnoException {
    return __kml_native.fsError(errno, syscall, path) as NodeJS.ErrnoException;
}

// ---- lib/fs.js ----

export function open(path: PathLike, callback: (err: NodeJS.ErrnoException | null, fd: number) => void): void;
export function open(path: PathLike, flags: OpenMode | undefined, callback: (err: NodeJS.ErrnoException | null, fd: number) => void): void;
export function open(path: PathLike, flags: OpenMode | undefined, mode: Mode | undefined | null, callback: (err: NodeJS.ErrnoException | null, fd: number) => void): void;
export function open(path: any, ...rest: any[]): void {
    const p = getValidatedPath(path, 'path');
    let flags: any = 'r';
    let mode = 0o666;
    let callback: any;
    if (rest.length < 2) {
        callback = rest[0];
    } else if (typeof rest[1] === 'function') {
        flags = rest[0];
        callback = rest[1];
    } else {
        flags = rest[0];
        mode = parseFileMode(rest[1], 'mode', 0o666);
        callback = rest[2];
    }
    const f = stringToFlags(flags);
    validateFunction(callback, 'cb');
    const cb = callback as (err: NodeJS.ErrnoException | null, fd: number) => void;
    __kml_native.fsOpen(p, f, mode, (errno: number, fd: number) => {
        if (errno !== 0) cb(uvError(errno, 'open', p), 0);
        else cb(null, fd);
    });
}

export function close(fd: number, callback?: ErrnoCallback): void {
    const n = getValidatedFd(fd);
    if (callback !== undefined) validateFunction(callback, 'cb');
    __kml_native.fsClose(n, (errno: number, result: number) => {
        const err = errno !== 0 ? uvError(errno, 'close') : null;
        if (callback) callback(err);
        else if (err) throw err;
    });
}

export function fsync(fd: number, callback: ErrnoCallback): void {
    const n = getValidatedFd(fd);
    validateFunction(callback, 'cb');
    __kml_native.fsFsync(n, (errno: number, result: number) => {
        callback(errno !== 0 ? uvError(errno, 'fsync') : null);
    });
}

type ReadCallback = (err: NodeJS.ErrnoException | null, bytesRead: number, buffer: Buffer) => void;

export interface ReadOptions {
    buffer?: Buffer;
    offset?: number;
    length?: number;
    position?: number | null;
}

export function read(fd: number, buffer: Buffer, offset: number, length: number, position: number | null, callback: ReadCallback): void;
export function read(fd: number, buffer: Buffer, options: ReadOptions, callback: ReadCallback): void;
export function read(fd: number, options: ReadOptions, callback: ReadCallback): void;
export function read(fd: number, buffer: Buffer, callback: ReadCallback): void;
export function read(fd: number, callback: ReadCallback): void;
export function read(fd: number, ...rest: any[]): void {
    const n = getValidatedFd(fd);
    let buffer: any = rest[0];
    let offset: any = rest[1];
    let length: any = rest[2];
    let position: any = rest[3];
    let callback: any = rest[4];
    if (rest.length <= 3) {
        let params: any = null;
        if (rest.length === 3) {
            // read(fd, buffer, options, callback)
            params = rest[1];
            callback = rest[2];
        } else if (rest.length === 2) {
            // read(fd, bufferOrOptions, callback)
            if (!Buffer.isBuffer(rest[0])) {
                params = rest[0];
                buffer = params?.buffer ?? Buffer.alloc(16384);
            }
            callback = rest[1];
        } else {
            // read(fd, callback)
            callback = rest[0];
            buffer = Buffer.alloc(16384);
        }
        offset = params?.offset ?? 0;
        length = params?.length ?? buffer.length - offset;
        position = params?.position ?? null;
    }
    if (!Buffer.isBuffer(buffer)) throw invalidArgType('buffer', 'an instance of Buffer, TypedArray, or DataView', buffer);
    validateFunction(callback, 'cb');
    if (offset === null || offset === undefined) offset = 0;
    else validateInteger(offset, 'offset', 0);
    length = length | 0;
    if (position === null || position === undefined) position = -1;
    const buf = buffer as Buffer;
    const cb = callback as ReadCallback;
    if (length === 0) {
        process.nextTick(() => { cb(null, 0, buf); });
        return;
    }
    if (buf.length === 0) {
        throw new NodeTypeError('ERR_INVALID_ARG_VALUE', "The argument 'buffer' is empty and cannot be written. Received <Buffer >");
    }
    validateOffsetLengthRead(offset, length, buf.length);
    __kml_native.fsRead(n, buf, offset, length, position, (errno: number, bytesRead: number) => {
        if (errno !== 0) cb(uvError(errno, 'read'), 0, buf);
        else cb(null, bytesRead, buf);
    });
}

type WriteCallback = (err: NodeJS.ErrnoException | null, written: number, buffer: any) => void;

export function write(fd: number, buffer: Buffer, offset: number | undefined | null, length: number | undefined | null, position: number | undefined | null, callback: WriteCallback): void;
export function write(fd: number, buffer: Buffer, offset: number | undefined | null, length: number | undefined | null, callback: WriteCallback): void;
export function write(fd: number, buffer: Buffer, offset: number | undefined | null, callback: WriteCallback): void;
export function write(fd: number, buffer: Buffer, callback: WriteCallback): void;
export function write(fd: number, string: string, position: number | undefined | null, encoding: BufferEncoding | undefined | null, callback: WriteCallback): void;
export function write(fd: number, string: string, position: number | undefined | null, callback: WriteCallback): void;
export function write(fd: number, string: string, callback: WriteCallback): void;
export function write(fd: number, data: any, ...rest: any[]): void {
    const n = getValidatedFd(fd);
    if (Buffer.isBuffer(data)) {
        const buf = data as Buffer;
        let offset: any = rest[0];
        let length: any = rest[1];
        let position: any = rest[2];
        let callback: any = rest[3];
        if (!callback) callback = position || length || offset;
        validateFunction(callback, 'cb');
        if (offset === null || offset === undefined || typeof offset === 'function') offset = 0;
        else validateInteger(offset, 'offset', 0);
        if (typeof length !== 'number') length = buf.length - offset;
        if (typeof position !== 'number') position = -1;
        validateOffsetLengthWrite(offset, length, buf.length);
        const cb = callback as WriteCallback;
        __kml_native.fsWrite(n, buf, offset, length, position, (errno: number, written: number) => {
            if (errno !== 0) cb(uvError(errno, 'write'), 0, buf);
            else cb(null, written, buf);
        });
        return;
    }
    if (typeof data !== 'string') {
        throw invalidArgType('buffer', 'of type string or an instance of Buffer, TypedArray, or DataView', data);
    }
    const str = data as string;
    let position: any = rest[0];
    let encoding: any = rest[1];
    let callback: any = rest[2];
    if (typeof position === 'function') {
        callback = position;
        position = null;
        encoding = 'utf8';
    } else if (typeof encoding === 'function') {
        callback = encoding;
        encoding = 'utf8';
    }
    validateFunction(callback, 'cb');
    const bytes = Buffer.from(str, (encoding ?? 'utf8') as BufferEncoding);
    const cb = callback as WriteCallback;
    __kml_native.fsWrite(n, bytes, 0, bytes.length, typeof position === 'number' ? position : -1, (errno: number, written: number) => {
        if (errno !== 0) cb(uvError(errno, 'write'), 0, str);
        else cb(null, written, str);
    });
}

export function writev(fd: number, buffers: Buffer[], position: number | null, callback: (err: NodeJS.ErrnoException | null, bytesWritten: number, buffers: Buffer[]) => void): void;
export function writev(fd: number, buffers: Buffer[], callback: (err: NodeJS.ErrnoException | null, bytesWritten: number, buffers: Buffer[]) => void): void;
export function writev(fd: number, buffers: Buffer[], ...rest: any[]): void {
    const n = getValidatedFd(fd);
    let position: any = rest[0];
    let callback: any = rest[1];
    if (!callback) callback = position;
    validateFunction(callback, 'cb');
    const cb = callback as (err: NodeJS.ErrnoException | null, bytesWritten: number, buffers: Buffer[]) => void;
    if (buffers.length === 0) {
        process.nextTick(() => { cb(null, 0, buffers); });
        return;
    }
    // One positioned write of the gathered buffers: writev(2)'s result.
    const all = Buffer.concat(buffers);
    __kml_native.fsWrite(n, all, 0, all.length, typeof position === 'number' ? position : -1, (errno: number, written: number) => {
        if (errno !== 0) cb(uvError(errno, 'write'), 0, buffers);
        else cb(null, written, buffers);
    });
}

// ---- lib/internal/fs/streams.js ----

export interface ReadStreamOptions {
    flags?: OpenMode;
    encoding?: BufferEncoding;
    fd?: number | null;
    mode?: number;
    autoClose?: boolean;
    emitClose?: boolean;
    start?: number;
    end?: number;
    highWaterMark?: number;
}

export interface WriteStreamOptions {
    flags?: OpenMode;
    encoding?: BufferEncoding;
    fd?: number | null;
    mode?: number;
    autoClose?: boolean;
    emitClose?: boolean;
    start?: number;
    flush?: boolean;
    highWaterMark?: number;
}

function streamOptions(options: any): any {
    if (options === null || options === undefined) return {};
    if (typeof options === 'string') return { encoding: options };
    if (typeof options !== 'object') throw invalidArgType('options', 'one of type string or object', options);
    return options;
}

// Node's errorOrDestroy (internal/streams/destroy.js), for a read error.
function errorOrDestroy(stream: ReadStream, err: Error): void {
    const r = stream._readableState!;
    if (r.destroyed) return;
    if (r.autoDestroy) {
        stream.destroy(err);
        return;
    }
    if (!r.errored) r.errored = err;
    if (r.errorEmitted) return;
    r.errorEmitted = true;
    stream.emit('error', err);
}

// Close a stream's descriptor, flushing first when asked (the streams'
// close/_close); clear() marks the stream closed once the close is issued.
function closeFile(fd: number | null, flush: boolean, err: Error | null, clear: () => void, cb: (err?: Error | null) => void): void {
    if (fd === null) {
        cb(err);
        return;
    }
    const n = fd;
    const doClose = (e: Error | null) => {
        close(n, (er: NodeJS.ErrnoException | null) => {
            cb(er ?? e);
        });
        clear();
    };
    if (flush) {
        fsync(n, (flushErr: NodeJS.ErrnoException | null) => { doClose(err ?? flushErr); });
    } else {
        doClose(err);
    }
}

export class ReadStream extends Readable {
    fd: number | null = null;
    path: string = '';
    flags: OpenMode = 'r';
    mode: number = 0o666;
    start: number | undefined = undefined;
    end: number = Infinity;
    pos: number | undefined = undefined;
    bytesRead = 0;
    private performingIO = false;
    private onIoDone: ((er: Error | null) => void) | null = null;

    constructor(path: PathLike | null, options?: BufferEncoding | ReadStreamOptions);
    constructor(path: any, options?: any) {
        const opts = streamOptions(options);
        const ro: ReadableOptions = {
            highWaterMark: opts.highWaterMark === undefined ? 64 * 1024 : opts.highWaterMark,
            encoding: opts.encoding,
            emitClose: opts.emitClose,
            autoDestroy: opts.autoClose === undefined ? true : opts.autoClose,
        };
        super(ro);
        if (opts.fd === null || opts.fd === undefined) {
            this.path = getValidatedPath(path, 'path');
            this.flags = opts.flags === undefined ? 'r' : opts.flags;
            this.mode = opts.mode === undefined ? 0o666 : opts.mode;
        } else {
            this.fd = getValidatedFd(opts.fd);
        }
        this.start = opts.start;
        if (this.start !== undefined) {
            validateInteger(this.start, 'start', 0);
            this.pos = this.start;
        }
        if (opts.end === undefined) {
            this.end = Infinity;
        } else if (opts.end !== Infinity) {
            validateInteger(opts.end, 'end', 0);
            this.end = opts.end;
            if (this.start !== undefined && this.start > this.end) {
                throw new NodeRangeError('ERR_OUT_OF_RANGE', 'The value of "start" is out of range. It must be <= "end" (here: ' + this.end + '). Received ' + this.start);
            }
        }
    }

    get pending(): boolean { return this.fd === null; }

    private flushOnClose(): boolean { return false; }

    get autoClose(): boolean { return this._readableState!.autoDestroy; }
    set autoClose(val: boolean) { this._readableState!.autoDestroy = val; }

    _construct(callback: (error?: Error | null) => void): void {
        if (this.fd !== null) {
            callback();
            return;
        }
        open(this.path, this.flags, this.mode, (er: NodeJS.ErrnoException | null, fd: number) => {
            if (er) {
                callback(er);
            } else {
                this.fd = fd;
                callback();
                this.emit('open', fd);
                this.emit('ready');
            }
        });
    }

    _read(size: number): void {
        const n = this.pos !== undefined ? Math.min(this.end - this.pos + 1, size) : Math.min(this.end - this.bytesRead + 1, size);
        if (n <= 0) {
            this.push(null);
            return;
        }
        const buf = Buffer.allocUnsafeSlow(n);
        this.performingIO = true;
        read(this.fd!, buf, 0, n, this.pos ?? null, (er: NodeJS.ErrnoException | null, bytesRead: number, b: Buffer) => {
            this.performingIO = false;
            if (this.destroyed) {
                // Tell _destroy that it is safe to close the descriptor now.
                const done = this.onIoDone;
                this.onIoDone = null;
                if (done !== null) done(er);
                return;
            }
            if (er) {
                errorOrDestroy(this, er);
            } else if (bytesRead > 0) {
                if (this.pos !== undefined) this.pos += bytesRead;
                this.bytesRead += bytesRead;
                let chunk = b;
                if (bytesRead !== b.length) {
                    // Copy rather than slice, not to keep a large buffer for a small read.
                    chunk = Buffer.allocUnsafeSlow(bytesRead);
                    b.copy(chunk, 0, 0, bytesRead);
                }
                this.push(chunk);
            } else {
                this.push(null);
            }
        });
    }

    _destroy(err: Error | null, cb: (error?: Error | null) => void): void {
        // A read in flight on the pool still uses the descriptor: close it
        // once the read is done.
        const clear = () => { this.fd = null; };
        if (this.performingIO) {
            this.onIoDone = (er: Error | null) => { closeFile(this.fd, this.flushOnClose(), err ?? er, clear, cb); };
        } else {
            closeFile(this.fd, this.flushOnClose(), err, clear, cb);
        }
    }

    close(cb?: (err?: Error | null) => void): void {
        if (typeof cb === 'function') finished(this, cb);
        this.destroy();
    }
}

export class WriteStream extends Writable {
    fd: number | null = null;
    path: string = '';
    flags: OpenMode = 'w';
    mode: number = 0o666;
    start: number | undefined = undefined;
    pos: number | undefined = undefined;
    bytesWritten = 0;
    flush = false;
    private performingIO = false;
    private onIoDone: ((er: Error | null) => void) | null = null;

    constructor(path: PathLike | null, options?: BufferEncoding | WriteStreamOptions);
    constructor(path: any, options?: any) {
        const opts = streamOptions(options);
        const wo: WritableOptions = {
            highWaterMark: opts.highWaterMark,
            decodeStrings: true,
            emitClose: opts.emitClose,
            autoDestroy: opts.autoClose === undefined ? true : opts.autoClose,
        };
        super(wo);
        if (opts.fd === null || opts.fd === undefined) {
            this.path = getValidatedPath(path, 'path');
            this.flags = opts.flags === undefined ? 'w' : opts.flags;
            this.mode = opts.mode === undefined ? 0o666 : opts.mode;
        } else {
            this.fd = getValidatedFd(opts.fd);
        }
        if (opts.flush !== null && opts.flush !== undefined) {
            if (typeof opts.flush !== 'boolean') throw invalidArgType('options.flush', 'of type boolean', opts.flush);
            this.flush = opts.flush;
        }
        this.start = opts.start;
        if (this.start !== undefined) {
            validateInteger(this.start, 'start', 0);
            this.pos = this.start;
        }
        if (opts.encoding) this.setDefaultEncoding(opts.encoding);
    }

    get pending(): boolean { return this.fd === null; }

    private flushOnClose(): boolean { return this.flush; }

    get autoClose(): boolean { return this._writableState!.autoDestroy; }
    set autoClose(val: boolean) { this._writableState!.autoDestroy = val; }

    _construct(callback: (error?: Error | null) => void): void {
        if (this.fd !== null) {
            callback();
            return;
        }
        open(this.path, this.flags, this.mode, (er: NodeJS.ErrnoException | null, fd: number) => {
            if (er) {
                callback(er);
            } else {
                this.fd = fd;
                callback();
                this.emit('open', fd);
                this.emit('ready');
            }
        });
    }

    private writeAll(data: Buffer, size: number, pos: number | undefined, cb: (er?: Error | null) => void, retries: number): void {
        write(this.fd!, data, 0, size, pos ?? null, (er: NodeJS.ErrnoException | null, bytesWritten: number, buffer: any) => {
            // No data available now: try again.
            if (er !== null && er.code === 'EAGAIN') {
                er = null;
                bytesWritten = 0;
            }
            if (this.destroyed || er) {
                cb(er ?? new NodeError('ERR_STREAM_DESTROYED', 'Cannot call write after a stream was destroyed'));
                return;
            }
            this.bytesWritten += bytesWritten;
            const tries = bytesWritten ? 0 : retries + 1;
            const left = size - bytesWritten;
            const next = pos === undefined ? undefined : pos + bytesWritten;
            // Up to five tries at writing a non-zero number of bytes.
            if (tries > 5) {
                cb(new NodeError('ERR_SYSTEM_ERROR', 'A system error occurred: write failed'));
            } else if (left) {
                this.writeAll(data.subarray(bytesWritten), left, next, cb, tries);
            } else {
                cb();
            }
        });
    }

    _write(data: any, encoding: BufferEncoding, cb: (error?: Error | null) => void): void {
        const buf = data as Buffer;
        this.performingIO = true;
        this.writeAll(buf, buf.length, this.pos, (er?: Error | null) => {
            this.performingIO = false;
            if (this.destroyed) {
                // Tell _destroy that it is safe to close the descriptor now.
                cb(er);
                const done = this.onIoDone;
                this.onIoDone = null;
                if (done !== null) done(er ?? null);
                return;
            }
            cb(er);
        }, 0);
        if (this.pos !== undefined) this.pos += buf.length;
    }

    _destroy(err: Error | null, cb: (error?: Error | null) => void): void {
        // A write in flight on the pool still uses the descriptor: close it
        // once the write is done.
        const clear = () => { this.fd = null; };
        if (this.performingIO) {
            this.onIoDone = (er: Error | null) => { closeFile(this.fd, this.flushOnClose(), err ?? er, clear, cb); };
        } else {
            closeFile(this.fd, this.flushOnClose(), err, clear, cb);
        }
    }

    close(cb?: (err?: Error | null) => void): void {
        if (cb) {
            if (this.closed) {
                process.nextTick(cb);
                return;
            }
            this.on('close', cb);
        }
        // Not auto-closing: destroy on 'finish'.
        if (!this.autoClose) this.on('finish', () => { this.destroy(); });
        // end(), not destroy(): https://github.com/nodejs/node/issues/2006
        this.end();
    }

    // There is no shutdown() for files.
    destroySoon(): this {
        this.end();
        return this;
    }
}

export function createReadStream(path: PathLike, options?: BufferEncoding | ReadStreamOptions): ReadStream;
export function createReadStream(path: any, options?: any): ReadStream {
    return new ReadStream(path, options);
}

export function createWriteStream(path: PathLike, options?: BufferEncoding | WriteStreamOptions): WriteStream;
export function createWriteStream(path: any, options?: any): WriteStream {
    return new WriteStream(path, options);
}
