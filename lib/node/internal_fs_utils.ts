// Node's lib/internal/fs/utils.js: the argument validation, option
// normalisation, errors, Stats/StatFs/Dirent and constants the fs modules
// share, and the binding their operations call — one libuv fs request per
// call (klainfs.c), synchronous or on the thread pool.

export type PathLike = string | Buffer | URL;
export type PathOrFileDescriptor = PathLike | number;
export type TimeLike = string | number | Date;
export type Mode = number | string;
export type OpenMode = number | string;
export type NoParamCallback = (err: NodeJS.ErrnoException | null) => void;
export interface ObjectEncodingOptions {
    encoding?: BufferEncoding | null | undefined;
}
export type EncodingOption = ObjectEncodingOptions | BufferEncoding | undefined | null;
export interface Abortable {
    signal?: AbortSignal | undefined;
}
export type WriteFileOptions =
    | (ObjectEncodingOptions & Abortable & {
        mode?: Mode | undefined;
        flag?: string | undefined;
        flush?: boolean | undefined;
    })
    | BufferEncoding
    | null;
export interface RmOptions {
    force?: boolean | undefined;
    maxRetries?: number | undefined;
    recursive?: boolean | undefined;
    retryDelay?: number | undefined;
}
export interface RmDirOptions {
    maxRetries?: number | undefined;
    recursive?: boolean | undefined;
    retryDelay?: number | undefined;
}
export interface MakeDirectoryOptions {
    recursive?: boolean | undefined;
    mode?: Mode | undefined;
}
export interface StatOptions {
    bigint?: boolean | undefined;
}
export interface StatSyncOptions extends StatOptions {
    throwIfNoEntry?: boolean | undefined;
}
export interface StatFsOptions {
    bigint?: boolean | undefined;
}

// A Node error with its `code` (ERR_*).
export class NodeError extends Error {
    code: string;
    constructor(code: string, message: string) {
        super(message);
        this.code = code;
    }
}

export class NodeTypeError extends TypeError {
    code: string;
    constructor(code: string, message: string) {
        super(message);
        this.code = code;
    }
}

export class NodeRangeError extends RangeError {
    code: string;
    constructor(code: string, message: string) {
        super(message);
        this.code = code;
    }
}

// Node's AbortError (internal/errors.js).
export class AbortError extends Error {
    code: string;
    constructor(message: string = 'The operation was aborted', options?: { cause?: any }) {
        super(message, options);
        this.code = 'ABORT_ERR';
        this.name = 'AbortError';
    }
}

// The AbortError of an aborted options.signal, or null.
export function checkAborted(signal: any): AbortError | null {
    if (signal && signal.aborted) return new AbortError(undefined, { cause: signal.reason });
    return null;
}

// Node's SystemError (internal/errors.js) for ERR_FS_EISDIR and friends:
// `<message>: <syscall> returned <code> (<desc>) <path>`.
export class SystemError extends Error {
    code: string;
    info: any;
    errno: number;
    syscall: string;
    path: string;
    constructor(key: string, prefix: string, errno: number, code: string, desc: string, syscall: string, path: string) {
        super(prefix + ': ' + syscall + ' returned ' + code + ' (' + desc + ') ' + path);
        this.name = 'SystemError';
        this.code = key;
        this.info = { code, message: desc, path, syscall, errno };
        this.errno = errno;
        this.syscall = syscall;
        this.path = path;
    }
}

// How Node's errors describe the value they received.
export function received(value: any): string {
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

export function invalidArgType(name: string, expected: string, value: any): NodeTypeError {
    return new NodeTypeError('ERR_INVALID_ARG_TYPE', 'The "' + name + '" argument must be ' + expected + '. ' + received(value));
}

export function invalidArgValue(name: string, value: any, reason: string): NodeTypeError {
    let shown = typeof value === 'string' ? "'" + value + "'" : String(value);
    return new NodeTypeError('ERR_INVALID_ARG_VALUE', "The argument '" + name + "' " + reason + '. Received ' + shown);
}

export function validateFunction(value: any, name: string): void {
    if (typeof value !== 'function') throw invalidArgType(name, 'of type function', value);
}

export function validateBoolean(value: any, name: string): void {
    if (typeof value !== 'boolean') throw invalidArgType(name, 'of type boolean', value);
}

export function validateObject(value: any, name: string): void {
    if (value === null || typeof value !== 'object' || Array.isArray(value)) throw invalidArgType(name, 'of type object', value);
}

export function outOfRange(name: string, range: string, value: any): NodeRangeError {
    return new NodeRangeError('ERR_OUT_OF_RANGE', 'The value of "' + name + '" is out of range. It must be ' + range + '. Received ' + String(value));
}

export function validateInteger(value: any, name: string, min: number): void {
    if (typeof value !== 'number') throw invalidArgType(name, 'of type number', value);
    if (!Number.isInteger(value)) throw outOfRange(name, 'an integer', value);
    if (value < min || value > Number.MAX_SAFE_INTEGER) throw outOfRange(name, '>= ' + min + ' && <= ' + Number.MAX_SAFE_INTEGER, value);
}

export function validateInt32(value: any, name: string, min: number): void {
    if (typeof value !== 'number') throw invalidArgType(name, 'of type number', value);
    if (!Number.isInteger(value)) throw outOfRange(name, 'an integer', value);
    if (value < min || value > 2147483647) throw outOfRange(name, '>= ' + min + ' && <= 2147483647', value);
}

export function validateUint32(value: any, name: string): void {
    if (typeof value !== 'number') throw invalidArgType(name, 'of type number', value);
    if (!Number.isInteger(value)) throw outOfRange(name, 'an integer', value);
    if (value < 0 || value > 4294967295) throw outOfRange(name, '>= 0 && <= 4294967295', value);
}

export function getValidatedFd(fd: any): number {
    validateInt32(fd, 'fd', 0);
    return fd;
}

export function validateOffsetLengthRead(offset: number, length: number, bufferLength: number): void {
    if (offset < 0) throw outOfRange('offset', '>= 0', offset);
    if (length < 0) throw outOfRange('length', '>= 0', length);
    if (offset + length > bufferLength) throw outOfRange('length', '<= ' + (bufferLength - offset), length);
}

export function validateOffsetLengthWrite(offset: number, length: number, byteLength: number): void {
    if (offset > byteLength) throw outOfRange('offset', '<= ' + byteLength, offset);
    if (length > byteLength - offset) throw outOfRange('length', '<= ' + (byteLength - offset), length);
    if (length < 0) throw outOfRange('length', '>= 0', length);
    validateInt32(length, 'length', 0);
}

// Node's isURL (internal/url.js).
function isURL(path: any): boolean {
    return path !== null && path !== undefined && typeof path === 'object' && !!path.href && !!path.protocol && path.auth === undefined && path.path === undefined;
}

export function getValidatedPath(path: any, name: string): string {
    let p: any = path;
    if (isURL(path)) {
        if (path.protocol !== 'file:') throw new NodeTypeError('ERR_INVALID_URL_SCHEME', 'The URL must be of scheme file');
        if (path.hostname !== '' && process.platform !== 'win32') {
            throw new NodeTypeError('ERR_INVALID_FILE_URL_HOST', 'File URL host must be "localhost" or empty on ' + process.platform);
        }
        const pathname: string = path.pathname;
        for (let n = 0; n < pathname.length; n++) {
            if (pathname[n] === '%') {
                const third = pathname.codePointAt(n + 2)! | 0x20;
                if (pathname[n + 1] === '2' && third === 102) {
                    throw new NodeTypeError('ERR_INVALID_FILE_URL_PATH', 'File URL path must not include encoded / characters');
                }
            }
        }
        p = decodeURIComponent(pathname);
    } else if (typeof path !== 'string' && !Buffer.isBuffer(path) && !(path instanceof Uint8Array)) {
        throw invalidArgType(name, 'of type string or an instance of Buffer or URL', path);
    }
    const s: string = typeof p === 'string' ? p : Buffer.from(p as Uint8Array).toString();
    if (s.indexOf('\u0000') !== -1) {
        throw new NodeTypeError('ERR_INVALID_ARG_VALUE', "The argument '" + name + "' must be a string, Uint8Array, or URL without null bytes. Received " + JSON.stringify(s));
    }
    return s;
}

export function stringToFlags(flags: any): number {
    if (typeof flags === 'number') return flags;
    if (flags === null || flags === undefined) return __kml_native.fsFlags('r');
    const n = typeof flags === 'string' ? __kml_native.fsFlags(flags) : -1;
    if (n < 0) {
        throw new NodeTypeError('ERR_INVALID_ARG_VALUE', "The argument 'flags' is invalid. Received " + (typeof flags === 'string' ? "'" + flags + "'" : String(flags)));
    }
    return n;
}

export function parseFileMode(value: any, name: string, def: number): number {
    if (value === null || value === undefined) return def;
    if (typeof value === 'string') {
        if (!/^[0-7]+$/.test(value)) {
            throw new NodeTypeError('ERR_INVALID_ARG_VALUE', "The argument '" + name + "' must be a 32-bit unsigned integer or an octal string. Received '" + value + "'");
        }
        return parseInt(value, 8);
    }
    validateUint32(value, name);
    return value;
}

const encodings: string[] = ['utf8', 'utf-8', 'hex', 'base64', 'base64url', 'latin1', 'binary', 'ascii', 'utf16le', 'utf-16le', 'ucs2', 'ucs-2', 'buffer'];

export function assertEncoding(encoding: any): void {
    if (encoding && encodings.indexOf(String(encoding).toLowerCase()) < 0) {
        throw new NodeTypeError('ERR_INVALID_ARG_VALUE', "The argument 'encoding' is invalid encoding. Received '" + String(encoding) + "'");
    }
}

// Node's getOptions: a string is the encoding; an object is the options.
export function getOptions(options: any, defaults: any): any {
    if (options === null || options === undefined || typeof options === 'function') return defaults;
    if (typeof options === 'string') {
        assertEncoding(options);
        const o: any = { ...defaults };
        o.encoding = options;
        return o;
    }
    if (typeof options !== 'object') throw invalidArgType('options', 'one of type string or an instance of Object', options);
    const o: any = { ...defaults, ...options };
    if (o.encoding !== 'buffer') assertEncoding(o.encoding);
    return o;
}

// Node's toUnixTimestamp: seconds, from a number, a numeric string or a Date.
export function toUnixTimestamp(time: any, name: string): number {
    // `+time == time`: a string equals its ToNumber unless that is NaN.
    if (typeof time === 'string' && !Number.isNaN(+time)) return +time;
    if (typeof time === 'number' && Number.isFinite(time)) {
        if (time < 0) return Date.now() / 1000;
        return time;
    }
    if (time instanceof Date) return (time as Date).getTime() / 1000;
    throw invalidArgType(name, 'of type string, number, or an instance of Date', time);
}

export function isArrayBufferView(v: any): boolean {
    return ArrayBuffer.isView(v);
}

// A view's bytes: a Uint8Array as it is, any other view copied out.
export function viewBytes(v: any): Uint8Array {
    if (v instanceof Uint8Array) return v as Uint8Array;
    if (v instanceof DataView) {
        const dv = v as DataView;
        return new Uint8Array(dv.buffer, dv.byteOffset, dv.byteLength);
    }
    const out = Buffer.alloc(v.byteLength);
    __kml_native.typedBytes(v, out);
    return out;
}

// ---- the binding ----

// The operations of klainfs.c (its KFS_* numbers).
export const OP_OPEN = 0;
export const OP_CLOSE = 1;
export const OP_FSYNC = 2;
export const OP_FDATASYNC = 3;
export const OP_READ = 4;
export const OP_WRITE = 5;
export const OP_STAT = 6;
export const OP_LSTAT = 7;
export const OP_FSTAT = 8;
export const OP_STATFS = 9;
export const OP_MKDIR = 10;
export const OP_MKDIRP = 11;
export const OP_RMDIR = 12;
export const OP_UNLINK = 13;
export const OP_RENAME = 14;
export const OP_LINK = 15;
export const OP_SYMLINK = 16;
export const OP_READLINK = 17;
export const OP_REALPATH = 18;
export const OP_MKDTEMP = 19;
export const OP_CHMOD = 20;
export const OP_FCHMOD = 21;
export const OP_CHOWN = 22;
export const OP_FCHOWN = 23;
export const OP_LCHOWN = 24;
export const OP_TRUNCATE = 25;
export const OP_FTRUNCATE = 26;
export const OP_ACCESS = 27;
export const OP_UTIMES = 28;
export const OP_FUTIMES = 29;
export const OP_LUTIMES = 30;
export const OP_COPYFILE = 31;
export const OP_READDIR = 32;

const noBytes: Buffer = Buffer.alloc(0);

export function uvError(errno: number, syscall: string, path?: string, dest?: string): NodeJS.ErrnoException {
    if (path === undefined) return __kml_native.fsError(errno, syscall) as NodeJS.ErrnoException;
    if (dest === undefined) return __kml_native.fsError(errno, syscall, path) as NodeJS.ErrnoException;
    return __kml_native.fsError(errno, syscall, path, dest) as NodeJS.ErrnoException;
}

// A synchronous request: its result, or its error thrown. path/dest name
// the files the error reports (none for a descriptor's operation).
export function callSync(op: number, syscall: string, path: string | undefined, dest: string | undefined,
                         a: number, b: number, c: number, d: number, data?: Uint8Array): number {
    const r = __kml_native.fsCall(op, path ?? '', dest ?? '', a, b, c, d, data ?? noBytes);
    if (r < 0) throw uvError(-r, syscall, path, dest);
    return r;
}

// A request on the thread pool: done(err, result, string result) on the
// loop thread.
export function callAsync(op: number, syscall: string, path: string | undefined, dest: string | undefined,
                          a: number, b: number, c: number, d: number, data: Uint8Array | undefined,
                          done: (err: NodeJS.ErrnoException | null, result: number, str: string) => void): void {
    __kml_native.fsCallAsync(op, path ?? '', dest ?? '', a, b, c, d, data ?? noBytes, (errno: number, result: number) => {
        const str = __kml_native.lastString();
        if (errno !== 0) done(uvError(errno, syscall, path, dest), 0, '');
        else done(null, result, str);
    });
}

export function lastString(): string {
    return __kml_native.lastString();
}

// ---- constants ----

const S_IFMT = 0o170000;
const S_IFREG = 0o100000;
const S_IFDIR = 0o040000;
const S_IFCHR = 0o020000;
const S_IFBLK = 0o060000;
const S_IFIFO = 0o010000;
const S_IFLNK = 0o120000;
const S_IFSOCK = 0o140000;

export const UV_DIRENT_UNKNOWN = 0;
export const UV_DIRENT_FILE = 1;
export const UV_DIRENT_DIR = 2;
export const UV_DIRENT_LINK = 3;
export const UV_DIRENT_FIFO = 4;
export const UV_DIRENT_SOCKET = 5;
export const UV_DIRENT_CHAR = 6;
export const UV_DIRENT_BLOCK = 7;

function makeConstants(): any {
    const c: any = { UV_FS_SYMLINK_DIR: 1, UV_FS_SYMLINK_JUNCTION: 2, O_RDONLY: 0, O_WRONLY: 1, O_RDWR: 2 };
    c.UV_DIRENT_UNKNOWN = 0;
    c.UV_DIRENT_FILE = 1;
    c.UV_DIRENT_DIR = 2;
    c.UV_DIRENT_LINK = 3;
    c.UV_DIRENT_FIFO = 4;
    c.UV_DIRENT_SOCKET = 5;
    c.UV_DIRENT_CHAR = 6;
    c.UV_DIRENT_BLOCK = 7;
    if (process.platform === 'linux') {
        c.EXTENSIONLESS_FORMAT_JAVASCRIPT = 0;
        c.EXTENSIONLESS_FORMAT_WASM = 1;
    }
    c.S_IFMT = S_IFMT;
    c.S_IFREG = S_IFREG;
    c.S_IFDIR = S_IFDIR;
    c.S_IFCHR = S_IFCHR;
    if (process.platform !== 'win32') c.S_IFBLK = S_IFBLK;
    if (process.platform !== 'win32') c.S_IFIFO = S_IFIFO;
    c.S_IFLNK = S_IFLNK;
    if (process.platform !== 'win32') c.S_IFSOCK = S_IFSOCK;
    if (process.platform === 'darwin') {
        c.O_CREAT = 0x200;
        c.O_EXCL = 0x800;
        c.UV_FS_O_FILEMAP = 0;
        c.O_NOCTTY = 0x20000;
        c.O_TRUNC = 0x400;
        c.O_APPEND = 0x8;
        c.O_DIRECTORY = 0x100000;
        c.O_NOFOLLOW = 0x100;
        c.O_SYNC = 0x80;
        c.O_DSYNC = 0x400000;
        c.O_SYMLINK = 0x200000;
        c.O_NONBLOCK = 0x4;
    } else if (process.platform === 'win32') {
        // This runtime's open() takes Linux's encoding on Windows too.
        c.O_CREAT = 0x40;
        c.O_EXCL = 0x80;
        c.UV_FS_O_FILEMAP = 0x20000000;
        c.O_TRUNC = 0x200;
        c.O_APPEND = 0x400;
    } else {
        c.O_CREAT = 0x40;
        c.O_EXCL = 0x80;
        c.UV_FS_O_FILEMAP = 0;
        c.O_NOCTTY = 0x100;
        c.O_TRUNC = 0x200;
        c.O_APPEND = 0x400;
        c.O_DIRECTORY = 0x10000;
        c.O_NOATIME = 0x40000;
        c.O_NOFOLLOW = 0x20000;
        c.O_SYNC = 0x101000;
        c.O_DSYNC = 0x1000;
        c.O_DIRECT = 0x4000;
        c.O_NONBLOCK = 0x800;
    }
    if (process.platform !== 'win32') {
        c.S_IRWXU = 0o700;
        c.S_IRUSR = 0o400;
        c.S_IWUSR = 0o200;
        c.S_IXUSR = 0o100;
        c.S_IRWXG = 0o70;
        c.S_IRGRP = 0o40;
        c.S_IWGRP = 0o20;
        c.S_IXGRP = 0o10;
        c.S_IRWXO = 0o7;
        c.S_IROTH = 0o4;
        c.S_IWOTH = 0o2;
        c.S_IXOTH = 0o1;
    } else {
        c.S_IRUSR = 0o400;
        c.S_IWUSR = 0o200;
    }
    c.F_OK = 0;
    c.R_OK = 4;
    c.W_OK = 2;
    c.X_OK = 1;
    c.UV_FS_COPYFILE_EXCL = 1;
    c.COPYFILE_EXCL = 1;
    c.UV_FS_COPYFILE_FICLONE = 2;
    c.COPYFILE_FICLONE = 2;
    c.UV_FS_COPYFILE_FICLONE_FORCE = 4;
    c.COPYFILE_FICLONE_FORCE = 4;
    return c;
}

export const constants: any = makeConstants();

export const F_OK = 0;
export const R_OK = 4;
export const W_OK = 2;
export const X_OK = 1;

// ---- Stats ----

function dateFromMs(ms: number): Date {
    return new Date(Math.round(ms));
}

function msFromTimeSpec(sec: number, nsec: number): number {
    return sec * 1000 + nsec / 1000000;
}

// StatsBase's kind predicates, over the mode.
function modeIs(mode: number, kind: number): boolean {
    return (mode & S_IFMT) === kind;
}

export class Stats {
    dev: number;
    mode: number;
    nlink: number;
    uid: number;
    gid: number;
    rdev: number;
    blksize: number;
    ino: number;
    size: number;
    blocks: number;
    atimeMs: number;
    mtimeMs: number;
    ctimeMs: number;
    birthtimeMs: number;

    constructor(dev: number, mode: number, nlink: number, uid: number, gid: number, rdev: number, blksize: number,
                ino: number, size: number, blocks: number, atimeMs: number, mtimeMs: number, ctimeMs: number, birthtimeMs: number) {
        this.dev = dev;
        this.mode = mode;
        this.nlink = nlink;
        this.uid = uid;
        this.gid = gid;
        this.rdev = rdev;
        this.blksize = blksize;
        this.ino = ino;
        this.size = size;
        this.blocks = blocks;
        this.atimeMs = atimeMs;
        this.mtimeMs = mtimeMs;
        this.ctimeMs = ctimeMs;
        this.birthtimeMs = birthtimeMs;
    }

    get atime(): Date { return dateFromMs(this.atimeMs); }
    get mtime(): Date { return dateFromMs(this.mtimeMs); }
    get ctime(): Date { return dateFromMs(this.ctimeMs); }
    get birthtime(): Date { return dateFromMs(this.birthtimeMs); }

    isDirectory(): boolean { return modeIs(this.mode, S_IFDIR); }
    isFile(): boolean { return modeIs(this.mode, S_IFREG); }
    isBlockDevice(): boolean { return modeIs(this.mode, S_IFBLK); }
    isCharacterDevice(): boolean { return modeIs(this.mode, S_IFCHR); }
    isSymbolicLink(): boolean { return modeIs(this.mode, S_IFLNK); }
    isFIFO(): boolean { return modeIs(this.mode, S_IFIFO); }
    isSocket(): boolean { return modeIs(this.mode, S_IFSOCK); }
}

// The bytes a stat request fills: 18 doubles (Node's statValues).
export function statBuffer(): Buffer {
    return Buffer.alloc(18 * 8);
}

export function getStatsFromBinding(b: Buffer): Stats {
    const v = (i: number): number => b.readDoubleLE(i * 8);
    return new Stats(v(0), v(1), v(2), v(3), v(4), v(5), v(6), v(7), v(8), v(9),
        msFromTimeSpec(v(10), v(11)), msFromTimeSpec(v(12), v(13)),
        msFromTimeSpec(v(14), v(15)), msFromTimeSpec(v(16), v(17)));
}

export class StatFs {
    type: number;
    bsize: number;
    frsize: number;
    blocks: number;
    bfree: number;
    bavail: number;
    files: number;
    ffree: number;

    constructor(type: number, bsize: number, frsize: number, blocks: number, bfree: number, bavail: number, files: number, ffree: number) {
        this.type = type;
        this.bsize = bsize;
        this.frsize = frsize;
        this.blocks = blocks;
        this.bfree = bfree;
        this.bavail = bavail;
        this.files = files;
        this.ffree = ffree;
    }
}

export function getStatFsFromBinding(b: Buffer): StatFs {
    const v = (i: number): number => b.readDoubleLE(i * 8);
    return new StatFs(v(0), v(1), v(2), v(3), v(4), v(5), v(6), v(7));
}

// ---- Dirent ----

export class Dirent {
    name: string;
    parentPath: string;
    private _kmlType: number;

    constructor(name: string, type: number, path: string) {
        this.name = name;
        this.parentPath = path;
        this._kmlType = type;
    }

    isDirectory(): boolean { return this._kmlType === UV_DIRENT_DIR; }
    isFile(): boolean { return this._kmlType === UV_DIRENT_FILE; }
    isBlockDevice(): boolean { return this._kmlType === UV_DIRENT_BLOCK; }
    isCharacterDevice(): boolean { return this._kmlType === UV_DIRENT_CHAR; }
    isSymbolicLink(): boolean { return this._kmlType === UV_DIRENT_LINK; }
    isFIFO(): boolean { return this._kmlType === UV_DIRENT_FIFO; }
    isSocket(): boolean { return this._kmlType === UV_DIRENT_SOCKET; }
}

// A readdir result (klainfs.c's encoding): [names, types].
export function decodeEntries(s: string): [string[], number[]] {
    const names: string[] = [];
    const types: number[] = [];
    if (s.length === 0) return [names, types];
    const parts = s.split('/');
    for (let i = 0; i < parts.length; i++) {
        const p = parts[i];
        types.push(p.charCodeAt(0) - 48);
        names.push(p.slice(1));
    }
    return [names, types];
}

// Node's getDirents: an entry of unknown type is lstat'ed.
export function direntsOf(path: string, names: string[], types: number[]): Dirent[] {
    const out: Dirent[] = [];
    for (let i = 0; i < names.length; i++) {
        let t = types[i];
        if (t === UV_DIRENT_UNKNOWN) {
            const b = statBuffer();
            const full = path + (path.endsWith('/') ? '' : '/') + names[i];
            if (__kml_native.fsCall(OP_LSTAT, full, '', 0, 0, 0, 0, b) >= 0) {
                const m = b.readDoubleLE(8) & S_IFMT;
                t = m === S_IFDIR ? UV_DIRENT_DIR : m === S_IFREG ? UV_DIRENT_FILE : m === S_IFLNK ? UV_DIRENT_LINK
                  : m === S_IFIFO ? UV_DIRENT_FIFO : m === S_IFSOCK ? UV_DIRENT_SOCKET : m === S_IFCHR ? UV_DIRENT_CHAR
                  : m === S_IFBLK ? UV_DIRENT_BLOCK : UV_DIRENT_UNKNOWN;
            }
        }
        out.push(new Dirent(names[i], t, path));
    }
    return out;
}

// Names as the encoding asks: strings, or Buffers for 'buffer'.
export function encodeNames(names: string[], encoding: any): any[] {
    if (encoding === 'buffer') return names.map((n: string) => Buffer.from(n));
    if (encoding && encoding !== 'utf8' && encoding !== 'utf-8') return names.map((n: string) => Buffer.from(n).toString(encoding));
    return names;
}

export function encodeString(s: string, encoding: any): any {
    if (encoding === 'buffer') return Buffer.from(s);
    if (encoding && encoding !== 'utf8' && encoding !== 'utf-8') return Buffer.from(s).toString(encoding);
    return s;
}
