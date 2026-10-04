// Node's lib/fs.js: the synchronous and callback file operations, ported
// from Node v24, and the file streams (ReadStream, WriteStream,
// createReadStream, createWriteStream) from lib/internal/fs/streams.js.
// Each operation is one libuv fs request (internal_fs_utils.ts's binding):
// a *Sync one runs on the calling thread and throws, the others run on the
// thread pool and call back on the loop thread, as libuv's do.
import { Readable, Writable, finished } from 'stream';
import type { ReadableOptions, WritableOptions } from 'stream';
import { EventEmitter } from 'events';
import { join as pathJoin, relative as pathRelative, resolve as pathResolve } from 'path';
import {
    NodeError, NodeTypeError, NodeRangeError, SystemError, invalidArgType, invalidArgValue, validateFunction, validateBoolean,
    validateObject, validateInteger, validateInt32, validateUint32, outOfRange, getValidatedFd,
    validateOffsetLengthRead, validateOffsetLengthWrite, getValidatedPath, stringToFlags, parseFileMode,
    getOptions, toUnixTimestamp, isArrayBufferView, viewBytes, uvError, callSync, callAsync, lastString,
    statBuffer, getStatsFromBinding, getStatFsFromBinding, decodeEntries, direntsOf, encodeNames, encodeString,
    Stats, StatFs, Dirent, constants, assertEncoding, checkAborted,
    OP_OPEN, OP_CLOSE, OP_FSYNC, OP_FDATASYNC, OP_READ, OP_WRITE, OP_STAT, OP_LSTAT, OP_FSTAT, OP_STATFS,
    OP_MKDIR, OP_MKDIRP, OP_RMDIR, OP_UNLINK, OP_RENAME, OP_LINK, OP_SYMLINK, OP_READLINK, OP_REALPATH,
    OP_MKDTEMP, OP_CHMOD, OP_FCHMOD, OP_CHOWN, OP_FCHOWN, OP_LCHOWN, OP_FTRUNCATE, OP_ACCESS, OP_UTIMES,
    OP_FUTIMES, OP_LUTIMES, OP_COPYFILE, OP_READDIR,
} from './internal_fs_utils';
import type {
    PathLike, PathOrFileDescriptor, TimeLike, Mode, OpenMode, NoParamCallback, ObjectEncodingOptions,
    EncodingOption, WriteFileOptions, RmOptions, RmDirOptions, MakeDirectoryOptions, StatOptions,
    StatSyncOptions, StatFsOptions, Abortable,
} from './internal_fs_utils';

export { Stats, Dirent, constants, F_OK, R_OK, W_OK, X_OK } from './internal_fs_utils';
export type {
    PathLike, PathOrFileDescriptor, TimeLike, Mode, OpenMode, NoParamCallback, ObjectEncodingOptions,
    EncodingOption, WriteFileOptions, RmOptions, RmDirOptions, MakeDirectoryOptions, StatOptions,
    StatSyncOptions, StatFsOptions, Abortable,
} from './internal_fs_utils';
export type StatsFs = StatFs;

type ErrnoCallback = (err: NodeJS.ErrnoException | null) => void;

// Node's makeCallback: the last argument must be the callback.
function makeCallback(cb: any): any {
    validateFunction(cb, 'cb');
    return cb;
}

function isFd(path: any): boolean {
    return typeof path === 'number' && (path >>> 0) === path;
}

function validateStringAfterArrayBufferView(data: any, name: string): void {
    if (typeof data !== 'string') {
        throw invalidArgType(name, 'of type string or an instance of Buffer, TypedArray, or DataView', data);
    }
}

// Node's getValidMode for access/copyFile: an integer in [0, 7].
function getValidMode(mode: any, type: string): number {
    const def = type === 'copyFile' ? 0 : 0;
    if (mode === null || mode === undefined) return def;
    validateInteger(mode, 'mode', -Number.MAX_SAFE_INTEGER);
    if (mode < 0 || mode > 7) {
        throw new NodeRangeError('ERR_OUT_OF_RANGE', 'mode is out of range: >= 0 && <= 7');
    }
    return mode;
}

function validatePosition(position: any, name: string): void {
    if (typeof position === 'number') {
        validateInteger(position, name, -1);
    } else if (typeof position !== 'bigint') {
        throw invalidArgType(name, 'of type number or bigint', position);
    }
}

function validateBuffer(buffer: any): void {
    if (!isArrayBufferView(buffer)) {
        throw invalidArgType('buffer', 'an instance of Buffer, TypedArray, or DataView', buffer);
    }
}

function validateUid(value: any, name: string): void {
    if (typeof value !== 'number') throw invalidArgType(name, 'of type number', value);
    if (!Number.isInteger(value)) throw outOfRange(name, 'an integer', value);
    if (value < -1 || value > 4294967295) throw outOfRange(name, '>= -1 && <= 4294967295', value);
}

// ---- open / close / fsync ----

export function openSync(path: PathLike, flags?: OpenMode, mode?: Mode | null): number {
    const p = getValidatedPath(path, 'path');
    return callSync(OP_OPEN, 'open', p, undefined, stringToFlags(flags), parseFileMode(mode, 'mode', 0o666), 0, 0);
}

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
    const cb = makeCallback(callback) as (err: NodeJS.ErrnoException | null, fd: number) => void;
    callAsync(OP_OPEN, 'open', p, undefined, f, mode, 0, 0, undefined, (err, fd) => { cb(err, fd); });
}

export function closeSync(fd: number): void {
    callSync(OP_CLOSE, 'close', undefined, undefined, getValidatedFd(fd), 0, 0, 0);
}

export function close(fd: number, callback?: NoParamCallback): void {
    const n = getValidatedFd(fd);
    if (callback !== undefined) validateFunction(callback, 'cb');
    callAsync(OP_CLOSE, 'close', undefined, undefined, n, 0, 0, 0, undefined, (err) => {
        if (callback) callback(err);
        else if (err) throw err;
    });
}

export function fsyncSync(fd: number): void {
    callSync(OP_FSYNC, 'fsync', undefined, undefined, getValidatedFd(fd), 0, 0, 0);
}

export function fsync(fd: number, callback: NoParamCallback): void {
    const n = getValidatedFd(fd);
    const cb = makeCallback(callback) as NoParamCallback;
    callAsync(OP_FSYNC, 'fsync', undefined, undefined, n, 0, 0, 0, undefined, (err) => { cb(err); });
}

export function fdatasyncSync(fd: number): void {
    callSync(OP_FDATASYNC, 'fdatasync', undefined, undefined, getValidatedFd(fd), 0, 0, 0);
}

export function fdatasync(fd: number, callback: NoParamCallback): void {
    const n = getValidatedFd(fd);
    const cb = makeCallback(callback) as NoParamCallback;
    callAsync(OP_FDATASYNC, 'fdatasync', undefined, undefined, n, 0, 0, 0, undefined, (err) => { cb(err); });
}

// ---- read / write ----

export interface ReadOptions {
    buffer?: Buffer;
    offset?: number;
    length?: number;
    position?: number | null;
}

export interface ReadSyncOptions {
    offset?: number | undefined;
    length?: number | undefined;
    position?: number | null | undefined;
}

export function readSync(fd: number, buffer: NodeJS.ArrayBufferView, offset: number, length: number, position?: number | null): number;
export function readSync(fd: number, buffer: NodeJS.ArrayBufferView, opts?: ReadSyncOptions): number;
export function readSync(fd: number, buffer: any, ...rest: any[]): number {
    const n = getValidatedFd(fd);
    validateBuffer(buffer);
    let offset: any = rest[0];
    let length: any = rest[1];
    let position: any = rest[2];
    if (rest.length <= 1 || (typeof offset === 'object' && offset !== null)) {
        const o: any = offset ?? {};
        if (offset !== undefined) validateObject(o, 'options');
        offset = o.offset ?? 0;
        length = o.length ?? buffer.byteLength - offset;
        position = o.position ?? null;
    }
    if (offset === undefined) offset = 0;
    else validateInteger(offset, 'offset', 0);
    length = length | 0;
    if (length === 0) return 0;
    if (buffer.byteLength === 0) {
        throw new NodeTypeError('ERR_INVALID_ARG_VALUE', "The argument 'buffer' is empty and cannot be written. Received <Buffer >");
    }
    validateOffsetLengthRead(offset, length, buffer.byteLength);
    if (position === null || position === undefined) position = -1;
    else validatePosition(position, 'position');
    return callSync(OP_READ, 'read', undefined, undefined, n, offset, length, Number(position), viewBytes(buffer));
}

type ReadCallback = (err: NodeJS.ErrnoException | null, bytesRead: number, buffer: Buffer) => void;

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
            if (!isArrayBufferView(rest[0])) {
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
        length = params?.length ?? buffer.byteLength - offset;
        position = params?.position ?? null;
    }
    validateBuffer(buffer);
    const cb = makeCallback(callback) as ReadCallback;
    if (offset === null || offset === undefined) offset = 0;
    else validateInteger(offset, 'offset', 0);
    length = length | 0;
    const buf = buffer as Buffer;
    if (length === 0) {
        process.nextTick(() => { cb(null, 0, buf); });
        return;
    }
    if (buf.byteLength === 0) {
        throw new NodeTypeError('ERR_INVALID_ARG_VALUE', "The argument 'buffer' is empty and cannot be written. Received <Buffer >");
    }
    validateOffsetLengthRead(offset, length, buf.byteLength);
    if (position === null || position === undefined) position = -1;
    else validatePosition(position, 'position');
    callAsync(OP_READ, 'read', undefined, undefined, n, offset, length, Number(position), viewBytes(buf), (err, bytesRead) => {
        cb(err, bytesRead, buf);
    });
}

export function readvSync(fd: number, buffers: readonly NodeJS.ArrayBufferView[], position?: number | null): number {
    const n = getValidatedFd(fd);
    let pos = position === null || position === undefined ? -1 : position;
    let total = 0;
    for (let i = 0; i < buffers.length; i++) {
        const b: any = buffers[i];
        if (b.byteLength === 0) continue;
        const r = callSync(OP_READ, 'read', undefined, undefined, n, 0, b.byteLength, pos, viewBytes(b));
        total += r;
        if (pos >= 0) pos += r;
        if (r < b.byteLength) break;
    }
    return total;
}

export function readv(fd: number, buffers: readonly NodeJS.ArrayBufferView[], cb: (err: NodeJS.ErrnoException | null, bytesRead: number, buffers: NodeJS.ArrayBufferView[]) => void): void;
export function readv(fd: number, buffers: readonly NodeJS.ArrayBufferView[], position: number | null, cb: (err: NodeJS.ErrnoException | null, bytesRead: number, buffers: NodeJS.ArrayBufferView[]) => void): void;
export function readv(fd: number, buffers: any, ...rest: any[]): void {
    const n = getValidatedFd(fd);
    let position: any = rest[0];
    let callback: any = rest[1];
    if (typeof position === 'function') {
        callback = position;
        position = null;
    }
    const cb = makeCallback(callback);
    const bufs: any[] = buffers;
    let pos: number = typeof position === 'number' ? position : -1;
    let total = 0;
    const next = (i: number): void => {
        if (i >= bufs.length) {
            cb(null, total, bufs);
            return;
        }
        const b: any = bufs[i];
        callAsync(OP_READ, 'read', undefined, undefined, n, 0, b.byteLength, pos, viewBytes(b), (err, r) => {
            if (err) {
                cb(err, 0, bufs);
                return;
            }
            total += r;
            if (pos >= 0) pos += r;
            if (r < b.byteLength) cb(null, total, bufs);
            else next(i + 1);
        });
    };
    next(0);
}

export interface WriteSyncOptions {
    offset?: number | undefined;
    length?: number | undefined;
    position?: number | null | undefined;
}

export function writeSync(fd: number, buffer: NodeJS.ArrayBufferView, offset?: number | null, length?: number | null, position?: number | null): number;
export function writeSync(fd: number, buffer: NodeJS.ArrayBufferView, options?: WriteSyncOptions): number;
export function writeSync(fd: number, string: string, position?: number | null, encoding?: BufferEncoding | null): number;
export function writeSync(fd: number, buffer: any, ...rest: any[]): number {
    const n = getValidatedFd(fd);
    let offset: any = rest[0];
    let length: any = rest[1];
    let position: any = rest[2];
    if (isArrayBufferView(buffer)) {
        if (typeof offset === 'object' && offset !== null) {
            const o: any = offset;
            offset = o.offset ?? 0;
            length = o.length ?? buffer.byteLength - offset;
            position = o.position ?? null;
        }
        if (position === undefined || position === null) position = -1;
        if (typeof offset !== 'number') offset = 0;
        if (typeof length !== 'number') length = buffer.byteLength - offset;
        validateOffsetLengthWrite(offset, length, buffer.byteLength);
        return callSync(OP_WRITE, 'write', undefined, undefined, n, offset, length, position, viewBytes(buffer));
    }
    validateStringAfterArrayBufferView(buffer, 'buffer');
    // writeSync(fd, string[, position[, encoding]])
    const encoding: any = length ?? 'utf8';
    assertEncoding(encoding);
    const bytes = Buffer.from(buffer as string, encoding as BufferEncoding);
    const pos = typeof offset === 'number' ? offset : -1;
    return callSync(OP_WRITE, 'write', undefined, undefined, n, 0, bytes.length, pos, bytes);
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
    if (isArrayBufferView(data)) {
        const buf: any = data;
        let offset: any = rest[0];
        let length: any = rest[1];
        let position: any = rest[2];
        let callback: any = rest[3];
        if (typeof offset === 'object' && offset !== null) {
            const o: any = offset;
            callback = length;
            offset = o.offset ?? 0;
            length = o.length ?? buf.byteLength - offset;
            position = o.position ?? null;
        }
        if (typeof callback !== 'function') callback = typeof position === 'function' ? position : typeof length === 'function' ? length : offset;
        const cb = makeCallback(callback) as WriteCallback;
        if (offset === null || offset === undefined || typeof offset === 'function') offset = 0;
        else validateInteger(offset, 'offset', 0);
        if (typeof length !== 'number') length = buf.byteLength - offset;
        if (typeof position !== 'number') position = -1;
        validateOffsetLengthWrite(offset, length, buf.byteLength);
        callAsync(OP_WRITE, 'write', undefined, undefined, n, offset, length, position, viewBytes(buf), (err, written) => {
            cb(err, written, buf);
        });
        return;
    }
    validateStringAfterArrayBufferView(data, 'buffer');
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
    const cb = makeCallback(callback) as WriteCallback;
    const bytes = Buffer.from(str, (encoding ?? 'utf8') as BufferEncoding);
    callAsync(OP_WRITE, 'write', undefined, undefined, n, 0, bytes.length, typeof position === 'number' ? position : -1, bytes, (err, written) => {
        cb(err, written, str);
    });
}

export function writevSync(fd: number, buffers: readonly NodeJS.ArrayBufferView[], position?: number | null): number {
    const n = getValidatedFd(fd);
    if (buffers.length === 0) return 0;
    const all = Buffer.concat(buffers.map((b: any) => viewBytes(b)));
    return callSync(OP_WRITE, 'write', undefined, undefined, n, 0, all.length, typeof position === 'number' ? position : -1, all);
}

export function writev(fd: number, buffers: Buffer[], position: number | null, callback: (err: NodeJS.ErrnoException | null, bytesWritten: number, buffers: Buffer[]) => void): void;
export function writev(fd: number, buffers: Buffer[], callback: (err: NodeJS.ErrnoException | null, bytesWritten: number, buffers: Buffer[]) => void): void;
export function writev(fd: number, buffers: Buffer[], ...rest: any[]): void {
    const n = getValidatedFd(fd);
    let position: any = rest[0];
    let callback: any = rest[1];
    if (!callback) callback = position;
    const cb = makeCallback(callback) as (err: NodeJS.ErrnoException | null, bytesWritten: number, buffers: Buffer[]) => void;
    if (buffers.length === 0) {
        process.nextTick(() => { cb(null, 0, buffers); });
        return;
    }
    // One positioned write of the gathered buffers: writev(2)'s result.
    const all = Buffer.concat(buffers);
    callAsync(OP_WRITE, 'write', undefined, undefined, n, 0, all.length, typeof position === 'number' ? position : -1, all, (err, written) => {
        cb(err, written, buffers);
    });
}

// ---- readFile / writeFile / appendFile ----

const kReadFileUnknownBufferLength = 64 * 1024;
const kReadFileBufferLength = 512 * 1024;

function fstatSize(fd: number, isUserFd: boolean): number {
    const b = statBuffer();
    const r = __kml_native.fsCall(OP_FSTAT, '', '', fd, 0, 0, 0, b);
    if (r < 0) {
        if (!isUserFd) closeSync(fd);
        throw uvError(-r, 'fstat');
    }
    const st = getStatsFromBinding(b);
    return st.isFile() ? st.size : 0;
}

function readAllSync(fd: number, isUserFd: boolean): Buffer {
    const size = fstatSize(fd, isUserFd);
    let pos = 0;
    if (size === 0) {
        const buffers: Buffer[] = [];
        while (true) {
            const b = Buffer.allocUnsafe(kReadFileUnknownBufferLength);
            let n = 0;
            try {
                n = callSync(OP_READ, 'read', undefined, undefined, fd, 0, kReadFileUnknownBufferLength, -1, b);
            } catch (err) {
                if (!isUserFd) closeSync(fd);
                throw err;
            }
            if (n === 0) break;
            buffers.push(b.subarray(0, n));
            pos += n;
        }
        return Buffer.concat(buffers, pos);
    }
    const buffer = Buffer.allocUnsafe(size);
    while (pos < size) {
        let n = 0;
        try {
            n = callSync(OP_READ, 'read', undefined, undefined, fd, pos, Math.min(size - pos, kReadFileBufferLength), -1, buffer);
        } catch (err) {
            if (!isUserFd) closeSync(fd);
            throw err;
        }
        if (n === 0) break;
        pos += n;
    }
    return pos < size ? buffer.subarray(0, pos) : buffer;
}

export function readFileSync(path: PathOrFileDescriptor, options?: { encoding?: null | undefined; flag?: string | undefined } | null): NonSharedBuffer;
export function readFileSync(path: PathOrFileDescriptor, options: { encoding: BufferEncoding; flag?: string | undefined } | BufferEncoding): string;
export function readFileSync(path: PathOrFileDescriptor, options?: (ObjectEncodingOptions & { flag?: string | undefined }) | BufferEncoding | null): string | NonSharedBuffer;
export function readFileSync(path: any, options?: any): any {
    const opts = getOptions(options, { flag: 'r' });
    const isUserFd = isFd(path);
    const fd = isUserFd ? (path as number) : openSync(getValidatedPath(path, 'path'), opts.flag ?? 'r', 0o666);
    const buffer = readAllSync(fd, isUserFd);
    if (!isUserFd) closeSync(fd);
    if (opts.encoding && opts.encoding !== 'buffer') return buffer.toString(opts.encoding);
    return buffer;
}

export function readFile(path: PathOrFileDescriptor, options: ({ encoding?: null | undefined; flag?: string | undefined } & Abortable) | undefined | null, callback: (err: NodeJS.ErrnoException | null, data: NonSharedBuffer) => void): void;
export function readFile(path: PathOrFileDescriptor, options: ({ encoding: BufferEncoding; flag?: string | undefined } & Abortable) | BufferEncoding, callback: (err: NodeJS.ErrnoException | null, data: string) => void): void;
export function readFile(path: PathOrFileDescriptor, options: (ObjectEncodingOptions & { flag?: string | undefined } & Abortable) | BufferEncoding | undefined | null, callback: (err: NodeJS.ErrnoException | null, data: string | NonSharedBuffer) => void): void;
export function readFile(path: PathOrFileDescriptor, callback: (err: NodeJS.ErrnoException | null, data: NonSharedBuffer) => void): void;
export function readFile(path: any, options: any, callback?: any): void {
    let cbArg: any = callback;
    let optArg: any = options;
    if (cbArg === undefined) {
        cbArg = options;
        optArg = undefined;
    }
    const cb = makeCallback(cbArg) as (err: any, data: any) => void;
    const opts = getOptions(optArg, { flag: 'r' });
    const isUserFd = isFd(path);
    const signal: any = opts.signal;
    const aborted0 = checkAborted(signal);
    if (aborted0 !== null) {
        cb(aborted0, undefined);
        return;
    }
    const finish = (fd: number, err: any, data: any): void => {
        if (isUserFd) {
            cb(err, data);
            return;
        }
        callAsync(OP_CLOSE, 'close', undefined, undefined, fd, 0, 0, 0, undefined, (closeErr) => {
            if (err) cb(err, undefined);
            else if (closeErr) cb(closeErr, undefined);
            else cb(null, data);
        });
    };
    const readFrom = (fd: number): void => {
        const b = statBuffer();
        callAsync(OP_FSTAT, 'fstat', undefined, undefined, fd, 0, 0, 0, b, (err) => {
            if (err) {
                finish(fd, err, undefined);
                return;
            }
            const st = getStatsFromBinding(b);
            const size = st.isFile() ? st.size : 0;
            const chunks: Buffer[] = [];
            let total = 0;
            const step = (): void => {
                const want = size === 0 ? kReadFileUnknownBufferLength : Math.min(size - total, kReadFileBufferLength);
                if (size !== 0 && want <= 0) {
                    done();
                    return;
                }
                const aborted = checkAborted(signal);
                if (aborted !== null) {
                    finish(fd, aborted, undefined);
                    return;
                }
                const chunk = Buffer.allocUnsafe(want);
                callAsync(OP_READ, 'read', undefined, undefined, fd, 0, want, -1, chunk, (rerr, n) => {
                    if (rerr) {
                        finish(fd, rerr, undefined);
                        return;
                    }
                    if (n === 0) {
                        done();
                        return;
                    }
                    chunks.push(chunk.subarray(0, n));
                    total += n;
                    step();
                });
            };
            const done = (): void => {
                const all = Buffer.concat(chunks, total);
                finish(fd, null, opts.encoding && opts.encoding !== 'buffer' ? all.toString(opts.encoding) : all);
            };
            step();
        });
    };
    if (isUserFd) {
        readFrom(path as number);
        return;
    }
    const p = getValidatedPath(path, 'path');
    const flags = stringToFlags(opts.flag ?? 'r');
    callAsync(OP_OPEN, 'open', p, undefined, flags, 0o666, 0, 0, undefined, (err, fd) => {
        if (err) cb(err, undefined);
        else readFrom(fd);
    });
}

// The bytes of writeFile's data.
function writeData(data: any, encoding: any): Uint8Array {
    if (isArrayBufferView(data)) return viewBytes(data);
    validateStringAfterArrayBufferView(data, 'data');
    return Buffer.from(data as string, (encoding || 'utf8') as BufferEncoding);
}

export function writeFileSync(file: PathOrFileDescriptor, data: string | NodeJS.ArrayBufferView, options?: WriteFileOptions): void;
export function writeFileSync(path: any, data: any, options?: any): void {
    const opts = getOptions(options, { encoding: 'utf8', mode: 0o666, flag: 'w', flush: false });
    const flush = opts.flush ?? false;
    validateBoolean(flush, 'options.flush');
    const flag = opts.flag || 'w';
    const bytes = writeData(data, opts.encoding);
    const isUserFd = isFd(path);
    const fd = isUserFd ? (path as number) : openSync(getValidatedPath(path, 'path'), flag, opts.mode);
    let offset = 0;
    let length = bytes.byteLength;
    try {
        while (length > 0) {
            const written = callSync(OP_WRITE, 'write', undefined, undefined, fd, offset, length, -1, bytes);
            offset += written;
            length -= written;
        }
        if (flush) fsyncSync(fd);
    } finally {
        if (!isUserFd) closeSync(fd);
    }
}

export function writeFile(file: PathOrFileDescriptor, data: string | NodeJS.ArrayBufferView, options: WriteFileOptions, callback: NoParamCallback): void;
export function writeFile(path: PathOrFileDescriptor, data: string | NodeJS.ArrayBufferView, callback: NoParamCallback): void;
export function writeFile(path: any, data: any, options: any, callback?: any): void {
    let cbArg: any = callback;
    let optArg: any = options;
    if (cbArg === undefined) {
        cbArg = options;
        optArg = undefined;
    }
    const cb = makeCallback(cbArg) as NoParamCallback;
    const opts = getOptions(optArg, { encoding: 'utf8', mode: 0o666, flag: 'w', flush: false });
    const flush = opts.flush ?? false;
    validateBoolean(flush, 'options.flush');
    const flag = opts.flag || 'w';
    const bytes = writeData(data, opts.encoding);
    const isUserFd = isFd(path);
    const signal: any = opts.signal;
    const aborted0 = checkAborted(signal);
    if (aborted0 !== null) {
        cb(aborted0);
        return;
    }
    const writeAll = (fd: number): void => {
        let offset = 0;
        const finish = (err: any): void => {
            if (isUserFd) {
                cb(err);
                return;
            }
            callAsync(OP_CLOSE, 'close', undefined, undefined, fd, 0, 0, 0, undefined, (closeErr) => {
                cb(err ?? closeErr);
            });
        };
        const step = (): void => {
            if (offset >= bytes.byteLength) {
                if (flush) callAsync(OP_FSYNC, 'fsync', undefined, undefined, fd, 0, 0, 0, undefined, (ferr) => { finish(ferr); });
                else finish(null);
                return;
            }
            const aborted = checkAborted(signal);
            if (aborted !== null) {
                finish(aborted);
                return;
            }
            callAsync(OP_WRITE, 'write', undefined, undefined, fd, offset, bytes.byteLength - offset, -1, bytes, (err, written) => {
                if (err) {
                    finish(err);
                    return;
                }
                offset += written;
                step();
            });
        };
        step();
    };
    if (isUserFd) {
        writeAll(path as number);
        return;
    }
    const p = getValidatedPath(path, 'path');
    callAsync(OP_OPEN, 'open', p, undefined, stringToFlags(flag), parseFileMode(opts.mode, 'mode', 0o666), 0, 0, undefined, (err, fd) => {
        if (err) cb(err);
        else writeAll(fd);
    });
}

function appendOptions(path: any, options: any): any {
    const opts = { ...getOptions(options, { encoding: 'utf8', mode: 0o666, flag: 'a' }) };
    if (!opts.flag || isFd(path)) opts.flag = 'a';
    return opts;
}

export function appendFileSync(path: PathOrFileDescriptor, data: string | Uint8Array, options?: WriteFileOptions): void;
export function appendFileSync(path: any, data: any, options?: any): void {
    writeFileSync(path, data, appendOptions(path, options));
}

export function appendFile(path: PathOrFileDescriptor, data: string | Uint8Array, options: WriteFileOptions, callback: NoParamCallback): void;
export function appendFile(file: PathOrFileDescriptor, data: string | Uint8Array, callback: NoParamCallback): void;
export function appendFile(path: any, data: any, options: any, callback?: any): void {
    let cbArg: any = callback;
    let optArg: any = options;
    if (cbArg === undefined) {
        cbArg = options;
        optArg = undefined;
    }
    const cb = makeCallback(cbArg) as NoParamCallback;
    writeFile(path, data, appendOptions(path, optArg), cb);
}

// ---- exists / access ----

let existsWarned = false;

export function existsSync(path: PathLike): boolean;
export function existsSync(path: any): boolean {
    let p: string;
    try {
        p = getValidatedPath(path, 'path');
    } catch (err) {
        if (!existsWarned && (err as any).code === 'ERR_INVALID_ARG_TYPE') {
            existsWarned = true;
            process.emitWarning('Passing invalid argument types to fs.existsSync is deprecated', 'DeprecationWarning', 'DEP0187');
        }
        return false;
    }
    return __kml_native.fsCall(OP_ACCESS, p, '', 0, 0, 0, 0, Buffer.alloc(0)) === 0;
}

export function exists(path: PathLike, callback: (exists: boolean) => void): void;
export function exists(path: any, callback: any): void {
    validateFunction(callback, 'cb');
    let p: string;
    try {
        p = getValidatedPath(path, 'path');
    } catch (err) {
        callback(false);
        return;
    }
    callAsync(OP_ACCESS, 'access', p, undefined, 0, 0, 0, 0, undefined, (err) => { callback(err === null); });
}

export function accessSync(path: PathLike, mode?: number): void {
    const p = getValidatedPath(path, 'path');
    callSync(OP_ACCESS, 'access', p, undefined, getValidMode(mode, 'access'), 0, 0, 0);
}

export function access(path: PathLike, callback: NoParamCallback): void;
export function access(path: PathLike, mode: number | undefined, callback: NoParamCallback): void;
export function access(path: any, mode: any, callback?: any): void {
    if (typeof mode === 'function') {
        callback = mode;
        mode = 0;
    }
    const p = getValidatedPath(path, 'path');
    const m = getValidMode(mode, 'access');
    const cb = makeCallback(callback) as NoParamCallback;
    callAsync(OP_ACCESS, 'access', p, undefined, m, 0, 0, 0, undefined, (err) => { cb(err); });
}

// ---- stat ----

function statSyncOf(op: number, syscall: string, path: string | undefined, fd: number, throwIfNoEntry: boolean): Stats | undefined {
    const b = statBuffer();
    const r = __kml_native.fsCall(op, path ?? '', '', fd, 0, 0, 0, b);
    if (r < 0) {
        const errno = -r;
        if (!throwIfNoEntry) {
            const code = __kml_native.errnoName(errno);
            if (code === 'ENOENT' || code === 'ENOTDIR') return undefined;
        }
        throw uvError(errno, syscall, path);
    }
    return getStatsFromBinding(b);
}

export function statSync(path: PathLike, options?: undefined): Stats;
export function statSync(path: PathLike, options?: StatSyncOptions & { bigint?: false | undefined; throwIfNoEntry: false }): Stats | undefined;
export function statSync(path: PathLike, options?: StatSyncOptions & { bigint?: false | undefined }): Stats;
export function statSync(path: any, options?: any): Stats | undefined {
    const p = getValidatedPath(path, 'path');
    return statSyncOf(OP_STAT, 'stat', p, 0, options?.throwIfNoEntry ?? true);
}

export function lstatSync(path: PathLike, options?: undefined): Stats;
export function lstatSync(path: PathLike, options?: StatSyncOptions & { bigint?: false | undefined; throwIfNoEntry: false }): Stats | undefined;
export function lstatSync(path: PathLike, options?: StatSyncOptions & { bigint?: false | undefined }): Stats;
export function lstatSync(path: any, options?: any): Stats | undefined {
    const p = getValidatedPath(path, 'path');
    return statSyncOf(OP_LSTAT, 'lstat', p, 0, options?.throwIfNoEntry ?? true);
}

export function fstatSync(fd: number, options?: StatOptions & { bigint?: false | undefined }): Stats;
export function fstatSync(fd: any, options?: any): Stats {
    return statSyncOf(OP_FSTAT, 'fstat', undefined, getValidatedFd(fd), true)!;
}

type StatCallback = (err: NodeJS.ErrnoException | null, stats: Stats) => void;

function statAsync(op: number, syscall: string, path: string | undefined, fd: number, cb: StatCallback): void {
    const b = statBuffer();
    callAsync(op, syscall, path, undefined, fd, 0, 0, 0, b, (err) => {
        if (err) cb(err, undefined as any);
        else cb(null, getStatsFromBinding(b));
    });
}

export function stat(path: PathLike, callback: StatCallback): void;
export function stat(path: PathLike, options: (StatOptions & { bigint?: false | undefined }) | undefined, callback: StatCallback): void;
export function stat(path: any, options: any, callback?: any): void {
    if (typeof options === 'function') callback = options;
    const cb = makeCallback(callback) as StatCallback;
    statAsync(OP_STAT, 'stat', getValidatedPath(path, 'path'), 0, cb);
}

export function lstat(path: PathLike, callback: StatCallback): void;
export function lstat(path: PathLike, options: (StatOptions & { bigint?: false | undefined }) | undefined, callback: StatCallback): void;
export function lstat(path: any, options: any, callback?: any): void {
    if (typeof options === 'function') callback = options;
    const cb = makeCallback(callback) as StatCallback;
    statAsync(OP_LSTAT, 'lstat', getValidatedPath(path, 'path'), 0, cb);
}

export function fstat(fd: number, callback: StatCallback): void;
export function fstat(fd: number, options: (StatOptions & { bigint?: false | undefined }) | undefined, callback: StatCallback): void;
export function fstat(fd: any, options: any, callback?: any): void {
    if (typeof options === 'function') callback = options;
    const cb = makeCallback(callback) as StatCallback;
    statAsync(OP_FSTAT, 'fstat', undefined, getValidatedFd(fd), cb);
}

export function statfsSync(path: PathLike, options?: StatFsOptions & { bigint?: false | undefined }): StatFs;
export function statfsSync(path: any, options?: any): StatFs {
    const b = Buffer.alloc(8 * 8);
    callSync(OP_STATFS, 'statfs', getValidatedPath(path, 'path'), undefined, 0, 0, 0, 0, b);
    return getStatFsFromBinding(b);
}

export function statfs(path: PathLike, callback: (err: NodeJS.ErrnoException | null, stats: StatFs) => void): void;
export function statfs(path: PathLike, options: (StatFsOptions & { bigint?: false | undefined }) | undefined, callback: (err: NodeJS.ErrnoException | null, stats: StatFs) => void): void;
export function statfs(path: any, options: any, callback?: any): void {
    if (typeof options === 'function') callback = options;
    const cb = makeCallback(callback) as (err: NodeJS.ErrnoException | null, stats: StatFs) => void;
    const b = Buffer.alloc(8 * 8);
    callAsync(OP_STATFS, 'statfs', getValidatedPath(path, 'path'), undefined, 0, 0, 0, 0, b, (err) => {
        if (err) cb(err, undefined as any);
        else cb(null, getStatFsFromBinding(b));
    });
}

// ---- directories ----

function mkdirArgs(options: any): [number, boolean] {
    let mode = 0o777;
    let recursive = false;
    if (typeof options === 'number' || typeof options === 'string') {
        mode = parseFileMode(options, 'mode', 0o777);
    } else if (options) {
        if (options.recursive !== undefined) {
            recursive = options.recursive;
            validateBoolean(recursive, 'options.recursive');
        }
        if (options.mode !== undefined) mode = parseFileMode(options.mode, 'options.mode', 0o777);
    }
    return [mode, recursive];
}

export function mkdirSync(path: PathLike, options: MakeDirectoryOptions & { recursive: true }): string | undefined;
export function mkdirSync(path: PathLike, options?: Mode | (MakeDirectoryOptions & { recursive?: false | undefined }) | null): void;
export function mkdirSync(path: PathLike, options?: Mode | MakeDirectoryOptions | null): string | undefined;
export function mkdirSync(path: any, options?: any): any {
    const [mode, recursive] = mkdirArgs(options);
    const p = getValidatedPath(path, 'path');
    if (!recursive) {
        callSync(OP_MKDIR, 'mkdir', p, undefined, mode, 0, 0, 0);
        return undefined;
    }
    const created = callSync(OP_MKDIRP, 'mkdir', p, undefined, mode, 0, 0, 0);
    return created === 1 ? lastString() : undefined;
}

export function mkdir(path: PathLike, options: MakeDirectoryOptions & { recursive: true }, callback: (err: NodeJS.ErrnoException | null, path?: string) => void): void;
export function mkdir(path: PathLike, options: Mode | MakeDirectoryOptions | null | undefined, callback: (err: NodeJS.ErrnoException | null, path?: string) => void): void;
export function mkdir(path: PathLike, callback: NoParamCallback): void;
export function mkdir(path: any, options: any, callback?: any): void {
    if (typeof options === 'function') {
        callback = options;
        options = undefined;
    }
    const [mode, recursive] = mkdirArgs(options);
    const p = getValidatedPath(path, 'path');
    const cb = makeCallback(callback);
    callAsync(recursive ? OP_MKDIRP : OP_MKDIR, 'mkdir', p, undefined, mode, 0, 0, 0, undefined, (err, created, str) => {
        if (err) cb(err);
        else if (recursive) cb(null, created === 1 ? str : undefined);
        else cb(null);
    });
}

export function mkdtempSync(prefix: PathLike, options?: EncodingOption): string;
export function mkdtempSync(prefix: any, options?: any): any {
    const opts = getOptions(options, {});
    const p = getValidatedPath(prefix, 'prefix') + 'XXXXXX';
    callSync(OP_MKDTEMP, 'mkdtemp', p, undefined, 0, 0, 0, 0);
    return encodeString(lastString(), opts.encoding);
}

export function mkdtemp(prefix: PathLike, callback: (err: NodeJS.ErrnoException | null, folder: string) => void): void;
export function mkdtemp(prefix: PathLike, options: EncodingOption, callback: (err: NodeJS.ErrnoException | null, folder: string) => void): void;
export function mkdtemp(prefix: any, options: any, callback?: any): void {
    if (typeof options === 'function') {
        callback = options;
        options = undefined;
    }
    const cb = makeCallback(callback);
    const opts = getOptions(options, {});
    const p = getValidatedPath(prefix, 'prefix') + 'XXXXXX';
    callAsync(OP_MKDTEMP, 'mkdtemp', p, undefined, 0, 0, 0, 0, undefined, (err, r, str) => {
        if (err) cb(err);
        else cb(null, encodeString(str, opts.encoding));
    });
}

function readdirOnce(path: string): [string[], number[]] {
    callSync(OP_READDIR, 'scandir', path, undefined, 0, 0, 0, 0);
    return decodeEntries(lastString());
}

// Node's readdirSyncRecursive: breadth first, names relative to the base.
function readdirSyncRecursive(basePath: string, options: any): any[] {
    const withFileTypes = !!options.withFileTypes;
    const results: any[] = [];
    const queue: string[] = [basePath];
    for (let q = 0; q < queue.length; q++) {
        const path = queue[q];
        const [names, types] = readdirOnce(path);
        if (withFileTypes) {
            const dirents = direntsOf(path, names, types);
            for (let i = 0; i < dirents.length; i++) {
                const d = dirents[i];
                results.push(d);
                if (d.isDirectory()) queue.push(pathJoin(d.parentPath, d.name));
            }
        } else {
            for (let i = 0; i < names.length; i++) {
                const resultPath = pathJoin(path, names[i]);
                results.push(pathRelative(basePath, resultPath));
                const st = statSyncOf(OP_STAT, 'stat', resultPath, 0, false);
                if (st !== undefined && st.isDirectory()) queue.push(resultPath);
            }
        }
    }
    if (!withFileTypes && options.encoding && options.encoding !== 'utf8') return encodeNames(results as string[], options.encoding);
    return results;
}

export function readdirSync(path: PathLike, options?: { encoding: BufferEncoding | null; withFileTypes?: false | undefined; recursive?: boolean | undefined } | BufferEncoding | null): string[];
export function readdirSync(path: PathLike, options: { encoding: 'buffer'; withFileTypes?: false | undefined; recursive?: boolean | undefined } | 'buffer'): NonSharedBuffer[];
export function readdirSync(path: PathLike, options?: (ObjectEncodingOptions & { withFileTypes?: false | undefined; recursive?: boolean | undefined }) | BufferEncoding | null): string[] | NonSharedBuffer[];
export function readdirSync(path: PathLike, options: ObjectEncodingOptions & { withFileTypes: true; recursive?: boolean | undefined }): Dirent[];
export function readdirSync(path: any, options?: any): any {
    const opts = getOptions(options, {});
    const p = getValidatedPath(path, 'path');
    if (opts.recursive !== null && opts.recursive !== undefined) validateBoolean(opts.recursive, 'options.recursive');
    if (opts.recursive) return readdirSyncRecursive(p, opts);
    const [names, types] = readdirOnce(p);
    if (opts.withFileTypes) return direntsOf(p, names, types);
    return encodeNames(names, opts.encoding);
}

export function readdir(path: PathLike, options: { encoding: BufferEncoding | null; withFileTypes?: false | undefined; recursive?: boolean | undefined } | BufferEncoding | undefined | null, callback: (err: NodeJS.ErrnoException | null, files: string[]) => void): void;
export function readdir(path: PathLike, options: { encoding: 'buffer'; withFileTypes?: false | undefined; recursive?: boolean | undefined } | 'buffer', callback: (err: NodeJS.ErrnoException | null, files: NonSharedBuffer[]) => void): void;
export function readdir(path: PathLike, callback: (err: NodeJS.ErrnoException | null, files: string[]) => void): void;
export function readdir(path: PathLike, options: ObjectEncodingOptions & { withFileTypes: true; recursive?: boolean | undefined }, callback: (err: NodeJS.ErrnoException | null, files: Dirent[]) => void): void;
export function readdir(path: any, options: any, callback?: any): void {
    if (typeof options === 'function') {
        callback = options;
        options = undefined;
    }
    const cb = makeCallback(callback);
    const opts = getOptions(options, {});
    const p = getValidatedPath(path, 'path');
    if (opts.recursive !== null && opts.recursive !== undefined) validateBoolean(opts.recursive, 'options.recursive');
    if (opts.recursive) {
        // Node's readdirRecursive: one directory at a time, in queue order.
        const withFileTypes = !!opts.withFileTypes;
        const results: any[] = [];
        const queue: string[] = [p];
        const next = (q: number): void => {
            if (q >= queue.length) {
                cb(null, !withFileTypes && opts.encoding && opts.encoding !== 'utf8' ? encodeNames(results as string[], opts.encoding) : results);
                return;
            }
            const dir = queue[q];
            callAsync(OP_READDIR, 'scandir', dir, undefined, 0, 0, 0, 0, undefined, (err, r, str) => {
                if (err) {
                    cb(err);
                    return;
                }
                const [names, types] = decodeEntries(str);
                const dirents = direntsOf(dir, names, types);
                for (let i = 0; i < dirents.length; i++) {
                    const d = dirents[i];
                    const full = pathJoin(dir, d.name);
                    results.push(withFileTypes ? d : pathRelative(p, full));
                    let isDir = d.isDirectory();
                    if (!withFileTypes && d.isSymbolicLink()) {
                        const st = statSyncOf(OP_STAT, 'stat', full, 0, false);
                        isDir = st !== undefined && st.isDirectory();
                    }
                    if (isDir) queue.push(full);
                }
                next(q + 1);
            });
        };
        next(0);
        return;
    }
    callAsync(OP_READDIR, 'scandir', p, undefined, 0, 0, 0, 0, undefined, (err, r, str) => {
        if (err) {
            cb(err);
            return;
        }
        const [names, types] = decodeEntries(str);
        if (opts.withFileTypes) cb(null, direntsOf(p, names, types));
        else cb(null, encodeNames(names, opts.encoding));
    });
}

// ---- rm / rmdir / unlink ----

function eisdir(path: string): SystemError {
    return new SystemError('ERR_FS_EISDIR', 'Path is a directory', 21, 'EISDIR', 'is a directory', 'rm', path);
}

function rmOptions(options: any): any {
    const o: any = { recursive: false, force: false, retryDelay: 100, maxRetries: 0 };
    if (options !== undefined && options !== null) {
        validateObject(options, 'options');
        if (options.recursive !== undefined) o.recursive = options.recursive;
        if (options.force !== undefined) o.force = options.force;
        if (options.retryDelay !== undefined) o.retryDelay = options.retryDelay;
        if (options.maxRetries !== undefined) o.maxRetries = options.maxRetries;
    }
    validateBoolean(o.recursive, 'options.recursive');
    validateInt32(o.retryDelay, 'options.retryDelay', 0);
    validateUint32(o.maxRetries, 'options.maxRetries');
    validateBoolean(o.force, 'options.force');
    return o;
}

// A tree's removal (std::filesystem::remove_all, as Node's rmSync): the
// contents, then the directory.
function removeTreeSync(path: string): void {
    const st = statSyncOf(OP_LSTAT, 'rm', path, 0, false);
    if (st === undefined) return;
    if (st.isDirectory()) {
        const [names] = readdirOnce(path);
        for (let i = 0; i < names.length; i++) removeTreeSync(pathJoin(path, names[i]));
        callSync(OP_RMDIR, 'rm', path, undefined, 0, 0, 0, 0);
    } else {
        callSync(OP_UNLINK, 'rm', path, undefined, 0, 0, 0, 0);
    }
}

export function rmSync(path: PathLike, options?: RmOptions): void {
    const p = getValidatedPath(path, 'path');
    const o = rmOptions(options);
    const st = statSyncOf(OP_LSTAT, 'lstat', p, 0, !o.force);
    if (st === undefined) return;
    if (st.isDirectory() && !o.recursive) throw eisdir(p);
    if (st.isDirectory()) removeTreeSync(p);
    else callSync(OP_UNLINK, 'rm', p, undefined, 0, 0, 0, 0);
}

// Node's rimraf (internal/fs/rimraf.js): the callback form of the removal.
function rimraf(path: string, cb: (err: any) => void): void {
    const b = statBuffer();
    callAsync(OP_LSTAT, 'lstat', path, undefined, 0, 0, 0, 0, b, (err) => {
        if (err) {
            cb(err.code === 'ENOENT' ? null : err);
            return;
        }
        if (!getStatsFromBinding(b).isDirectory()) {
            callAsync(OP_UNLINK, 'unlink', path, undefined, 0, 0, 0, 0, undefined, (uerr) => {
                cb(uerr && uerr.code !== 'ENOENT' ? uerr : null);
            });
            return;
        }
        callAsync(OP_READDIR, 'scandir', path, undefined, 0, 0, 0, 0, undefined, (rerr, r, str) => {
            if (rerr) {
                cb(rerr);
                return;
            }
            const [names] = decodeEntries(str);
            let left = names.length;
            let failed: any = null;
            const removeDir = (): void => {
                callAsync(OP_RMDIR, 'rmdir', path, undefined, 0, 0, 0, 0, undefined, (derr) => {
                    cb(derr && derr.code !== 'ENOENT' ? derr : null);
                });
            };
            if (left === 0) {
                removeDir();
                return;
            }
            for (let i = 0; i < names.length; i++) {
                rimraf(pathJoin(path, names[i]), (cerr) => {
                    if (cerr && failed === null) failed = cerr;
                    left--;
                    if (left === 0) {
                        if (failed !== null) cb(failed);
                        else removeDir();
                    }
                });
            }
        });
    });
}

export function rm(path: PathLike, callback: NoParamCallback): void;
export function rm(path: PathLike, options: RmOptions, callback: NoParamCallback): void;
export function rm(path: any, options: any, callback?: any): void {
    if (typeof options === 'function') {
        callback = options;
        options = undefined;
    }
    const cb = makeCallback(callback) as NoParamCallback;
    const p = getValidatedPath(path, 'path');
    const o = rmOptions(options);
    const b = statBuffer();
    callAsync(OP_LSTAT, 'lstat', p, undefined, 0, 0, 0, 0, b, (err) => {
        if (err) {
            cb(err.code === 'ENOENT' && o.force ? null : err);
            return;
        }
        if (getStatsFromBinding(b).isDirectory() && !o.recursive) {
            cb(eisdir(p));
            return;
        }
        rimraf(p, (rerr) => { cb(rerr); });
    });
}

let rmdirWarned = false;

function rmdirRecursiveWarning(): void {
    if (rmdirWarned) return;
    rmdirWarned = true;
    process.emitWarning('In future versions of Node.js, fs.rmdir(path, { recursive: true }) will be removed. Use fs.rm(path, { recursive: true }) instead', 'DeprecationWarning', 'DEP0147');
}

export function rmdirSync(path: PathLike, options?: RmDirOptions): void {
    const p = getValidatedPath(path, 'path');
    if (options?.recursive) {
        rmdirRecursiveWarning();
        const st = statSyncOf(OP_LSTAT, 'lstat', p, 0, true);
        if (st !== undefined && st.isDirectory()) removeTreeSync(p);
        return;
    }
    callSync(OP_RMDIR, 'rmdir', p, undefined, 0, 0, 0, 0);
}

export function rmdir(path: PathLike, callback: NoParamCallback): void;
export function rmdir(path: PathLike, options: RmDirOptions, callback: NoParamCallback): void;
export function rmdir(path: any, options: any, callback?: any): void {
    if (typeof options === 'function') {
        callback = options;
        options = undefined;
    }
    const cb = makeCallback(callback) as NoParamCallback;
    const p = getValidatedPath(path, 'path');
    if (options?.recursive) {
        rmdirRecursiveWarning();
        rimraf(p, (err) => { cb(err); });
        return;
    }
    callAsync(OP_RMDIR, 'rmdir', p, undefined, 0, 0, 0, 0, undefined, (err) => { cb(err); });
}

export function unlinkSync(path: PathLike): void {
    callSync(OP_UNLINK, 'unlink', getValidatedPath(path, 'path'), undefined, 0, 0, 0, 0);
}

export function unlink(path: PathLike, callback: NoParamCallback): void {
    const p = getValidatedPath(path, 'path');
    const cb = makeCallback(callback) as NoParamCallback;
    callAsync(OP_UNLINK, 'unlink', p, undefined, 0, 0, 0, 0, undefined, (err) => { cb(err); });
}

// ---- rename / link / symlink / copyFile ----

export function renameSync(oldPath: PathLike, newPath: PathLike): void {
    const o = getValidatedPath(oldPath, 'oldPath');
    const n = getValidatedPath(newPath, 'newPath');
    callSync(OP_RENAME, 'rename', o, n, 0, 0, 0, 0);
}

export function rename(oldPath: PathLike, newPath: PathLike, callback: NoParamCallback): void {
    const o = getValidatedPath(oldPath, 'oldPath');
    const n = getValidatedPath(newPath, 'newPath');
    const cb = makeCallback(callback) as NoParamCallback;
    callAsync(OP_RENAME, 'rename', o, n, 0, 0, 0, 0, undefined, (err) => { cb(err); });
}

export function linkSync(existingPath: PathLike, newPath: PathLike): void {
    const e = getValidatedPath(existingPath, 'existingPath');
    const n = getValidatedPath(newPath, 'newPath');
    callSync(OP_LINK, 'link', e, n, 0, 0, 0, 0);
}

export function link(existingPath: PathLike, newPath: PathLike, callback: NoParamCallback): void {
    const e = getValidatedPath(existingPath, 'existingPath');
    const n = getValidatedPath(newPath, 'newPath');
    const cb = makeCallback(callback) as NoParamCallback;
    callAsync(OP_LINK, 'link', e, n, 0, 0, 0, 0, undefined, (err) => { cb(err); });
}

function symlinkType(type: any): number {
    if (type !== undefined && type !== null && type !== 'dir' && type !== 'file' && type !== 'junction') {
        throw new NodeTypeError('ERR_INVALID_ARG_VALUE', "The argument 'type' must be one of: 'dir', 'file', 'junction', null, undefined. Received " + (typeof type === 'string' ? "'" + type + "'" : String(type)));
    }
    return type === 'file' ? 1 : type === 'dir' ? 2 : type === 'junction' ? 3 : 0;
}

export function symlinkSync(target: PathLike, path: PathLike, type?: 'dir' | 'file' | 'junction' | null): void {
    const t = symlinkType(type);
    const tg = getValidatedPath(target, 'target');
    const p = getValidatedPath(path, 'path');
    callSync(OP_SYMLINK, 'symlink', tg, p, t, 0, 0, 0);
}

export function symlink(target: PathLike, path: PathLike, callback: NoParamCallback): void;
export function symlink(target: PathLike, path: PathLike, type: 'dir' | 'file' | 'junction' | undefined | null, callback: NoParamCallback): void;
export function symlink(target: any, path: any, type: any, callback?: any): void {
    if (typeof type === 'function') {
        callback = type;
        type = undefined;
    }
    const cb = makeCallback(callback) as NoParamCallback;
    const t = symlinkType(type);
    const tg = getValidatedPath(target, 'target');
    const p = getValidatedPath(path, 'path');
    callAsync(OP_SYMLINK, 'symlink', tg, p, t, 0, 0, 0, undefined, (err) => { cb(err); });
}

export function copyFileSync(src: PathLike, dest: PathLike, mode?: number): void {
    const s = getValidatedPath(src, 'src');
    const d = getValidatedPath(dest, 'dest');
    callSync(OP_COPYFILE, 'copyfile', s, d, getValidMode(mode, 'copyFile'), 0, 0, 0);
}

export function copyFile(src: PathLike, dest: PathLike, callback: NoParamCallback): void;
export function copyFile(src: PathLike, dest: PathLike, mode: number, callback: NoParamCallback): void;
export function copyFile(src: any, dest: any, mode: any, callback?: any): void {
    if (typeof mode === 'function') {
        callback = mode;
        mode = 0;
    }
    const s = getValidatedPath(src, 'src');
    const d = getValidatedPath(dest, 'dest');
    const m = getValidMode(mode, 'copyFile');
    const cb = makeCallback(callback) as NoParamCallback;
    callAsync(OP_COPYFILE, 'copyfile', s, d, m, 0, 0, 0, undefined, (err) => { cb(err); });
}

// ---- readlink / realpath ----

export function readlinkSync(path: PathLike, options?: EncodingOption): string;
export function readlinkSync(path: any, options?: any): any {
    const opts = getOptions(options, {});
    callSync(OP_READLINK, 'readlink', getValidatedPath(path, 'path'), undefined, 0, 0, 0, 0);
    return encodeString(lastString(), opts.encoding);
}

export function readlink(path: PathLike, callback: (err: NodeJS.ErrnoException | null, linkString: string) => void): void;
export function readlink(path: PathLike, options: EncodingOption, callback: (err: NodeJS.ErrnoException | null, linkString: string) => void): void;
export function readlink(path: any, options: any, callback?: any): void {
    if (typeof options === 'function') {
        callback = options;
        options = undefined;
    }
    const cb = makeCallback(callback);
    const opts = getOptions(options, {});
    callAsync(OP_READLINK, 'readlink', getValidatedPath(path, 'path'), undefined, 0, 0, 0, 0, undefined, (err, r, str) => {
        if (err) cb(err);
        else cb(null, encodeString(str, opts.encoding));
    });
}

// Node's realpathSync (lib/fs.js): the path resolved one component at a
// time, each symbolic link read and followed — errors name the component.
function realpathJS(p: string): string {
    p = pathResolve(p);
    const seenLinks = new Map<string, string>();
    const knownHard = new Set<string>();
    const isWin = process.platform === 'win32';
    const isSep = (c: string): boolean => c === '/' || (isWin && c === '\\');
    let current = '';
    let base = '';
    let previous = '';
    let pos = 0;
    // The root: '/' (or the drive on Windows).
    const rootEnd = (s: string): number => {
        if (isWin && s.length >= 2 && s[1] === ':') return s.length > 2 && isSep(s[2]) ? 3 : 2;
        return isSep(s[0]) ? 1 : 0;
    };
    const start = (): void => {
        const r = rootEnd(p);
        current = p.slice(0, r);
        base = current;
        previous = '';
        pos = r;
        if (isWin && !knownHard.has(base)) {
            statSyncOf(OP_LSTAT, 'lstat', base, 0, true);
            knownHard.add(base);
        }
    };
    start();
    while (pos < p.length) {
        let result = -1;
        for (let i = pos; i < p.length; i++) {
            if (isSep(p[i])) {
                result = i;
                break;
            }
        }
        previous = current;
        if (result === -1) {
            const last = p.slice(pos);
            current += last;
            base = previous + last;
            pos = p.length;
        } else {
            current += p.slice(pos, result + 1);
            base = previous + p.slice(pos, result);
            pos = result + 1;
        }
        if (knownHard.has(base) || base === '') continue;
        const st = statSyncOf(OP_LSTAT, 'lstat', base, 0, true)!;
        if (!st.isSymbolicLink()) {
            knownHard.add(base);
            continue;
        }
        let linkTarget: string;
        const id = st.dev.toString(32) + ':' + st.ino.toString(32);
        const seen = seenLinks.get(id);
        if (seen !== undefined) {
            linkTarget = seen;
        } else {
            statSyncOf(OP_STAT, 'stat', base, 0, true);
            callSync(OP_READLINK, 'readlink', base, undefined, 0, 0, 0, 0);
            linkTarget = lastString();
            seenLinks.set(id, linkTarget);
        }
        const resolvedLink = pathResolve(previous, linkTarget);
        p = pathResolve(resolvedLink, p.slice(pos));
        start();
    }
    return p;
}

export function realpathSync(path: PathLike, options?: EncodingOption): string;
export function realpathSync(path: any, options?: any): any {
    const opts = getOptions(options, {});
    const p = getValidatedPath(path, 'path');
    return encodeString(realpathJS(p), opts.encoding);
}

realpathSync.native = (path: PathLike, options?: EncodingOption): string => {
    const opts = getOptions(options, {});
    callSync(OP_REALPATH, 'realpath', getValidatedPath(path, 'path'), undefined, 0, 0, 0, 0);
    return encodeString(lastString(), opts.encoding);
};

export function realpath(path: PathLike, callback: (err: NodeJS.ErrnoException | null, resolvedPath: string) => void): void;
export function realpath(path: PathLike, options: EncodingOption, callback: (err: NodeJS.ErrnoException | null, resolvedPath: string) => void): void;
export function realpath(path: any, options: any, callback?: any): void {
    if (typeof options === 'function') {
        callback = options;
        options = undefined;
    }
    const cb = makeCallback(callback);
    let result: any;
    try {
        result = realpathSync(path, options);
    } catch (err) {
        process.nextTick(() => { cb(err); });
        return;
    }
    process.nextTick(() => { cb(null, result); });
}

realpath.native = (path: PathLike, options: any, callback?: any): void => {
    if (typeof options === 'function') {
        callback = options;
        options = undefined;
    }
    const cb = makeCallback(callback);
    const opts = getOptions(options, {});
    callAsync(OP_REALPATH, 'realpath', getValidatedPath(path, 'path'), undefined, 0, 0, 0, 0, undefined, (err, r, str) => {
        if (err) cb(err);
        else cb(null, encodeString(str, opts.encoding));
    });
};

// ---- chmod / chown / truncate / utimes ----

export function chmodSync(path: PathLike, mode: Mode): void {
    const p = getValidatedPath(path, 'path');
    callSync(OP_CHMOD, 'chmod', p, undefined, parseFileMode(mode, 'mode', 0), 0, 0, 0);
}

export function chmod(path: PathLike, mode: Mode, callback: NoParamCallback): void {
    const p = getValidatedPath(path, 'path');
    const m = parseFileMode(mode, 'mode', 0);
    const cb = makeCallback(callback) as NoParamCallback;
    callAsync(OP_CHMOD, 'chmod', p, undefined, m, 0, 0, 0, undefined, (err) => { cb(err); });
}

export function fchmodSync(fd: number, mode: Mode): void {
    const n = getValidatedFd(fd);
    callSync(OP_FCHMOD, 'fchmod', undefined, undefined, n, parseFileMode(mode, 'mode', 0), 0, 0);
}

export function fchmod(fd: number, mode: Mode, callback: NoParamCallback): void {
    const n = getValidatedFd(fd);
    const m = parseFileMode(mode, 'mode', 0);
    const cb = makeCallback(callback) as NoParamCallback;
    callAsync(OP_FCHMOD, 'fchmod', undefined, undefined, n, m, 0, 0, undefined, (err) => { cb(err); });
}

export function chownSync(path: PathLike, uid: number, gid: number): void {
    const p = getValidatedPath(path, 'path');
    validateUid(uid, 'uid');
    validateUid(gid, 'gid');
    callSync(OP_CHOWN, 'chown', p, undefined, uid, gid, 0, 0);
}

export function chown(path: PathLike, uid: number, gid: number, callback: NoParamCallback): void {
    const p = getValidatedPath(path, 'path');
    validateUid(uid, 'uid');
    validateUid(gid, 'gid');
    const cb = makeCallback(callback) as NoParamCallback;
    callAsync(OP_CHOWN, 'chown', p, undefined, uid, gid, 0, 0, undefined, (err) => { cb(err); });
}

export function lchownSync(path: PathLike, uid: number, gid: number): void {
    const p = getValidatedPath(path, 'path');
    validateUid(uid, 'uid');
    validateUid(gid, 'gid');
    callSync(OP_LCHOWN, 'lchown', p, undefined, uid, gid, 0, 0);
}

export function lchown(path: PathLike, uid: number, gid: number, callback: NoParamCallback): void {
    const p = getValidatedPath(path, 'path');
    validateUid(uid, 'uid');
    validateUid(gid, 'gid');
    const cb = makeCallback(callback) as NoParamCallback;
    callAsync(OP_LCHOWN, 'lchown', p, undefined, uid, gid, 0, 0, undefined, (err) => { cb(err); });
}

export function fchownSync(fd: number, uid: number, gid: number): void {
    const n = getValidatedFd(fd);
    validateUid(uid, 'uid');
    validateUid(gid, 'gid');
    callSync(OP_FCHOWN, 'fchown', undefined, undefined, n, uid, gid, 0);
}

export function fchown(fd: number, uid: number, gid: number, callback: NoParamCallback): void {
    const n = getValidatedFd(fd);
    validateUid(uid, 'uid');
    validateUid(gid, 'gid');
    const cb = makeCallback(callback) as NoParamCallback;
    callAsync(OP_FCHOWN, 'fchown', undefined, undefined, n, uid, gid, 0, undefined, (err) => { cb(err); });
}

function truncLen(len: any): number {
    if (len === undefined || len === null) return 0;
    validateInteger(len, 'len', -Number.MAX_SAFE_INTEGER);
    return Math.max(0, len);
}

export function ftruncateSync(fd: number, len?: number): void {
    const n = getValidatedFd(fd);
    callSync(OP_FTRUNCATE, 'ftruncate', undefined, undefined, n, truncLen(len), 0, 0);
}

export function ftruncate(fd: number, callback: NoParamCallback): void;
export function ftruncate(fd: number, len: number | undefined | null, callback: NoParamCallback): void;
export function ftruncate(fd: any, len: any, callback?: any): void {
    if (typeof len === 'function') {
        callback = len;
        len = 0;
    }
    const n = getValidatedFd(fd);
    const l = truncLen(len);
    const cb = makeCallback(callback) as NoParamCallback;
    callAsync(OP_FTRUNCATE, 'ftruncate', undefined, undefined, n, l, 0, 0, undefined, (err) => { cb(err); });
}

export function truncateSync(path: PathLike, len?: number): void {
    if (isFd(path)) {
        ftruncateSync(path as any as number, len);
        return;
    }
    const fd = openSync(path, 'r+');
    try {
        ftruncateSync(fd, len);
    } finally {
        closeSync(fd);
    }
}

export function truncate(path: PathLike, callback: NoParamCallback): void;
export function truncate(path: PathLike, len: number | undefined | null, callback: NoParamCallback): void;
export function truncate(path: any, len: any, callback?: any): void {
    if (typeof len === 'function') {
        callback = len;
        len = 0;
    }
    const cb = makeCallback(callback) as NoParamCallback;
    const l = truncLen(len);
    const p = getValidatedPath(path, 'path');
    callAsync(OP_OPEN, 'open', p, undefined, stringToFlags('r+'), 0o666, 0, 0, undefined, (err, fd) => {
        if (err) {
            cb(err);
            return;
        }
        callAsync(OP_FTRUNCATE, 'ftruncate', undefined, undefined, fd, l, 0, 0, undefined, (terr) => {
            callAsync(OP_CLOSE, 'close', undefined, undefined, fd, 0, 0, 0, undefined, (cerr) => {
                cb(terr ?? cerr);
            });
        });
    });
}

export function utimesSync(path: PathLike, atime: TimeLike, mtime: TimeLike): void {
    const p = getValidatedPath(path, 'path');
    callSync(OP_UTIMES, 'utime', p, undefined, toUnixTimestamp(atime, 'atime'), toUnixTimestamp(mtime, 'mtime'), 0, 0);
}

export function utimes(path: PathLike, atime: TimeLike, mtime: TimeLike, callback: NoParamCallback): void {
    const p = getValidatedPath(path, 'path');
    const a = toUnixTimestamp(atime, 'atime');
    const m = toUnixTimestamp(mtime, 'mtime');
    const cb = makeCallback(callback) as NoParamCallback;
    callAsync(OP_UTIMES, 'utime', p, undefined, a, m, 0, 0, undefined, (err) => { cb(err); });
}

export function lutimesSync(path: PathLike, atime: TimeLike, mtime: TimeLike): void {
    const p = getValidatedPath(path, 'path');
    callSync(OP_LUTIMES, 'lutime', p, undefined, toUnixTimestamp(atime, 'atime'), toUnixTimestamp(mtime, 'mtime'), 0, 0);
}

export function lutimes(path: PathLike, atime: TimeLike, mtime: TimeLike, callback: NoParamCallback): void {
    const p = getValidatedPath(path, 'path');
    const a = toUnixTimestamp(atime, 'atime');
    const m = toUnixTimestamp(mtime, 'mtime');
    const cb = makeCallback(callback) as NoParamCallback;
    callAsync(OP_LUTIMES, 'lutime', p, undefined, a, m, 0, 0, undefined, (err) => { cb(err); });
}

export function futimesSync(fd: number, atime: TimeLike, mtime: TimeLike): void {
    const n = getValidatedFd(fd);
    callSync(OP_FUTIMES, 'futime', undefined, undefined, n, toUnixTimestamp(atime, 'atime'), toUnixTimestamp(mtime, 'mtime'), 0);
}

export function futimes(fd: number, atime: TimeLike, mtime: TimeLike, callback: NoParamCallback): void {
    const n = getValidatedFd(fd);
    const a = toUnixTimestamp(atime, 'atime');
    const m = toUnixTimestamp(mtime, 'mtime');
    const cb = makeCallback(callback) as NoParamCallback;
    callAsync(OP_FUTIMES, 'futime', undefined, undefined, n, a, m, 0, undefined, (err) => { cb(err); });
}

// ---- lib/internal/fs/watchers.js: FSWatcher ----

// The watchers alive: a watcher's listener stays reachable while the
// runtime can call it.
const liveWatchers = new Map<number, any>();

export class FSWatcher extends EventEmitter {
    private _kmlHandle: number = -1;
    private _kmlPath: string = '';
    private _kmlEncoding: any = undefined;

    _kmlStart(path: string, encoding: any): void {
        this._kmlPath = path;
        this._kmlEncoding = encoding;
        const onEvent = (isRename: number, unused: number): void => {
            let filename = lastString();
            // A watched file reports its own name, as libuv does.
            if (filename === this._kmlPath) {
                const i = Math.max(filename.lastIndexOf('/'), process.platform === 'win32' ? filename.lastIndexOf('\\') : -1);
                filename = filename.slice(i + 1);
            }
            this.emit('change', isRename !== 0 ? 'rename' : 'change', encodeString(filename, this._kmlEncoding));
        };
        this._kmlHandle = __kml_native.fsWatch(path, onEvent);
        liveWatchers.set(this._kmlHandle, onEvent);
    }

    close(): void {
        if (this._kmlHandle < 0) return;
        __kml_native.fsWatchClose(this._kmlHandle);
        liveWatchers.delete(this._kmlHandle);
        this._kmlHandle = -1;
        process.nextTick(() => { this.emit('close'); });
    }

    ref(): this { return this; }
    unref(): this { return this; }
}

export interface WatchOptions {
    encoding?: BufferEncoding | 'buffer' | undefined;
    persistent?: boolean | undefined;
    recursive?: boolean | undefined;
    signal?: AbortSignal | undefined;
}

export type WatchListener<T> = (event: 'rename' | 'change', filename: T | null) => void;

export function watch(filename: PathLike, options?: WatchOptions | BufferEncoding | null, listener?: WatchListener<string>): FSWatcher;
export function watch(filename: PathLike, listener?: WatchListener<string>): FSWatcher;
export function watch(filename: any, options?: any, listener?: any): FSWatcher {
    if (typeof options === 'function') {
        listener = options;
        options = undefined;
    }
    const opts = getOptions(options, {});
    if (opts.persistent !== undefined) validateBoolean(opts.persistent, 'options.persistent');
    if (opts.recursive !== undefined) validateBoolean(opts.recursive, 'options.recursive');
    const p = getValidatedPath(filename, 'filename');
    const watcher = new FSWatcher();
    watcher._kmlStart(p, opts.encoding);
    if (listener) watcher.addListener('change', listener);
    const signal: any = opts.signal;
    if (signal) {
        if (signal.aborted) process.nextTick(() => { watcher.close(); });
        else signal.addEventListener('abort', () => { watcher.close(); }, { once: true });
    }
    return watcher;
}

// ---- lib/internal/fs/promises.js: FileHandle ----

// FileHandle is fs/promises' handle on an open descriptor: its operations
// resolve promises, and it closes once every stream using it lets it go
// (kRef/kUnref).
class FileHandle extends EventEmitter {
    private kfd: number;
    private refs = 1;
    private closePromise: Promise<void> | null = null;
    private closeResolve: (() => void) | null = null;
    private closeReject: ((err: any) => void) | null = null;

    constructor(fd: number) {
        super();
        this.kfd = fd;
    }

    get fd(): number { return this.kfd; }

    private checkOpen(syscall: string): Error | null {
        if (this.kfd === -1) {
            const e = new NodeError('EBADF', 'file closed') as any;
            e.errno = -9;
            e.syscall = syscall;
            return e as Error;
        }
        return null;
    }

    // Each operation holds a reference, so close() waits for it.
    private call<T>(syscall: string, run: (fd: number, done: (err: any, value: T) => void) => void): Promise<T> {
        return new Promise<T>((resolve, reject) => {
            const closed = this.checkOpen(syscall);
            if (closed !== null) {
                reject(closed);
                return;
            }
            if (this.closePromise !== null) {
                reject(new NodeError('EBADF', 'The FileHandle is closing'));
                return;
            }
            this._kmlRef();
            run(this.kfd, (err: any, value: T) => {
                this._kmlUnref();
                if (err) reject(err);
                else resolve(value);
            });
        });
    }

    read(buffer?: Buffer, offset?: number, length?: number, position?: number | null): Promise<{ bytesRead: number, buffer: Buffer }> {
        const buf = buffer ?? Buffer.alloc(16384);
        const off = offset ?? 0;
        const len = length ?? buf.length - off;
        return this.call<{ bytesRead: number, buffer: Buffer }>('read', (fd, done) => {
            read(fd, buf, off, len, position ?? null, (err: NodeJS.ErrnoException | null, bytesRead: number, b: Buffer) => {
                done(err, { bytesRead, buffer: b });
            });
        });
    }

    write(data: any, offset?: any, length?: any, position?: any): Promise<{ bytesWritten: number, buffer: any }> {
        return this.call<{ bytesWritten: number, buffer: any }>('write', (fd, done) => {
            const cb = (err: NodeJS.ErrnoException | null, bytesWritten: number, buffer: any): void => {
                done(err, { bytesWritten, buffer });
            };
            if (typeof data === 'string') {
                write(fd, data as string, offset === undefined ? null : offset, length === undefined ? null : length, cb);
            } else {
                const b = data as Buffer;
                write(fd, b, offset ?? 0, length ?? b.length - (offset ?? 0), position ?? null, cb);
            }
        });
    }

    writev(buffers: Buffer[], position?: number | null): Promise<{ bytesWritten: number, buffers: Buffer[] }> {
        return this.call<{ bytesWritten: number, buffers: Buffer[] }>('writev', (fd, done) => {
            writev(fd, buffers, position ?? null, (err: NodeJS.ErrnoException | null, bytesWritten: number, bs: Buffer[]) => {
                done(err, { bytesWritten, buffers: bs });
            });
        });
    }

    sync(): Promise<void> {
        return this.call<void>('fsync', (fd, done) => { fsync(fd, (err: NodeJS.ErrnoException | null) => { done(err, undefined); }); });
    }

    datasync(): Promise<void> {
        return this.call<void>('fdatasync', (fd, done) => { fsync(fd, (err: NodeJS.ErrnoException | null) => { done(err, undefined); }); });
    }

    stat(): Promise<any> {
        return this.call<any>('fstat', (fd, done) => { fstat(fd, (err: NodeJS.ErrnoException | null, st: any) => { done(err, st); }); });
    }

    truncate(len: number = 0): Promise<void> {
        return this.call<void>('ftruncate', (fd, done) => { ftruncate(fd, len, (err: NodeJS.ErrnoException | null) => { done(err, undefined); }); });
    }

    chmod(mode: number): Promise<void> {
        return this.call<void>('fchmod', (fd, done) => { fchmod(fd, mode, (err: NodeJS.ErrnoException | null) => { done(err, undefined); }); });
    }

    utimes(atime: any, mtime: any): Promise<void> {
        return this.call<void>('futimes', (fd, done) => { futimes(fd, atime, mtime, (err: NodeJS.ErrnoException | null) => { done(err, undefined); }); });
    }

    async readFile(options?: any): Promise<any> {
        const encoding: any = typeof options === 'string' ? options : options?.encoding;
        const chunks: Buffer[] = [];
        while (true) {
            const r = await this.read(Buffer.alloc(64 * 1024), 0, 64 * 1024, null);
            if (r.bytesRead === 0) break;
            chunks.push(r.buffer.subarray(0, r.bytesRead));
        }
        const all = Buffer.concat(chunks);
        return encoding ? all.toString(encoding) : all;
    }

    async writeFile(data: any, options?: any): Promise<void> {
        const encoding: any = typeof options === 'string' ? options : (options?.encoding ?? 'utf8');
        let buf: Buffer = typeof data === 'string' ? Buffer.from(data as string, encoding) : data as Buffer;
        while (buf.length > 0) {
            const r = await this.write(buf, 0, buf.length, null);
            buf = buf.subarray(r.bytesWritten);
        }
    }

    appendFile(data: any, options?: any): Promise<void> {
        return this.writeFile(data, options);
    }

    close(): Promise<void> {
        if (this.kfd === -1) return Promise.resolve();
        if (this.closePromise !== null) return this.closePromise;
        this.refs--;
        if (this.refs === 0) {
            const fd = this.kfd;
            this.kfd = -1;
            this.closePromise = new Promise<void>((resolve, reject) => {
                close(fd, (err: NodeJS.ErrnoException | null) => {
                    this.closePromise = null;
                    if (err) reject(err);
                    else resolve();
                });
            });
        } else {
            this.closePromise = new Promise<void>((resolve, reject) => {
                this.closeResolve = () => {
                    this.closePromise = null;
                    resolve();
                };
                this.closeReject = (err: any) => {
                    this.closePromise = null;
                    reject(err);
                };
            });
        }
        this.emit('close');
        return this.closePromise!;
    }

    createReadStream(options?: ReadStreamOptions): ReadStream {
        const o: any = options ?? {};
        return new ReadStream(null, { ...o, fd: this });
    }

    createWriteStream(options?: WriteStreamOptions): WriteStream {
        const o: any = options ?? {};
        return new WriteStream(null, { ...o, fd: this });
    }

    _kmlRef(): void { this.refs++; }

    _kmlUnref(): void {
        this.refs--;
        if (this.refs === 0) {
            const fd = this.kfd;
            this.kfd = -1;
            close(fd, (err: NodeJS.ErrnoException | null) => {
                const resolve = this.closeResolve;
                const reject = this.closeReject;
                this.closeResolve = null;
                this.closeReject = null;
                if (err) {
                    if (reject !== null) reject(err);
                } else if (resolve !== null) {
                    resolve();
                }
            });
        }
    }
}

export { FileHandle as _kmlFileHandle };

// _kmlOpenFileHandle is fs/promises' open(): a FileHandle on the file.
export function _kmlOpenFileHandle(path: any, flags?: any, mode?: any): Promise<FileHandle> {
    return new Promise<FileHandle>((resolve, reject) => {
        open(path, flags ?? 'r', mode ?? 0o666, (err: NodeJS.ErrnoException | null, fd: number) => {
            if (err) reject(err);
            else resolve(new FileHandle(fd));
        });
    });
}

// ---- lib/internal/fs/streams.js ----

export interface ReadStreamOptions {
    flags?: OpenMode;
    encoding?: BufferEncoding;
    fd?: any;
    fs?: any;
    signal?: AbortSignal;
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
    fd?: any;
    fs?: any;
    signal?: AbortSignal;
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

// The file operations a stream runs (Node's kFs): fs's own, the
// `options.fs` override (called as given), or a FileHandle's.
interface StreamFsOps {
    open: (path: string, flags: OpenMode, mode: number, cb: (err: NodeJS.ErrnoException | null, fd: number) => void) => void;
    close: (fd: number, cb: (err: NodeJS.ErrnoException | null) => void) => void;
    fsync: (fd: number, cb: (err: NodeJS.ErrnoException | null) => void) => void;
    read: (fd: number, buf: Buffer, offset: number, length: number, pos: number | null, cb: (err: NodeJS.ErrnoException | null, bytesRead: number, buf: Buffer) => void) => void;
    write: (fd: number, buf: Buffer, offset: number, length: number, pos: number | null, cb: (err: NodeJS.ErrnoException | null, bytesWritten: number, buf: any) => void) => void;
}

const defaultStreamFs: StreamFsOps = {
    open: (path, flags, mode, cb) => { open(path, flags, mode, cb); },
    close: (fd, cb) => { close(fd, cb); },
    fsync: (fd, cb) => { fsync(fd, cb); },
    read: (fd, buf, offset, length, pos, cb) => { read(fd, buf, offset, length, pos, cb); },
    write: (fd, buf, offset, length, pos, cb) => { write(fd, buf, offset, length, pos, cb); },
};

// customStreamFs calls an `options.fs` object's own methods, falling back to
// fs's for the ones it does not define (Node's `options.fs || fs` per op
// reads the whole object, so an override supplies every op it is asked for).
function customStreamFs(ofs: any): StreamFsOps {
    return {
        open: (path, flags, mode, cb) => { ofs.open(path, flags, mode, cb); },
        close: (fd, cb) => { ofs.close(fd, cb); },
        fsync: (fd, cb) => { ofs.fsync(fd, cb); },
        read: (fd, buf, offset, length, pos, cb) => { ofs.read(fd, buf, offset, length, pos, cb); },
        write: (fd, buf, offset, length, pos, cb) => { ofs.write(fd, buf, offset, length, pos, cb); },
    };
}

// fileHandleStreamFs runs a stream's operations through its FileHandle
// (FileHandleOperations): closing lets go of the handle's reference.
function fileHandleStreamFs(handle: FileHandle): StreamFsOps {
    return {
        open: (path, flags, mode, cb) => { throw new NodeError('ERR_METHOD_NOT_IMPLEMENTED', 'The open() method is not implemented'); },
        close: (fd, cb) => {
            handle._kmlUnref();
            handle.close().then(() => { cb(null); }, (err: any) => { cb(err); });
        },
        fsync: (fd, cb) => { handle.sync().then(() => { cb(null); }, (err: any) => { cb(err); }); },
        read: (fd, buf, offset, length, pos, cb) => {
            handle.read(buf, offset, length, pos).then((r: any) => { cb(null, r.bytesRead, r.buffer); }, (err: any) => { cb(err, 0, buf); });
        },
        write: (fd, buf, offset, length, pos, cb) => {
            handle.write(buf, offset, length, pos).then((r: any) => { cb(null, r.bytesWritten, r.buffer); }, (err: any) => { cb(err, 0, buf); });
        },
    };
}

// importFd takes options.fd: a descriptor, or a FileHandle whose 'close'
// closes the stream.
function importFd(options: any, onHandleClose: () => void): { fd: number, fs: StreamFsOps } {
    const fd: any = options.fd;
    if (typeof fd === 'number') {
        return { fd: getValidatedFd(fd), fs: options.fs ? customStreamFs(options.fs) : defaultStreamFs };
    }
    if (fd instanceof FileHandle) {
        if (options.fs) throw new NodeError('ERR_METHOD_NOT_IMPLEMENTED', 'The FileHandle with fs method is not implemented');
        const handle: FileHandle = fd;
        handle._kmlRef();
        handle.on('close', onHandleClose);
        return { fd: getValidatedFd(handle.fd), fs: fileHandleStreamFs(handle) };
    }
    throw invalidArgType('options.fd', 'of type number or an instance of FileHandle', fd);
}

function validateFsOp(ofs: any, name: string): void {
    if (typeof ofs[name] !== 'function') throw invalidArgType('options.fs.' + name, 'of type function', ofs[name]);
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
function closeFile(ops: StreamFsOps, fd: number | null, flush: boolean, err: Error | null, clear: () => void, cb: (err?: Error | null) => void): void {
    if (fd === null) {
        cb(err);
        return;
    }
    const n = fd;
    const doClose = (e: Error | null) => {
        ops.close(n, (er: NodeJS.ErrnoException | null) => {
            cb(er ?? e);
        });
        clear();
    };
    if (flush) {
        ops.fsync(n, (flushErr: NodeJS.ErrnoException | null) => { doClose(err ?? flushErr); });
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
    private kfs: StreamFsOps = defaultStreamFs;

    constructor(path: PathLike | null, options?: BufferEncoding | ReadStreamOptions);
    constructor(path: any, options?: any) {
        const opts = streamOptions(options);
        const ro: ReadableOptions = {
            highWaterMark: opts.highWaterMark === undefined ? 64 * 1024 : opts.highWaterMark,
            encoding: opts.encoding,
            emitClose: opts.emitClose,
            autoDestroy: opts.autoClose === undefined ? true : opts.autoClose,
            signal: opts.signal,
        };
        super(ro);
        if (opts.fd === null || opts.fd === undefined) {
            if (opts.fs) {
                validateFsOp(opts.fs, 'open');
                this.kfs = customStreamFs(opts.fs);
            }
            this.path = getValidatedPath(path, 'path');
            this.flags = opts.flags === undefined ? 'r' : opts.flags;
            this.mode = opts.mode === undefined ? 0o666 : opts.mode;
        } else {
            const imported = importFd(opts, () => { this.close(); });
            this.fd = imported.fd;
            this.kfs = imported.fs;
        }
        if (opts.fs) {
            validateFsOp(opts.fs, 'read');
            if (opts.autoClose !== false) validateFsOp(opts.fs, 'close');
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
        this.kfs.open(this.path, this.flags, this.mode, (er: NodeJS.ErrnoException | null, fd: number) => {
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
        this.kfs.read(this.fd!, buf, 0, n, this.pos ?? null, (er: NodeJS.ErrnoException | null, bytesRead: number, b: Buffer) => {
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
            this.onIoDone = (er: Error | null) => { closeFile(this.kfs, this.fd, this.flushOnClose(), err ?? er, clear, cb); };
        } else {
            closeFile(this.kfs, this.fd, this.flushOnClose(), err, clear, cb);
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
    private kfs: StreamFsOps = defaultStreamFs;

    constructor(path: PathLike | null, options?: BufferEncoding | WriteStreamOptions);
    constructor(path: any, options?: any) {
        const opts = streamOptions(options);
        const wo: WritableOptions = {
            highWaterMark: opts.highWaterMark,
            decodeStrings: true,
            emitClose: opts.emitClose,
            autoDestroy: opts.autoClose === undefined ? true : opts.autoClose,
            signal: opts.signal,
        };
        super(wo);
        if (opts.fd === null || opts.fd === undefined) {
            if (opts.fs) {
                validateFsOp(opts.fs, 'open');
                this.kfs = customStreamFs(opts.fs);
            }
            this.path = getValidatedPath(path, 'path');
            this.flags = opts.flags === undefined ? 'w' : opts.flags;
            this.mode = opts.mode === undefined ? 0o666 : opts.mode;
        } else {
            const imported = importFd(opts, () => { this.close(); });
            this.fd = imported.fd;
            this.kfs = imported.fs;
        }
        if (opts.fs) {
            if (typeof opts.fs.write !== 'function' && typeof opts.fs.writev !== 'function') throw invalidArgType('options.fs.write', 'of type function', opts.fs.write);
            if (opts.autoClose !== false) validateFsOp(opts.fs, 'close');
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
        this.kfs.open(this.path, this.flags, this.mode, (er: NodeJS.ErrnoException | null, fd: number) => {
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
        this.kfs.write(this.fd!, data, 0, size, pos ?? null, (er: NodeJS.ErrnoException | null, bytesWritten: number, buffer: any) => {
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
            this.onIoDone = (er: Error | null) => { closeFile(this.kfs, this.fd, this.flushOnClose(), err ?? er, clear, cb); };
        } else {
            closeFile(this.kfs, this.fd, this.flushOnClose(), err, clear, cb);
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

