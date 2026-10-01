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
    createObjectURL(blob: Blob): string;
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
// implements with a plain type; the streams are not declared yet).
declare namespace NodeJS {
    type Signals =
        | "SIGABRT" | "SIGALRM" | "SIGBUS" | "SIGCHLD" | "SIGCONT" | "SIGFPE" | "SIGHUP" | "SIGILL" | "SIGINT" | "SIGIO"
        | "SIGIOT" | "SIGKILL" | "SIGPIPE" | "SIGPOLL" | "SIGPROF" | "SIGPWR" | "SIGQUIT" | "SIGSEGV" | "SIGSTKFLT"
        | "SIGSTOP" | "SIGSYS" | "SIGTERM" | "SIGTRAP" | "SIGTSTP" | "SIGTTIN" | "SIGTTOU" | "SIGUNUSED" | "SIGURG"
        | "SIGUSR1" | "SIGUSR2" | "SIGVTALRM" | "SIGWINCH" | "SIGXCPU" | "SIGXFSZ" | "SIGBREAK" | "SIGLOST" | "SIGINFO";
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
    type BeforeExitListener = (code: number) => void;
    type DisconnectListener = () => void;
    type ExitListener = (code: number) => void;
    type RejectionHandledListener = (promise: Promise<unknown>) => void;
    type UncaughtExceptionOrigin = "uncaughtException" | "unhandledRejection";
    type UncaughtExceptionListener = (error: Error, origin: UncaughtExceptionOrigin) => void;
    type UnhandledRejectionListener = (reason: unknown, promise: Promise<unknown>) => void;
    type WarningListener = (warning: Error) => void;
    type MessageListener = (message: unknown, sendHandle: unknown) => void;
    type SignalsListener = (signal: Signals) => void;
    interface EmitWarningOptions {
        type?: string | undefined;
        code?: string | undefined;
        ctor?: Function | undefined;
        detail?: string | undefined;
    }
    interface Control {
        ref(): void;
        unref(): void;
    }
    // Process is an EventEmitter; its members are declared here, as
    // @types/node's own overloads are.
    interface Process {
        addListener(event: "beforeExit", listener: BeforeExitListener): this;
        addListener(event: "disconnect", listener: DisconnectListener): this;
        addListener(event: "exit", listener: ExitListener): this;
        addListener(event: "rejectionHandled", listener: RejectionHandledListener): this;
        addListener(event: "uncaughtException", listener: UncaughtExceptionListener): this;
        addListener(event: "uncaughtExceptionMonitor", listener: UncaughtExceptionListener): this;
        addListener(event: "unhandledRejection", listener: UnhandledRejectionListener): this;
        addListener(event: "warning", listener: WarningListener): this;
        addListener(event: "message", listener: MessageListener): this;
        addListener(event: Signals, listener: SignalsListener): this;
        addListener(event: string | symbol, listener: (...args: any[]) => void): this;
        on(event: "beforeExit", listener: BeforeExitListener): this;
        on(event: "disconnect", listener: DisconnectListener): this;
        on(event: "exit", listener: ExitListener): this;
        on(event: "rejectionHandled", listener: RejectionHandledListener): this;
        on(event: "uncaughtException", listener: UncaughtExceptionListener): this;
        on(event: "uncaughtExceptionMonitor", listener: UncaughtExceptionListener): this;
        on(event: "unhandledRejection", listener: UnhandledRejectionListener): this;
        on(event: "warning", listener: WarningListener): this;
        on(event: "message", listener: MessageListener): this;
        on(event: Signals, listener: SignalsListener): this;
        on(event: string | symbol, listener: (...args: any[]) => void): this;
        once(event: "beforeExit", listener: BeforeExitListener): this;
        once(event: "disconnect", listener: DisconnectListener): this;
        once(event: "exit", listener: ExitListener): this;
        once(event: "rejectionHandled", listener: RejectionHandledListener): this;
        once(event: "uncaughtException", listener: UncaughtExceptionListener): this;
        once(event: "uncaughtExceptionMonitor", listener: UncaughtExceptionListener): this;
        once(event: "unhandledRejection", listener: UnhandledRejectionListener): this;
        once(event: "warning", listener: WarningListener): this;
        once(event: "message", listener: MessageListener): this;
        once(event: Signals, listener: SignalsListener): this;
        once(event: string | symbol, listener: (...args: any[]) => void): this;
        prependListener(event: "beforeExit", listener: BeforeExitListener): this;
        prependListener(event: "disconnect", listener: DisconnectListener): this;
        prependListener(event: "exit", listener: ExitListener): this;
        prependListener(event: "rejectionHandled", listener: RejectionHandledListener): this;
        prependListener(event: "uncaughtException", listener: UncaughtExceptionListener): this;
        prependListener(event: "uncaughtExceptionMonitor", listener: UncaughtExceptionListener): this;
        prependListener(event: "unhandledRejection", listener: UnhandledRejectionListener): this;
        prependListener(event: "warning", listener: WarningListener): this;
        prependListener(event: "message", listener: MessageListener): this;
        prependListener(event: Signals, listener: SignalsListener): this;
        prependListener(event: string | symbol, listener: (...args: any[]) => void): this;
        prependOnceListener(event: "beforeExit", listener: BeforeExitListener): this;
        prependOnceListener(event: "disconnect", listener: DisconnectListener): this;
        prependOnceListener(event: "exit", listener: ExitListener): this;
        prependOnceListener(event: "rejectionHandled", listener: RejectionHandledListener): this;
        prependOnceListener(event: "uncaughtException", listener: UncaughtExceptionListener): this;
        prependOnceListener(event: "uncaughtExceptionMonitor", listener: UncaughtExceptionListener): this;
        prependOnceListener(event: "unhandledRejection", listener: UnhandledRejectionListener): this;
        prependOnceListener(event: "warning", listener: WarningListener): this;
        prependOnceListener(event: "message", listener: MessageListener): this;
        prependOnceListener(event: Signals, listener: SignalsListener): this;
        prependOnceListener(event: string | symbol, listener: (...args: any[]) => void): this;
        emit(event: "beforeExit", code: number): boolean;
        emit(event: "disconnect"): boolean;
        emit(event: "exit", code: number): boolean;
        emit(event: "rejectionHandled", promise: Promise<unknown>): boolean;
        emit(event: "uncaughtException", error: Error): boolean;
        emit(event: "uncaughtExceptionMonitor", error: Error): boolean;
        emit(event: "unhandledRejection", reason: unknown, promise: Promise<unknown>): boolean;
        emit(event: "warning", warning: Error): boolean;
        emit(event: "message", message: unknown, sendHandle: unknown): this;
        emit(event: Signals, signal?: Signals): boolean;
        emit(event: string | symbol, ...args: any[]): boolean;
        off(event: string | symbol, listener: (...args: any[]) => void): this;
        removeListener(event: string | symbol, listener: (...args: any[]) => void): this;
        removeAllListeners(eventName?: string | symbol): this;
        setMaxListeners(n: number): this;
        getMaxListeners(): number;
        listeners(eventName: string | symbol): Function[];
        rawListeners(eventName: string | symbol): Function[];
        listenerCount(eventName: string | symbol, listener?: Function): number;
        eventNames(): Array<string | symbol>;
        emitWarning(warning: string | Error, ctor?: Function): void;
        emitWarning(warning: string | Error, type?: string, ctor?: Function): void;
        emitWarning(warning: string | Error, type?: string, code?: string, ctor?: Function): void;
        emitWarning(warning: string | Error, options?: EmitWarningOptions): void;
        send?(message: any, sendHandle?: any, options?: { keepOpen?: boolean | undefined }, callback?: (error: Error | null) => void): boolean;
        disconnect?(): void;
        connected: boolean;
        channel?: Control;
        argv: string[];
        argv0: string;
        execArgv: string[];
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

// Node's `url` module, its code-generated part (@types/node's url.d.ts);
// the legacy API is lib/node/internal_url.ts.
declare module "url" {
    interface FileUrlToPathOptions {
        windows?: boolean | undefined;
    }
    interface PathToFileUrlOptions {
        windows?: boolean | undefined;
    }
    interface HttpOptions {
        protocol: string;
        hostname: string;
        hash: string;
        search: string;
        pathname: string;
        path: string;
        href: string;
        port?: number;
        auth?: string;
    }
    export function fileURLToPath(url: string | URL, options?: FileUrlToPathOptions): string;
    export function pathToFileURL(path: string, options?: PathToFileUrlOptions): URL;
    export function urlToHttpOptions(url: URL): HttpOptions;
    export function domainToASCII(domain: string): string;
    export function domainToUnicode(domain: string): string;
    export { URL, URLSearchParams };
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
/** @intrinsic btoa */
declare function btoa(data: string): string;
/** @intrinsic atob */
declare function atob(data: string): string;

// The WHATWG streams (@types/node's stream/web.d.ts globals, TypeScript's
// lib.dom.d.ts shapes): ReadableStream, WritableStream, TransformStream,
// their readers, writers and controllers, the queuing strategies and
// CompressionStream/DecompressionStream. Byte streams (BYOB readers,
// ReadableByteStreamController) are not implemented, so not declared.
interface QueuingStrategySize<T = any> {
    (chunk: T): number;
}
interface QueuingStrategy<T = any> {
    highWaterMark?: number;
    size?: QueuingStrategySize<T>;
}
interface QueuingStrategyInit {
    highWaterMark: number;
}
interface ByteLengthQueuingStrategy extends QueuingStrategy<ArrayBufferView> {
    readonly highWaterMark: number;
    readonly size: QueuingStrategySize<ArrayBufferView>;
}
declare var ByteLengthQueuingStrategy: {
    prototype: ByteLengthQueuingStrategy;
    new (init: QueuingStrategyInit): ByteLengthQueuingStrategy;
};
interface CountQueuingStrategy extends QueuingStrategy {
    readonly highWaterMark: number;
    readonly size: QueuingStrategySize;
}
declare var CountQueuingStrategy: {
    prototype: CountQueuingStrategy;
    new (init: QueuingStrategyInit): CountQueuingStrategy;
};
type ReadableStreamType = "bytes";
type ReadableStreamController<T> = ReadableStreamDefaultController<T>;
type ReadableStreamReader<T> = ReadableStreamDefaultReader<T>;
interface UnderlyingSourceCancelCallback {
    (reason?: any): void | PromiseLike<void>;
}
interface UnderlyingSourcePullCallback<R> {
    (controller: ReadableStreamController<R>): void | PromiseLike<void>;
}
interface UnderlyingSourceStartCallback<R> {
    (controller: ReadableStreamController<R>): any;
}
interface UnderlyingDefaultSource<R = any> {
    cancel?: UnderlyingSourceCancelCallback;
    pull?: (controller: ReadableStreamDefaultController<R>) => void | PromiseLike<void>;
    start?: (controller: ReadableStreamDefaultController<R>) => any;
    type?: undefined;
}
interface UnderlyingSource<R = any> {
    autoAllocateChunkSize?: number;
    cancel?: UnderlyingSourceCancelCallback;
    pull?: UnderlyingSourcePullCallback<R>;
    start?: UnderlyingSourceStartCallback<R>;
    type?: ReadableStreamType;
}
interface UnderlyingSinkAbortCallback {
    (reason?: any): void | PromiseLike<void>;
}
interface UnderlyingSinkCloseCallback {
    (): void | PromiseLike<void>;
}
interface UnderlyingSinkStartCallback {
    (controller: WritableStreamDefaultController): any;
}
interface UnderlyingSinkWriteCallback<W> {
    (chunk: W, controller: WritableStreamDefaultController): void | PromiseLike<void>;
}
interface UnderlyingSink<W = any> {
    abort?: UnderlyingSinkAbortCallback;
    close?: UnderlyingSinkCloseCallback;
    start?: UnderlyingSinkStartCallback;
    type?: undefined;
    write?: UnderlyingSinkWriteCallback<W>;
}
interface TransformerFlushCallback<O> {
    (controller: TransformStreamDefaultController<O>): void | PromiseLike<void>;
}
interface TransformerStartCallback<O> {
    (controller: TransformStreamDefaultController<O>): any;
}
interface TransformerTransformCallback<I, O> {
    (chunk: I, controller: TransformStreamDefaultController<O>): void | PromiseLike<void>;
}
interface Transformer<I = any, O = any> {
    flush?: TransformerFlushCallback<O>;
    readableType?: undefined;
    start?: TransformerStartCallback<O>;
    transform?: TransformerTransformCallback<I, O>;
    writableType?: undefined;
}
interface StreamPipeOptions {
    preventAbort?: boolean;
    preventCancel?: boolean;
    preventClose?: boolean;
    signal?: AbortSignal;
}
interface ReadableWritablePair<R = any, W = any> {
    readable: ReadableStream<R>;
    writable: WritableStream<W>;
}
interface ReadableStreamIteratorOptions {
    preventCancel?: boolean;
}
interface ReadableStreamReadDoneResult<T> {
    done: true;
    value: T | undefined;
}
interface ReadableStreamReadValueResult<T> {
    done: false;
    value: T;
}
type ReadableStreamReadResult<T> = ReadableStreamReadValueResult<T> | ReadableStreamReadDoneResult<T>;
interface ReadableStreamAsyncIterator<T> extends AsyncIterableIterator<T> {
    [Symbol.asyncIterator](): ReadableStreamAsyncIterator<T>;
}
interface ReadableStream<R = any> {
    readonly locked: boolean;
    cancel(reason?: any): Promise<void>;
    getReader(): ReadableStreamDefaultReader<R>;
    pipeThrough<T>(transform: ReadableWritablePair<T, R>, options?: StreamPipeOptions): ReadableStream<T>;
    pipeTo(destination: WritableStream<R>, options?: StreamPipeOptions): Promise<void>;
    tee(): [ReadableStream<R>, ReadableStream<R>];
    values(options?: ReadableStreamIteratorOptions): ReadableStreamAsyncIterator<R>;
    [Symbol.asyncIterator](options?: ReadableStreamIteratorOptions): ReadableStreamAsyncIterator<R>;
}
declare var ReadableStream: {
    prototype: ReadableStream;
    from<T>(iterable: Iterable<T> | AsyncIterable<T>): ReadableStream<T>;
    new <R = any>(underlyingSource: UnderlyingDefaultSource<R>, strategy?: QueuingStrategy<R>): ReadableStream<R>;
    new <R = any>(underlyingSource?: UnderlyingSource<R>, strategy?: QueuingStrategy<R>): ReadableStream<R>;
};
interface ReadableStreamGenericReader {
    readonly closed: Promise<void>;
    cancel(reason?: any): Promise<void>;
}
interface ReadableStreamDefaultReader<R = any> extends ReadableStreamGenericReader {
    read(): Promise<ReadableStreamReadResult<R>>;
    releaseLock(): void;
}
declare var ReadableStreamDefaultReader: {
    prototype: ReadableStreamDefaultReader;
    new <R = any>(stream: ReadableStream<R>): ReadableStreamDefaultReader<R>;
};
interface ReadableStreamDefaultController<R = any> {
    readonly desiredSize: number | null;
    close(): void;
    enqueue(chunk: R): void;
    error(e?: any): void;
}
declare var ReadableStreamDefaultController: {
    prototype: ReadableStreamDefaultController;
    new (): ReadableStreamDefaultController;
};
interface WritableStream<W = any> {
    readonly locked: boolean;
    abort(reason?: any): Promise<void>;
    close(): Promise<void>;
    getWriter(): WritableStreamDefaultWriter<W>;
}
declare var WritableStream: {
    prototype: WritableStream;
    new <W = any>(underlyingSink?: UnderlyingSink<W>, strategy?: QueuingStrategy<W>): WritableStream<W>;
};
interface WritableStreamDefaultWriter<W = any> {
    readonly closed: Promise<void>;
    readonly desiredSize: number | null;
    readonly ready: Promise<void>;
    abort(reason?: any): Promise<void>;
    close(): Promise<void>;
    releaseLock(): void;
    write(chunk?: W): Promise<void>;
}
declare var WritableStreamDefaultWriter: {
    prototype: WritableStreamDefaultWriter;
    new <W = any>(stream: WritableStream<W>): WritableStreamDefaultWriter<W>;
};
interface WritableStreamDefaultController {
    readonly signal: AbortSignal;
    error(e?: any): void;
}
declare var WritableStreamDefaultController: {
    prototype: WritableStreamDefaultController;
    new (): WritableStreamDefaultController;
};
interface TransformStream<I = any, O = any> {
    readonly readable: ReadableStream<O>;
    readonly writable: WritableStream<I>;
}
declare var TransformStream: {
    prototype: TransformStream;
    new <I = any, O = any>(transformer?: Transformer<I, O>, writableStrategy?: QueuingStrategy<I>, readableStrategy?: QueuingStrategy<O>): TransformStream<I, O>;
};
interface TransformStreamDefaultController<O = any> {
    readonly desiredSize: number | null;
    enqueue(chunk?: O): void;
    error(reason?: any): void;
    terminate(): void;
}
declare var TransformStreamDefaultController: {
    prototype: TransformStreamDefaultController;
    new (): TransformStreamDefaultController;
};
interface GenericTransformStream {
    readonly readable: ReadableStream;
    readonly writable: WritableStream;
}
type CompressionFormat = "deflate" | "deflate-raw" | "gzip";
interface CompressionStream extends GenericTransformStream {
    readonly readable: ReadableStream<Uint8Array<ArrayBuffer>>;
    readonly writable: WritableStream<BufferSource>;
}
declare var CompressionStream: {
    prototype: CompressionStream;
    new (format: CompressionFormat): CompressionStream;
};
interface DecompressionStream extends GenericTransformStream {
    readonly readable: ReadableStream<Uint8Array<ArrayBuffer>>;
    readonly writable: WritableStream<BufferSource>;
}
declare var DecompressionStream: {
    prototype: DecompressionStream;
    new (format: CompressionFormat): DecompressionStream;
};

// Blob (TypeScript's lib.dom.d.ts). The `endings` option is not
// implemented, so not declared.
type BlobPart = BufferSource | Blob | string;
interface BlobPropertyBag {
    type?: string;
}
interface Blob {
    readonly size: number;
    readonly type: string;
    arrayBuffer(): Promise<ArrayBuffer>;
    bytes(): Promise<Uint8Array<ArrayBuffer>>;
    slice(start?: number, end?: number, contentType?: string): Blob;
    stream(): ReadableStream<Uint8Array<ArrayBuffer>>;
    text(): Promise<string>;
}
declare var Blob: {
    prototype: Blob;
    new (blobParts?: BlobPart[], options?: BlobPropertyBag): Blob;
};

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

// performance and the user-timing entry classes (@types/node's
// perf_hooks globals).
interface PerformanceEntry {
    readonly duration: number;
    readonly entryType: string;
    readonly name: string;
    readonly startTime: number;
    toJSON(): any;
}
declare var PerformanceEntry: {
    prototype: PerformanceEntry;
    new (): PerformanceEntry;
};
interface PerformanceMark extends PerformanceEntry {
    readonly detail: any;
}
declare var PerformanceMark: {
    prototype: PerformanceMark;
    new (name: string, options?: PerformanceMarkOptions): PerformanceMark;
};
interface PerformanceMeasure extends PerformanceEntry {
    readonly detail: any;
}
declare var PerformanceMeasure: {
    prototype: PerformanceMeasure;
    new (): PerformanceMeasure;
};
interface PerformanceMarkOptions {
    detail?: unknown | undefined;
    startTime?: number | undefined;
}
interface PerformanceMeasureOptions {
    detail?: unknown | undefined;
    duration?: number | undefined;
    end?: number | string | undefined;
    start?: number | string | undefined;
}
interface Performance {
    readonly timeOrigin: number;
    clearMarks(name?: string): void;
    clearMeasures(name?: string): void;
    clearResourceTimings(): void;
    getEntries(): PerformanceEntry[];
    getEntriesByName(name: string, type?: string): PerformanceEntry[];
    getEntriesByType(type: string): PerformanceEntry[];
    mark(name: string, options?: PerformanceMarkOptions): PerformanceMark;
    measure(name: string, startMark?: string, endMark?: string): PerformanceMeasure;
    measure(name: string, options: PerformanceMeasureOptions): PerformanceMeasure;
    now(): number;
    timerify<T extends (...params: any[]) => any>(fn: T, options?: { histogram?: any }): T;
    toJSON(): any;
}
declare var Performance: {
    prototype: Performance;
    new (): Performance;
};
declare var performance: Performance;
interface PerformanceObserverEntryList {
    getEntries(): PerformanceEntry[];
    getEntriesByName(name: string, type?: string): PerformanceEntry[];
    getEntriesByType(type: string): PerformanceEntry[];
}
declare var PerformanceObserverEntryList: {
    prototype: PerformanceObserverEntryList;
    new (): PerformanceObserverEntryList;
};
type PerformanceObserverCallback = (list: PerformanceObserverEntryList, observer: PerformanceObserver) => void;
interface PerformanceObserverInit {
    entryTypes?: ReadonlyArray<string> | undefined;
    type?: string | undefined;
    buffered?: boolean | undefined;
}
interface PerformanceObserver {
    disconnect(): void;
    observe(options?: PerformanceObserverInit): void;
    takeRecords(): PerformanceEntry[];
}
declare var PerformanceObserver: {
    prototype: PerformanceObserver;
    new (callback: PerformanceObserverCallback): PerformanceObserver;
    readonly supportedstrings: ReadonlyArray<string>;
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

// MessageEvent, CloseEvent, ErrorEvent, WebSocket and EventSource
// (TypeScript's lib.dom.d.ts; Node's are undici's: lib/node/kml_event_target.ts,
// kml_websocket.ts and kml_eventsource.ts implement them). A message's
// ports and source are `any`: MessagePort is not declared yet.
interface MessageEventInit<T = any> extends EventInit {
    data?: T;
    lastEventId?: string;
    origin?: string;
    ports?: any[];
    source?: any;
}
interface MessageEvent<T = any> extends Event {
    readonly data: T;
    readonly lastEventId: string;
    readonly origin: string;
    readonly ports: ReadonlyArray<any>;
    readonly source: any;
}
declare var MessageEvent: {
    prototype: MessageEvent;
    new <T>(type: string, eventInitDict?: MessageEventInit<T>): MessageEvent<T>;
};
interface CloseEventInit extends EventInit {
    code?: number;
    reason?: string;
    wasClean?: boolean;
}
interface CloseEvent extends Event {
    readonly code: number;
    readonly reason: string;
    readonly wasClean: boolean;
}
declare var CloseEvent: {
    prototype: CloseEvent;
    new (type: string, eventInitDict?: CloseEventInit): CloseEvent;
};
interface ErrorEventInit extends EventInit {
    colno?: number;
    error?: any;
    filename?: string;
    lineno?: number;
    message?: string;
}
interface ErrorEvent extends Event {
    readonly colno: number;
    readonly error: any;
    readonly filename: string;
    readonly lineno: number;
    readonly message: string;
}
declare var ErrorEvent: {
    prototype: ErrorEvent;
    new (type: string, eventInitDict?: ErrorEventInit): ErrorEvent;
};
type BinaryType = "arraybuffer" | "blob";
interface WebSocketEventMap {
    "close": CloseEvent;
    "error": Event;
    "message": MessageEvent;
    "open": Event;
}
interface WebSocket extends EventTarget {
    binaryType: BinaryType;
    readonly bufferedAmount: number;
    readonly extensions: string;
    onclose: ((this: WebSocket, ev: CloseEvent) => any) | null;
    onerror: ((this: WebSocket, ev: Event) => any) | null;
    onmessage: ((this: WebSocket, ev: MessageEvent) => any) | null;
    onopen: ((this: WebSocket, ev: Event) => any) | null;
    readonly protocol: string;
    readonly readyState: 0 | 1 | 2 | 3;
    readonly url: string;
    close(code?: number, reason?: string): void;
    send(data: BufferSource | Blob | string): void;
    readonly CONNECTING: 0;
    readonly OPEN: 1;
    readonly CLOSING: 2;
    readonly CLOSED: 3;
    addEventListener<K extends keyof WebSocketEventMap>(type: K, listener: (this: WebSocket, ev: WebSocketEventMap[K]) => any, options?: boolean | AddEventListenerOptions): void;
    addEventListener(type: string, listener: EventListenerOrEventListenerObject, options?: boolean | AddEventListenerOptions): void;
    removeEventListener<K extends keyof WebSocketEventMap>(type: K, listener: (this: WebSocket, ev: WebSocketEventMap[K]) => any, options?: boolean | EventListenerOptions): void;
    removeEventListener(type: string, listener: EventListenerOrEventListenerObject, options?: boolean | EventListenerOptions): void;
}
declare var WebSocket: {
    prototype: WebSocket;
    new (url: string | URL, protocols?: string | string[]): WebSocket;
    readonly CONNECTING: 0;
    readonly OPEN: 1;
    readonly CLOSING: 2;
    readonly CLOSED: 3;
};
interface EventSourceInit {
    withCredentials?: boolean;
}
interface EventSourceEventMap {
    "error": Event;
    "message": MessageEvent;
    "open": Event;
}
interface EventSource extends EventTarget {
    onerror: ((this: EventSource, ev: Event) => any) | null;
    onmessage: ((this: EventSource, ev: MessageEvent) => any) | null;
    onopen: ((this: EventSource, ev: Event) => any) | null;
    readonly readyState: number;
    readonly url: string;
    readonly withCredentials: boolean;
    close(): void;
    readonly CONNECTING: 0;
    readonly OPEN: 1;
    readonly CLOSED: 2;
    addEventListener<K extends keyof EventSourceEventMap>(type: K, listener: (this: EventSource, ev: EventSourceEventMap[K]) => any, options?: boolean | AddEventListenerOptions): void;
    addEventListener(type: string, listener: (this: EventSource, event: MessageEvent) => any, options?: boolean | AddEventListenerOptions): void;
    addEventListener(type: string, listener: EventListenerOrEventListenerObject, options?: boolean | AddEventListenerOptions): void;
    removeEventListener<K extends keyof EventSourceEventMap>(type: K, listener: (this: EventSource, ev: EventSourceEventMap[K]) => any, options?: boolean | EventListenerOptions): void;
    removeEventListener(type: string, listener: (this: EventSource, event: MessageEvent) => any, options?: boolean | EventListenerOptions): void;
    removeEventListener(type: string, listener: EventListenerOrEventListenerObject, options?: boolean | EventListenerOptions): void;
}
declare var EventSource: {
    prototype: EventSource;
    new (url: string | URL, eventSourceInitDict?: EventSourceInit): EventSource;
    readonly CONNECTING: 0;
    readonly OPEN: 1;
    readonly CLOSED: 2;
};

// Node's `util` module: the code-generated part (@types/node's util.d.ts
// `format` and `inspect`); `promisify` is lib/node/internal_util.ts.
