// Node's `events` module: lib/events.js (Node v24), in TypeScript. The
// listeners of an emitter live as Node keeps them: a null-prototype object
// from event name to one function, or to an array of them once there are
// more (`_events`, `_eventsCount`, `_maxListeners`).
// Its default export is EventEmitter itself, as in Node.

import { inspect } from './internal_util_inspect';
import { NodeTypeError, NodeRangeError } from './internal_errors';

class AbortError extends Error {
    code: string = 'ABORT_ERR';
    cause: any;
    constructor(message?: string, options?: { cause?: any }) {
        super(message === undefined ? 'The operation was aborted' : message);
        this.name = 'AbortError';
        this.cause = options?.cause;
    }
}

function received(v: any): string {
    if (v === null) return ' Received null';
    if (v === undefined) return ' Received undefined';
    if (typeof v === 'function') return ' Received function ' + (v.name ? v.name : '<anonymous>');
    if (typeof v === 'object') return ' Received an instance of ' + (v.constructor && v.constructor.name ? v.constructor.name : 'Object');
    let shown = inspect(v);
    if (shown.length > 28) shown = shown.slice(0, 25) + '...';
    return ' Received type ' + typeof v + ' (' + shown + ')';
}

function checkListener(listener: any): void {
    if (typeof listener !== 'function') {
        throw new NodeTypeError('ERR_INVALID_ARG_TYPE', 'The "listener" argument must be of type function.' + received(listener));
    }
}

function validateNumber(value: any, name: string, min: number): void {
    if (typeof value !== 'number') {
        throw new NodeTypeError('ERR_INVALID_ARG_TYPE', 'The "' + name + '" argument must be of type number.' + received(value));
    }
    if (value < min || Number.isNaN(value)) {
        throw new NodeRangeError('ERR_OUT_OF_RANGE', 'The value of "' + name + '" is out of range. It must be a non-negative number. Received ' + inspect(value));
    }
}

function validateInteger(value: any, name: string, min: number): void {
    if (typeof value !== 'number') {
        throw new NodeTypeError('ERR_INVALID_ARG_TYPE', 'The "' + name + '" argument must be of type number.' + received(value));
    }
    if (!Number.isInteger(value)) {
        throw new NodeRangeError('ERR_OUT_OF_RANGE', 'The value of "' + name + '" is out of range. It must be an integer. Received ' + inspect(value));
    }
    if (value < min || value > Number.MAX_SAFE_INTEGER) {
        throw new NodeRangeError('ERR_OUT_OF_RANGE', 'The value of "' + name + '" is out of range. It must be >= ' + min + ' && <= ' + Number.MAX_SAFE_INTEGER + '. Received ' + inspect(value));
    }
}

export interface EventEmitterOptions {
    captureRejections?: boolean | undefined;
}

export interface StaticEventEmitterOptions {
    signal?: AbortSignal | undefined;
}

export interface StaticEventEmitterIteratorOptions extends StaticEventEmitterOptions {
    close?: string[] | undefined;
    highWaterMark?: number | undefined;
    lowWaterMark?: number | undefined;
    // Internal (readline): yield each event's first argument.
    _kmlFirstEventParam?: boolean | undefined;
}

const kRejection: unique symbol = Symbol.for('nodejs.rejection');
const kErrorMonitor: unique symbol = Symbol('events.errorMonitor');
let defaultMaxListenersValue = 10;
let defaultCaptureRejections = false;

function emitUnhandledRejectionOrErr(ee: any, err: any, type: string | symbol, args: any[]): void {
    if (typeof ee[kRejection] === 'function') {
        ee[kRejection](err, type, ...args);
    } else {
        const prev = ee._kmlCapture;
        try {
            ee._kmlCapture = false;
            ee.emit('error', err);
        } finally {
            ee._kmlCapture = prev;
        }
    }
}

// A listener's returned promise, when the emitter captures rejections: its
// rejection is the emitter's 'error' (or its Symbol.for('nodejs.rejection')).
function addCatch(that: any, promise: any, type: string | symbol, args: any[]): void {
    if (!that._kmlCapture) return;
    try {
        const then = promise.then;
        if (typeof then === 'function') {
            promise.then(undefined, (err: any) => {
                process.nextTick(() => { emitUnhandledRejectionOrErr(that, err, type, args); });
            });
        }
    } catch (err) {
        that.emit('error', err);
    }
}

function arrayClone(arr: any[]): any[] {
    return arr.slice();
}

function unwrapListeners(arr: any[]): any[] {
    const ret: any[] = arr.slice();
    for (let i = 0; i < ret.length; ++i) {
        const orig = ret[i].listener;
        if (typeof orig === 'function') ret[i] = orig;
    }
    return ret;
}

function _getMaxListeners(that: any): number {
    if (that._maxListeners === undefined) return defaultMaxListenersValue;
    return that._maxListeners;
}

function _addListener(target: any, type: string | symbol, listener: any, prepend: boolean): any {
    checkListener(listener);
    let events: any = target._events;
    let existing: any = undefined;
    if (events === undefined) {
        events = Object.create(null);
        target._events = events;
        target._eventsCount = 0;
    } else {
        // To avoid recursion in the case that type === "newListener": before
        // adding it to the listeners, first emit "newListener".
        if (events.newListener !== undefined) {
            target.emit('newListener', type, listener.listener ?? listener);
            // A newListener handler may have replaced this._events.
            events = target._events;
        }
        existing = events[type];
    }
    if (existing === undefined) {
        // Optimize the case of one listener: no extra array.
        events[type] = listener;
        ++target._eventsCount;
    } else {
        if (typeof existing === 'function') {
            // Adding the second element: change to an array.
            existing = prepend ? [listener, existing] : [existing, listener];
            events[type] = existing;
        } else if (prepend) {
            existing.unshift(listener);
        } else {
            existing.push(listener);
        }
        // Check for listener leak.
        const m = _getMaxListeners(target);
        if (m > 0 && existing.length > m && !existing.warned) {
            existing.warned = true;
            const name = target.constructor && target.constructor.name ? target.constructor.name : 'EventEmitter';
            process.emitWarning('Possible EventEmitter memory leak detected. ' + existing.length + ' ' + String(type) +
                ' listeners added to [' + name + ']. MaxListeners is ' + m + '. Use emitter.setMaxListeners() to increase limit',
                'MaxListenersExceededWarning');
        }
    }
    return target;
}

function _onceWrap(target: any, type: string | symbol, listener: any): any {
    const state: any = { fired: false, wrapFn: undefined };
    const wrapped: any = (...args: any[]): any => {
        if (!state.fired) {
            target.removeListener(type, state.wrapFn);
            state.fired = true;
            return listener.apply(target, args);
        }
        return undefined;
    };
    wrapped.listener = listener;
    state.wrapFn = wrapped;
    return wrapped;
}

function _listeners(target: any, type: string | symbol, unwrap: boolean): Function[] {
    const events = target._events;
    if (events === undefined) return [];
    const evlistener = events[type];
    if (evlistener === undefined) return [];
    if (typeof evlistener === 'function') {
        return unwrap ? [evlistener.listener || evlistener] : [evlistener];
    }
    return unwrap ? unwrapListeners(evlistener) : arrayClone(evlistener);
}

export class EventEmitter<T = any> {
    _events: any = undefined;
    _eventsCount = 0;
    _maxListeners: number | undefined = undefined;
    _kmlCapture = false;

    static readonly captureRejectionSymbol: unique symbol = kRejection;
    static readonly errorMonitor: unique symbol = kErrorMonitor;

    static get defaultMaxListeners(): number {
        return defaultMaxListenersValue;
    }

    static set defaultMaxListeners(arg: number) {
        if (typeof arg !== 'number' || arg < 0 || Number.isNaN(arg)) {
            throw new NodeRangeError('ERR_OUT_OF_RANGE', 'The value of "defaultMaxListeners" is out of range. It must be a non-negative number. Received ' + inspect(arg));
        }
        defaultMaxListenersValue = arg;
    }

    static get captureRejections(): boolean {
        return defaultCaptureRejections;
    }

    static set captureRejections(value: boolean) {
        if (typeof value !== 'boolean') {
            throw new NodeTypeError('ERR_INVALID_ARG_TYPE', 'The "EventEmitter.captureRejections" property must be of type boolean.' + received(value));
        }
        defaultCaptureRejections = value;
    }

    constructor(opts?: EventEmitterOptions) {
        this._events = Object.create(null);
        this._eventsCount = 0;
        if (opts !== undefined && opts !== null && opts.captureRejections !== undefined) {
            if (typeof opts.captureRejections !== 'boolean') {
                throw new NodeTypeError('ERR_INVALID_ARG_TYPE', 'The "options.captureRejections" property must be of type boolean.' + received(opts.captureRejections));
            }
            this._kmlCapture = opts.captureRejections;
        } else {
            this._kmlCapture = defaultCaptureRejections;
        }
    }

    setMaxListeners(n: number): this {
        validateNumber(n, 'setMaxListeners', 0);
        this._maxListeners = n;
        return this;
    }

    getMaxListeners(): number {
        return _getMaxListeners(this);
    }

    emit(eventName: string | symbol, ...args: any[]): boolean {
        let doError = eventName === 'error';
        const events: any = this._events;
        if (events !== undefined) {
            if (doError && events[kErrorMonitor] !== undefined) this.emit(kErrorMonitor, ...args);
            doError = doError && events.error === undefined;
        } else if (!doError) {
            return false;
        }
        // If there is no 'error' event listener then throw.
        if (doError) {
            let er: any = undefined;
            if (args.length > 0) er = args[0];
            if (er instanceof Error) {
                throw er; // Unhandled 'error' event
            }
            let stringifiedEr: any;
            try {
                stringifiedEr = inspect(er);
            } catch {
                stringifiedEr = er;
            }
            // At least give some kind of context to the user.
            const err: any = new Error('Unhandled error.' + (er === undefined ? '' : ' (' + stringifiedEr + ')'));
            err.code = 'ERR_UNHANDLED_ERROR';
            err.context = er;
            throw err; // Unhandled 'error' event
        }
        const handler = events[eventName];
        if (handler === undefined) return false;
        if (typeof handler === 'function') {
            const result = handler.apply(this, args);
            if (result !== undefined && result !== null) addCatch(this, result, eventName, args);
        } else {
            const listeners = arrayClone(handler);
            const len = listeners.length;
            for (let i = 0; i < len; ++i) {
                const result = listeners[i].apply(this, args);
                if (result !== undefined && result !== null) addCatch(this, result, eventName, args);
            }
        }
        return true;
    }

    addListener(eventName: string | symbol, listener: (...args: any[]) => any): this {
        _addListener(this, eventName, listener, false);
        return this;
    }

    on(eventName: string | symbol, listener: (...args: any[]) => any): this {
        _addListener(this, eventName, listener, false);
        return this;
    }

    prependListener(eventName: string | symbol, listener: (...args: any[]) => any): this {
        _addListener(this, eventName, listener, true);
        return this;
    }

    once(eventName: string | symbol, listener: (...args: any[]) => any): this {
        checkListener(listener);
        this.on(eventName, _onceWrap(this, eventName, listener));
        return this;
    }

    prependOnceListener(eventName: string | symbol, listener: (...args: any[]) => any): this {
        checkListener(listener);
        this.prependListener(eventName, _onceWrap(this, eventName, listener));
        return this;
    }

    removeListener(eventName: string | symbol, listener: (...args: any[]) => any): this {
        checkListener(listener);
        const events: any = this._events;
        if (events === undefined) return this;
        const list: any = events[eventName];
        if (list === undefined) return this;
        const fn: any = listener;
        if (list === fn || list.listener === fn) {
            this._eventsCount -= 1;
            if (this._eventsCount === 0) {
                this._events = Object.create(null);
            } else {
                delete events[eventName];
                if (events.removeListener) this.emit('removeListener', eventName, list.listener || fn);
            }
        } else if (typeof list !== 'function') {
            let position = -1;
            for (let i = list.length - 1; i >= 0; i--) {
                if (list[i] === fn || list[i].listener === fn) {
                    position = i;
                    break;
                }
            }
            if (position < 0) return this;
            if (position === 0) list.shift();
            else list.splice(position, 1);
            if (list.length === 1) events[eventName] = list[0];
            if (events.removeListener !== undefined) this.emit('removeListener', eventName, fn);
        }
        return this;
    }

    off(eventName: string | symbol, listener: (...args: any[]) => any): this {
        return this.removeListener(eventName, listener);
    }

    removeAllListeners(eventName?: string | symbol): this {
        const events: any = this._events;
        if (events === undefined) return this;
        // Not listening for removeListener, no need to emit.
        if (events.removeListener === undefined) {
            if (eventName === undefined) {
                this._events = Object.create(null);
                this._eventsCount = 0;
            } else if (events[eventName] !== undefined) {
                if (--this._eventsCount === 0) this._events = Object.create(null);
                else delete events[eventName];
            }
            return this;
        }
        // Emit removeListener for all listeners on all events.
        if (eventName === undefined) {
            for (const key of Object.keys(events)) {
                if (key === 'removeListener') continue;
                this.removeAllListeners(key);
            }
            this.removeAllListeners('removeListener');
            this._events = Object.create(null);
            this._eventsCount = 0;
            return this;
        }
        const listeners: any = events[eventName];
        if (typeof listeners === 'function') {
            this.removeListener(eventName, listeners);
        } else if (listeners !== undefined) {
            // LIFO order
            for (let i = listeners.length - 1; i >= 0; i--) {
                this.removeListener(eventName, listeners[i]);
            }
        }
        return this;
    }

    listeners(eventName: string | symbol): Function[] {
        return _listeners(this, eventName, true);
    }

    rawListeners(eventName: string | symbol): Function[] {
        return _listeners(this, eventName, false);
    }

    listenerCount(eventName: string | symbol, listener?: Function): number {
        const events: any = this._events;
        if (events !== undefined) {
            const evlistener: any = events[eventName];
            const fn: any = listener;
            if (typeof evlistener === 'function') {
                if (fn !== undefined && fn !== null) {
                    return fn === evlistener || fn === evlistener.listener ? 1 : 0;
                }
                return 1;
            } else if (evlistener !== undefined) {
                if (fn !== undefined && fn !== null) {
                    let matching = 0;
                    for (let i = 0, l = evlistener.length; i < l; i++) {
                        if (evlistener[i] === fn || evlistener[i].listener === fn) matching++;
                    }
                    return matching;
                }
                return evlistener.length;
            }
        }
        return 0;
    }

    eventNames(): (string | symbol)[] {
        return this._eventsCount > 0 ? Reflect.ownKeys(this._events) : [];
    }

    // The deprecated static form: emitter.listenerCount(type).
    static listenerCount(emitter: EventEmitter, eventName: string | symbol): number {
        return emitter.listenerCount(eventName);
    }

    static once(emitter: any, eventName: string | symbol, options?: StaticEventEmitterOptions): Promise<any[]> {
        return once(emitter, eventName, options);
    }

    static on(emitter: any, eventName: string | symbol, options?: StaticEventEmitterIteratorOptions): AsyncIterableIterator<any[]> {
        return on(emitter, eventName, options);
    }

    static getEventListeners(emitter: any, name: string | symbol): Function[] {
        return getEventListeners(emitter, name);
    }

    static getMaxListeners(emitter: any): number {
        return getMaxListeners(emitter);
    }

    // `require('events').EventEmitter`: the module is the class, and the
    // class names itself (lib/events.js's EventEmitter.EventEmitter).
    static get EventEmitter(): typeof EventEmitter {
        return EventEmitter;
    }

    static setMaxListeners(n?: number, ...eventTargets: any[]): void {
        setMaxListeners(n, ...eventTargets);
    }
}

export default EventEmitter;
export const errorMonitor: unique symbol = kErrorMonitor;
export const captureRejectionSymbol: unique symbol = kRejection;

function eventTargetAgnosticAddListener(emitter: any, name: string | symbol, listener: any, once: boolean): void {
    if (typeof emitter.on === 'function') {
        if (once) emitter.once(name, listener);
        else emitter.on(name, listener);
    } else if (typeof emitter.addEventListener === 'function') {
        emitter.addEventListener(name, listener, { once: once });
    } else {
        throw new NodeTypeError('ERR_INVALID_ARG_TYPE', 'The "emitter" argument must be an instance of EventEmitter or EventTarget.' + received(emitter));
    }
}

function eventTargetAgnosticRemoveListener(emitter: any, name: string | symbol, listener: any): void {
    if (typeof emitter.removeListener === 'function') {
        emitter.removeListener(name, listener);
    } else if (typeof emitter.removeEventListener === 'function') {
        emitter.removeEventListener(name, listener);
    } else {
        throw new NodeTypeError('ERR_INVALID_ARG_TYPE', 'The "emitter" argument must be an instance of EventEmitter or EventTarget.' + received(emitter));
    }
}

// A promise of the arguments of emitter's next `name` event; rejected by
// an 'error' event first (an EventEmitter's) or the signal's abort.
export function once(emitter: any, name: string | symbol, options?: StaticEventEmitterOptions): Promise<any[]> {
    const signal: any = options?.signal;
    if (signal !== undefined && signal !== null && signal.aborted) {
        return Promise.reject(new AbortError(undefined, { cause: signal.reason }));
    }
    return new Promise<any[]>((resolve, reject) => {
        let abortListener: any = undefined;
        const errorListener = (err: any): void => {
            emitter.removeListener(name, resolver);
            if (signal !== undefined && signal !== null) eventTargetAgnosticRemoveListener(signal, 'abort', abortListener);
            reject(err);
        };
        const resolver = (...args: any[]): void => {
            if (typeof emitter.removeListener === 'function') emitter.removeListener('error', errorListener);
            if (signal !== undefined && signal !== null) eventTargetAgnosticRemoveListener(signal, 'abort', abortListener);
            resolve(args);
        };
        eventTargetAgnosticAddListener(emitter, name, resolver, true);
        if (name !== 'error' && typeof emitter.once === 'function') {
            // EventTarget does not have `error` event semantics like Node
            // EventEmitters, we listen to `error` events only on EventEmitters.
            emitter.once('error', errorListener);
        }
        abortListener = (): void => {
            eventTargetAgnosticRemoveListener(emitter, name, resolver);
            eventTargetAgnosticRemoveListener(emitter, 'error', errorListener);
            reject(new AbortError(undefined, { cause: signal?.reason }));
        };
        if (signal !== undefined && signal !== null) eventTargetAgnosticAddListener(signal, 'abort', abortListener, true);
    });
}

// The async iterator of `on(emitter, event)`: each event's arguments, in
// order; an 'error' event throws from the next read. More than highWaterMark
// unread events pause the emitter, and reading below lowWaterMark resumes
// it. The internal `_kmlFirstEventParam` option yields each event's first
// argument instead (Node's kFirstEventParam, for readline).
class EventIterator {
    private unconsumedEvents: any[] = [];
    private unconsumedPromises: { resolve: (v: any) => void; reject: (e: any) => void }[] = [];
    private paused = false;
    private error: any = null;
    private finished = false;
    private size = 0;
    private highWatermark: number;
    private lowWatermark: number;
    private emitter: any;
    private event: string | symbol;
    private signal: any;
    private closeEvents: string[];
    private eventHandler: any;
    private errorHandler: any;
    private closeHandler: any;
    private abortListener: any;

    constructor(emitter: any, event: string | symbol, options?: StaticEventEmitterIteratorOptions) {
        const opts: any = options ?? {};
        this.emitter = emitter;
        this.event = event;
        this.signal = opts.signal;
        // Support both highWaterMark and highWatermark for backward compatibility
        this.highWatermark = opts.highWaterMark ?? opts.highWatermark ?? Number.MAX_SAFE_INTEGER;
        validateInteger(this.highWatermark, 'options.highWaterMark', 1);
        // Support both lowWaterMark and lowWatermark for backward compatibility
        this.lowWatermark = opts.lowWaterMark ?? opts.lowWatermark ?? 1;
        validateInteger(this.lowWatermark, 'options.lowWaterMark', 1);
        this.closeEvents = opts.close ?? [];
        const push = (value: any): void => {
            const p = this.unconsumedPromises.shift();
            if (p === undefined) {
                this.size++;
                if (!this.paused && this.size > this.highWatermark) {
                    this.paused = true;
                    this.emitter.pause();
                }
                this.unconsumedEvents.push(value);
            } else {
                p.resolve({ value: value, done: false });
            }
        };
        this.eventHandler = opts._kmlFirstEventParam === true
            ? (value: any): void => { push(value); }
            : (...args: any[]): void => { push(args); };
        this.errorHandler = (err: any): void => {
            const p = this.unconsumedPromises.shift();
            if (p !== undefined) p.reject(err);
            else this.error = err;
            this.closeHandlerNow();
        };
        this.closeHandler = (): void => { this.closeHandlerNow(); };
        this.abortListener = (): void => {
            this.errorHandler(new AbortError(undefined, { cause: this.signal?.reason }));
        };
        eventTargetAgnosticAddListener(emitter, event, this.eventHandler, false);
        if (event !== 'error' && typeof emitter.on === 'function') emitter.on('error', this.errorHandler);
        for (const c of this.closeEvents) eventTargetAgnosticAddListener(emitter, c, this.closeHandler, false);
        if (this.signal !== undefined && this.signal !== null) {
            eventTargetAgnosticAddListener(this.signal, 'abort', this.abortListener, true);
        }
    }

    private removeAll(): void {
        eventTargetAgnosticRemoveListener(this.emitter, this.event, this.eventHandler);
        if (this.event !== 'error' && typeof this.emitter.removeListener === 'function') this.emitter.removeListener('error', this.errorHandler);
        for (const c of this.closeEvents) eventTargetAgnosticRemoveListener(this.emitter, c, this.closeHandler);
        if (this.signal !== undefined && this.signal !== null) eventTargetAgnosticRemoveListener(this.signal, 'abort', this.abortListener);
    }

    private closeHandlerNow(): Promise<any> {
        this.removeAll();
        this.finished = true;
        this.paused = false;
        const done = { value: undefined, done: true };
        while (this.unconsumedPromises.length > 0) {
            const p = this.unconsumedPromises.shift();
            if (p !== undefined) p.resolve(done);
        }
        return Promise.resolve(done);
    }

    next(): Promise<any> {
        // First, we consume all unread events.
        if (this.size > 0) {
            const value = this.unconsumedEvents.shift();
            this.size--;
            if (this.paused && this.size < this.lowWatermark) {
                this.emitter.resume(); // Can not be finished yet
                this.paused = false;
            }
            return Promise.resolve({ value: value, done: false });
        }
        // Then we error, if an error happened. This happens one time if at
        // all, because after 'error' we stop listening.
        if (this.error !== null) {
            const p = Promise.reject(this.error);
            // Only the first element errors.
            this.error = null;
            return p;
        }
        // If the iterator is finished, resolve to done.
        if (this.finished) return this.closeHandlerNow();
        // Wait until an event happens.
        return new Promise<any>((resolve, reject) => {
            this.unconsumedPromises.push({ resolve: resolve, reject: reject });
        });
    }

    return(): Promise<any> {
        return this.closeHandlerNow();
    }

    throw(err: any): Promise<any> {
        if (!(err instanceof Error)) {
            throw new NodeTypeError('ERR_INVALID_ARG_TYPE', 'The "EventEmitter.AsyncIterator" property must be an instance of Error.' + received(err));
        }
        this.errorHandler(err);
        return Promise.resolve({ value: undefined, done: true });
    }

    [Symbol.asyncIterator](): EventIterator {
        return this;
    }
}

// An async iterator over emitter's `event` events: each one's arguments.
export function on(emitter: any, event: string | symbol, options?: StaticEventEmitterIteratorOptions): AsyncIterableIterator<any[]> {
    const signal: any = options?.signal;
    if (signal !== undefined && signal !== null && signal.aborted) {
        throw new AbortError(undefined, { cause: signal.reason });
    }
    const it: any = new EventIterator(emitter, event, options);
    return it;
}

// The listeners of an EventEmitter's (or EventTarget's) event.
export function getEventListeners(emitterOrTarget: any, type: string | symbol): Function[] {
    if (typeof emitterOrTarget.listeners === 'function') return emitterOrTarget.listeners(type);
    throw new NodeTypeError('ERR_INVALID_ARG_TYPE', 'The "emitter" argument must be an instance of EventEmitter or EventTarget.' + received(emitterOrTarget));
}

export function getMaxListeners(emitterOrTarget: any): number {
    if (typeof emitterOrTarget.getMaxListeners === 'function') return _getMaxListeners(emitterOrTarget);
    throw new NodeTypeError('ERR_INVALID_ARG_TYPE', 'The "emitter" argument must be an instance of EventEmitter or EventTarget.' + received(emitterOrTarget));
}

// Sets the max listeners of each target, or with none the default.
export function setMaxListeners(n?: number, ...eventTargets: any[]): void {
    const value = n === undefined ? defaultMaxListenersValue : n;
    validateNumber(value, 'n', 0);
    if (eventTargets.length === 0) {
        defaultMaxListenersValue = value;
    } else {
        for (const target of eventTargets) {
            if (typeof target.setMaxListeners === 'function') {
                target.setMaxListeners(value);
            } else {
                throw new NodeTypeError('ERR_INVALID_ARG_TYPE', 'The "eventTargets" argument must be an instance of EventEmitter or EventTarget.' + received(target));
            }
        }
    }
}

// The deprecated module-level listenerCount(emitter, type).
export function listenerCount(emitter: EventEmitter, type: string | symbol): number {
    return emitter.listenerCount(type);
}
