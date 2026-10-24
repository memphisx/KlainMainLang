// Node's timers, ported from Node v24's lib/internal/timers.js and
// lib/timers.js (TDD-00245): Timeout and Immediate objects that hold their own
// state, timers of one duration sharing a linked list, a priority queue of
// the lists ordered by expiry, and Node's processTimers / processImmediate
// run by the binding (timerssrc/timers.c, Node's src/timers.cc) when its one
// timer fires and in the check phase. Each timer runs in the async context
// frame it was created in (AsyncLocalStorage); async hooks are left out, as
// elsewhere in this runtime.
//
// The globals (setTimeout, …) are kml_timers.ts's; `timers` and
// `timers/promises` call the functions here.

import { NodeTypeError } from './internal_errors';
import { emitWarning } from './internal_process_warning';
import { inspect } from './internal_util_inspect';

// Timeout values > TIMEOUT_MAX are set to 1.
export const TIMEOUT_MAX = 2147483647;

const kRefed = Symbol('refed');
const kHasPrimitive = Symbol('kHasPrimitive');
const asyncIdSymbol = Symbol('asyncId');
const triggerIdSymbol = Symbol('triggerId');
const asyncContextFrame = Symbol('kAsyncContextFrame');

let nextAsyncId = 1;
let timerListId = Number.MIN_SAFE_INTEGER;
let nextExpiry = Infinity;
// timeoutInfo[0]: the referenced Timeouts.
let refCount = 0;
// immediateInfo[kCount] and [kRefCount].
let immediateCount = 0;
let immediateRefCount = 0;

let warnedNegativeNumber = false;
let warnedNotNumber = false;

// A node of the linked lists: a TimersList is a list's sentinel, a Timeout
// one of its members (Node's internal/linkedlist). _idleTimeout is a
// Timeout's, declared here so a Timeout's own properties keep Node's order.
export class TimerLink {
    _idleTimeout: number = -1;
    _idlePrev: TimerLink | null = null;
    _idleNext: TimerLink | null = null;
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

export function validateFunction(value: any, name: string): void {
    if (typeof value !== 'function') {
        throw NodeTypeError('ERR_INVALID_ARG_TYPE', 'The "' + name + '" argument must be of type function.' + received(value));
    }
}

function initAsyncResource(resource: any): void {
    resource[asyncIdSymbol] = ++nextAsyncId;
    resource[triggerIdSymbol] = 1;
    resource[asyncContextFrame] = __kml_native.asyncContextGet();
}

export class Timeout extends TimerLink {
    _idleStart: number | null;
    _onTimeout: ((...args: any[]) => void) | null;
    _timerArgs: any[] | undefined;
    _repeat: number | null;
    _destroyed: boolean;
    [kRefed]: boolean = false;
    [kHasPrimitive]: boolean = false;
    [asyncIdSymbol]: number = 0;
    [triggerIdSymbol]: number = 0;
    [asyncContextFrame]: any = undefined;

    constructor(callback: ((...args: any[]) => void) | null, after: any, args: any[] | undefined, isRepeat: boolean, isRefed: boolean) {
        super();
        let ms: number = after === undefined ? 1 : after * 1; // Coalesce to number or NaN
        if (!(ms >= 1 && ms <= TIMEOUT_MAX)) {
            if (ms > TIMEOUT_MAX) {
                emitWarning(ms + ' does not fit into a 32-bit signed integer.\nTimeout duration was set to 1.', 'TimeoutOverflowWarning');
            } else if (ms < 0 && !warnedNegativeNumber) {
                warnedNegativeNumber = true;
                emitWarning(ms + ' is a negative number.\nTimeout duration was set to 1.', 'TimeoutNegativeWarning');
            } else if (Number.isNaN(ms) && !warnedNotNumber) {
                warnedNotNumber = true;
                emitWarning(ms + ' is not a number.\nTimeout duration was set to 1.', 'TimeoutNaNWarning');
            }
            ms = 1; // Schedule on next tick, follows browser behavior
        }
        this._idleTimeout = ms;
        this._idlePrev = this;
        this._idleNext = this;
        this._idleStart = null;
        this._onTimeout = callback;
        this._timerArgs = args;
        this._repeat = isRepeat ? ms : null;
        this._destroyed = false;
        if (isRefed) incRefCount();
        this[kRefed] = isRefed;
        this[kHasPrimitive] = false;
        initAsyncResource(this);
    }

    // Only one level: the lists the links lead to show as [TimersList].
    [inspect.custom](depth: number, options: any): any {
        const opts: any = {};
        const ok = Object.keys(options);
        for (let i = 0; i < ok.length; i++) opts[ok[i]] = options[ok[i]];
        opts.depth = 0;
        opts.customInspect = false;
        return inspect(this, opts);
    }

    refresh(): this {
        if (this[kRefed]) active(this);
        else unrefActive(this);
        return this;
    }

    unref(): this {
        if (this[kRefed]) {
            this[kRefed] = false;
            if (!this._destroyed) decRefCount();
        }
        return this;
    }

    ref(): this {
        if (!this[kRefed]) {
            this[kRefed] = true;
            if (!this._destroyed) incRefCount();
        }
        return this;
    }

    hasRef(): boolean {
        return this[kRefed];
    }

    close(): this {
        clearTimeout(this);
        return this;
    }

    [Symbol.toPrimitive](): number {
        const id: number = this[asyncIdSymbol];
        if (!this[kHasPrimitive]) {
            this[kHasPrimitive] = true;
            knownTimersById.set(id, this);
        }
        return id;
    }
}

class TimersList extends TimerLink {
    expiry: number;
    id: number;
    msecs: number;
    priorityQueuePosition: number;

    constructor(expiry: number, msecs: number) {
        super();
        this._idleNext = this;
        this._idlePrev = this;
        this.expiry = expiry;
        this.id = timerListId++;
        this.msecs = msecs;
        this.priorityQueuePosition = 0;
    }
}

// Node's internal/linkedlist over the _idlePrev/_idleNext links.
function listPeek(list: TimerLink): Timeout | null {
    if (list._idlePrev === list) return null;
    return list._idlePrev as Timeout;
}

function listRemove(item: TimerLink): void {
    if (item._idleNext) item._idleNext._idlePrev = item._idlePrev;
    if (item._idlePrev) item._idlePrev._idleNext = item._idleNext;
    item._idleNext = null;
    item._idlePrev = null;
}

function listAppend(list: TimerLink, item: TimerLink): void {
    if (item._idleNext || item._idlePrev) listRemove(item);
    // Items are linked with _idleNext -> _idlePrev, oldest at the head.
    item._idleNext = list._idleNext;
    (list._idleNext as TimerLink)._idlePrev = item;
    item._idlePrev = list;
    list._idleNext = item;
}

function listIsEmpty(list: TimerLink): boolean {
    return list._idleNext === list;
}

// Node's internal/priority_queue: a binary min-heap (1-based) of the lists,
// by expiry and then by id.
const heap: TimersList[] = [];
let heapSize = 0;

function compareTimersLists(a: TimersList, b: TimersList): number {
    const expiryDiff = a.expiry - b.expiry;
    if (expiryDiff === 0) return a.id - b.id;
    return expiryDiff;
}

function heapSet(pos: number, item: TimersList): void {
    while (heap.length <= pos) heap.push(item);
    heap[pos] = item;
    item.priorityQueuePosition = pos;
}

function heapInsert(value: TimersList): void {
    heapSize++;
    heapSet(heapSize, value);
    percolateUp(heapSize);
}

function heapPeek(): TimersList | null {
    return heapSize > 0 ? heap[1] : null;
}

function percolateDown(pos: number): void {
    const size = heapSize;
    const hsize = size >> 1;
    const item = heap[pos];
    while (pos <= hsize) {
        let child = pos << 1;
        const nextChild = child + 1;
        let childItem = heap[child];
        if (nextChild <= size && compareTimersLists(heap[nextChild], childItem) < 0) {
            child = nextChild;
            childItem = heap[nextChild];
        }
        if (compareTimersLists(item, childItem) <= 0) break;
        heapSet(pos, childItem);
        pos = child;
    }
    heapSet(pos, item);
}

function percolateUp(pos: number): void {
    const item = heap[pos];
    while (pos > 1) {
        const parent = heap[pos >> 1];
        if (compareTimersLists(parent, item) <= 0) break;
        heapSet(pos, parent);
        pos = pos >> 1;
    }
    heapSet(pos, item);
}

function heapRemoveAt(pos: number): void {
    const size = --heapSize;
    heap[pos] = heap[size + 1];
    if (size > 0 && pos <= size) {
        heap[pos].priorityQueuePosition = pos;
        if (pos > 1 && compareTimersLists(heap[pos >> 1], heap[pos]) > 0) percolateUp(pos);
        else percolateDown(pos);
    }
}

function heapShift(): void {
    if (heapSize === 0) return;
    heapRemoveAt(1);
}

// The lists by duration, and the timers user code has turned into numbers.
const timerListMap = new Map<number, TimersList>();
const knownTimersById = new Map<number, Timeout>();

function incRefCount(): void {
    if (refCount++ === 0) __kml_native.timersToggleRef(1);
}

function decRefCount(): void {
    if (--refCount === 0) __kml_native.timersToggleRef(0);
}

// Schedule or re-schedule a timer.
export function active(item: Timeout): void {
    insertGuarded(item, true);
}

// Internal APIs that need timeouts use unrefActive() so that they do not
// keep the process open.
export function unrefActive(item: Timeout): void {
    insertGuarded(item, false);
}

function insertGuarded(item: Timeout, refed: boolean): void {
    const msecs = item._idleTimeout;
    if (msecs < 0) return;
    insert(item, msecs, __kml_native.timersNow());
    const isDestroyed = item._destroyed;
    if (isDestroyed || !item[asyncIdSymbol]) {
        item._destroyed = false;
        initAsyncResource(item);
    }
    if (isDestroyed) {
        if (refed) incRefCount();
    } else if (refed === !item[kRefed]) {
        if (refed) incRefCount();
        else decRefCount();
    }
    item[kRefed] = refed;
}

export function insert(item: Timeout, after: number, start: number): void {
    // Truncate so that accuracy of sub-millisecond timers is not assumed.
    const msecs = Math.trunc(after);
    item._idleStart = start;
    let list = timerListMap.get(msecs);
    if (list === undefined) {
        const expiry = start + msecs;
        list = new TimersList(expiry, msecs);
        timerListMap.set(msecs, list);
        heapInsert(list);
        if (nextExpiry > expiry) {
            __kml_native.timersSchedule(msecs);
            nextExpiry = expiry;
        }
    }
    listAppend(list, item);
}

// Remove a timer: cancel the timeout and reset the relevant properties.
function unenroll(item: Timeout): void {
    if (item._destroyed) return;
    item._destroyed = true;
    if (item[kHasPrimitive]) knownTimersById.delete(item[asyncIdSymbol]);
    listRemove(item);
    // Only referenced lists are deleted: unreferenced ones are likely to be
    // recreated shortly after (http).
    if (item[kRefed]) {
        const msecs = Math.trunc(item._idleTimeout);
        const list = timerListMap.get(msecs);
        if (list !== undefined && listIsEmpty(list)) {
            heapRemoveAt(list.priorityQueuePosition);
            timerListMap.delete(list.msecs);
        }
        decRefCount();
    }
    // If active is called later, it must not insert again.
    item._idleTimeout = -1;
}

// A linked list of setImmediate() requests.
class ImmediateList {
    head: Immediate | null = null;
    tail: Immediate | null = null;

    append(item: Immediate): void {
        if (this.tail !== null) {
            this.tail._idleNext = item;
            item._idlePrev = this.tail;
        } else {
            this.head = item;
        }
        this.tail = item;
    }

    remove(item: Immediate): void {
        if (item._idleNext) item._idleNext._idlePrev = item._idlePrev;
        if (item._idlePrev) item._idlePrev._idleNext = item._idleNext;
        if (item === this.head) this.head = item._idleNext;
        if (item === this.tail) this.tail = item._idlePrev;
        item._idleNext = null;
        item._idlePrev = null;
    }
}

const immediateQueue = new ImmediateList();
// Immediates still to run after one threw.
const outstandingQueue = new ImmediateList();

export class Immediate {
    _idleNext: Immediate | null;
    _idlePrev: Immediate | null;
    _onImmediate: ((...args: any[]) => void) | null;
    _argv: any[] | undefined;
    _destroyed: boolean;
    [kRefed]: boolean | null = false;
    [asyncIdSymbol]: number = 0;
    [triggerIdSymbol]: number = 0;
    [asyncContextFrame]: any = undefined;

    constructor(callback: (...args: any[]) => void, args: any[] | undefined) {
        this._idleNext = null;
        this._idlePrev = null;
        this._onImmediate = callback;
        this._argv = args;
        this._destroyed = false;
        this[kRefed] = false;
        initAsyncResource(this);
        this.ref();
        immediateCount++;
        __kml_native.immediateCount(immediateCount);
        immediateQueue.append(this);
    }

    ref(): this {
        if (this[kRefed] === false) {
            this[kRefed] = true;
            if (immediateRefCount++ === 0) __kml_native.immediateToggleRef(1);
        }
        return this;
    }

    unref(): this {
        if (this[kRefed] === true) {
            this[kRefed] = false;
            if (--immediateRefCount === 0) __kml_native.immediateToggleRef(0);
        }
        return this;
    }

    hasRef(): boolean {
        return !!this[kRefed];
    }
}

function processImmediate(): void {
    const queue = outstandingQueue.head !== null ? outstandingQueue : immediateQueue;
    let immediate = queue.head;
    // Clear the linked list early in case new setImmediate() calls occur
    // while immediate callbacks are executed.
    if (queue !== outstandingQueue) {
        queue.head = null;
        queue.tail = null;
    }
    let prevImmediate: Immediate | null = null;
    let ranAtLeastOneImmediate = false;
    while (immediate !== null) {
        if (ranAtLeastOneImmediate) __kml_native.runMicrotasks();
        else ranAtLeastOneImmediate = true;
        // This Immediate may have been cleared while the microtasks above
        // ran: the previous one's _idleNext was not.
        if (immediate._destroyed) {
            immediate = prevImmediate !== null ? prevImmediate._idleNext : null;
            outstandingQueue.head = immediate;
            continue;
        }
        immediate._destroyed = true;
        immediateCount--;
        __kml_native.immediateCount(immediateCount);
        if (immediate[kRefed]) {
            if (--immediateRefCount === 0) __kml_native.immediateToggleRef(0);
        }
        immediate[kRefed] = null;
        prevImmediate = immediate;
        const priorContextFrame = __kml_native.asyncContextGet();
        __kml_native.asyncContextSet(immediate[asyncContextFrame]);
        try {
            // Called as Node calls it, a method of the immediate.
            const argv = immediate._argv;
            if (argv === undefined) immediate._onImmediate!();
            else immediate._onImmediate!(...argv);
        } finally {
            immediate._onImmediate = null;
            immediate = immediate._idleNext;
            outstandingQueue.head = immediate;
        }
        __kml_native.asyncContextSet(priorContextFrame);
    }
    if (queue === outstandingQueue) outstandingQueue.head = null;
}

function processTimers(now: number): number {
    nextExpiry = Infinity;
    let list: TimersList | null;
    let ranAtLeastOneList = false;
    while ((list = heapPeek()) !== null) {
        if (list.expiry > now) {
            nextExpiry = list.expiry;
            return refCount > 0 ? nextExpiry : -nextExpiry;
        }
        if (ranAtLeastOneList) __kml_native.runMicrotasks();
        else ranAtLeastOneList = true;
        listOnTimeout(list, now);
    }
    return 0;
}

function listOnTimeout(list: TimersList, now: number): void {
    const msecs = list.msecs;
    let ranAtLeastOneTimer = false;
    let timer: Timeout | null;
    while ((timer = listPeek(list)) !== null) {
        const diff = now - (timer._idleStart as number);
        // Too early for this timer: there are more scheduled for later.
        if (diff < msecs) {
            list.expiry = Math.max((timer._idleStart as number) + msecs, now + 1);
            list.id = timerListId++;
            percolateDown(1);
            return;
        }
        if (ranAtLeastOneTimer) __kml_native.runMicrotasks();
        else ranAtLeastOneTimer = true;
        listRemove(timer);
        const asyncId: number = timer[asyncIdSymbol];
        if (!timer._onTimeout) {
            if (!timer._destroyed) {
                timer._destroyed = true;
                if (timer[kHasPrimitive]) knownTimersById.delete(asyncId);
                if (timer[kRefed]) refCount--;
            }
            continue;
        }
        const priorContextFrame = __kml_native.asyncContextGet();
        __kml_native.asyncContextSet(timer[asyncContextFrame]);
        let start = 0;
        if (timer._repeat) start = __kml_native.timersNow();
        try {
            // Called as Node calls it, a method of the timer.
            const args = timer._timerArgs;
            if (args === undefined) timer._onTimeout!();
            else timer._onTimeout!(...args);
        } finally {
            if (timer._repeat && timer._idleTimeout !== -1) {
                timer._idleTimeout = timer._repeat;
                insert(timer, timer._idleTimeout, start);
            } else if (!timer._idleNext && !timer._idlePrev && !timer._destroyed) {
                timer._destroyed = true;
                if (timer[kHasPrimitive]) knownTimersById.delete(asyncId);
                if (timer[kRefed]) refCount--;
            }
        }
        __kml_native.asyncContextSet(priorContextFrame);
    }
    // The list is empty: drop it, unless it was removed and recreated since.
    if (list === timerListMap.get(msecs)) {
        timerListMap.delete(msecs);
        heapShift();
    }
}

__kml_native.timersSetup(
    (now: number, unused: number): void => { __kml_native.timersNext(processTimers(now)); },
    (unused: number, unused2: number): void => { processImmediate(); },
);

// ---- lib/timers.js ----

export function setTimeout(callback: (...args: any[]) => void, after?: any, ...args: any[]): Timeout {
    validateFunction(callback, 'callback');
    const timeout = new Timeout(callback, after, args.length > 0 ? args : undefined, false, true);
    insert(timeout, timeout._idleTimeout, __kml_native.timersNow());
    return timeout;
}

export function setInterval(callback: (...args: any[]) => void, repeat?: any, ...args: any[]): Timeout {
    validateFunction(callback, 'callback');
    const timeout = new Timeout(callback, repeat, args.length > 0 ? args : undefined, true, true);
    insert(timeout, timeout._idleTimeout, __kml_native.timersNow());
    return timeout;
}

// A timer that does not keep the process open (setUnrefTimeout).
export function setUnrefTimeout(callback: (...args: any[]) => void, after: number): Timeout {
    validateFunction(callback, 'callback');
    const timer = new Timeout(callback, after, undefined, false, false);
    insert(timer, timer._idleTimeout, __kml_native.timersNow());
    return timer;
}

export function clearTimeout(timer: any): void {
    if (timer instanceof Timeout) {
        if (timer._onTimeout) {
            timer._onTimeout = null;
            unenroll(timer);
        }
        return;
    }
    if (typeof timer === 'number' || typeof timer === 'string') {
        const timerInstance = knownTimersById.get(Number(timer));
        if (timerInstance !== undefined) {
            timerInstance._onTimeout = null;
            unenroll(timerInstance);
        }
    }
}

export function clearInterval(timer: any): void {
    clearTimeout(timer);
}

export function setImmediate(callback: (...args: any[]) => void, ...args: any[]): Immediate {
    validateFunction(callback, 'callback');
    return new Immediate(callback, args.length > 0 ? args : undefined);
}

export function clearImmediate(immediate: any): void {
    if (!(immediate instanceof Immediate) || !immediate._onImmediate || immediate._destroyed) return;
    immediateCount--;
    __kml_native.immediateCount(immediateCount);
    immediate._destroyed = true;
    if (immediate[kRefed] && --immediateRefCount === 0) __kml_native.immediateToggleRef(0);
    immediate[kRefed] = null;
    immediate._onImmediate = null;
    immediateQueue.remove(immediate);
}

// The forms timers/promises arms its promise timers with.
export function startTimeout(fn: () => void, ms: number): Timeout {
    return setTimeout(fn, ms);
}

export function stopTimeout(timer: Timeout): void {
    clearTimeout(timer);
}

export function startInterval(fn: () => void, ms: number): Timeout {
    return setInterval(fn, ms);
}

export function stopInterval(timer: Timeout): void {
    clearInterval(timer);
}

export function startImmediate(fn: () => void): Immediate {
    return setImmediate(fn);
}

export function stopImmediate(immediate: Immediate): void {
    clearImmediate(immediate);
}
