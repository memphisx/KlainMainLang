// kml:global
// Node's `perf_hooks` and the `performance` global, ported from Node v24's
// lib/internal/perf/performance.js, usertiming.js, observe.js,
// performance_entry.js, histogram.js, event_loop_delay.js and timerify.js:
// user-timing marks and measures kept as entries, observers delivered
// asynchronously (after the entry, on a later turn of the loop), histograms and
// the event-loop-delay monitor. The clock is the runtime's monotonic one
// (lib/native.d.ts perfNow/perfTimeOrigin). A program that names one of the
// globals without declaring it imports this module.
//
// A histogram keeps its samples exactly, where Node's is an HDR histogram
// (3 significant figures): a percentile of large values can differ in its
// last digits. eventLoopUtilization and nodeTiming's startup milestones are
// not ported.
//
// kml:default-namespace — `import perf from 'perf_hooks'` reads this
// module's exports, as Node's default export carries them.

import { NodeError, NodeTypeError, NodeRangeError } from './internal_errors';

function received(value: any): string {
    if (value === null || value === undefined) return ' Received ' + String(value);
    if (typeof value === 'function') return ' Received function';
    if (typeof value === 'object') return ' Received an instance of Object';
    let shown = String(value);
    if (shown.length > 28) shown = shown.slice(0, 25) + '...';
    if (typeof value === 'string') shown = "'" + shown + "'";
    return ' Received type ' + typeof value + ' (' + shown + ')';
}

function invalidTimestamp(value: number): Error {
    return new NodeTypeError('ERR_PERFORMANCE_INVALID_TIMESTAMP', value + ' is not a valid timestamp');
}

function now(): number {
    return __kml_native.perfNow();
}

// ---- entries (performance_entry.js) ----

export type EntryType = 'dns' | 'function' | 'gc' | 'http' | 'http2' | 'mark' | 'measure' | 'net' | 'resource';

export class PerformanceEntry {
    readonly name: string;
    readonly entryType: EntryType;
    readonly startTime: number;
    readonly duration: number;
    protected _detail: any;

    constructor(name: string, entryType: EntryType, startTime: number, duration: number, detail: any) {
        this.name = name;
        this.entryType = entryType;
        this.startTime = startTime;
        this.duration = duration;
        this._detail = detail;
    }

    get detail(): any {
        return this._detail;
    }

    toJSON(): any {
        return {
            name: this.name,
            entryType: this.entryType,
            startTime: this.startTime,
            duration: this.duration,
            detail: this._detail,
        };
    }
}

export interface MarkOptions {
    detail?: unknown | undefined;
    startTime?: number | undefined;
}

export interface MeasureOptions {
    detail?: unknown | undefined;
    duration?: number | undefined;
    end?: number | string | undefined;
    start?: number | string | undefined;
}

// ---- user timing (usertiming.js) ----

const markTimings = new Map<string, number>();
const markEntries: PerformanceEntry[] = [];
const measureEntries: PerformanceEntry[] = [];

function getMark(name: any): number {
    if (typeof name === 'number') {
        if ((name as number) < 0) throw invalidTimestamp(name as number);
        return name as number;
    }
    const key = '' + name;
    const ts = markTimings.get(key);
    if (ts === undefined) {
        throw new DOMException('The "' + key + '" performance mark has not been set', 'SyntaxError');
    }
    return ts;
}

export class PerformanceMark extends PerformanceEntry {
    constructor(name: string, options?: MarkOptions) {
        const n = '' + name;
        let startTime = now();
        let detail: any = null;
        if (options !== undefined && options !== null) {
            if (typeof options !== 'object') {
                throw new NodeTypeError('ERR_INVALID_ARG_TYPE', 'The "options" argument must be of type object.' + received(options));
            }
            if (options.startTime !== undefined) {
                if (typeof options.startTime !== 'number') {
                    throw new NodeTypeError('ERR_INVALID_ARG_TYPE', 'The "startTime" argument must be of type number.' + received(options.startTime));
                }
                startTime = options.startTime;
            }
            if (startTime < 0) throw invalidTimestamp(startTime);
            detail = options.detail !== undefined && options.detail !== null ? structuredClone(options.detail) : null;
        }
        super(n, 'mark', startTime, 0, detail);
        markTimings.set(n, startTime);
    }
}

export class PerformanceMeasure extends PerformanceEntry {}

function measureInvalid(message: string): Error {
    return new NodeTypeError('ERR_PERFORMANCE_MEASURE_INVALID_OPTIONS', message);
}

function calculateStartDuration(startOrMeasureOptions: any, endMark: any): { start: number; duration: number } {
    const opts: any = startOrMeasureOptions ?? 0;
    let start: any = undefined;
    let end: any = undefined;
    let duration: any = undefined;
    let optionsValid = false;
    if (typeof opts === 'object') {
        start = opts.start;
        end = opts.end;
        duration = opts.duration;
        optionsValid = start !== undefined || end !== undefined;
    }
    if (optionsValid) {
        if (endMark !== undefined) throw measureInvalid('endMark must not be specified');
        if (start !== undefined && end !== undefined && duration !== undefined) {
            throw measureInvalid('Must not have options.start, options.end, and options.duration specified');
        }
    }
    let endTime: number;
    if (endMark !== undefined) endTime = getMark(endMark);
    else if (optionsValid && end !== undefined) endTime = getMark(end);
    else if (optionsValid && start !== undefined && duration !== undefined) endTime = getMark(start) + getMark(duration);
    else endTime = now();
    let startTime: number;
    if (typeof opts === 'string') startTime = getMark(opts);
    else if (optionsValid && start !== undefined) startTime = getMark(start);
    else if (optionsValid && duration !== undefined && end !== undefined) startTime = endTime - getMark(duration);
    else startTime = 0;
    return { start: startTime, duration: endTime - startTime };
}

// ---- observers (observe.js) ----

const kSupportedEntryTypes: EntryType[] = ['dns', 'function', 'gc', 'http', 'http2', 'mark', 'measure', 'net', 'resource'];

const observers: PerformanceObserver[] = [];
let pending: PerformanceObserver[] = [];
let isPending = false;

function queuePending(observer: PerformanceObserver): void {
    if (pending.indexOf(observer) < 0) pending.push(observer);
    if (isPending) return;
    isPending = true;
    setImmediate(() => {
        isPending = false;
        const list = pending;
        pending = [];
        for (const o of list) o._kmlDispatch();
    });
}

function enqueue(entry: PerformanceEntry): void {
    for (const o of observers) o._kmlMaybeBuffer(entry);
}

function bufferUserTiming(entry: PerformanceEntry): void {
    if (entry.entryType === 'mark') markEntries.push(entry);
    else measureEntries.push(entry);
}

export class PerformanceObserverEntryList {
    private entries: PerformanceEntry[];

    constructor(entries: PerformanceEntry[]) {
        this.entries = entries.slice().sort((a, b) => a.startTime - b.startTime);
    }

    getEntries(): PerformanceEntry[] {
        return this.entries.slice();
    }

    getEntriesByType(type: string): PerformanceEntry[] {
        const t = '' + type;
        return this.entries.filter((e) => e.entryType === t);
    }

    getEntriesByName(name: string, type?: string): PerformanceEntry[] {
        const n = '' + name;
        return this.entries.filter((e) => e.name === n && (type === undefined || e.entryType === '' + type));
    }
}

export interface PerformanceObserverInit {
    entryTypes?: ReadonlyArray<EntryType> | undefined;
    type?: EntryType | undefined;
    buffered?: boolean | undefined;
}

export type PerformanceObserverCallback = (list: PerformanceObserverEntryList, observer: PerformanceObserver) => void;

export class PerformanceObserver {
    private callback: PerformanceObserverCallback;
    private buffer: PerformanceEntry[] = [];
    private entryTypes: string[] = [];
    private typeForm: number = 0; // 0 none yet, 1 entryTypes, 2 type

    static get supportedEntryTypes(): ReadonlyArray<string> {
        return kSupportedEntryTypes.slice();
    }

    constructor(callback: PerformanceObserverCallback) {
        if (typeof callback !== 'function') {
            throw new NodeTypeError('ERR_INVALID_ARG_TYPE', 'The "callback" argument must be of type function.' + received(callback));
        }
        this.callback = callback;
    }

    observe(options?: PerformanceObserverInit): void {
        const o: any = options ?? {};
        if (typeof o !== 'object') {
            throw new NodeTypeError('ERR_INVALID_ARG_TYPE', 'The "options" argument must be of type object.' + received(o));
        }
        const entryTypes: any = o.entryTypes;
        const type: any = o.type;
        if (entryTypes === undefined && type === undefined) throw new NodeTypeError('ERR_MISSING_ARGS', 'The "options.entryTypes" or "options.type" argument must be specified');
        if (entryTypes != null && type != null) {
            throw new NodeTypeError('ERR_INVALID_ARG_VALUE', "The property 'options.entryTypes' can not be used with 'options.type'. Received " + String(entryTypes));
        }
        if (this.typeForm === 1 && type !== undefined) throw new NodeError('ERR_INVALID_ARG_VALUE', "The property 'options.type' is invalid for an observer observing entryTypes");
        if (this.typeForm === 2 && entryTypes !== undefined) throw new NodeError('ERR_INVALID_ARG_VALUE', "The property 'options.entryTypes' is invalid for an observer observing type");
        if (entryTypes !== undefined) {
            if (!Array.isArray(entryTypes)) {
                throw new NodeTypeError('ERR_INVALID_ARG_TYPE', 'The "options.entryTypes" property must be an instance of Array.' + received(entryTypes));
            }
            this.typeForm = 1;
            this.entryTypes = [];
            for (const t of entryTypes as any[]) {
                const s = '' + t;
                if (kSupportedEntryTypes.indexOf(s as EntryType) >= 0 && this.entryTypes.indexOf(s) < 0) this.entryTypes.push(s);
            }
        } else {
            const s = '' + type;
            if (kSupportedEntryTypes.indexOf(s as EntryType) < 0) return;
            this.typeForm = 2;
            if (this.entryTypes.indexOf(s) < 0) this.entryTypes.push(s);
            if (o.buffered) {
                const entries = s === 'mark' ? markEntries : s === 'measure' ? measureEntries : [];
                for (const e of entries) this.buffer.push(e);
                if (entries.length > 0) queuePending(this);
            }
        }
        if (this.entryTypes.length === 0) {
            this.disconnect();
            return;
        }
        if (observers.indexOf(this) < 0) observers.push(this);
    }

    disconnect(): void {
        const i = observers.indexOf(this);
        if (i >= 0) observers.splice(i, 1);
        const j = pending.indexOf(this);
        if (j >= 0) pending.splice(j, 1);
        this.buffer = [];
        this.entryTypes = [];
        this.typeForm = 0;
    }

    takeRecords(): PerformanceEntry[] {
        const list = this.buffer;
        this.buffer = [];
        return list;
    }

    _kmlMaybeBuffer(entry: PerformanceEntry): void {
        if (this.entryTypes.indexOf(entry.entryType) < 0) return;
        this.buffer.push(entry);
        queuePending(this);
    }

    _kmlDispatch(): void {
        const records = this.takeRecords();
        if (records.length === 0) return;
        this.callback(new PerformanceObserverEntryList(records), this);
    }
}

// ---- histograms (histogram.js) ----

export class Histogram {
    protected values: number[] = [];
    protected exceedsCount = 0;

    get count(): number {
        return this.values.length;
    }

    get countBigInt(): bigint {
        return BigInt(this.values.length);
    }

    get min(): number {
        if (this.values.length === 0) return 9223372036854776000;
        let m = this.values[0];
        for (const v of this.values) if (v < m) m = v;
        return m;
    }

    get minBigInt(): bigint {
        return BigInt(this.values.length === 0 ? 0 : this.min);
    }

    get max(): number {
        let m = 0;
        for (const v of this.values) if (v > m) m = v;
        return m;
    }

    get maxBigInt(): bigint {
        return BigInt(this.max);
    }

    get mean(): number {
        if (this.values.length === 0) return NaN;
        let sum = 0;
        for (const v of this.values) sum += v;
        return sum / this.values.length;
    }

    get stddev(): number {
        if (this.values.length === 0) return NaN;
        const mean = this.mean;
        let sq = 0;
        for (const v of this.values) sq += (v - mean) * (v - mean);
        return Math.sqrt(sq / this.values.length);
    }

    get exceeds(): number {
        return this.exceedsCount;
    }

    get exceedsBigInt(): bigint {
        return BigInt(this.exceedsCount);
    }

    percentile(percentile: number): number {
        if (typeof percentile !== 'number') {
            throw new NodeTypeError('ERR_INVALID_ARG_TYPE', 'The "percentile" argument must be of type number.' + received(percentile));
        }
        if (!(percentile > 0 && percentile <= 100)) {
            throw new NodeRangeError('ERR_OUT_OF_RANGE', 'The value of "percentile" is out of range. It must be > 0 && <= 100. Received ' + percentile);
        }
        if (this.values.length === 0) return 0;
        const sorted = this.values.slice().sort((a, b) => a - b);
        const rank = Math.ceil((percentile / 100) * sorted.length);
        return sorted[Math.max(0, rank - 1)];
    }

    percentileBigInt(percentile: number): bigint {
        return BigInt(this.percentile(percentile));
    }

    get percentiles(): Map<number, number> {
        const out = new Map<number, number>();
        if (this.values.length === 0) {
            out.set(100, 0);
            return out;
        }
        const sorted = this.values.slice().sort((a, b) => a - b);
        const steps = [0, 50, 75, 87.5, 93.75, 96.875, 98.4375, 99.21875, 100];
        for (const p of steps) {
            const rank = Math.ceil((p / 100) * sorted.length);
            out.set(p, sorted[Math.max(0, rank - 1)]);
        }
        return out;
    }

    get percentilesBigInt(): Map<number, bigint> {
        const out = new Map<number, bigint>();
        this.percentiles.forEach((v, k) => { out.set(k, BigInt(v)); });
        return out;
    }

    reset(): void {
        this.values = [];
        this.exceedsCount = 0;
    }

    toJSON(): any {
        const percentiles: any = {};
        this.percentiles.forEach((v, k) => { percentiles[k] = v; });
        return {
            count: this.count,
            min: this.min,
            max: this.max,
            mean: this.mean,
            exceeds: this.exceeds,
            stddev: this.stddev,
            percentiles,
        };
    }
}

export interface CreateHistogramOptions {
    lowest?: number | bigint | undefined;
    highest?: number | bigint | undefined;
    figures?: number | undefined;
}

export class RecordableHistogram extends Histogram {
    private lowest: number;
    private highest: number;
    private prevDelta = 0;

    constructor(lowest: number, highest: number) {
        super();
        this.lowest = lowest;
        this.highest = highest;
    }

    record(val: number | bigint): void {
        const v = typeof val === 'bigint' ? Number(val as bigint) : val as number;
        if (typeof v !== 'number' || !Number.isInteger(v) || v < 1) {
            throw new NodeRangeError('ERR_OUT_OF_RANGE', 'The value of "val" is out of range. It must be >= 1 && <= 9007199254740991. Received ' + String(val));
        }
        if (v < this.lowest || v > this.highest) {
            this.exceedsCount++;
            return;
        }
        this.values.push(v);
    }

    recordDelta(): void {
        const t = Math.round(now() * 1e6);
        if (this.prevDelta > 0) {
            const d = t - this.prevDelta;
            if (d >= 1) this.values.push(d);
        }
        this.prevDelta = t;
    }

    add(other: RecordableHistogram): void {
        for (const v of other.values) this.values.push(v);
        this.exceedsCount += other.exceedsCount;
    }
}

export function createHistogram(options?: CreateHistogramOptions): RecordableHistogram {
    let lowest = 1;
    let highest = Number.MAX_SAFE_INTEGER;
    if (options !== undefined) {
        if (options.lowest !== undefined) lowest = Number(options.lowest);
        if (options.highest !== undefined) highest = Number(options.highest);
        if (lowest < 1) {
            throw new NodeRangeError('ERR_OUT_OF_RANGE', 'The value of "options.lowest" is out of range. It must be >= 1 && <= 9007199254740991. Received ' + lowest);
        }
        if (highest < 2 * lowest) {
            throw new NodeRangeError('ERR_OUT_OF_RANGE', 'The value of "options.highest" is out of range. It must be >= ' + (2 * lowest) + ' && <= 9007199254740991. Received ' + highest);
        }
    }
    return new RecordableHistogram(lowest, highest);
}

// ---- event loop delay (event_loop_delay.js) ----

export interface EventLoopMonitorOptions {
    resolution?: number | undefined;
}

export class IntervalHistogram extends Histogram {
    private resolution: number;
    private timer: NodeJS.Timeout | undefined = undefined;
    private last = 0;

    constructor(resolution: number) {
        super();
        this.resolution = resolution;
    }

    enable(): boolean {
        if (this.timer !== undefined) return false;
        this.last = now();
        this.timer = setInterval(() => {
            const t = now();
            // The delay past the interval the timer asked for, in ns.
            const delay = Math.round((t - this.last - this.resolution) * 1e6);
            this.last = t;
            this.values.push(delay >= 1 ? delay : 1);
        }, this.resolution);
        this.timer.unref();
        return true;
    }

    disable(): boolean {
        if (this.timer === undefined) return false;
        clearInterval(this.timer);
        this.timer = undefined;
        return true;
    }

    [Symbol.dispose](): void {
        this.disable();
    }
}

export function monitorEventLoopDelay(options?: EventLoopMonitorOptions): IntervalHistogram {
    let resolution = 10;
    if (options !== undefined) {
        if (typeof options !== 'object' || options === null) {
            throw new NodeTypeError('ERR_INVALID_ARG_TYPE', 'The "options" argument must be of type object.' + received(options));
        }
        if (options.resolution !== undefined) {
            if (typeof options.resolution !== 'number') {
                throw new NodeTypeError('ERR_INVALID_ARG_TYPE', 'The "options.resolution" property must be of type number.' + received(options.resolution));
            }
            if (options.resolution <= 0 || !Number.isInteger(options.resolution)) {
                throw new NodeRangeError('ERR_OUT_OF_RANGE', 'The value of "options.resolution" is out of range. It must be > 0 && <= 9007199254740991. Received ' + options.resolution);
            }
            resolution = options.resolution;
        }
    }
    return new IntervalHistogram(resolution);
}

// ---- Performance (performance.js) ----

export class Performance {
    get timeOrigin(): number {
        return __kml_native.perfTimeOrigin();
    }

    now(): number {
        return now();
    }

    mark(name: string, options?: MarkOptions): PerformanceMark {
        const m = new PerformanceMark(name, options);
        enqueue(m);
        bufferUserTiming(m);
        return m;
    }

    measure(name: string, startOrMeasureOptions?: string | MeasureOptions, endMark?: string): PerformanceMeasure {
        if (typeof name !== 'string') {
            throw new NodeTypeError('ERR_INVALID_ARG_TYPE', 'The "name" argument must be of type string.' + received(name));
        }
        const r = calculateStartDuration(startOrMeasureOptions, endMark);
        const o: any = startOrMeasureOptions;
        const detail = o !== undefined && o !== null && typeof o === 'object' && o.detail != null ? structuredClone(o.detail) : null;
        const m = new PerformanceMeasure(name, 'measure', r.start, r.duration, detail);
        enqueue(m);
        bufferUserTiming(m);
        return m;
    }

    clearMarks(name?: string): void {
        if (name !== undefined) {
            const n = '' + name;
            markTimings.delete(n);
            for (let i = markEntries.length - 1; i >= 0; i--) if (markEntries[i].name === n) markEntries.splice(i, 1);
            return;
        }
        markTimings.clear();
        markEntries.length = 0;
    }

    clearMeasures(name?: string): void {
        if (name !== undefined) {
            const n = '' + name;
            for (let i = measureEntries.length - 1; i >= 0; i--) if (measureEntries[i].name === n) measureEntries.splice(i, 1);
            return;
        }
        measureEntries.length = 0;
    }

    clearResourceTimings(name?: string): void {}

    getEntries(): PerformanceEntry[] {
        return markEntries.concat(measureEntries).sort((a, b) => a.startTime - b.startTime);
    }

    getEntriesByName(name: string, type?: EntryType): PerformanceEntry[] {
        const n = '' + name;
        return this.getEntries().filter((e) => e.name === n && (type === undefined || e.entryType === type));
    }

    getEntriesByType(type: EntryType): PerformanceEntry[] {
        if (type === 'mark') return markEntries.slice().sort((a, b) => a.startTime - b.startTime);
        if (type === 'measure') return measureEntries.slice().sort((a, b) => a.startTime - b.startTime);
        return [];
    }

    timerify<T extends (...args: any[]) => any>(fn: T): T {
        if (typeof fn !== 'function') {
            throw new NodeTypeError('ERR_INVALID_ARG_TYPE', 'The "fn" argument must be of type function.' + received(fn));
        }
        const name = (fn as any).name || 'anonymous';
        const timed = (...args: any[]): any => {
            const start = now();
            const result = Reflect.apply(fn, undefined, args);
            const entry = new PerformanceEntry(name, 'function', start, now() - start, args);
            enqueue(entry);
            return result;
        };
        return timed as any;
    }

    toJSON(): any {
        return { timeOrigin: this.timeOrigin };
    }
}

export const performance = new Performance(); // kml:global
