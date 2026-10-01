// Node's `async_hooks`: AsyncLocalStorage and AsyncResource, ported from
// Node v24's lib/internal/async_local_storage/async_context_frame.js,
// lib/internal/async_context_frame.js and lib/async_hooks.js. The current
// async context frame is a Map from each AsyncLocalStorage to its store; the
// runtime keeps it per task and carries it into tasks it spawns and timers
// it schedules (lib/native.d.ts asyncContextGet/asyncContextSet), where Node
// keeps it as V8's continuation-preserved embedder data.
//
// createHook, executionAsyncId and the async-id lifecycle are not ported:
// an AsyncResource's asyncId() numbers resources in creation order, with no
// init/before/after/destroy events to observe.
//
// kml:default-namespace — `import ah from 'async_hooks'` reads this module's
// exports, as Node's default export carries them.

class NodeTypeError extends TypeError {
    code: string;
    constructor(code: string, message: string) {
        super(message);
        this.code = code;
    }
}

function received(value: any): string {
    if (value === null || value === undefined) return ' Received ' + String(value);
    if (typeof value === 'function') return ' Received function';
    if (typeof value === 'object') return ' Received an instance of Object';
    let shown = String(value);
    if (shown.length > 28) shown = shown.slice(0, 25) + '...';
    if (typeof value === 'string') shown = "'" + shown + "'";
    return ' Received type ' + typeof value + ' (' + shown + ')';
}

function validateFunction(value: any, name: string): void {
    if (typeof value !== 'function') {
        throw new NodeTypeError('ERR_INVALID_ARG_TYPE', 'The "' + name + '" argument must be of type function.' + received(value));
    }
}

// ---- the async context frame (AsyncContextFrame) ----

function currentFrame(): any {
    return __kml_native.asyncContextGet();
}

function setFrame(frame: any): void {
    __kml_native.asyncContextSet(frame);
}

function exchangeFrame(frame: any): any {
    const prior = currentFrame();
    setFrame(frame);
    return prior;
}

// A new frame: the current one's entries, and store's set to data.
function frameWith(store: any, data: any): Map<any, any> {
    const prior: any = currentFrame();
    const frame = new Map<any, any>();
    if (prior !== undefined) {
        (prior as Map<any, any>).forEach((v: any, k: any) => { frame.set(k, v); });
    }
    frame.set(store, data);
    return frame;
}

// ---- AsyncResource ----

let nextAsyncId = 1;

export interface AsyncResourceOptions {
    triggerAsyncId?: number | undefined;
    requireManualDestroy?: boolean | undefined;
}

export class AsyncResource {
    private contextFrame: any;
    private asyncIdValue: number;
    private triggerAsyncIdValue: number;

    constructor(type: string, opts?: number | AsyncResourceOptions) {
        if (typeof type !== 'string') {
            throw new NodeTypeError('ERR_INVALID_ARG_TYPE', 'The "type" argument must be of type string.' + received(type));
        }
        let trigger = 0;
        if (typeof opts === 'number') trigger = opts;
        else if (opts !== undefined && opts.triggerAsyncId !== undefined) trigger = opts.triggerAsyncId;
        this.asyncIdValue = ++nextAsyncId;
        this.triggerAsyncIdValue = trigger;
        this.contextFrame = currentFrame();
    }

    runInAsyncScope<R>(fn: (...args: any[]) => R, thisArg?: any, ...args: any[]): R {
        const prior = exchangeFrame(this.contextFrame);
        try {
            return Reflect.apply(fn, thisArg, args);
        } finally {
            setFrame(prior);
        }
    }

    emitDestroy(): this {
        return this;
    }

    asyncId(): number {
        return this.asyncIdValue;
    }

    triggerAsyncId(): number {
        return this.triggerAsyncIdValue;
    }

    bind(fn: (...args: any[]) => any, thisArg?: any): (...args: any[]) => any {
        validateFunction(fn, 'fn');
        const resource = this;
        if (thisArg === undefined) {
            return function (this: any, ...args: any[]): any {
                return resource.runInAsyncScope(fn, this, ...args);
            };
        }
        return (...args: any[]): any => resource.runInAsyncScope(fn, thisArg, ...args);
    }

    static bind(fn: (...args: any[]) => any, type?: string, thisArg?: any): (...args: any[]) => any {
        const t = type || (fn as any).name || 'bound-anonymous-fn';
        return new AsyncResource(t).bind(fn, thisArg);
    }
}

// ---- AsyncLocalStorage ----

export interface AsyncLocalStorageOptions {
    defaultValue?: any;
    name?: string | undefined;
}

export class AsyncLocalStorage<T> {
    private defaultValue: any = undefined;
    private nameValue: string = '';

    constructor(options?: AsyncLocalStorageOptions) {
        if (options !== undefined) {
            if (options === null || typeof options !== 'object') {
                throw new NodeTypeError('ERR_INVALID_ARG_TYPE', 'The "options" argument must be of type object.' + received(options));
            }
            this.defaultValue = options.defaultValue;
            if (options.name !== undefined) this.nameValue = '' + options.name;
        }
    }

    get name(): string {
        return this.nameValue;
    }

    static bind(fn: (...args: any[]) => any): (...args: any[]) => any {
        return AsyncResource.bind(fn);
    }

    static snapshot(): (fn: (...args: any[]) => any, ...args: any[]) => any {
        return AsyncResource.bind((cb: (...args: any[]) => any, ...args: any[]): any => Reflect.apply(cb, undefined, args));
    }

    disable(): void {
        const frame: any = currentFrame();
        if (frame !== undefined) (frame as Map<any, any>).delete(this);
    }

    enterWith(store: T): void {
        setFrame(frameWith(this, store));
    }

    run<R>(store: T, callback: (...args: any[]) => R, ...args: any[]): R {
        const prior = currentFrame();
        this.enterWith(store);
        try {
            return Reflect.apply(callback, undefined, args);
        } finally {
            setFrame(prior);
        }
    }

    exit<R>(callback: (...args: any[]) => R, ...args: any[]): R {
        return this.run(undefined as any, callback, ...args);
    }

    getStore(): T | undefined {
        const frame: any = currentFrame();
        if (frame === undefined || !(frame as Map<any, any>).has(this)) return this.defaultValue;
        return (frame as Map<any, any>).get(this);
    }
}
