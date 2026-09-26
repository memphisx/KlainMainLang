// The Node.js and web globals this compiler implements (TDD-00230 P3.1), in
// TypeScript's and @types/node's own shapes; tools/libconform checks them
// against TypeScript's library.

interface Console {
    assert(condition?: boolean, ...data: any[]): void;
    count(label?: string): void;
    countReset(label?: string): void;
    debug(...data: any[]): void;
    dir(item?: any, options?: any): void;
    error(...data: any[]): void;
    group(...data: any[]): void;
    groupCollapsed(...data: any[]): void;
    groupEnd(): void;
    info(...data: any[]): void;
    log(...data: any[]): void;
    table(tabularData?: any, properties?: string[]): void;
    time(label?: string): void;
    timeEnd(label?: string): void;
    trace(...data: any[]): void;
    warn(...data: any[]): void;
}

declare var console: Console;

// Node's `path` module (@types/node's PlatformPath, as exported functions).
declare module "path" {
    interface ParsedPath {
        root: string;
        dir: string;
        base: string;
        ext: string;
        name: string;
    }
    interface FormatInputPathObject {
        root?: string | undefined;
        dir?: string | undefined;
        base?: string | undefined;
        ext?: string | undefined;
        name?: string | undefined;
    }
    export function normalize(path: string): string;
    export function join(...paths: string[]): string;
    export function resolve(...paths: string[]): string;
    export function isAbsolute(path: string): boolean;
    export function relative(from: string, to: string): string;
    export function dirname(path: string): string;
    export function basename(path: string, suffix?: string): string;
    export function extname(path: string): string;
    export const sep: "\\" | "/";
    export const delimiter: ";" | ":";
    export function parse(path: string): ParsedPath;
    export function format(pathObject: FormatInputPathObject): string;
    export function toNamespacedPath(path: string): string;
}

// Node's `os` module (@types/node's os.d.ts, without `constants`,
// `getPriority`/`setPriority` and the Buffer-encoded `userInfo`).
declare module "os" {
    interface CpuInfo {
        model: string;
        speed: number;
        times: {
            user: number;
            nice: number;
            sys: number;
            idle: number;
            irq: number;
        };
    }
    interface NetworkInterfaceBase {
        address: string;
        netmask: string;
        mac: string;
        internal: boolean;
        cidr: string | null;
        scopeid?: number;
    }
    interface NetworkInterfaceInfoIPv4 extends NetworkInterfaceBase {
        family: "IPv4";
    }
    interface NetworkInterfaceInfoIPv6 extends NetworkInterfaceBase {
        family: "IPv6";
        scopeid: number;
    }
    interface UserInfo<T> {
        username: T;
        uid: number;
        gid: number;
        shell: T | null;
        homedir: T;
    }
    interface UserInfoOptions {
        encoding?: BufferEncoding | "buffer" | undefined;
    }
    interface UserInfoOptionsWithStringEncoding extends UserInfoOptions {
        encoding?: BufferEncoding | undefined;
    }
    type NetworkInterfaceInfo = NetworkInterfaceInfoIPv4 | NetworkInterfaceInfoIPv6;
    export function hostname(): string;
    export function loadavg(): number[];
    export function uptime(): number;
    export function freemem(): number;
    export function totalmem(): number;
    export function cpus(): CpuInfo[];
    export function availableParallelism(): number;
    export function type(): string;
    export function release(): string;
    export function networkInterfaces(): NodeJS.Dict<NetworkInterfaceInfo[]>;
    export function homedir(): string;
    export function userInfo(options?: UserInfoOptionsWithStringEncoding): UserInfo<string>;
    export const devNull: string;
    export const EOL: string;
    export function arch(): string;
    export function version(): string;
    export function platform(): NodeJS.Platform;
    export function machine(): string;
    export function tmpdir(): string;
    export function endianness(): "BE" | "LE";
}

// @types/node's Buffer (buffer.d.ts, and buffer.buffer.d.ts for TypeScript
// 5.7 and later). Buffer.from's arguments are plain ArrayLike, ArrayBuffer
// and string types: @types/node's WithImplicitCoercion and
// ImplicitArrayBuffer are conditional types the checker does not model.
interface BufferConstructor {
    new (str: string, encoding?: BufferEncoding): Buffer<ArrayBuffer>;
    new (size: number): Buffer<ArrayBuffer>;
    new (array: ArrayLike<number>): Buffer<ArrayBuffer>;
    new <TArrayBuffer extends ArrayBufferLike = ArrayBuffer>(arrayBuffer: TArrayBuffer): Buffer<TArrayBuffer>;
    from(array: ArrayLike<number>): Buffer<ArrayBuffer>;
    from<TArrayBuffer extends ArrayBufferLike>(arrayBuffer: TArrayBuffer, byteOffset?: number, length?: number): Buffer<TArrayBuffer>;
    from(string: string, encoding?: BufferEncoding): Buffer<ArrayBuffer>;
    from(arrayOrString: ArrayLike<number> | string): Buffer<ArrayBuffer>;
    of(...items: number[]): Buffer<ArrayBuffer>;
    concat(list: readonly Uint8Array[], totalLength?: number): Buffer<ArrayBuffer>;
    alloc(size: number, fill?: string | Uint8Array | number, encoding?: BufferEncoding): Buffer<ArrayBuffer>;
    allocUnsafe(size: number): Buffer<ArrayBuffer>;
    allocUnsafeSlow(size: number): Buffer<ArrayBuffer>;
    isBuffer(obj: any): obj is Buffer;
    isEncoding(encoding: string): encoding is BufferEncoding;
    byteLength(string: string | NodeJS.ArrayBufferView | ArrayBufferLike, encoding?: BufferEncoding): number;
    compare(buf1: Uint8Array, buf2: Uint8Array): -1 | 0 | 1;
    poolSize: number;
}
interface Buffer<TArrayBuffer extends ArrayBufferLike = ArrayBufferLike> extends Uint8Array<TArrayBuffer> {
    slice(start?: number, end?: number): Buffer<ArrayBuffer>;
    subarray(start?: number, end?: number): Buffer<TArrayBuffer>;
    write(string: string, encoding?: BufferEncoding): number;
    write(string: string, offset: number, encoding?: BufferEncoding): number;
    write(string: string, offset: number, length: number, encoding?: BufferEncoding): number;
    toString(encoding?: BufferEncoding, start?: number, end?: number): string;
    toJSON(): {
        type: "Buffer";
        data: number[];
    };
    equals(otherBuffer: Uint8Array): boolean;
    compare(
        target: Uint8Array,
        targetStart?: number,
        targetEnd?: number,
        sourceStart?: number,
        sourceEnd?: number,
        ): -1 | 0 | 1;
    copy(target: Uint8Array, targetStart?: number, sourceStart?: number, sourceEnd?: number): number;
    writeBigInt64BE(value: bigint, offset?: number): number;
    writeBigInt64LE(value: bigint, offset?: number): number;
    writeBigUInt64BE(value: bigint, offset?: number): number;
    writeBigUint64BE(value: bigint, offset?: number): number;
    writeBigUInt64LE(value: bigint, offset?: number): number;
    writeBigUint64LE(value: bigint, offset?: number): number;
    writeUIntLE(value: number, offset: number, byteLength: number): number;
    writeUintLE(value: number, offset: number, byteLength: number): number;
    writeUIntBE(value: number, offset: number, byteLength: number): number;
    writeUintBE(value: number, offset: number, byteLength: number): number;
    writeIntLE(value: number, offset: number, byteLength: number): number;
    writeIntBE(value: number, offset: number, byteLength: number): number;
    readBigUInt64BE(offset?: number): bigint;
    readBigUint64BE(offset?: number): bigint;
    readBigUInt64LE(offset?: number): bigint;
    readBigUint64LE(offset?: number): bigint;
    readBigInt64BE(offset?: number): bigint;
    readBigInt64LE(offset?: number): bigint;
    readUIntLE(offset: number, byteLength: number): number;
    readUintLE(offset: number, byteLength: number): number;
    readUIntBE(offset: number, byteLength: number): number;
    readUintBE(offset: number, byteLength: number): number;
    readIntLE(offset: number, byteLength: number): number;
    readIntBE(offset: number, byteLength: number): number;
    readUInt8(offset?: number): number;
    readUint8(offset?: number): number;
    readUInt16LE(offset?: number): number;
    readUint16LE(offset?: number): number;
    readUInt16BE(offset?: number): number;
    readUint16BE(offset?: number): number;
    readUInt32LE(offset?: number): number;
    readUint32LE(offset?: number): number;
    readUInt32BE(offset?: number): number;
    readUint32BE(offset?: number): number;
    readInt8(offset?: number): number;
    readInt16LE(offset?: number): number;
    readInt16BE(offset?: number): number;
    readInt32LE(offset?: number): number;
    readInt32BE(offset?: number): number;
    readFloatLE(offset?: number): number;
    readFloatBE(offset?: number): number;
    readDoubleLE(offset?: number): number;
    readDoubleBE(offset?: number): number;
    reverse(): this;
    swap16(): this;
    swap32(): this;
    swap64(): this;
    writeUInt8(value: number, offset?: number): number;
    writeUint8(value: number, offset?: number): number;
    writeUInt16LE(value: number, offset?: number): number;
    writeUint16LE(value: number, offset?: number): number;
    writeUInt16BE(value: number, offset?: number): number;
    writeUint16BE(value: number, offset?: number): number;
    writeUInt32LE(value: number, offset?: number): number;
    writeUint32LE(value: number, offset?: number): number;
    writeUInt32BE(value: number, offset?: number): number;
    writeUint32BE(value: number, offset?: number): number;
    writeInt8(value: number, offset?: number): number;
    writeInt16LE(value: number, offset?: number): number;
    writeInt16BE(value: number, offset?: number): number;
    writeInt32LE(value: number, offset?: number): number;
    writeInt32BE(value: number, offset?: number): number;
    writeFloatLE(value: number, offset?: number): number;
    writeFloatBE(value: number, offset?: number): number;
    writeDoubleLE(value: number, offset?: number): number;
    writeDoubleBE(value: number, offset?: number): number;
    fill(value: string | Uint8Array | number, offset?: number, end?: number, encoding?: BufferEncoding): this;
    fill(value: string | Uint8Array | number, offset: number, encoding: BufferEncoding): this;
    fill(value: string | Uint8Array | number, encoding: BufferEncoding): this;
    indexOf(value: string | number | Uint8Array, byteOffset?: number, encoding?: BufferEncoding): number;
    indexOf(value: string | number | Uint8Array, encoding: BufferEncoding): number;
    lastIndexOf(value: string | number | Uint8Array, byteOffset?: number, encoding?: BufferEncoding): number;
    lastIndexOf(value: string | number | Uint8Array, encoding: BufferEncoding): number;
    includes(value: string | number | Buffer, byteOffset?: number, encoding?: BufferEncoding): boolean;
    includes(value: string | number | Buffer, encoding: BufferEncoding): boolean;
}
declare var Buffer: BufferConstructor;
// The web URL and URLSearchParams (TypeScript's lib.dom.d.ts).
interface URL {
    hash: string;
    host: string;
    hostname: string;
    href: string;
    toString(): string;
    readonly origin: string;
    password: string;
    pathname: string;
    port: string;
    protocol: string;
    search: string;
    readonly searchParams: URLSearchParams;
    username: string;
    toJSON(): string;
}
declare var URL: {
    prototype: URL;
    new (url: string | URL, base?: string | URL): URL;
    canParse(url: string | URL, base?: string | URL): boolean;
    createObjectURL(obj: Blob | MediaSource): string;
    parse(url: string | URL, base?: string | URL): URL | null;
    revokeObjectURL(url: string): void;
};
interface URLSearchParams {
    readonly size: number;
    append(name: string, value: string): void;
    delete(name: string, value?: string): void;
    get(name: string): string | null;
    getAll(name: string): string[];
    has(name: string, value?: string): boolean;
    set(name: string, value: string): void;
    sort(): void;
    toString(): string;
    forEach(callbackfn: (value: string, key: string, parent: URLSearchParams) => void, thisArg?: any): void;
}
declare var URLSearchParams: {
    prototype: URLSearchParams;
    new (init?: string[][] | Record<string, string> | string | URLSearchParams): URLSearchParams;
};

type NonSharedBuffer = Buffer<ArrayBuffer>;
type AllowSharedBuffer = Buffer<ArrayBufferLike>;

// @types/node's buffer.d.ts global.
type BufferEncoding = "ascii" | "utf8" | "utf-8" | "utf16le" | "utf-16le" | "ucs2" | "ucs-2" | "base64" | "base64url" | "latin1" | "binary" | "hex";

// Node's `process` (@types/node's NodeJS.Process: the members this compiler
// implements with a plain type; streams and events are not declared yet).
declare namespace NodeJS {
    interface Dict<T> {
        [key: string]: T | undefined;
    }
    interface ProcessEnv extends Dict<string> {}
    type Platform = "aix" | "android" | "darwin" | "freebsd" | "haiku" | "linux" | "openbsd" | "sunos" | "win32" | "cygwin" | "netbsd";
    type Architecture = "arm" | "arm64" | "ia32" | "loong64" | "mips" | "mipsel" | "ppc" | "ppc64" | "riscv64" | "s390" | "s390x" | "x64";
    interface MemoryUsage {
        rss: number;
        heapTotal: number;
        heapUsed: number;
        external: number;
        arrayBuffers: number;
    }
    interface Process {
        argv: string[];
        argv0: string;
        execPath: string;
        env: ProcessEnv;
        exitCode: number | string | null | undefined;
        readonly pid: number;
        readonly ppid: number;
        readonly platform: Platform;
        readonly arch: Architecture;
        readonly version: string;
        title: string;
        cwd(): string;
        chdir(directory: string): void;
        exit(code?: number | string | null): never;
        uptime(): number;
        memoryUsage(): MemoryUsage;
        kill(pid: number, signal?: string | number): true;
        nextTick(callback: Function, ...args: any[]): void;
    }
    type TypedArray<TArrayBuffer extends ArrayBufferLike = ArrayBufferLike> =
        | Uint8Array<TArrayBuffer>
        | Uint8ClampedArray<TArrayBuffer>
        | Uint16Array<TArrayBuffer>
        | Uint32Array<TArrayBuffer>
        | Int8Array<TArrayBuffer>
        | Int16Array<TArrayBuffer>
        | Int32Array<TArrayBuffer>
        | BigUint64Array<TArrayBuffer>
        | BigInt64Array<TArrayBuffer>
        | Float32Array<TArrayBuffer>
        | Float64Array<TArrayBuffer>;
    type ArrayBufferView<TArrayBuffer extends ArrayBufferLike = ArrayBufferLike> = TypedArray<TArrayBuffer> | DataView<TArrayBuffer>;
    interface ErrnoException extends Error {
        errno?: number | undefined;
        code?: string | undefined;
        path?: string | undefined;
        syscall?: string | undefined;
    }
    // A timer handle. Its members (ref, unref, hasRef, refresh, close) are
    // not implemented: the handle is the timer's id.
    interface Timeout {}
    interface Immediate {}
}

declare var process: NodeJS.Process;

// Node's timers (@types/node's timers.d.ts globals).
declare function setTimeout<TArgs extends any[]>(callback: (...args: TArgs) => void, delay?: number, ...args: TArgs): NodeJS.Timeout;
declare function setTimeout(callback: (_: void) => void, delay?: number): NodeJS.Timeout;
declare function setInterval<TArgs extends any[]>(callback: (...args: TArgs) => void, delay?: number, ...args: TArgs): NodeJS.Timeout;
declare function setInterval(callback: (_: void) => void, delay?: number): NodeJS.Timeout;
declare function setImmediate<TArgs extends any[]>(callback: (...args: TArgs) => void, ...args: TArgs): NodeJS.Immediate;
declare function setImmediate(callback: (_: void) => void): NodeJS.Immediate;
declare function clearTimeout(timeout: NodeJS.Timeout | string | number | undefined): void;
declare function clearInterval(timeout: NodeJS.Timeout | string | number | undefined): void;
declare function clearImmediate(immediate: NodeJS.Immediate | undefined): void;
declare function queueMicrotask(callback: () => void): void;

// Node's `fs` module, its synchronous functions (@types/node's fs.d.ts).
// Stats has no Date-valued times and Dirent no `path`: neither is
// implemented. Buffer, URL and the typed-array views are not declared yet,
// so a path or data argument is not checked against them.
declare module "fs" {
    type PathLike = string | Buffer | URL;
    type PathOrFileDescriptor = PathLike | number;
    type TimeLike = string | number | Date;
    type Mode = number | string;
    type OpenMode = number | string;
    interface ObjectEncodingOptions {
        encoding?: BufferEncoding | null | undefined;
    }
    type EncodingOption = ObjectEncodingOptions | BufferEncoding | undefined | null;
    type WriteFileOptions =
        | (ObjectEncodingOptions & {
            mode?: Mode | undefined;
            flag?: string | undefined;
            flush?: boolean | undefined;
        })
        | BufferEncoding
        | null;
    interface RmOptions {
        force?: boolean | undefined;
        maxRetries?: number | undefined;
        recursive?: boolean | undefined;
        retryDelay?: number | undefined;
    }
    interface RmDirOptions {
        maxRetries?: number | undefined;
        recursive?: boolean | undefined;
        retryDelay?: number | undefined;
    }
    interface MakeDirectoryOptions {
        recursive?: boolean | undefined;
        mode?: Mode | undefined;
    }
    interface StatOptions {
        bigint?: boolean | undefined;
    }
    interface StatSyncOptions extends StatOptions {
        throwIfNoEntry?: boolean | undefined;
    }
    interface StatFsOptions {
        bigint?: boolean | undefined;
    }
    interface StatsBase<T> {
        isFile(): boolean;
        isDirectory(): boolean;
        isSymbolicLink(): boolean;
        dev: T;
        ino: T;
        mode: T;
        nlink: T;
        uid: T;
        gid: T;
        rdev: T;
        size: T;
        blksize: T;
        blocks: T;
        atimeMs: T;
        mtimeMs: T;
        ctimeMs: T;
        birthtimeMs: T;
    }
    interface Stats extends StatsBase<number> {}
    interface StatsFsBase<T> {
        type: T;
        bsize: T;
        blocks: T;
        bfree: T;
        bavail: T;
        files: T;
        ffree: T;
    }
    interface StatsFs extends StatsFsBase<number> {}
    interface Dirent {
        isFile(): boolean;
        isDirectory(): boolean;
        isBlockDevice(): boolean;
        isCharacterDevice(): boolean;
        isSymbolicLink(): boolean;
        isFIFO(): boolean;
        isSocket(): boolean;
        name: string;
        parentPath: string;
    }
    export function readFileSync(path: PathOrFileDescriptor, options?: { encoding?: null | undefined; flag?: string | undefined } | null): NonSharedBuffer;
    export function readFileSync(path: PathOrFileDescriptor, options: { encoding: BufferEncoding; flag?: string | undefined } | BufferEncoding): string;
    export function readFileSync(path: PathOrFileDescriptor, options?: (ObjectEncodingOptions & { flag?: string | undefined }) | BufferEncoding | null): string | NonSharedBuffer;
    export function writeFileSync(file: PathOrFileDescriptor, data: string | NodeJS.ArrayBufferView, options?: WriteFileOptions): void;
    export function appendFileSync(path: PathOrFileDescriptor, data: string | Uint8Array, options?: WriteFileOptions): void;
    export function existsSync(path: PathLike): boolean;
    export function statSync(path: PathLike, options?: StatSyncOptions & { bigint?: false | undefined }): Stats;
    export function lstatSync(path: PathLike, options?: StatSyncOptions & { bigint?: false | undefined }): Stats;
    export function fstatSync(fd: number, options?: StatOptions & { bigint?: false | undefined }): Stats;
    export function statfsSync(path: PathLike, options?: StatFsOptions & { bigint?: false | undefined }): StatsFs;
    export function rmSync(path: PathLike, options?: RmOptions): void;
    export function realpathSync(path: PathLike, options?: EncodingOption): string;
    export function mkdtempSync(prefix: string, options?: EncodingOption): string;
    export function symlinkSync(target: PathLike, path: PathLike, type?: "dir" | "file" | "junction" | null): void;
    export function linkSync(existingPath: PathLike, newPath: PathLike): void;
    export function readlinkSync(path: PathLike, options?: EncodingOption): string;
    export function chmodSync(path: PathLike, mode: Mode): void;
    export function fchmodSync(fd: number, mode: Mode): void;
    export function truncateSync(path: PathLike, len?: number): void;
    export function ftruncateSync(fd: number, len?: number): void;
    export function accessSync(path: PathLike, mode?: number): void;
    export function openSync(path: PathLike, flags: OpenMode, mode?: Mode | null): number;
    export function closeSync(fd: number): void;
    export function fsyncSync(fd: number): void;
    export function fdatasyncSync(fd: number): void;
    export function writeSync(fd: number, string: string, position?: number | null, encoding?: BufferEncoding | null): number;
    export function unlinkSync(path: PathLike): void;
    export function mkdirSync(path: PathLike, options: MakeDirectoryOptions & { recursive: true }): string | undefined;
    export function mkdirSync(path: PathLike, options?: Mode | (MakeDirectoryOptions & { recursive?: false | undefined }) | null): void;
    export function mkdirSync(path: PathLike, options?: Mode | MakeDirectoryOptions | null): string | undefined;
    export function rmdirSync(path: PathLike, options?: RmDirOptions): void;
    export function readdirSync(path: PathLike, options?: { encoding: BufferEncoding | null; withFileTypes?: false | undefined; recursive?: boolean | undefined } | BufferEncoding | null): string[];
    export function readdirSync(path: PathLike, options?: (ObjectEncodingOptions & { withFileTypes?: false | undefined; recursive?: boolean | undefined }) | BufferEncoding | null): string[] | NonSharedBuffer[];
    export function readdirSync(path: PathLike, options: ObjectEncodingOptions & { withFileTypes: true; recursive?: boolean | undefined }): Dirent[];
    export function renameSync(oldPath: PathLike, newPath: PathLike): void;
    export function copyFileSync(src: PathLike, dest: PathLike, mode?: number): void;
    export function utimesSync(path: PathLike, atime: TimeLike, mtime: TimeLike): void;
    export function futimesSync(fd: number, atime: TimeLike, mtime: TimeLike): void;

    // The callback forms (fs.d.ts). A callback's error is null on success.
    type NoParamCallback = (err: NodeJS.ErrnoException | null) => void;
    export function readFile(path: PathOrFileDescriptor, options: { encoding?: null | undefined; flag?: string | undefined } | undefined | null, callback: (err: NodeJS.ErrnoException | null, data: NonSharedBuffer) => void): void;
    export function readFile(path: PathOrFileDescriptor, options: { encoding: BufferEncoding; flag?: string | undefined } | BufferEncoding, callback: (err: NodeJS.ErrnoException | null, data: string) => void): void;
    export function readFile(path: PathOrFileDescriptor, options: (ObjectEncodingOptions & { flag?: string | undefined }) | BufferEncoding | undefined | null, callback: (err: NodeJS.ErrnoException | null, data: string | NonSharedBuffer) => void): void;
    export function readFile(path: PathOrFileDescriptor, callback: (err: NodeJS.ErrnoException | null, data: NonSharedBuffer) => void): void;
    export function writeFile(file: PathOrFileDescriptor, data: string | NodeJS.ArrayBufferView, options: WriteFileOptions, callback: NoParamCallback): void;
    export function writeFile(path: PathOrFileDescriptor, data: string | NodeJS.ArrayBufferView, callback: NoParamCallback): void;
    export function appendFile(path: PathOrFileDescriptor, data: string | Uint8Array, options: WriteFileOptions, callback: NoParamCallback): void;
    export function appendFile(file: PathOrFileDescriptor, data: string | Uint8Array, callback: NoParamCallback): void;
    export function unlink(path: PathLike, callback: NoParamCallback): void;
    export function mkdir(path: PathLike, options: Mode | MakeDirectoryOptions | null | undefined, callback: (err: NodeJS.ErrnoException | null, path?: string) => void): void;
    export function mkdir(path: PathLike, callback: NoParamCallback): void;
    export function rmdir(path: PathLike, callback: NoParamCallback): void;
    export function rmdir(path: PathLike, options: RmDirOptions, callback: NoParamCallback): void;
    export function rename(oldPath: PathLike, newPath: PathLike, callback: NoParamCallback): void;
    export function copyFile(src: PathLike, dest: PathLike, callback: NoParamCallback): void;
    export function copyFile(src: PathLike, dest: PathLike, mode: number, callback: NoParamCallback): void;
    export function readdir(path: PathLike, options: { encoding: BufferEncoding | null; withFileTypes?: false | undefined; recursive?: boolean | undefined } | BufferEncoding | undefined | null, callback: (err: NodeJS.ErrnoException | null, files: string[]) => void): void;
    export function readdir(path: PathLike, callback: (err: NodeJS.ErrnoException | null, files: string[]) => void): void;
    export function readdir(path: PathLike, options: ObjectEncodingOptions & { withFileTypes: true; recursive?: boolean | undefined }, callback: (err: NodeJS.ErrnoException | null, files: Dirent[]) => void): void;
    export function stat(path: PathLike, callback: (err: NodeJS.ErrnoException | null, stats: Stats) => void): void;
    export function stat(path: PathLike, options: (StatOptions & { bigint?: false | undefined }) | undefined, callback: (err: NodeJS.ErrnoException | null, stats: Stats) => void): void;
    export function lstat(path: PathLike, callback: (err: NodeJS.ErrnoException | null, stats: Stats) => void): void;
    export function lstat(path: PathLike, options: (StatOptions & { bigint?: false | undefined }) | undefined, callback: (err: NodeJS.ErrnoException | null, stats: Stats) => void): void;
    export function fstat(fd: number, callback: (err: NodeJS.ErrnoException | null, stats: Stats) => void): void;
    export function fstat(fd: number, options: (StatOptions & { bigint?: false | undefined }) | undefined, callback: (err: NodeJS.ErrnoException | null, stats: Stats) => void): void;
    export function statfs(path: PathLike, callback: (err: NodeJS.ErrnoException | null, stats: StatsFs) => void): void;
    export function statfs(path: PathLike, options: (StatFsOptions & { bigint?: false | undefined }) | undefined, callback: (err: NodeJS.ErrnoException | null, stats: StatsFs) => void): void;
    export function rm(path: PathLike, callback: NoParamCallback): void;
    export function rm(path: PathLike, options: RmOptions, callback: NoParamCallback): void;
    export function utimes(path: PathLike, atime: TimeLike, mtime: TimeLike, callback: NoParamCallback): void;
    export function futimes(fd: number, atime: TimeLike, mtime: TimeLike, callback: NoParamCallback): void;
    export function ftruncate(fd: number, callback: NoParamCallback): void;
    export function ftruncate(fd: number, len: number | undefined | null, callback: NoParamCallback): void;
    export function fchmod(fd: number, mode: Mode, callback: NoParamCallback): void;
    export function realpath(path: PathLike, callback: (err: NodeJS.ErrnoException | null, resolvedPath: string) => void): void;
    export function mkdtemp(prefix: string, callback: (err: NodeJS.ErrnoException | null, folder: string) => void): void;
    export function readlink(path: PathLike, callback: (err: NodeJS.ErrnoException | null, linkString: string) => void): void;
    export function link(existingPath: PathLike, newPath: PathLike, callback: NoParamCallback): void;
    export function symlink(target: PathLike, path: PathLike, callback: NoParamCallback): void;
    export function symlink(target: PathLike, path: PathLike, type: "dir" | "file" | "junction" | undefined | null, callback: NoParamCallback): void;
    export function chmod(path: PathLike, mode: Mode, callback: NoParamCallback): void;
    export function truncate(path: PathLike, callback: NoParamCallback): void;
    export function truncate(path: PathLike, len: number | undefined | null, callback: NoParamCallback): void;
    export function access(path: PathLike, callback: NoParamCallback): void;
    export function access(path: PathLike, mode: number | undefined, callback: NoParamCallback): void;

    // `fs.promises` (fs/promises.d.ts's functions, as its members).
    interface FsPromises {
        readFile(path: PathLike, options?: { encoding?: null | undefined; flag?: string | undefined } | null): Promise<NonSharedBuffer>;
        readFile(path: PathLike, options: { encoding: BufferEncoding; flag?: string | undefined } | BufferEncoding): Promise<string>;
        readFile(path: PathLike, options?: (ObjectEncodingOptions & { flag?: string | undefined }) | BufferEncoding | null): Promise<string | NonSharedBuffer>;
        writeFile(file: PathLike, data: string | NodeJS.ArrayBufferView, options?: WriteFileOptions): Promise<void>;
        appendFile(path: PathLike, data: string | NodeJS.ArrayBufferView, options?: WriteFileOptions): Promise<void>;
        unlink(path: PathLike): Promise<void>;
        mkdir(path: PathLike, options: MakeDirectoryOptions & { recursive: true }): Promise<string | undefined>;
        mkdir(path: PathLike, options?: Mode | (MakeDirectoryOptions & { recursive?: false | undefined }) | null): Promise<void>;
        mkdir(path: PathLike, options?: Mode | MakeDirectoryOptions | null): Promise<string | undefined>;
        rmdir(path: PathLike, options?: RmDirOptions): Promise<void>;
        rename(oldPath: PathLike, newPath: PathLike): Promise<void>;
        copyFile(src: PathLike, dest: PathLike, mode?: number): Promise<void>;
        readdir(path: PathLike, options?: (ObjectEncodingOptions & { withFileTypes?: false | undefined; recursive?: boolean | undefined }) | BufferEncoding | null): Promise<string[]>;
        readdir(path: PathLike, options: ObjectEncodingOptions & { withFileTypes: true; recursive?: boolean | undefined }): Promise<Dirent[]>;
        stat(path: PathLike, opts?: StatOptions & { bigint?: false | undefined }): Promise<Stats>;
        lstat(path: PathLike, opts?: StatOptions & { bigint?: false | undefined }): Promise<Stats>;
        statfs(path: PathLike, opts?: StatFsOptions & { bigint?: false | undefined }): Promise<StatsFs>;
        rm(path: PathLike, options?: RmOptions): Promise<void>;
        utimes(path: PathLike, atime: TimeLike, mtime: TimeLike): Promise<void>;
        realpath(path: PathLike): Promise<string>;
        mkdtemp(prefix: string): Promise<string>;
        readlink(path: PathLike): Promise<string>;
        link(existingPath: PathLike, newPath: PathLike): Promise<void>;
        symlink(target: PathLike, path: PathLike, type?: string | null): Promise<void>;
        chmod(path: PathLike, mode: Mode): Promise<void>;
        truncate(path: PathLike, len?: number): Promise<void>;
        access(path: PathLike, mode?: number): Promise<void>;
    }
    export const promises: FsPromises;
}

// Node's `fs/promises` module (@types/node's fs/promises.d.ts, the functions
// this compiler implements).
declare module "fs/promises" {
    type PathLike = string | Buffer | URL;
    type Mode = number | string;
    interface ObjectEncodingOptions {
        encoding?: BufferEncoding | null | undefined;
    }
    type WriteFileOptions =
        | (ObjectEncodingOptions & {
            mode?: Mode | undefined;
            flag?: string | undefined;
            flush?: boolean | undefined;
        })
        | BufferEncoding
        | null;
    interface MakeDirectoryOptions {
        recursive?: boolean | undefined;
        mode?: Mode | undefined;
    }
    interface RmDirOptions {
        maxRetries?: number | undefined;
        recursive?: boolean | undefined;
        retryDelay?: number | undefined;
    }
    interface Dirent {
        isFile(): boolean;
        isDirectory(): boolean;
        isBlockDevice(): boolean;
        isCharacterDevice(): boolean;
        isSymbolicLink(): boolean;
        isFIFO(): boolean;
        isSocket(): boolean;
        name: string;
        parentPath: string;
    }
    export function readFile(path: PathLike, options?: { encoding?: null | undefined; flag?: string | undefined } | null): Promise<NonSharedBuffer>;
    export function readFile(path: PathLike, options: { encoding: BufferEncoding; flag?: string | undefined } | BufferEncoding): Promise<string>;
    export function readFile(path: PathLike, options?: (ObjectEncodingOptions & { flag?: string | undefined }) | BufferEncoding | null): Promise<string | NonSharedBuffer>;
    export function writeFile(file: PathLike, data: string | NodeJS.ArrayBufferView, options?: WriteFileOptions): Promise<void>;
    export function appendFile(path: PathLike, data: string | NodeJS.ArrayBufferView, options?: WriteFileOptions): Promise<void>;
    export function unlink(path: PathLike): Promise<void>;
    export function mkdir(path: PathLike, options: MakeDirectoryOptions & { recursive: true }): Promise<string | undefined>;
    export function mkdir(path: PathLike, options?: Mode | (MakeDirectoryOptions & { recursive?: false | undefined }) | null): Promise<void>;
    export function mkdir(path: PathLike, options?: Mode | MakeDirectoryOptions | null): Promise<string | undefined>;
    export function rmdir(path: PathLike, options?: RmDirOptions): Promise<void>;
    export function rename(oldPath: PathLike, newPath: PathLike): Promise<void>;
    export function copyFile(src: PathLike, dest: PathLike, mode?: number): Promise<void>;
    export function readdir(path: PathLike, options?: (ObjectEncodingOptions & { withFileTypes?: false | undefined; recursive?: boolean | undefined }) | BufferEncoding | null): Promise<string[]>;
    export function readdir(path: PathLike, options: ObjectEncodingOptions & { withFileTypes: true; recursive?: boolean | undefined }): Promise<Dirent[]>;
    type TimeLike = string | number | Date;
    interface RmOptions {
        force?: boolean | undefined;
        maxRetries?: number | undefined;
        recursive?: boolean | undefined;
        retryDelay?: number | undefined;
    }
    interface StatOptions {
        bigint?: boolean | undefined;
    }
    interface StatFsOptions {
        bigint?: boolean | undefined;
    }
    interface Stats {
        isFile(): boolean;
        isDirectory(): boolean;
        isSymbolicLink(): boolean;
        isBlockDevice(): boolean;
        isCharacterDevice(): boolean;
        isFIFO(): boolean;
        isSocket(): boolean;
        dev: number; ino: number; mode: number; nlink: number; uid: number; gid: number; rdev: number;
        size: number; blksize: number; blocks: number;
        atimeMs: number; mtimeMs: number; ctimeMs: number; birthtimeMs: number;
        atime: Date; mtime: Date; ctime: Date; birthtime: Date;
    }
    interface StatsFs {
        type: number; bsize: number; blocks: number; bfree: number; bavail: number; files: number; ffree: number;
    }
    export function stat(path: PathLike, opts?: StatOptions & { bigint?: false | undefined }): Promise<Stats>;
    export function lstat(path: PathLike, opts?: StatOptions & { bigint?: false | undefined }): Promise<Stats>;
    export function statfs(path: PathLike, opts?: StatFsOptions & { bigint?: false | undefined }): Promise<StatsFs>;
    export function rm(path: PathLike, options?: RmOptions): Promise<void>;
    export function utimes(path: PathLike, atime: TimeLike, mtime: TimeLike): Promise<void>;
    export function realpath(path: PathLike): Promise<string>;
    export function mkdtemp(prefix: string): Promise<string>;
    export function readlink(path: PathLike): Promise<string>;
    export function link(existingPath: PathLike, newPath: PathLike): Promise<void>;
    export function symlink(target: PathLike, path: PathLike, type?: string | null): Promise<void>;
    export function chmod(path: PathLike, mode: Mode): Promise<void>;
    export function truncate(path: PathLike, len?: number): Promise<void>;
    export function access(path: PathLike, mode?: number): Promise<void>;
}

// Node's `events` module (@types/node's events.d.ts). @types/node types
// each listener and emit() through conditional types over the event map
// (Key, Args, Listener), which the checker does not model yet: every event
// here takes `...args: any[]`, the default map's shape, whatever the map.
// EventEmitter is a class there; here it is the interface and constructor
// pair TypeScript's own library uses for Map.
declare module "events" {
    interface EventEmitterOptions {
        captureRejections?: boolean | undefined;
    }
    type DefaultEventMap = [never];
    type AnyRest = [...args: any[]];
    interface EventEmitter<T = any> {
        on(eventName: string | symbol, listener: (...args: any[]) => void): this;
        once(eventName: string | symbol, listener: (...args: any[]) => void): this;
        off(eventName: string | symbol, listener: (...args: any[]) => void): this;
        removeListener(eventName: string | symbol, listener: (...args: any[]) => void): this;
        removeAllListeners(eventName?: string | symbol): this;
        emit(eventName: string | symbol, ...args: any[]): boolean;
        listenerCount(eventName: string | symbol, listener?: Function): number;
        eventNames(): (string | symbol)[];
    }
    interface EventEmitterConstructor {
        new <T = any>(options?: EventEmitterOptions): EventEmitter<T>;
        readonly prototype: EventEmitter;
    }
    var EventEmitter: EventEmitterConstructor;
    export function once(emitter: EventEmitter, eventName: string | symbol): Promise<any[]>;
    export function on(emitter: EventEmitter, eventName: string | symbol): AsyncIterableIterator<any[]>;
}

// The fetch API: Node's (undici-types' fetch.d.ts, through @types/node's
// web-globals/fetch.d.ts) in TypeScript's own library shapes (lib.dom.d.ts):
// fetch, Request, Response and Headers.
type RequestInfo = Request | string;
type BodyInit =
    | ArrayBuffer
    | AsyncIterable<Uint8Array>
    | Blob
    | FormData
    | Iterable<Uint8Array>
    | NodeJS.ArrayBufferView
    | URLSearchParams
    | null
    | string;
type HeadersInit = [string, string][] | Record<string, string> | Headers;
type RequestCache = "default" | "force-cache" | "no-cache" | "no-store" | "only-if-cached" | "reload";
type RequestCredentials = "omit" | "include" | "same-origin";
type RequestDestination = "" | "audio" | "audioworklet" | "document" | "embed" | "font" | "frame" | "iframe" | "image" | "json" | "manifest" | "object" | "paintworklet" | "report" | "script" | "sharedworker" | "style" | "track" | "video" | "worker" | "xslt";
type ReferrerPolicy = "" | "no-referrer" | "no-referrer-when-downgrade" | "origin" | "origin-when-cross-origin" | "same-origin" | "strict-origin" | "strict-origin-when-cross-origin" | "unsafe-url";
type RequestMode = "cors" | "navigate" | "no-cors" | "same-origin";
type RequestRedirect = "error" | "follow" | "manual";
type RequestDuplex = "half";
type ResponseType = "basic" | "cors" | "default" | "error" | "opaque" | "opaqueredirect";
interface Body {
    readonly body: ReadableStream<Uint8Array> | null;
    readonly bodyUsed: boolean;
    arrayBuffer(): Promise<ArrayBuffer>;
    blob(): Promise<Blob>;
    bytes(): Promise<Uint8Array<ArrayBuffer>>;
    formData(): Promise<FormData>;
    json(): Promise<any>;
    text(): Promise<string>;
}
interface Headers {
    append(name: string, value: string): void;
    delete(name: string): void;
    get(name: string): string | null;
    getSetCookie(): string[];
    has(name: string): boolean;
    set(name: string, value: string): void;
    forEach(callbackfn: (value: string, key: string, parent: Headers) => void, thisArg?: any): void;
}
declare var Headers: {
    prototype: Headers;
    new (init?: HeadersInit): Headers;
};
interface RequestInit {
    body?: BodyInit | null;
    cache?: RequestCache;
    credentials?: RequestCredentials;
    // Node's (undici's) streaming upload option.
    duplex?: RequestDuplex;
    headers?: HeadersInit;
    integrity?: string;
    keepalive?: boolean;
    method?: string;
    mode?: RequestMode;
    redirect?: RequestRedirect;
    referrer?: string;
    referrerPolicy?: ReferrerPolicy;
    signal?: AbortSignal | null;
    window?: null;
}
interface Request extends Body {
    readonly cache: RequestCache;
    readonly credentials: RequestCredentials;
    readonly destination: RequestDestination;
    readonly headers: Headers;
    readonly integrity: string;
    readonly keepalive: boolean;
    readonly method: string;
    readonly mode: RequestMode;
    readonly redirect: RequestRedirect;
    readonly referrer: string;
    readonly referrerPolicy: ReferrerPolicy;
    readonly signal: AbortSignal;
    readonly url: string;
    clone(): Request;
}
declare var Request: {
    prototype: Request;
    new (input: RequestInfo | URL, init?: RequestInit): Request;
};
interface ResponseInit {
    headers?: HeadersInit;
    status?: number;
    statusText?: string;
}
interface Response extends Body {
    readonly headers: Headers;
    readonly ok: boolean;
    readonly redirected: boolean;
    readonly status: number;
    readonly statusText: string;
    readonly type: ResponseType;
    readonly url: string;
    clone(): Response;
}
declare var Response: {
    prototype: Response;
    new (body?: BodyInit | null, init?: ResponseInit): Response;
    error(): Response;
    json(data: any, init?: ResponseInit): Response;
    redirect(url: string | URL, status?: number): Response;
};
declare function fetch(input: RequestInfo | URL, init?: RequestInit): Promise<Response>;

// TextEncoder and TextDecoder (TypeScript's lib.dom.d.ts).
type AllowSharedBufferSource = ArrayBufferLike | ArrayBufferView<ArrayBufferLike>;
type BufferSource = ArrayBufferView<ArrayBuffer> | ArrayBuffer;
interface TextDecoderCommon {
    readonly encoding: string;
    readonly fatal: boolean;
    readonly ignoreBOM: boolean;
}
interface TextDecoderOptions {
    fatal?: boolean;
    ignoreBOM?: boolean;
}
interface TextDecodeOptions {
    stream?: boolean;
}
interface TextDecoder extends TextDecoderCommon {
    decode(input?: AllowSharedBufferSource, options?: TextDecodeOptions): string;
}
declare var TextDecoder: {
    prototype: TextDecoder;
    new (label?: string, options?: TextDecoderOptions): TextDecoder;
};
interface TextEncoderCommon {
    readonly encoding: string;
}
interface TextEncoderEncodeIntoResult {
    read: number;
    written: number;
}
interface TextEncoder extends TextEncoderCommon {
    encode(input?: string): Uint8Array<ArrayBuffer>;
    encodeInto(source: string, destination: Uint8Array<ArrayBufferLike>): TextEncoderEncodeIntoResult;
}
declare var TextEncoder: {
    prototype: TextEncoder;
    new (): TextEncoder;
};

// EventTarget, Event, CustomEvent, AbortController and AbortSignal
// (TypeScript's lib.dom.d.ts).
type DOMHighResTimeStamp = number;
interface EventListener {
    (evt: Event): void;
}
interface EventListenerObject {
    handleEvent(object: Event): void;
}
type EventListenerOrEventListenerObject = EventListener | EventListenerObject;
interface EventListenerOptions {
    capture?: boolean;
}
interface AddEventListenerOptions extends EventListenerOptions {
    once?: boolean;
    passive?: boolean;
    signal?: AbortSignal;
}
interface EventInit {
    bubbles?: boolean;
    cancelable?: boolean;
    composed?: boolean;
}
interface Event {
    readonly bubbles: boolean;
    cancelBubble: boolean;
    readonly cancelable: boolean;
    readonly composed: boolean;
    readonly currentTarget: EventTarget | null;
    readonly defaultPrevented: boolean;
    readonly eventPhase: number;
    readonly isTrusted: boolean;
    returnValue: boolean;
    readonly srcElement: EventTarget | null;
    readonly target: EventTarget | null;
    readonly timeStamp: DOMHighResTimeStamp;
    readonly type: string;
    composedPath(): EventTarget[];
    initEvent(type: string, bubbles?: boolean, cancelable?: boolean): void;
    preventDefault(): void;
    stopImmediatePropagation(): void;
    stopPropagation(): void;
    readonly NONE: 0;
    readonly CAPTURING_PHASE: 1;
    readonly AT_TARGET: 2;
    readonly BUBBLING_PHASE: 3;
}
declare var Event: {
    prototype: Event;
    new (type: string, eventInitDict?: EventInit): Event;
    readonly NONE: 0;
    readonly CAPTURING_PHASE: 1;
    readonly AT_TARGET: 2;
    readonly BUBBLING_PHASE: 3;
};
interface EventTarget {
    addEventListener(type: string, callback: EventListenerOrEventListenerObject | null, options?: AddEventListenerOptions | boolean): void;
    dispatchEvent(event: Event): boolean;
    removeEventListener(type: string, callback: EventListenerOrEventListenerObject | null, options?: EventListenerOptions | boolean): void;
}
declare var EventTarget: {
    prototype: EventTarget;
    new (): EventTarget;
};
interface CustomEventInit<T = any> extends EventInit {
    detail?: T;
}
interface CustomEvent<T = any> extends Event {
    readonly detail: T;
    initCustomEvent(type: string, bubbles?: boolean, cancelable?: boolean, detail?: T): void;
}
declare var CustomEvent: {
    prototype: CustomEvent;
    new <T>(type: string, eventInitDict?: CustomEventInit<T>): CustomEvent<T>;
};
interface AbortController {
    readonly signal: AbortSignal;
    abort(reason?: any): void;
}
declare var AbortController: {
    prototype: AbortController;
    new (): AbortController;
};
interface AbortSignalEventMap {
    "abort": Event;
}
interface AbortSignal extends EventTarget {
    readonly aborted: boolean;
    onabort: ((this: AbortSignal, ev: Event) => any) | null;
    readonly reason: any;
    throwIfAborted(): void;
    addEventListener<K extends keyof AbortSignalEventMap>(type: K, listener: (this: AbortSignal, ev: AbortSignalEventMap[K]) => any, options?: boolean | AddEventListenerOptions): void;
    addEventListener(type: string, listener: EventListenerOrEventListenerObject, options?: boolean | AddEventListenerOptions): void;
    removeEventListener<K extends keyof AbortSignalEventMap>(type: K, listener: (this: AbortSignal, ev: AbortSignalEventMap[K]) => any, options?: boolean | EventListenerOptions): void;
    removeEventListener(type: string, listener: EventListenerOrEventListenerObject, options?: boolean | EventListenerOptions): void;
}
declare var AbortSignal: {
    prototype: AbortSignal;
    new (): AbortSignal;
    abort(reason?: any): AbortSignal;
    any(signals: AbortSignal[]): AbortSignal;
    timeout(milliseconds: number): AbortSignal;
};

// Node's `util` module: the code-generated part (@types/node's util.d.ts
// `format` and `inspect`); `promisify` is lib/node/internal_util.ts.
declare module "util" {
    export interface InspectOptions {
        showHidden?: boolean | undefined;
        depth?: number | null | undefined;
        colors?: boolean | undefined;
        customInspect?: boolean | undefined;
        showProxy?: boolean | undefined;
        maxArrayLength?: number | null | undefined;
        maxStringLength?: number | null | undefined;
        breakLength?: number | undefined;
        compact?: boolean | number | undefined;
        sorted?: boolean | ((a: string, b: string) => number) | undefined;
        getters?: "get" | "set" | boolean | undefined;
        numericSeparator?: boolean | undefined;
    }
    export function format(format?: any, ...param: any[]): string;
    export function inspect(object: any, showHidden?: boolean, depth?: number | null, color?: boolean): string;
    export function inspect(object: any, options?: InspectOptions): string;
}

