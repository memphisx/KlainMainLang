// kml:global
// Node's `worker_threads`, ported from Node v24's lib/worker_threads.js,
// lib/internal/worker.js, lib/internal/worker/io.js and
// lib/internal/worker/messaging.js: Worker, MessageChannel, MessagePort,
// BroadcastChannel, and a worker's parentPort, workerData and threadId.
//
// A Worker is a thread of this program that evaluates its own modules
// afresh — every builtin module, the modules its file imports, then the file
// — with module state of its own, as a Node worker's isolate does. The
// worker's file is compiled in: `new Worker(path)` names one of the program's
// worker modules (a path literal the compiler resolved, relative to the file
// it is written in). A message is the structured clone of the value, handed
// to the receiving thread: a SharedArrayBuffer (and a view over one) shares
// its memory, a MessagePort in the transfer list moves to the receiver.
//
// Also the web's Worker shape (`onmessage`, `onerror`) and a worker's
// `self` (`self.onmessage`, `self.postMessage`).
//
// kml:default-namespace — `import wt from 'worker_threads'` reads this
// module's exports, as Node's default export carries them.
import { EventEmitter } from 'events';
import { structuredCloneAny } from './internal_structured_clone';

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

function dataCloneError(message: string): Error {
    return new DOMException(message, 'DataCloneError');
}

// ---- messages: structured clone, with ports moved and shared memory kept ----

// A transferred port in flight: the receiver rebuilds a MessagePort on it.
class PortToken {
    id: number;
    constructor(id: number) {
        this.id = id;
    }
}

const untransferable = new WeakSet<any>();

// A view over a SharedArrayBuffer, rebuilt over the same memory.
function shareView(value: any): any {
    const buf = value.buffer as SharedArrayBuffer;
    const off: number = value.byteOffset;
    const n: number = value.length;
    if (value instanceof Uint8Array) return new Uint8Array(buf, off, n);
    if (value instanceof Int8Array) return new Int8Array(buf, off, n);
    if (value instanceof Uint8ClampedArray) return new Uint8ClampedArray(buf, off, n);
    if (value instanceof Int16Array) return new Int16Array(buf, off, n);
    if (value instanceof Uint16Array) return new Uint16Array(buf, off, n);
    if (value instanceof Int32Array) return new Int32Array(buf, off, n);
    if (value instanceof Uint32Array) return new Uint32Array(buf, off, n);
    if (value instanceof Float32Array) return new Float32Array(buf, off, n);
    if (value instanceof Float64Array) return new Float64Array(buf, off, n);
    if (value instanceof BigInt64Array) return new BigInt64Array(buf, off, n);
    if (value instanceof BigUint64Array) return new BigUint64Array(buf, off, n);
    return new DataView(buf, off, value.byteLength);
}

function isSharedView(value: any): boolean {
    return ArrayBuffer.isView(value) && (value as any).buffer instanceof SharedArrayBuffer;
}

// The value as it crosses: ports replaced by tokens (each must be in the
// transfer list), shared memory kept, everything else cloned.
function serialize(value: any, transfer: any[]): any {
    const ports: MessagePort[] = [];
    for (const t of transfer) {
        if (t instanceof MessagePort) {
            if (untransferable.has(t)) throw dataCloneError('An ArrayBuffer is marked as untransferable.');
            if (t._kmlId < 0) throw dataCloneError('MessagePort in transfer list is already detached');
            ports.push(t as MessagePort);
        } else if (!(t instanceof ArrayBuffer) && !(t instanceof SharedArrayBuffer)) {
            throw new NodeTypeError('ERR_INVALID_ARG_TYPE', 'Found invalid value in transferList.');
        }
    }
    const copy = cloneForTransfer(value, ports, new Map<any, any>());
    for (const p of ports) p._kmlDetach();
    return copy;
}

function cloneForTransfer(value: any, ports: MessagePort[], tokens: Map<any, any>): any {
    const memory = new Map<any, any>();
    const clone = (v: any): any => {
        if (typeof v === 'function' || typeof v === 'symbol') throw dataCloneError(String(v) + ' could not be cloned.');
        if (v === null || typeof v !== 'object') return v;
        const seen = memory.get(v);
        if (seen !== undefined) return seen;
        if (v instanceof MessagePort) {
            let tok = tokens.get(v);
            if (tok === undefined) {
                if (ports.indexOf(v as MessagePort) < 0) {
                    throw dataCloneError('Object that needs transfer was found in message but not listed in transferList');
                }
                tok = new PortToken((v as MessagePort)._kmlId);
                tokens.set(v, tok);
            }
            memory.set(v, tok);
            return tok;
        }
        if (v instanceof SharedArrayBuffer) return v;
        if (isSharedView(v)) {
            const view = shareView(v);
            memory.set(v, view);
            return view;
        }
        if (v instanceof Map) {
            const copy = new Map<any, any>();
            memory.set(v, copy);
            v.forEach((x: any, k: any) => { copy.set(clone(k), clone(x)); });
            return copy;
        }
        if (v instanceof Set) {
            const copy = new Set<any>();
            memory.set(v, copy);
            v.forEach((x: any) => { copy.add(clone(x)); });
            return copy;
        }
        if (Array.isArray(v)) {
            const src: any = v;
            const elements = new Array<any>(src.length);
            const copy: any = elements;
            memory.set(v, copy);
            for (const key of Object.keys(src)) copy[key] = clone(src[key]);
            return copy;
        }
        if (v instanceof Date || v instanceof RegExp || v instanceof Error || v instanceof ArrayBuffer || ArrayBuffer.isView(v)) {
            const copy = structuredCloneAny(v);
            memory.set(v, copy);
            return copy;
        }
        const copy: any = {};
        memory.set(v, copy);
        for (const key of Object.keys(v)) copy[key] = clone(v[key]);
        return copy;
    };
    return clone(value);
}

// The received value with each port token made a MessagePort of this thread.
function revive(value: any): any {
    if (value === null || typeof value !== 'object') return value;
    if (value instanceof PortToken) return new MessagePort((value as PortToken).id);
    const seen = new Set<any>();
    const walk = (v: any): any => {
        if (v === null || typeof v !== 'object') return v;
        if (v instanceof PortToken) return new MessagePort((v as PortToken).id);
        if (seen.has(v) || v instanceof SharedArrayBuffer || ArrayBuffer.isView(v) || v instanceof ArrayBuffer) return v;
        seen.add(v);
        if (v instanceof Map) {
            const entries: any[] = [];
            v.forEach((x: any, k: any) => { entries.push([k, x]); });
            for (const e of entries) v.set(walk(e[0]), walk(e[1]));
            return v;
        }
        if (v instanceof Set) return v;
        if (Array.isArray(v)) {
            const a: any = v;
            for (let i = 0; i < a.length; i++) a[i] = walk(a[i]);
            return v;
        }
        for (const key of Object.keys(v)) v[key] = walk(v[key]);
        return v;
    };
    return walk(value);
}

function transferListOf(options: any): any[] {
    if (options === undefined || options === null) return [];
    if (Array.isArray(options)) return options;
    if (typeof options === 'object' && options.transfer !== undefined) {
        if (!Array.isArray(options.transfer)) {
            throw new NodeTypeError('ERR_INVALID_ARG_TYPE', 'The "options.transfer" property must be of type Array.');
        }
        return options.transfer;
    }
    return [];
}

// The web's EventTarget surface over an EventEmitter: a listener receives a
// MessageEvent for 'message'/'messageerror', else an Event.
function addWebListener(target: EventEmitter, type: string, listener: (e: any) => void, options?: any): void {
    const once = typeof options === 'object' && options !== null && options.once === true;
    const wrapped = (value: any) => {
        if (type === 'message' || type === 'messageerror') listener(new MessageEvent(type, { data: value }));
        else listener(new Event(type));
    };
    (wrapped as any)._kmlListener = listener;
    if (once) target.once(type, wrapped);
    else target.on(type, wrapped);
}

function removeWebListener(target: EventEmitter, type: string, listener: (e: any) => void): void {
    for (const l of target.rawListeners(type)) {
        const f: any = l;
        if (f._kmlListener === listener || (f.listener !== undefined && f.listener._kmlListener === listener)) {
            target.removeListener(type, f);
            return;
        }
    }
}

// ---- MessagePort ------------------------------------------------------------

// One end of a MessageChannel (Node's MessagePort, a NodeEventTarget): a
// 'message' listener starts it, and a started, referenced port holds the
// loop open until it closes.
export class MessagePort extends EventEmitter {
    _kmlId: number;
    _kmlStarted = false;
    _kmlRefd = true;
    _kmlClosed = false;
    private _kmlOnMessage: ((e: any) => void) | null = null;
    private _kmlOnMessageError: ((e: any) => void) | null = null;

    constructor(id: number) {
        super();
        this._kmlId = id;
        this.on('newListener', (name: any) => {
            if (name === 'message') this.start();
        });
        this.on('removeListener', (name: any) => {
            if (name === 'message' && this.listenerCount('message') === 0 && this._kmlStarted) this._kmlStop();
        });
    }

    postMessage(value: any, transferList?: any): void {
        if (this._kmlId < 0 || this._kmlClosed) return;
        const transfer = transferListOf(transferList);
        for (const t of transfer) {
            if (t === this) throw dataCloneError('Transfer list contains source port');
        }
        const copy = serialize(value, transfer);
        __kml_native.mportPost(this._kmlId, copy);
    }

    start(): void {
        if (this._kmlStarted || this._kmlId < 0) return;
        this._kmlStarted = true;
        __kml_native.mportStart(this._kmlId, (kind: number, unused: number) => {
            this._kmlDrain();
            if (kind === 1) this._kmlOnClose();
        });
        if (!this._kmlRefd) __kml_native.mportRef(this._kmlId, false);
    }

    _kmlStop(): void {
        this._kmlStarted = false;
        if (this._kmlId >= 0) __kml_native.mportStop(this._kmlId);
    }

    _kmlDrain(): void {
        while (this._kmlId >= 0 && __kml_native.mportHas(this._kmlId)) {
            const value = revive(__kml_native.mportTake(this._kmlId));
            this.emit('message', value);
        }
    }

    _kmlOnClose(): void {
        if (this._kmlClosed) return;
        this._kmlClosed = true;
        this._kmlStarted = false;
        this.emit('close');
    }

    // A port moved to another thread: this object no longer is its end.
    _kmlDetach(): void {
        if (this._kmlId < 0) return;
        __kml_native.mportStop(this._kmlId);
        this._kmlId = -1;
        this._kmlStarted = false;
    }

    close(): void {
        if (this._kmlId < 0 || this._kmlClosed) return;
        if (!this._kmlStarted) {
            // Its 'close' still arrives on the loop.
            this._kmlStarted = true;
            __kml_native.mportStart(this._kmlId, (kind: number, unused: number) => {
                if (kind === 1) this._kmlOnClose();
            });
        }
        __kml_native.mportClose(this._kmlId);
    }

    ref(): void {
        this._kmlRefd = true;
        if (this._kmlId >= 0) __kml_native.mportRef(this._kmlId, true);
    }

    unref(): void {
        this._kmlRefd = false;
        if (this._kmlId >= 0) __kml_native.mportRef(this._kmlId, false);
    }

    hasRef(): boolean {
        return this._kmlRefd && this._kmlStarted && !this._kmlClosed;
    }

    // The web's EventTarget surface: listeners receive a MessageEvent.
    addEventListener(type: string, listener: (e: any) => void, options?: any): void {
        addWebListener(this, type, listener, options);
    }

    removeEventListener(type: string, listener: (e: any) => void): void {
        removeWebListener(this, type, listener);
    }

    get onmessage(): ((e: any) => void) | null {
        return this._kmlOnMessage;
    }

    set onmessage(fn: ((e: any) => void) | null) {
        if (this._kmlOnMessage !== null) this.removeEventListener('message', this._kmlOnMessage);
        this._kmlOnMessage = fn;
        if (fn !== null) this.addEventListener('message', fn);
    }

    get onmessageerror(): ((e: any) => void) | null {
        return this._kmlOnMessageError;
    }

    set onmessageerror(fn: ((e: any) => void) | null) {
        if (this._kmlOnMessageError !== null) this.removeEventListener('messageerror', this._kmlOnMessageError);
        this._kmlOnMessageError = fn;
        if (fn !== null) this.addEventListener('messageerror', fn);
    }
}

export class MessageChannel {
    readonly port1: MessagePort;
    readonly port2: MessagePort;
    constructor() {
        const id = __kml_native.mportPair();
        this.port1 = new MessagePort(id);
        this.port2 = new MessagePort(id + 1);
    }
}

// The oldest message waiting on port, taken without its 'message' event.
export function receiveMessageOnPort(port: MessagePort): { message: any } | undefined {
    if (!(port instanceof MessagePort)) {
        throw new NodeTypeError('ERR_INVALID_ARG_TYPE', 'The "port" argument must be a MessagePort instance');
    }
    if (port._kmlId < 0 || !__kml_native.mportHas(port._kmlId)) return undefined;
    return { message: revive(__kml_native.mportTake(port._kmlId)) };
}

export function markAsUntransferable(object: any): void {
    if ((typeof object !== 'object' && typeof object !== 'function') || object === null) return;
    untransferable.add(object);
}

export function isMarkedAsUntransferable(object: any): boolean {
    if (object === null || object === undefined) return false;
    return untransferable.has(object);
}

export function markAsUncloneable(object: any): void {
    if ((typeof object !== 'object' && typeof object !== 'function') || object === null) return;
    untransferable.add(object);
}

// ---- this thread ------------------------------------------------------------

export const isMainThread: boolean = __kml_native.workerSelf(0) === 0;
export const threadId: number = __kml_native.workerThreadId(-1);
export const isInternalThread = false;
export const resourceLimits: any = {};
export const SHARE_ENV: symbol = Symbol.for('nodejs.worker_threads.SHARE_ENV');

const selfData: any = isMainThread ? undefined : __kml_native.workerSelfData();
export const workerData: any = isMainThread ? null : revive(selfData.data);
const environmentData = new Map<any, any>();
if (!isMainThread && selfData.env !== undefined) {
    const envEntries: any[] = selfData.env;
    for (const e of envEntries) environmentData.set(e[0], e[1]);
}

export function setEnvironmentData(key: any, value: any): void {
    if (value === undefined) environmentData.delete(key);
    else environmentData.set(key, value);
}

export function getEnvironmentData(key: any): any {
    return environmentData.get(key);
}

export const parentPort: MessagePort | null = isMainThread ? null : new MessagePort(__kml_native.workerSelf(1));
const internalPort: MessagePort | null = isMainThread ? null : new MessagePort(__kml_native.workerSelf(2));

if (!isMainThread) {
    // An uncaught error ends this worker; the parent's 'error' listener
    // receives it (the runtime then ends the thread with code 1).
    __kml_native.processHook(3, (error: any, unused: any) => {
        let copy: any;
        try {
            copy = serialize(error, []);
        } catch (e) {
            copy = new Error(String(error));
        }
        __kml_native.mportPost((internalPort as MessagePort)._kmlId, { type: 'error', error: copy });
    });
}

// ---- Worker -------------------------------------------------------------------

export interface WorkerOptions {
    argv?: any[] | undefined;
    env?: any;
    eval?: boolean | undefined;
    workerData?: any;
    stdin?: boolean | undefined;
    stdout?: boolean | undefined;
    stderr?: boolean | undefined;
    execArgv?: string[] | undefined;
    resourceLimits?: any;
    transferList?: any[] | undefined;
    trackUnmanagedFds?: boolean | undefined;
    name?: string | undefined;
}

export class Worker extends EventEmitter {
    private _kmlThreadId = -1;
    readonly resourceLimits: any = {};
    private _kmlId = -1;
    private _kmlPort: MessagePort;
    private _kmlInternal: MessagePort;
    private _kmlExitCode: number | undefined = undefined;
    private _kmlExitWaiters: ((code: number) => void)[] = [];
    private _kmlOnMessage: ((e: any) => void) | null = null;
    private _kmlOnError: ((e: any) => void) | null = null;

    constructor(filename: string | URL, options?: WorkerOptions) {
        super();
        let path: string;
        if (filename instanceof URL) {
            if ((filename as URL).protocol !== 'file:') {
                throw new NodeTypeError('ERR_INVALID_URL_SCHEME', 'The URL must be of scheme file');
            }
            path = decodeURIComponent((filename as URL).pathname);
        } else if (typeof filename === 'string') {
            path = filename as string;
        } else {
            throw new NodeTypeError('ERR_INVALID_ARG_TYPE', 'The "filename" argument must be of type string or an instance of URL.');
        }
        const opts: any = options === undefined ? {} : options;
        if (opts.eval === true) {
            throw new NodeError('ERR_WORKER_UNSUPPORTED_OPERATION', 'Worker code given as a string (eval: true) cannot run: a worker is compiled into the program');
        }
        const pub = __kml_native.mportPair();
        const internal = __kml_native.mportPair();
        this._kmlPort = new MessagePort(pub);
        this._kmlInternal = new MessagePort(internal);
        const transfer: any[] = Array.isArray(opts.transferList) ? opts.transferList : [];
        const env: any[] = [];
        environmentData.forEach((v: any, k: any) => { env.push([k, v]); });
        const data = serialize({ data: opts.workerData, env: env }, transfer);
        const id = __kml_native.workerSpawn(path, pub + 1, internal + 1, data, (kind: number, code: number) => {
            if (kind === 0) {
                this.emit('online');
                return;
            }
            this._kmlOnExit(code);
        });
        if (id === -1) {
            const e: any = new Error("Cannot find module '" + path + "'");
            e.code = 'ERR_MODULE_NOT_FOUND';
            throw e;
        }
        if (id < 0) {
            throw new NodeError('ERR_WORKER_INIT_FAILED', 'Worker initialization failure: ' + __kml_native.errnoName(-id));
        }
        this._kmlId = id;
        this._kmlThreadId = __kml_native.workerThreadId(id);
        // The worker holds the loop, not its ports.
        this._kmlPort.on('message', (value: any) => { this.emit('message', value); });
        this._kmlPort.unref();
        this._kmlInternal.on('message', (msg: any) => {
            if (msg !== null && typeof msg === 'object' && msg.type === 'error') this.emit('error', msg.error);
        });
        this._kmlInternal.unref();
    }

    private _kmlOnExit(code: number): void {
        __kml_native.workerExited(this._kmlId);
        // What the worker posted before it ended arrives first.
        this._kmlPort._kmlDrain();
        this._kmlInternal._kmlDrain();
        this._kmlExitCode = code;
        this._kmlThreadId = -1;
        this._kmlPort.close();
        this._kmlInternal.close();
        this.emit('exit', code);
        const waiters = this._kmlExitWaiters;
        this._kmlExitWaiters = [];
        for (const w of waiters) w(code);
        this.removeAllListeners('message');
        this.removeAllListeners('messageerrors');
    }

    get threadId(): number {
        return this._kmlThreadId;
    }

    postMessage(value: any, transferList?: any): void {
        this._kmlPort.postMessage(value, transferList);
    }

    terminate(callback?: (err: any, code: number) => void): Promise<number> {
        return new Promise<number>((resolve) => {
            if (this._kmlExitCode !== undefined) {
                resolve(this._kmlExitCode as number);
                return;
            }
            this._kmlExitWaiters.push((code: number) => {
                if (callback !== undefined) callback(null, code);
                resolve(code);
            });
            __kml_native.workerTerminate(this._kmlId);
        });
    }

    ref(): void {
        if (this._kmlExitCode === undefined) __kml_native.workerRef(this._kmlId, true);
    }

    unref(): void {
        if (this._kmlExitCode === undefined) __kml_native.workerRef(this._kmlId, false);
    }

    getHeapSnapshot(): Promise<any> {
        return Promise.reject(new NodeError('ERR_WORKER_NOT_RUNNING', 'Worker instance not running'));
    }

    // The web's Worker shape.
    get onmessage(): ((e: any) => void) | null {
        return this._kmlOnMessage;
    }

    set onmessage(fn: ((e: any) => void) | null) {
        this._kmlOnMessage = fn;
        this.removeAllListeners('message');
        if (fn !== null) {
            const f = fn as (e: any) => void;
            this.on('message', (value: any) => { f(new MessageEvent('message', { data: value })); });
        }
    }

    get onerror(): ((e: any) => void) | null {
        return this._kmlOnError;
    }

    set onerror(fn: ((e: any) => void) | null) {
        this._kmlOnError = fn;
        this.removeAllListeners('error');
        if (fn !== null) {
            const f = fn as (e: any) => void;
            this.on('error', (error: any) => {
                const message = error !== null && typeof error === 'object' && typeof error.message === 'string' ? error.message : String(error);
                f(new ErrorEvent('error', { message: message, error: error }));
            });
        }
    }
}

// ---- BroadcastChannel ---------------------------------------------------------

// Every open channel of a name, process-wide (klainpool.c keeps the
// registry): each has a port pair whose second end the others post into, so
// a message reaches it on its own thread's loop. Each receiver gets a clone
// of its own.
export class BroadcastChannel extends EventEmitter {
    readonly name: string;
    private _kmlClosed = false;
    private _kmlOnMessage: ((e: any) => void) | null = null;
    _kmlIn: MessagePort;
    _kmlOut: number;

    constructor(name: string) {
        super();
        if (arguments.length === 0) {
            throw new NodeTypeError('ERR_MISSING_ARGS', 'The "name" argument must be specified');
        }
        this.name = String(name);
        const id = __kml_native.mportPair();
        this._kmlIn = new MessagePort(id);
        this._kmlOut = id + 1;
        __kml_native.bcJoin(this.name, id + 1);
        // Open, it holds the loop until close(), as Node's does.
        this._kmlIn.on('message', (value: any) => {
            if (!this._kmlClosed) this.emit('message', value);
        });
    }

    postMessage(message: any): void {
        if (this._kmlClosed) {
            throw new DOMException('BroadcastChannel is closed.', 'InvalidStateError');
        }
        const n = __kml_native.bcCount(this.name);
        for (let i = 0; i < n; i++) {
            const out = __kml_native.bcMember(this.name, i);
            if (out >= 0 && out !== this._kmlOut) __kml_native.mportPost(out, serialize(message, []));
        }
    }

    close(): void {
        if (this._kmlClosed) return;
        this._kmlClosed = true;
        __kml_native.bcLeave(this.name, this._kmlOut);
        this._kmlIn.close();
    }

    ref(): this {
        this._kmlIn.ref();
        return this;
    }

    unref(): this {
        this._kmlIn.unref();
        return this;
    }

    addEventListener(type: string, listener: (e: any) => void, options?: any): void {
        addWebListener(this, type, listener, options);
    }

    removeEventListener(type: string, listener: (e: any) => void): void {
        removeWebListener(this, type, listener);
    }

    get onmessage(): ((e: any) => void) | null {
        return this._kmlOnMessage;
    }

    set onmessage(fn: ((e: any) => void) | null) {
        if (this._kmlOnMessage !== null) this.removeEventListener('message', this._kmlOnMessage);
        this._kmlOnMessage = fn;
        if (fn !== null) this.addEventListener('message', fn);
    }
}

// ---- a worker's global scope (the web's `self`) --------------------------------

class WorkerGlobalScope {
    private _kmlOnMessage: ((e: any) => void) | null = null;

    postMessage(value: any, transfer?: any): void {
        if (parentPort === null) throw new ReferenceError('self.postMessage is only available in a worker');
        parentPort.postMessage(value, transfer);
    }

    get onmessage(): ((e: any) => void) | null {
        return this._kmlOnMessage;
    }

    set onmessage(fn: ((e: any) => void) | null) {
        if (parentPort === null) throw new ReferenceError('self.onmessage is only available in a worker');
        parentPort.onmessage = fn;
        this._kmlOnMessage = fn;
    }

    close(): void {
        __kml_native.processReallyExit(0);
    }
}

export const self: any = new WorkerGlobalScope(); // kml:global
