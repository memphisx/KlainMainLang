// kml:global
// Event, CustomEvent, EventTarget, AbortSignal and AbortController —
// globals, as Node's lib/internal/event_target.js and
// lib/internal/abort_controller.js have them (TDD-00232 Stage 1) — and
// MessageEvent, CloseEvent and ErrorEvent, as undici's (Stage 2). A program that
// names one without declaring it imports this module.

export interface EventInit {
    bubbles?: boolean;
    cancelable?: boolean;
    composed?: boolean;
}

export class Event {
    static readonly NONE = 0;
    static readonly CAPTURING_PHASE = 1;
    static readonly AT_TARGET = 2;
    static readonly BUBBLING_PHASE = 3;
    readonly NONE = 0;
    readonly CAPTURING_PHASE = 1;
    readonly AT_TARGET = 2;
    readonly BUBBLING_PHASE = 3;

    #type: string;
    #bubbles: boolean;
    #cancelable: boolean;
    #composed: boolean;
    #defaultPrevented = false;
    #timeStamp: number;
    #propagationStopped = false;
    #immediateStopped = false;
    #target: EventTarget | null = null;
    #currentTarget: EventTarget | null = null;
    #phase = 0;
    #dispatching = false;
    #passive = false;
    #trusted = false;

    constructor(type: string, options?: EventInit) {
        if (arguments.length === 0) {
            throw new TypeError('The "type" argument must be specified');
        }
        this.#type = `${type}`;
        this.#bubbles = options?.bubbles === true;
        this.#cancelable = options?.cancelable === true;
        this.#composed = options?.composed === true;
        this.#timeStamp = __kml_native.perfNow();
    }

    get type(): string { return this.#type; }
    get bubbles(): boolean { return this.#bubbles; }
    get cancelable(): boolean { return this.#cancelable; }
    get composed(): boolean { return this.#composed; }
    get defaultPrevented(): boolean { return this.#cancelable && this.#defaultPrevented; }
    get timeStamp(): number { return this.#timeStamp; }
    get isTrusted(): boolean { return this.#trusted; }
    get target(): EventTarget | null { return this.#target; }
    get currentTarget(): EventTarget | null { return this.#currentTarget; }
    get srcElement(): EventTarget | null { return this.#target; }
    get eventPhase(): number { return this.#phase; }
    get returnValue(): boolean { return !this.defaultPrevented; }
    get cancelBubble(): boolean { return this.#propagationStopped; }
    set cancelBubble(value: boolean) {
        if (value) this.#propagationStopped = true;
    }

    composedPath(): EventTarget[] {
        return this.#dispatching && this.#currentTarget !== null ? [this.#currentTarget] : [];
    }
    preventDefault(): void {
        if (!this.#passive) this.#defaultPrevented = true;
    }
    stopPropagation(): void {
        this.#propagationStopped = true;
    }
    stopImmediatePropagation(): void {
        this.#propagationStopped = true;
        this.#immediateStopped = true;
    }

    // The dispatch state EventTarget drives (not part of the public API).
    static _begin(ev: Event, target: EventTarget, trusted: boolean): void {
        ev.#target = target;
        ev.#currentTarget = target;
        ev.#phase = 2;
        ev.#dispatching = true;
        ev.#immediateStopped = false;
        if (trusted) ev.#trusted = true;
    }
    static _end(ev: Event): void {
        ev.#currentTarget = null;
        ev.#phase = 0;
        ev.#dispatching = false;
    }
    static _dispatching(ev: Event): boolean { return ev.#dispatching; }
    static _stopped(ev: Event): boolean { return ev.#immediateStopped; }
    static _setPassive(ev: Event, passive: boolean): void { ev.#passive = passive; }
}

export interface CustomEventInit extends EventInit {
    detail?: any;
}

// CustomEvent<T>'s implementation: the declaration (lib/node.d.ts) carries
// T; the value is any.
export class CustomEvent extends Event {
    #detail: any;
    constructor(type: string, options?: CustomEventInit) {
        if (arguments.length === 0) {
            throw new TypeError('The "type" argument must be specified');
        }
        super(type, options);
        // Node's detail defaults to null.
        this.#detail = options?.detail ?? null;
    }
    get detail(): any { return this.#detail; }
}

export interface EventListenerOptions {
    capture?: boolean;
}

export interface AddEventListenerOptions extends EventListenerOptions {
    once?: boolean;
    passive?: boolean;
    signal?: AbortSignal;
}

export interface EventListener {
    (evt: Event): void;
}

export interface EventListenerObject {
    handleEvent(object: Event): void;
}

// One registration: the listener (a function or an object with
// handleEvent), its capture flag and options.
interface Registration {
    listener: any;
    capture: boolean;
    once: boolean;
    passive: boolean;
    removed: boolean;
}

export class EventTarget {
    #listeners = new Map<string, Registration[]>();

    // Listeners and options are `any` here: the checker types the calls
    // from the declaration (lib/node.d.ts), and a function-or-object union is
    // not a representation code generation has.
    addEventListener(type: string, listener: any, options?: any): void {
        if (listener === null || listener === undefined) return;
        const l: any = listener;
        let capture = false;
        let once = false;
        let passive = false;
        let signal: AbortSignal | undefined = undefined;
        if (typeof options === "boolean") {
            capture = options;
        } else if (options !== undefined && options !== null) {
            capture = options.capture === true;
            once = options.once === true;
            passive = options.passive === true;
            signal = options.signal;
        }
        if (signal !== undefined && signal.aborted) return;
        const key = `${type}`;
        let list = this.#listeners.get(key);
        if (list === undefined) {
            list = [];
            this.#listeners.set(key, list);
        }
        for (const r of list) {
            if (r.listener === l && r.capture === capture) return;
        }
        const reg: Registration = { listener: l, capture, once, passive, removed: false };
        list.push(reg);
        if (signal !== undefined) {
            signal.addEventListener("abort", () => {
                this.removeEventListener(key, listener, { capture });
            }, { once: true });
        }
    }

    removeEventListener(type: string, listener: any, options?: any): void {
        if (listener === null || listener === undefined) return;
        const l: any = listener;
        const capture = typeof options === "boolean" ? options : options?.capture === true;
        const key = `${type}`;
        const list = this.#listeners.get(key);
        if (list === undefined) return;
        for (let i = 0; i < list.length; i++) {
            const r = list[i];
            if (r.listener === l && r.capture === capture) {
                r.removed = true;
                list.splice(i, 1);
                return;
            }
        }
    }

    dispatchEvent(event: Event): boolean {
        return EventTarget._dispatch(this, event, false);
    }

    static _dispatch(target: EventTarget, event: Event, trusted: boolean): boolean {
        if (Event._dispatching(event)) {
            throw new DOMException("The event is already being dispatched", "InvalidStateError");
        }
        Event._begin(event, target, trusted);
        const list = target.#listeners.get(event.type);
        if (list !== undefined) {
            for (const r of list.slice()) {
                if (r.removed) continue;
                if (r.once) target.removeEventListener(event.type, r.listener, { capture: r.capture });
                Event._setPassive(event, r.passive);
                try {
                    const l = r.listener;
                    if (typeof l === "function") {
                        l.call(target, event);
                    } else {
                        l.handleEvent(event);
                    }
                } catch (err) {
                    process.nextTick(() => { throw err; });
                }
                Event._setPassive(event, false);
                if (Event._stopped(event)) break;
            }
        }
        Event._end(event);
        return !event.defaultPrevented;
    }
}

export class AbortSignal extends EventTarget {
    // Read by fetch's native abort check (codegen/llvm/emit_signal.go):
    // #aborted, #reason, and #deadline, AbortSignal.timeout's
    // performance.now() deadline (0: none), which cancels a transfer the
    // loop is not free to run the timer for.
    #aborted = false;
    #reason: any = undefined;
    #deadline = 0;
    #onabort: ((this: AbortSignal, ev: Event) => any) | null = null;
    #onabortRegistered = false;

    static #creating = false;

    constructor() {
        super();
        if (!AbortSignal.#creating) throw new TypeError("Illegal constructor");
        AbortSignal.#creating = false;
    }

    get aborted(): boolean { return this.#aborted; }
    get reason(): any { return this.#reason; }

    get onabort(): ((this: AbortSignal, ev: Event) => any) | null { return this.#onabort; }
    set onabort(handler: ((this: AbortSignal, ev: Event) => any) | null) {
        this.#onabort = typeof handler === "function" ? handler : null;
        // An event handler attribute holds its place among the listeners from
        // its first assignment.
        if (this.#onabort !== null && !this.#onabortRegistered) {
            this.#onabortRegistered = true;
            this.addEventListener("abort", (ev: Event) => {
                const h: any = this.#onabort;
                if (h !== null) h.call(this, ev);
            });
        }
    }

    throwIfAborted(): void {
        if (this.#aborted) throw this.#reason;
    }

    static _new(): AbortSignal {
        AbortSignal.#creating = true;
        return new AbortSignal();
    }

    static _abort(signal: AbortSignal, reason: any): void {
        if (signal.#aborted) return;
        signal.#aborted = true;
        signal.#reason = reason;
        EventTarget._dispatch(signal, new Event("abort"), true);
    }

    static abort(reason?: any): AbortSignal {
        const signal = AbortSignal._new();
        signal.#aborted = true;
        signal.#reason = reason === undefined ? new DOMException("This operation was aborted", "AbortError") : reason;
        return signal;
    }

    static timeout(milliseconds: number): AbortSignal {
        const signal = AbortSignal._new();
        signal.#deadline = __kml_native.perfNow() + milliseconds;
        const t = setTimeout(() => {
            AbortSignal._abort(signal, new DOMException("The operation was aborted due to timeout", "TimeoutError"));
        }, milliseconds);
        t.unref();
        return signal;
    }

    static any(signals: AbortSignal[]): AbortSignal {
        const result = AbortSignal._new();
        for (const s of signals) {
            if (s.aborted) {
                result.#aborted = true;
                result.#reason = s.reason;
                return result;
            }
        }
        for (const s of signals) {
            s.addEventListener("abort", () => { AbortSignal._abort(result, s.reason); }, { once: true });
        }
        return result;
    }
}

export class AbortController {
    #signal: AbortSignal = AbortSignal._new();
    get signal(): AbortSignal { return this.#signal; }
    abort(reason?: any): void {
        AbortSignal._abort(this.#signal, reason === undefined ? new DOMException("This operation was aborted", "AbortError") : reason);
    }
}

// MessageEvent, CloseEvent and ErrorEvent (undici's lib/web/websocket/events.js).
export interface MessageEventInit extends EventInit {
    data?: any;
    origin?: string;
    lastEventId?: string;
    source?: any;
    ports?: any[];
}

export class MessageEvent extends Event {
    #data: any;
    #origin: string;
    #lastEventId: string;
    #source: any;
    #ports: any[];
    constructor(type: string, options?: MessageEventInit) {
        if (arguments.length === 0) {
            throw new TypeError('The "type" argument must be specified');
        }
        super(type, options);
        this.#data = options?.data ?? null;
        this.#origin = options?.origin ?? '';
        this.#lastEventId = options?.lastEventId ?? '';
        this.#source = options?.source ?? null;
        this.#ports = options?.ports ?? [];
    }
    get data(): any { return this.#data; }
    get origin(): string { return this.#origin; }
    get lastEventId(): string { return this.#lastEventId; }
    get source(): any { return this.#source; }
    get ports(): any[] { return this.#ports; }
}

export interface CloseEventInit extends EventInit {
    wasClean?: boolean;
    code?: number;
    reason?: string;
}

export class CloseEvent extends Event {
    #wasClean: boolean;
    #code: number;
    #reason: string;
    constructor(type: string, options?: CloseEventInit) {
        if (arguments.length === 0) {
            throw new TypeError('The "type" argument must be specified');
        }
        super(type, options);
        this.#wasClean = options?.wasClean ?? false;
        this.#code = options?.code ?? 0;
        this.#reason = options?.reason ?? '';
    }
    get wasClean(): boolean { return this.#wasClean; }
    get code(): number { return this.#code; }
    get reason(): string { return this.#reason; }
}

export interface ErrorEventInit extends EventInit {
    message?: string;
    filename?: string;
    lineno?: number;
    colno?: number;
    error?: any;
}

export class ErrorEvent extends Event {
    #message: string;
    #filename: string;
    #lineno: number;
    #colno: number;
    #error: any;
    constructor(type: string, options?: ErrorEventInit) {
        if (arguments.length === 0) {
            throw new TypeError('The "type" argument must be specified');
        }
        super(type, options);
        this.#message = options?.message ?? '';
        this.#filename = options?.filename ?? '';
        this.#lineno = options?.lineno ?? 0;
        this.#colno = options?.colno ?? 0;
        this.#error = options?.error;
    }
    get message(): string { return this.#message; }
    get filename(): string { return this.#filename; }
    get lineno(): number { return this.#lineno; }
    get colno(): number { return this.#colno; }
    get error(): any { return this.#error; }
}
