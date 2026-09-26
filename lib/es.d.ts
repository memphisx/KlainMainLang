/*
 * The ECMAScript builtin surface this compiler implements, as declarations
 * the checker reads (TDD-00230 P3.1). Written for this compiler: each
 * signature is one it compiles, in a form its checker models. A name and
 * member set the real TypeScript library has but this file lacks is not an
 * error here; the checker treats these types' member lists as incomplete.
 */

interface Object {
    constructor: Function;
    toString(): string;
    toLocaleString(): string;
    valueOf(): Object;
    hasOwnProperty(v: string): boolean;
    isPrototypeOf(v: Object): boolean;
    propertyIsEnumerable(v: string): boolean;
}

interface ObjectConstructor {
    readonly prototype: Object;
    keys(o: {}): string[];
    values(o: {}): any[];
    entries(o: {}): [string, any][];
    assign(target: any, ...sources: any[]): any;
    freeze<T>(o: T): T;
    isFrozen(o: any): boolean;
    seal<T>(o: T): T;
    isSealed(o: any): boolean;
    getPrototypeOf(o: any): any;
    setPrototypeOf(o: any, proto: object | null): any;
    create(o: object | null, properties?: any): any;
    getOwnPropertyNames(o: any): string[];
    is(value1: any, value2: any): boolean;
    fromEntries(entries: Iterable<readonly any[]>): any;
}

declare var Object: ObjectConstructor;

interface Function {
    readonly name: string;
    readonly length: number;
    apply(this: Function, thisArg: any, argArray?: any): any;
    call(this: Function, thisArg: any, ...argArray: any[]): any;
    bind(this: Function, thisArg: any, ...argArray: any[]): any;
    toString(): string;
}

interface String {
    readonly length: number;
    toString(): string;
    /** @lower __kml_String_charAt @link string */
    charAt(pos: number): string;
    /** @lower __kml_String_charCodeAt @link string */
    charCodeAt(index: number): number;
    /** @lower __kml_String_codePointAt @link string */
    codePointAt(pos: number): number | undefined;
    /** @lower __kml_String_concat @link string */
    concat(...strings: string[]): string;
    /** @lower __kml_String_indexOf @link string */
    indexOf(searchString: string, position?: number): number;
    /** @lower __kml_String_lastIndexOf @link string */
    lastIndexOf(searchString: string, position?: number): number;
    /** @lower __kml_String_includes @link string */
    includes(searchString: string, position?: number): boolean;
    /** @lower __kml_String_startsWith @link string */
    startsWith(searchString: string, position?: number): boolean;
    /** @lower __kml_String_endsWith @link string */
    endsWith(searchString: string, endPosition?: number): boolean;
    localeCompare(that: string, locales?: string | string[], options?: any): number;
    normalize(form?: string): string;
    /** @lower __kml_String_padStart @link string */
    padStart(maxLength: number, fillString?: string): string;
    /** @lower __kml_String_padEnd @link string */
    padEnd(maxLength: number, fillString?: string): string;
    /** @lower __kml_String_repeat @link string */
    repeat(count: number): string;
    replace(searchValue: string | RegExp, replaceValue: string): string;
    replace(searchValue: string | RegExp, replacer: (substring: string, ...args: any[]) => string): string;
    replaceAll(searchValue: string | RegExp, replaceValue: string): string;
    replaceAll(searchValue: string | RegExp, replacer: (substring: string, ...args: any[]) => string): string;
    search(regexp: string | RegExp): number;
    /** @lower __kml_String_slice @link string */
    slice(start?: number, end?: number): string;
    /** @lower __kml_String_split @link string */
    split(separator: string, limit?: number): string[];
    split(separator: RegExp, limit?: number): string[];
    split(separator: string | RegExp, limit?: number): string[];
    /** @lower __kml_String_substring @link string */
    substring(start: number, end?: number): string;
    /** @lower __kml_String_substr @link string */
    substr(from: number, length?: number): string;
    /** @lower __kml_String_toLowerCase @link casemap */
    toLowerCase(): string;
    /** @lower __kml_String_toUpperCase @link casemap */
    toUpperCase(): string;
    toLocaleLowerCase(locales?: string | string[]): string;
    toLocaleUpperCase(locales?: string | string[]): string;
    /** @lower __kml_trim */
    trim(): string;
    /** @lower __kml_trim_start */
    trimStart(): string;
    /** @lower __kml_trim_end */
    trimEnd(): string;
    /** @lower __kml_String_at @link string */
    at(index: number): string | undefined;
    valueOf(): string;
}

interface StringConstructor {
    new (value?: any): String;
    (value?: any): string;
    readonly prototype: String;
    fromCharCode(...codes: number[]): string;
    fromCodePoint(...codePoints: number[]): string;
    raw(template: { raw: readonly string[] }, ...substitutions: any[]): string;
}

declare var String: StringConstructor;

interface Symbol {
    toString(): string;
    valueOf(): symbol;
    readonly description: string | undefined;
}

interface SymbolConstructor {
    readonly prototype: Symbol;
    (description?: string | number): symbol;
    for(key: string): symbol;
    keyFor(sym: symbol): string | undefined;
    readonly iterator: unique symbol;
    readonly asyncIterator: unique symbol;
}

declare var Symbol: SymbolConstructor;

interface Boolean {
    valueOf(): boolean;
}

interface BooleanConstructor {
    new (value?: any): Boolean;
    <T>(value?: T): boolean;
    readonly prototype: Boolean;
}

declare var Boolean: BooleanConstructor;

interface Number {
    /** @lower __kml_Number_toString @link number */
    toString(radix?: number): string;
    /** @lower __kml_Number_toFixed @link number */
    toFixed(fractionDigits?: number): string;
    /** @lower __kml_Number_toExponential @link number */
    toExponential(fractionDigits?: number): string;
    /** @lower __kml_Number_toPrecision @link number */
    toPrecision(precision?: number): string;
    valueOf(): number;
}

interface NumberConstructor {
    new (value?: any): Number;
    (value?: any): number;
    readonly prototype: Number;
    readonly MAX_VALUE: number;
    readonly MIN_VALUE: number;
    readonly NaN: number;
    readonly NEGATIVE_INFINITY: number;
    readonly POSITIVE_INFINITY: number;
    readonly EPSILON: number;
    readonly MAX_SAFE_INTEGER: number;
    readonly MIN_SAFE_INTEGER: number;
    isFinite(number: unknown): boolean;
    isInteger(number: unknown): boolean;
    isNaN(number: unknown): boolean;
    isSafeInteger(number: unknown): boolean;
    parseFloat(string: string): number;
    parseInt(string: string, radix?: number): number;
}

declare var Number: NumberConstructor;

// A method with `@lower f` compiles to a call of the libm function f, whose
// result is the one ECMAScript's Math specifies (TDD-00230 P3.2); the others
// have JavaScript semantics of their own (Math.round's ties, pow's NaN
// cases, …) and keep their own lowering.
interface Math {
    readonly E: number;
    readonly LN10: number;
    readonly LN2: number;
    readonly LOG2E: number;
    readonly LOG10E: number;
    readonly PI: number;
    readonly SQRT1_2: number;
    readonly SQRT2: number;
    abs(x: number): number;
    /** @lower acos @link m */
    acos(x: number): number;
    /** @lower acosh @link m */
    acosh(x: number): number;
    /** @lower asin @link m */
    asin(x: number): number;
    /** @lower asinh @link m */
    asinh(x: number): number;
    /** @lower atan @link m */
    atan(x: number): number;
    /** @lower atanh @link m */
    atanh(x: number): number;
    /** @lower atan2 @link m */
    atan2(y: number, x: number): number;
    cbrt(x: number): number;
    ceil(x: number): number;
    clz32(x: number): number;
    /** @lower cos @link m */
    cos(x: number): number;
    /** @lower cosh @link m */
    cosh(x: number): number;
    /** @lower exp @link m */
    exp(x: number): number;
    /** @lower expm1 @link m */
    expm1(x: number): number;
    floor(x: number): number;
    fround(x: number): number;
    hypot(...values: number[]): number;
    imul(x: number, y: number): number;
    /** @lower log @link m */
    log(x: number): number;
    /** @lower log10 @link m */
    log10(x: number): number;
    /** @lower log1p @link m */
    log1p(x: number): number;
    /** @lower log2 @link m */
    log2(x: number): number;
    max(...values: number[]): number;
    min(...values: number[]): number;
    pow(x: number, y: number): number;
    random(): number;
    round(x: number): number;
    sign(x: number): number;
    /** @lower sin @link m */
    sin(x: number): number;
    /** @lower sinh @link m */
    sinh(x: number): number;
    /** @lower sqrt @link m */
    sqrt(x: number): number;
    /** @lower tan @link m */
    tan(x: number): number;
    /** @lower tanh @link m */
    tanh(x: number): number;
    trunc(x: number): number;
}

declare var Math: Math;

interface JSON {
    parse(text: string, reviver?: (this: any, key: string, value: any) => any): any;
    stringify(value: any, replacer?: (this: any, key: string, value: any) => any, space?: string | number): string;
    stringify(value: any, replacer?: (number | string)[] | null, space?: string | number): string;
}

declare var JSON: JSON;

interface TemplateStringsArray extends ReadonlyArray<string> {
    readonly raw: readonly string[];
}

interface ReadonlyArray<T> {
    readonly length: number;
    toString(): string;
    join(separator?: string): string;
    slice(start?: number, end?: number): T[];
    indexOf(searchElement: T, fromIndex?: number): number;
    lastIndexOf(searchElement: T, fromIndex?: number): number;
    includes(searchElement: T, fromIndex?: number): boolean;
    every<S extends T>(predicate: (value: T, index: number, array: readonly T[]) => value is S, thisArg?: any): this is readonly S[];
    every(predicate: (value: T, index: number, array: readonly T[]) => unknown, thisArg?: any): boolean;
    some(predicate: (value: T, index: number, array: readonly T[]) => unknown, thisArg?: any): boolean;
    forEach(callbackfn: (value: T, index: number, array: readonly T[]) => void, thisArg?: any): void;
    map<U>(callbackfn: (value: T, index: number, array: readonly T[]) => U, thisArg?: any): U[];
    filter<S extends T>(predicate: (value: T, index: number, array: readonly T[]) => value is S, thisArg?: any): S[];
    filter(predicate: (value: T, index: number, array: readonly T[]) => unknown, thisArg?: any): T[];
    find<S extends T>(predicate: (value: T, index: number, obj: readonly T[]) => value is S, thisArg?: any): S | undefined;
    find(predicate: (value: T, index: number, obj: readonly T[]) => unknown, thisArg?: any): T | undefined;
    findIndex(predicate: (value: T, index: number, obj: readonly T[]) => unknown, thisArg?: any): number;
    at(index: number): T | undefined;
}

interface Array<T> {
    length: number;
    toString(): string;
    pop(): T | undefined;
    push(...items: T[]): number;
    concat(...items: (T | T[])[]): T[];
    join(separator?: string): string;
    reverse(): T[];
    shift(): T | undefined;
    slice(start?: number, end?: number): T[];
    sort(compareFn?: (a: T, b: T) => number): this;
    splice(start: number, deleteCount?: number, ...items: T[]): T[];
    unshift(...items: T[]): number;
    indexOf(searchElement: T, fromIndex?: number): number;
    lastIndexOf(searchElement: T, fromIndex?: number): number;
    includes(searchElement: T, fromIndex?: number): boolean;
    every<S extends T>(predicate: (value: T, index: number, array: T[]) => value is S, thisArg?: any): this is S[];
    every(predicate: (value: T, index: number, array: T[]) => unknown, thisArg?: any): boolean;
    some(predicate: (value: T, index: number, array: T[]) => unknown, thisArg?: any): boolean;
    forEach(callbackfn: (value: T, index: number, array: T[]) => void, thisArg?: any): void;
    map<U>(callbackfn: (value: T, index: number, array: T[]) => U, thisArg?: any): U[];
    filter<S extends T>(predicate: (value: T, index: number, array: T[]) => value is S, thisArg?: any): S[];
    filter(predicate: (value: T, index: number, array: T[]) => unknown, thisArg?: any): T[];
    reduce(callbackfn: (previousValue: T, currentValue: T, currentIndex: number, array: T[]) => T): T;
    reduce(callbackfn: (previousValue: T, currentValue: T, currentIndex: number, array: T[]) => T, initialValue: T): T;
    reduce<U>(callbackfn: (previousValue: U, currentValue: T, currentIndex: number, array: T[]) => U, initialValue: U): U;
    reduceRight(callbackfn: (previousValue: T, currentValue: T, currentIndex: number, array: T[]) => T): T;
    reduceRight(callbackfn: (previousValue: T, currentValue: T, currentIndex: number, array: T[]) => T, initialValue: T): T;
    reduceRight<U>(callbackfn: (previousValue: U, currentValue: T, currentIndex: number, array: T[]) => U, initialValue: U): U;
    find<S extends T>(predicate: (value: T, index: number, obj: T[]) => value is S, thisArg?: any): S | undefined;
    find(predicate: (value: T, index: number, obj: T[]) => unknown, thisArg?: any): T | undefined;
    findIndex(predicate: (value: T, index: number, obj: T[]) => unknown, thisArg?: any): number;
    findLast<S extends T>(predicate: (value: T, index: number, array: T[]) => value is S, thisArg?: any): S | undefined;
    findLast(predicate: (value: T, index: number, obj: T[]) => unknown, thisArg?: any): T | undefined;
    findLastIndex(predicate: (value: T, index: number, obj: T[]) => unknown, thisArg?: any): number;
    fill(value: T, start?: number, end?: number): this;
    copyWithin(target: number, start: number, end?: number): this;
    flatMap<U>(callback: (value: T, index: number, array: T[]) => U | readonly U[], thisArg?: any): U[];
    at(index: number): T | undefined;
    toReversed(): T[];
    toSorted(compareFn?: (a: T, b: T) => number): T[];
    toSpliced(start: number, deleteCount?: number, ...items: T[]): T[];
    with(index: number, value: T): T[];
}

interface ArrayConstructor {
    new (arrayLength?: number): any[];
    new <T>(arrayLength: number): T[];
    new <T>(...items: T[]): T[];
    (arrayLength?: number): any[];
    <T>(arrayLength: number): T[];
    <T>(...items: T[]): T[];
    readonly prototype: any[];
    isArray(arg: any): arg is any[];
    of<T>(...items: T[]): T[];
}

declare var Array: ArrayConstructor;

interface Error {
    name: string;
    message: string;
    stack?: string;
    cause?: unknown;
}

interface ErrorConstructor {
    new (message?: string, options?: { cause?: unknown }): Error;
    (message?: string, options?: { cause?: unknown }): Error;
    readonly prototype: Error;
}

declare var Error: ErrorConstructor;

interface TypeError extends Error {}
interface TypeErrorConstructor {
    new (message?: string, options?: { cause?: unknown }): TypeError;
    (message?: string, options?: { cause?: unknown }): TypeError;
    readonly prototype: TypeError;
}
declare var TypeError: TypeErrorConstructor;

interface RangeError extends Error {}
interface RangeErrorConstructor {
    new (message?: string, options?: { cause?: unknown }): RangeError;
    (message?: string, options?: { cause?: unknown }): RangeError;
    readonly prototype: RangeError;
}
declare var RangeError: RangeErrorConstructor;

interface SyntaxError extends Error {}
interface SyntaxErrorConstructor {
    new (message?: string, options?: { cause?: unknown }): SyntaxError;
    (message?: string, options?: { cause?: unknown }): SyntaxError;
    readonly prototype: SyntaxError;
}
declare var SyntaxError: SyntaxErrorConstructor;

interface ReferenceError extends Error {}
interface ReferenceErrorConstructor {
    new (message?: string, options?: { cause?: unknown }): ReferenceError;
    (message?: string, options?: { cause?: unknown }): ReferenceError;
    readonly prototype: ReferenceError;
}
declare var ReferenceError: ReferenceErrorConstructor;

interface EvalError extends Error {}
interface EvalErrorConstructor {
    new (message?: string, options?: { cause?: unknown }): EvalError;
    (message?: string, options?: { cause?: unknown }): EvalError;
    readonly prototype: EvalError;
}
declare var EvalError: EvalErrorConstructor;

interface URIError extends Error {}
interface URIErrorConstructor {
    new (message?: string, options?: { cause?: unknown }): URIError;
    (message?: string, options?: { cause?: unknown }): URIError;
    readonly prototype: URIError;
}
declare var URIError: URIErrorConstructor;

interface AggregateError extends Error {
    errors: any[];
}
interface AggregateErrorConstructor {
    new (errors: Iterable<any>, message?: string, options?: { cause?: unknown }): AggregateError;
    (errors: Iterable<any>, message?: string, options?: { cause?: unknown }): AggregateError;
    readonly prototype: AggregateError;
}
declare var AggregateError: AggregateErrorConstructor;

interface Date {
    toString(): string;
    toDateString(): string;
    toTimeString(): string;
    toLocaleString(locales?: string | string[], options?: any): string;
    toLocaleDateString(locales?: string | string[], options?: any): string;
    toLocaleTimeString(locales?: string | string[], options?: any): string;
    valueOf(): number;
    getTime(): number;
    getFullYear(): number;
    getUTCFullYear(): number;
    getMonth(): number;
    getUTCMonth(): number;
    getDate(): number;
    getUTCDate(): number;
    getDay(): number;
    getUTCDay(): number;
    getHours(): number;
    getUTCHours(): number;
    getMinutes(): number;
    getUTCMinutes(): number;
    getSeconds(): number;
    getUTCSeconds(): number;
    getMilliseconds(): number;
    getUTCMilliseconds(): number;
    getTimezoneOffset(): number;
    setTime(time: number): number;
    setMilliseconds(ms: number): number;
    setSeconds(sec: number, ms?: number): number;
    setMinutes(min: number, sec?: number, ms?: number): number;
    setHours(hours: number, min?: number, sec?: number, ms?: number): number;
    setDate(date: number): number;
    setMonth(month: number, date?: number): number;
    setFullYear(year: number, month?: number, date?: number): number;
    toUTCString(): string;
    toISOString(): string;
    toJSON(key?: any): string;
}

interface DateConstructor {
    new (value?: number | string | Date): Date;
    new (year: number, monthIndex: number, date?: number, hours?: number, minutes?: number, seconds?: number, ms?: number): Date;
    (): string;
    readonly prototype: Date;
    parse(s: string): number;
    UTC(year: number, monthIndex?: number, date?: number, hours?: number, minutes?: number, seconds?: number, ms?: number): number;
    now(): number;
}

declare var Date: DateConstructor;

interface RegExpMatchArray extends Array<string> {
    index?: number;
    input?: string;
}

interface RegExpExecArray extends Array<string> {
    index: number;
    input: string;
}

interface RegExp {
    exec(string: string): RegExpExecArray | null;
    test(string: string): boolean;
    readonly source: string;
    readonly global: boolean;
    readonly ignoreCase: boolean;
    readonly multiline: boolean;
    readonly flags: string;
    readonly sticky: boolean;
    readonly unicode: boolean;
    lastIndex: number;
}

interface RegExpConstructor {
    new (pattern: RegExp | string, flags?: string): RegExp;
    (pattern: RegExp | string, flags?: string): RegExp;
    readonly prototype: RegExp;
}

declare var RegExp: RegExpConstructor;

interface Iterable<T> {}
interface Iterator<T> {}
interface IterableIterator<T> extends Iterator<T> {}
interface AsyncIterable<T> {}
interface AsyncIterator<T> {}
interface AsyncIterableIterator<T> extends AsyncIterator<T> {}

interface PromiseLike<T> {
    then<TResult1 = T, TResult2 = never>(onfulfilled?: ((value: T) => TResult1 | PromiseLike<TResult1>) | undefined | null, onrejected?: ((reason: any) => TResult2 | PromiseLike<TResult2>) | undefined | null): PromiseLike<TResult1 | TResult2>;
}

interface Promise<T> {
    then<TResult1 = T, TResult2 = never>(onfulfilled?: ((value: T) => TResult1 | PromiseLike<TResult1>) | undefined | null, onrejected?: ((reason: any) => TResult2 | PromiseLike<TResult2>) | undefined | null): Promise<TResult1 | TResult2>;
    catch<TResult = never>(onrejected?: ((reason: any) => TResult | PromiseLike<TResult>) | undefined | null): Promise<T | TResult>;
    finally(onfinally?: (() => void) | undefined | null): Promise<T>;
}

interface PromiseConstructor {
    readonly prototype: Promise<any>;
    new <T>(executor: (resolve: (value: T | PromiseLike<T>) => void, reject: (reason?: any) => void) => void): Promise<T>;
    reject<T = never>(reason?: any): Promise<T>;
    resolve(): Promise<void>;
    resolve<T>(value: T): Promise<Awaited<T>>;
}

declare var Promise: PromiseConstructor;

type Awaited<T> = T;

interface Map<K, V> {
    clear(): void;
    delete(key: K): boolean;
    forEach(callbackfn: (value: V, key: K, map: Map<K, V>) => void, thisArg?: any): void;
    get(key: K): V | undefined;
    has(key: K): boolean;
    set(key: K, value: V): this;
    readonly size: number;
}

interface MapConstructor {
    new (): Map<any, any>;
    new <K, V>(entries?: readonly (readonly [K, V])[] | null): Map<K, V>;
    new <K, V>(iterable?: Iterable<readonly [K, V]> | null): Map<K, V>;
    readonly prototype: Map<any, any>;
}

declare var Map: MapConstructor;

interface Set<T> {
    add(value: T): this;
    clear(): void;
    delete(value: T): boolean;
    forEach(callbackfn: (value: T, value2: T, set: Set<T>) => void, thisArg?: any): void;
    has(value: T): boolean;
    readonly size: number;
}

interface SetConstructor {
    new <T = any>(values?: readonly T[] | null): Set<T>;
    new <T>(iterable?: Iterable<T> | null): Set<T>;
    readonly prototype: Set<any>;
}

declare var Set: SetConstructor;

interface WeakMap<K extends object, V> {
    delete(key: K): boolean;
    get(key: K): V | undefined;
    has(key: K): boolean;
    set(key: K, value: V): this;
}

interface WeakMapConstructor {
    new <K extends object = object, V = any>(entries?: readonly (readonly [K, V])[] | null): WeakMap<K, V>;
    new <K extends object, V>(iterable: Iterable<readonly [K, V]>): WeakMap<K, V>;
    readonly prototype: WeakMap<object, any>;
}

declare var WeakMap: WeakMapConstructor;

interface WeakSet<T extends object> {
    add(value: T): this;
    delete(value: T): boolean;
    has(value: T): boolean;
}

interface WeakSetConstructor {
    new <T extends object = object>(values?: readonly T[] | null): WeakSet<T>;
    new <T extends object>(iterable: Iterable<T>): WeakSet<T>;
    readonly prototype: WeakSet<object>;
}

declare var WeakSet: WeakSetConstructor;

declare var NaN: number;
declare var Infinity: number;
declare function parseInt(string: string, radix?: number): number;
declare function parseFloat(string: string): number;
declare function isNaN(number: number): boolean;
declare function isFinite(number: number): boolean;
declare function decodeURI(encodedURI: string): string;
declare function decodeURIComponent(encodedURIComponent: string): string;
declare function encodeURI(uri: string): string;
declare function encodeURIComponent(uriComponent: string | number | boolean): string;
declare function escape(string: string): string;
declare function unescape(string: string): string;

// The typed arrays and ArrayBuffer (TypeScript's lib.es5.d.ts and
// lib.es2020.bigint.d.ts). ArrayBufferLike is the union ArrayBufferTypes'
// indexed access names there.
interface ArrayLike<T> {
    readonly length: number;
    readonly [n: number]: T;
}
interface ArrayBuffer {
    readonly byteLength: number;
    slice(begin?: number, end?: number): ArrayBuffer;
}
type ArrayBufferLike = ArrayBuffer | SharedArrayBuffer;
interface ArrayBufferConstructor {
    readonly prototype: ArrayBuffer;
    new (byteLength: number): ArrayBuffer;
    isView(arg: any): arg is ArrayBufferView;
}
interface ArrayBufferConstructor {
    new (): ArrayBuffer;
}
interface ArrayBufferConstructor {
    new (byteLength: number, options?: { maxByteLength?: number; }): ArrayBuffer;
}
declare var ArrayBuffer: ArrayBufferConstructor;
interface ArrayBufferView<TArrayBuffer extends ArrayBufferLike = ArrayBufferLike> {
    readonly buffer: TArrayBuffer;
    readonly byteLength: number;
    readonly byteOffset: number;
}
interface SharedArrayBuffer {
    readonly byteLength: number;
    slice(begin?: number, end?: number): SharedArrayBuffer;
    readonly [Symbol.toStringTag]: "SharedArrayBuffer";
}
interface Int8Array<TArrayBuffer extends ArrayBufferLike = ArrayBufferLike> {
    readonly BYTES_PER_ELEMENT: number;
    readonly buffer: TArrayBuffer;
    readonly byteLength: number;
    readonly byteOffset: number;
    copyWithin(target: number, start: number, end?: number): this;
    every(predicate: (value: number, index: number, array: this) => unknown, thisArg?: any): boolean;
    fill(value: number, start?: number, end?: number): this;
    filter(predicate: (value: number, index: number, array: this) => any, thisArg?: any): Int8Array<ArrayBuffer>;
    find(predicate: (value: number, index: number, obj: this) => boolean, thisArg?: any): number | undefined;
    findIndex(predicate: (value: number, index: number, obj: this) => boolean, thisArg?: any): number;
    forEach(callbackfn: (value: number, index: number, array: this) => void, thisArg?: any): void;
    indexOf(searchElement: number, fromIndex?: number): number;
    join(separator?: string): string;
    lastIndexOf(searchElement: number, fromIndex?: number): number;
    readonly length: number;
    map(callbackfn: (value: number, index: number, array: this) => number, thisArg?: any): Int8Array<ArrayBuffer>;
    reduce(callbackfn: (previousValue: number, currentValue: number, currentIndex: number, array: this) => number): number;
    reduce(callbackfn: (previousValue: number, currentValue: number, currentIndex: number, array: this) => number, initialValue: number): number;
    reduce<U>(callbackfn: (previousValue: U, currentValue: number, currentIndex: number, array: this) => U, initialValue: U): U;
    reduceRight(callbackfn: (previousValue: number, currentValue: number, currentIndex: number, array: this) => number): number;
    reduceRight(callbackfn: (previousValue: number, currentValue: number, currentIndex: number, array: this) => number, initialValue: number): number;
    reduceRight<U>(callbackfn: (previousValue: U, currentValue: number, currentIndex: number, array: this) => U, initialValue: U): U;
    reverse(): this;
    set(array: ArrayLike<number>, offset?: number): void;
    slice(start?: number, end?: number): Int8Array<ArrayBuffer>;
    some(predicate: (value: number, index: number, array: this) => unknown, thisArg?: any): boolean;
    sort(compareFn?: (a: number, b: number) => number): this;
    subarray(begin?: number, end?: number): Int8Array<TArrayBuffer>;
    toLocaleString(): string;
    toString(): string;
    valueOf(): this;
    [index: number]: number;
}
interface Int8ArrayConstructor {
    readonly prototype: Int8Array<ArrayBufferLike>;
    new (length: number): Int8Array<ArrayBuffer>;
    new (array: ArrayLike<number>): Int8Array<ArrayBuffer>;
    new <TArrayBuffer extends ArrayBufferLike = ArrayBuffer>(buffer: TArrayBuffer, byteOffset?: number, length?: number): Int8Array<TArrayBuffer>;
    new (buffer: ArrayBuffer, byteOffset?: number, length?: number): Int8Array<ArrayBuffer>;
    new (array: ArrayLike<number> | ArrayBuffer): Int8Array<ArrayBuffer>;
    readonly BYTES_PER_ELEMENT: number;
    of(...items: number[]): Int8Array<ArrayBuffer>;
    from(arrayLike: ArrayLike<number>): Int8Array<ArrayBuffer>;
    from<T>(arrayLike: ArrayLike<T>, mapfn: (v: T, k: number) => number, thisArg?: any): Int8Array<ArrayBuffer>;
}
declare var Int8Array: Int8ArrayConstructor;
interface Uint8Array<TArrayBuffer extends ArrayBufferLike = ArrayBufferLike> {
    readonly BYTES_PER_ELEMENT: number;
    readonly buffer: TArrayBuffer;
    readonly byteLength: number;
    readonly byteOffset: number;
    copyWithin(target: number, start: number, end?: number): this;
    every(predicate: (value: number, index: number, array: this) => unknown, thisArg?: any): boolean;
    fill(value: number, start?: number, end?: number): this;
    filter(predicate: (value: number, index: number, array: this) => any, thisArg?: any): Uint8Array<ArrayBuffer>;
    find(predicate: (value: number, index: number, obj: this) => boolean, thisArg?: any): number | undefined;
    findIndex(predicate: (value: number, index: number, obj: this) => boolean, thisArg?: any): number;
    forEach(callbackfn: (value: number, index: number, array: this) => void, thisArg?: any): void;
    indexOf(searchElement: number, fromIndex?: number): number;
    join(separator?: string): string;
    lastIndexOf(searchElement: number, fromIndex?: number): number;
    readonly length: number;
    map(callbackfn: (value: number, index: number, array: this) => number, thisArg?: any): Uint8Array<ArrayBuffer>;
    reduce(callbackfn: (previousValue: number, currentValue: number, currentIndex: number, array: this) => number): number;
    reduce(callbackfn: (previousValue: number, currentValue: number, currentIndex: number, array: this) => number, initialValue: number): number;
    reduce<U>(callbackfn: (previousValue: U, currentValue: number, currentIndex: number, array: this) => U, initialValue: U): U;
    reduceRight(callbackfn: (previousValue: number, currentValue: number, currentIndex: number, array: this) => number): number;
    reduceRight(callbackfn: (previousValue: number, currentValue: number, currentIndex: number, array: this) => number, initialValue: number): number;
    reduceRight<U>(callbackfn: (previousValue: U, currentValue: number, currentIndex: number, array: this) => U, initialValue: U): U;
    reverse(): this;
    set(array: ArrayLike<number>, offset?: number): void;
    slice(start?: number, end?: number): Uint8Array<ArrayBuffer>;
    some(predicate: (value: number, index: number, array: this) => unknown, thisArg?: any): boolean;
    sort(compareFn?: (a: number, b: number) => number): this;
    subarray(begin?: number, end?: number): Uint8Array<TArrayBuffer>;
    toLocaleString(): string;
    toString(): string;
    valueOf(): this;
    [index: number]: number;
}
interface Uint8ArrayConstructor {
    readonly prototype: Uint8Array<ArrayBufferLike>;
    new (length: number): Uint8Array<ArrayBuffer>;
    new (array: ArrayLike<number>): Uint8Array<ArrayBuffer>;
    new <TArrayBuffer extends ArrayBufferLike = ArrayBuffer>(buffer: TArrayBuffer, byteOffset?: number, length?: number): Uint8Array<TArrayBuffer>;
    new (buffer: ArrayBuffer, byteOffset?: number, length?: number): Uint8Array<ArrayBuffer>;
    new (array: ArrayLike<number> | ArrayBuffer): Uint8Array<ArrayBuffer>;
    readonly BYTES_PER_ELEMENT: number;
    of(...items: number[]): Uint8Array<ArrayBuffer>;
    from(arrayLike: ArrayLike<number>): Uint8Array<ArrayBuffer>;
    from<T>(arrayLike: ArrayLike<T>, mapfn: (v: T, k: number) => number, thisArg?: any): Uint8Array<ArrayBuffer>;
}
declare var Uint8Array: Uint8ArrayConstructor;
interface Uint8ClampedArray<TArrayBuffer extends ArrayBufferLike = ArrayBufferLike> {
    readonly BYTES_PER_ELEMENT: number;
    readonly buffer: TArrayBuffer;
    readonly byteLength: number;
    readonly byteOffset: number;
    copyWithin(target: number, start: number, end?: number): this;
    every(predicate: (value: number, index: number, array: this) => unknown, thisArg?: any): boolean;
    fill(value: number, start?: number, end?: number): this;
    filter(predicate: (value: number, index: number, array: this) => any, thisArg?: any): Uint8ClampedArray<ArrayBuffer>;
    find(predicate: (value: number, index: number, obj: this) => boolean, thisArg?: any): number | undefined;
    findIndex(predicate: (value: number, index: number, obj: this) => boolean, thisArg?: any): number;
    forEach(callbackfn: (value: number, index: number, array: this) => void, thisArg?: any): void;
    indexOf(searchElement: number, fromIndex?: number): number;
    join(separator?: string): string;
    lastIndexOf(searchElement: number, fromIndex?: number): number;
    readonly length: number;
    map(callbackfn: (value: number, index: number, array: this) => number, thisArg?: any): Uint8ClampedArray<ArrayBuffer>;
    reduce(callbackfn: (previousValue: number, currentValue: number, currentIndex: number, array: this) => number): number;
    reduce(callbackfn: (previousValue: number, currentValue: number, currentIndex: number, array: this) => number, initialValue: number): number;
    reduce<U>(callbackfn: (previousValue: U, currentValue: number, currentIndex: number, array: this) => U, initialValue: U): U;
    reduceRight(callbackfn: (previousValue: number, currentValue: number, currentIndex: number, array: this) => number): number;
    reduceRight(callbackfn: (previousValue: number, currentValue: number, currentIndex: number, array: this) => number, initialValue: number): number;
    reduceRight<U>(callbackfn: (previousValue: U, currentValue: number, currentIndex: number, array: this) => U, initialValue: U): U;
    reverse(): this;
    set(array: ArrayLike<number>, offset?: number): void;
    slice(start?: number, end?: number): Uint8ClampedArray<ArrayBuffer>;
    some(predicate: (value: number, index: number, array: this) => unknown, thisArg?: any): boolean;
    sort(compareFn?: (a: number, b: number) => number): this;
    subarray(begin?: number, end?: number): Uint8ClampedArray<TArrayBuffer>;
    toLocaleString(): string;
    toString(): string;
    valueOf(): this;
    [index: number]: number;
}
interface Uint8ClampedArrayConstructor {
    readonly prototype: Uint8ClampedArray<ArrayBufferLike>;
    new (length: number): Uint8ClampedArray<ArrayBuffer>;
    new (array: ArrayLike<number>): Uint8ClampedArray<ArrayBuffer>;
    new <TArrayBuffer extends ArrayBufferLike = ArrayBuffer>(buffer: TArrayBuffer, byteOffset?: number, length?: number): Uint8ClampedArray<TArrayBuffer>;
    new (buffer: ArrayBuffer, byteOffset?: number, length?: number): Uint8ClampedArray<ArrayBuffer>;
    new (array: ArrayLike<number> | ArrayBuffer): Uint8ClampedArray<ArrayBuffer>;
    readonly BYTES_PER_ELEMENT: number;
    of(...items: number[]): Uint8ClampedArray<ArrayBuffer>;
    from(arrayLike: ArrayLike<number>): Uint8ClampedArray<ArrayBuffer>;
    from<T>(arrayLike: ArrayLike<T>, mapfn: (v: T, k: number) => number, thisArg?: any): Uint8ClampedArray<ArrayBuffer>;
}
declare var Uint8ClampedArray: Uint8ClampedArrayConstructor;
interface Int16Array<TArrayBuffer extends ArrayBufferLike = ArrayBufferLike> {
    readonly BYTES_PER_ELEMENT: number;
    readonly buffer: TArrayBuffer;
    readonly byteLength: number;
    readonly byteOffset: number;
    copyWithin(target: number, start: number, end?: number): this;
    every(predicate: (value: number, index: number, array: this) => unknown, thisArg?: any): boolean;
    fill(value: number, start?: number, end?: number): this;
    filter(predicate: (value: number, index: number, array: this) => any, thisArg?: any): Int16Array<ArrayBuffer>;
    find(predicate: (value: number, index: number, obj: this) => boolean, thisArg?: any): number | undefined;
    findIndex(predicate: (value: number, index: number, obj: this) => boolean, thisArg?: any): number;
    forEach(callbackfn: (value: number, index: number, array: this) => void, thisArg?: any): void;
    indexOf(searchElement: number, fromIndex?: number): number;
    join(separator?: string): string;
    lastIndexOf(searchElement: number, fromIndex?: number): number;
    readonly length: number;
    map(callbackfn: (value: number, index: number, array: this) => number, thisArg?: any): Int16Array<ArrayBuffer>;
    reduce(callbackfn: (previousValue: number, currentValue: number, currentIndex: number, array: this) => number): number;
    reduce(callbackfn: (previousValue: number, currentValue: number, currentIndex: number, array: this) => number, initialValue: number): number;
    reduce<U>(callbackfn: (previousValue: U, currentValue: number, currentIndex: number, array: this) => U, initialValue: U): U;
    reduceRight(callbackfn: (previousValue: number, currentValue: number, currentIndex: number, array: this) => number): number;
    reduceRight(callbackfn: (previousValue: number, currentValue: number, currentIndex: number, array: this) => number, initialValue: number): number;
    reduceRight<U>(callbackfn: (previousValue: U, currentValue: number, currentIndex: number, array: this) => U, initialValue: U): U;
    reverse(): this;
    set(array: ArrayLike<number>, offset?: number): void;
    slice(start?: number, end?: number): Int16Array<ArrayBuffer>;
    some(predicate: (value: number, index: number, array: this) => unknown, thisArg?: any): boolean;
    sort(compareFn?: (a: number, b: number) => number): this;
    subarray(begin?: number, end?: number): Int16Array<TArrayBuffer>;
    toLocaleString(): string;
    toString(): string;
    valueOf(): this;
    [index: number]: number;
}
interface Int16ArrayConstructor {
    readonly prototype: Int16Array<ArrayBufferLike>;
    new (length: number): Int16Array<ArrayBuffer>;
    new (array: ArrayLike<number>): Int16Array<ArrayBuffer>;
    new <TArrayBuffer extends ArrayBufferLike = ArrayBuffer>(buffer: TArrayBuffer, byteOffset?: number, length?: number): Int16Array<TArrayBuffer>;
    new (buffer: ArrayBuffer, byteOffset?: number, length?: number): Int16Array<ArrayBuffer>;
    new (array: ArrayLike<number> | ArrayBuffer): Int16Array<ArrayBuffer>;
    readonly BYTES_PER_ELEMENT: number;
    of(...items: number[]): Int16Array<ArrayBuffer>;
    from(arrayLike: ArrayLike<number>): Int16Array<ArrayBuffer>;
    from<T>(arrayLike: ArrayLike<T>, mapfn: (v: T, k: number) => number, thisArg?: any): Int16Array<ArrayBuffer>;
}
declare var Int16Array: Int16ArrayConstructor;
interface Uint16Array<TArrayBuffer extends ArrayBufferLike = ArrayBufferLike> {
    readonly BYTES_PER_ELEMENT: number;
    readonly buffer: TArrayBuffer;
    readonly byteLength: number;
    readonly byteOffset: number;
    copyWithin(target: number, start: number, end?: number): this;
    every(predicate: (value: number, index: number, array: this) => unknown, thisArg?: any): boolean;
    fill(value: number, start?: number, end?: number): this;
    filter(predicate: (value: number, index: number, array: this) => any, thisArg?: any): Uint16Array<ArrayBuffer>;
    find(predicate: (value: number, index: number, obj: this) => boolean, thisArg?: any): number | undefined;
    findIndex(predicate: (value: number, index: number, obj: this) => boolean, thisArg?: any): number;
    forEach(callbackfn: (value: number, index: number, array: this) => void, thisArg?: any): void;
    indexOf(searchElement: number, fromIndex?: number): number;
    join(separator?: string): string;
    lastIndexOf(searchElement: number, fromIndex?: number): number;
    readonly length: number;
    map(callbackfn: (value: number, index: number, array: this) => number, thisArg?: any): Uint16Array<ArrayBuffer>;
    reduce(callbackfn: (previousValue: number, currentValue: number, currentIndex: number, array: this) => number): number;
    reduce(callbackfn: (previousValue: number, currentValue: number, currentIndex: number, array: this) => number, initialValue: number): number;
    reduce<U>(callbackfn: (previousValue: U, currentValue: number, currentIndex: number, array: this) => U, initialValue: U): U;
    reduceRight(callbackfn: (previousValue: number, currentValue: number, currentIndex: number, array: this) => number): number;
    reduceRight(callbackfn: (previousValue: number, currentValue: number, currentIndex: number, array: this) => number, initialValue: number): number;
    reduceRight<U>(callbackfn: (previousValue: U, currentValue: number, currentIndex: number, array: this) => U, initialValue: U): U;
    reverse(): this;
    set(array: ArrayLike<number>, offset?: number): void;
    slice(start?: number, end?: number): Uint16Array<ArrayBuffer>;
    some(predicate: (value: number, index: number, array: this) => unknown, thisArg?: any): boolean;
    sort(compareFn?: (a: number, b: number) => number): this;
    subarray(begin?: number, end?: number): Uint16Array<TArrayBuffer>;
    toLocaleString(): string;
    toString(): string;
    valueOf(): this;
    [index: number]: number;
}
interface Uint16ArrayConstructor {
    readonly prototype: Uint16Array<ArrayBufferLike>;
    new (length: number): Uint16Array<ArrayBuffer>;
    new (array: ArrayLike<number>): Uint16Array<ArrayBuffer>;
    new <TArrayBuffer extends ArrayBufferLike = ArrayBuffer>(buffer: TArrayBuffer, byteOffset?: number, length?: number): Uint16Array<TArrayBuffer>;
    new (buffer: ArrayBuffer, byteOffset?: number, length?: number): Uint16Array<ArrayBuffer>;
    new (array: ArrayLike<number> | ArrayBuffer): Uint16Array<ArrayBuffer>;
    readonly BYTES_PER_ELEMENT: number;
    of(...items: number[]): Uint16Array<ArrayBuffer>;
    from(arrayLike: ArrayLike<number>): Uint16Array<ArrayBuffer>;
    from<T>(arrayLike: ArrayLike<T>, mapfn: (v: T, k: number) => number, thisArg?: any): Uint16Array<ArrayBuffer>;
}
declare var Uint16Array: Uint16ArrayConstructor;
interface Int32Array<TArrayBuffer extends ArrayBufferLike = ArrayBufferLike> {
    readonly BYTES_PER_ELEMENT: number;
    readonly buffer: TArrayBuffer;
    readonly byteLength: number;
    readonly byteOffset: number;
    copyWithin(target: number, start: number, end?: number): this;
    every(predicate: (value: number, index: number, array: this) => unknown, thisArg?: any): boolean;
    fill(value: number, start?: number, end?: number): this;
    filter(predicate: (value: number, index: number, array: this) => any, thisArg?: any): Int32Array<ArrayBuffer>;
    find(predicate: (value: number, index: number, obj: this) => boolean, thisArg?: any): number | undefined;
    findIndex(predicate: (value: number, index: number, obj: this) => boolean, thisArg?: any): number;
    forEach(callbackfn: (value: number, index: number, array: this) => void, thisArg?: any): void;
    indexOf(searchElement: number, fromIndex?: number): number;
    join(separator?: string): string;
    lastIndexOf(searchElement: number, fromIndex?: number): number;
    readonly length: number;
    map(callbackfn: (value: number, index: number, array: this) => number, thisArg?: any): Int32Array<ArrayBuffer>;
    reduce(callbackfn: (previousValue: number, currentValue: number, currentIndex: number, array: this) => number): number;
    reduce(callbackfn: (previousValue: number, currentValue: number, currentIndex: number, array: this) => number, initialValue: number): number;
    reduce<U>(callbackfn: (previousValue: U, currentValue: number, currentIndex: number, array: this) => U, initialValue: U): U;
    reduceRight(callbackfn: (previousValue: number, currentValue: number, currentIndex: number, array: this) => number): number;
    reduceRight(callbackfn: (previousValue: number, currentValue: number, currentIndex: number, array: this) => number, initialValue: number): number;
    reduceRight<U>(callbackfn: (previousValue: U, currentValue: number, currentIndex: number, array: this) => U, initialValue: U): U;
    reverse(): this;
    set(array: ArrayLike<number>, offset?: number): void;
    slice(start?: number, end?: number): Int32Array<ArrayBuffer>;
    some(predicate: (value: number, index: number, array: this) => unknown, thisArg?: any): boolean;
    sort(compareFn?: (a: number, b: number) => number): this;
    subarray(begin?: number, end?: number): Int32Array<TArrayBuffer>;
    toLocaleString(): string;
    toString(): string;
    valueOf(): this;
    [index: number]: number;
}
interface Int32ArrayConstructor {
    readonly prototype: Int32Array<ArrayBufferLike>;
    new (length: number): Int32Array<ArrayBuffer>;
    new (array: ArrayLike<number>): Int32Array<ArrayBuffer>;
    new <TArrayBuffer extends ArrayBufferLike = ArrayBuffer>(buffer: TArrayBuffer, byteOffset?: number, length?: number): Int32Array<TArrayBuffer>;
    new (buffer: ArrayBuffer, byteOffset?: number, length?: number): Int32Array<ArrayBuffer>;
    new (array: ArrayLike<number> | ArrayBuffer): Int32Array<ArrayBuffer>;
    readonly BYTES_PER_ELEMENT: number;
    of(...items: number[]): Int32Array<ArrayBuffer>;
    from(arrayLike: ArrayLike<number>): Int32Array<ArrayBuffer>;
    from<T>(arrayLike: ArrayLike<T>, mapfn: (v: T, k: number) => number, thisArg?: any): Int32Array<ArrayBuffer>;
}
declare var Int32Array: Int32ArrayConstructor;
interface Uint32Array<TArrayBuffer extends ArrayBufferLike = ArrayBufferLike> {
    readonly BYTES_PER_ELEMENT: number;
    readonly buffer: TArrayBuffer;
    readonly byteLength: number;
    readonly byteOffset: number;
    copyWithin(target: number, start: number, end?: number): this;
    every(predicate: (value: number, index: number, array: this) => unknown, thisArg?: any): boolean;
    fill(value: number, start?: number, end?: number): this;
    filter(predicate: (value: number, index: number, array: this) => any, thisArg?: any): Uint32Array<ArrayBuffer>;
    find(predicate: (value: number, index: number, obj: this) => boolean, thisArg?: any): number | undefined;
    findIndex(predicate: (value: number, index: number, obj: this) => boolean, thisArg?: any): number;
    forEach(callbackfn: (value: number, index: number, array: this) => void, thisArg?: any): void;
    indexOf(searchElement: number, fromIndex?: number): number;
    join(separator?: string): string;
    lastIndexOf(searchElement: number, fromIndex?: number): number;
    readonly length: number;
    map(callbackfn: (value: number, index: number, array: this) => number, thisArg?: any): Uint32Array<ArrayBuffer>;
    reduce(callbackfn: (previousValue: number, currentValue: number, currentIndex: number, array: this) => number): number;
    reduce(callbackfn: (previousValue: number, currentValue: number, currentIndex: number, array: this) => number, initialValue: number): number;
    reduce<U>(callbackfn: (previousValue: U, currentValue: number, currentIndex: number, array: this) => U, initialValue: U): U;
    reduceRight(callbackfn: (previousValue: number, currentValue: number, currentIndex: number, array: this) => number): number;
    reduceRight(callbackfn: (previousValue: number, currentValue: number, currentIndex: number, array: this) => number, initialValue: number): number;
    reduceRight<U>(callbackfn: (previousValue: U, currentValue: number, currentIndex: number, array: this) => U, initialValue: U): U;
    reverse(): this;
    set(array: ArrayLike<number>, offset?: number): void;
    slice(start?: number, end?: number): Uint32Array<ArrayBuffer>;
    some(predicate: (value: number, index: number, array: this) => unknown, thisArg?: any): boolean;
    sort(compareFn?: (a: number, b: number) => number): this;
    subarray(begin?: number, end?: number): Uint32Array<TArrayBuffer>;
    toLocaleString(): string;
    toString(): string;
    valueOf(): this;
    [index: number]: number;
}
interface Uint32ArrayConstructor {
    readonly prototype: Uint32Array<ArrayBufferLike>;
    new (length: number): Uint32Array<ArrayBuffer>;
    new (array: ArrayLike<number>): Uint32Array<ArrayBuffer>;
    new <TArrayBuffer extends ArrayBufferLike = ArrayBuffer>(buffer: TArrayBuffer, byteOffset?: number, length?: number): Uint32Array<TArrayBuffer>;
    new (buffer: ArrayBuffer, byteOffset?: number, length?: number): Uint32Array<ArrayBuffer>;
    new (array: ArrayLike<number> | ArrayBuffer): Uint32Array<ArrayBuffer>;
    readonly BYTES_PER_ELEMENT: number;
    of(...items: number[]): Uint32Array<ArrayBuffer>;
    from(arrayLike: ArrayLike<number>): Uint32Array<ArrayBuffer>;
    from<T>(arrayLike: ArrayLike<T>, mapfn: (v: T, k: number) => number, thisArg?: any): Uint32Array<ArrayBuffer>;
}
declare var Uint32Array: Uint32ArrayConstructor;
interface Float32Array<TArrayBuffer extends ArrayBufferLike = ArrayBufferLike> {
    readonly BYTES_PER_ELEMENT: number;
    readonly buffer: TArrayBuffer;
    readonly byteLength: number;
    readonly byteOffset: number;
    copyWithin(target: number, start: number, end?: number): this;
    every(predicate: (value: number, index: number, array: this) => unknown, thisArg?: any): boolean;
    fill(value: number, start?: number, end?: number): this;
    filter(predicate: (value: number, index: number, array: this) => any, thisArg?: any): Float32Array<ArrayBuffer>;
    find(predicate: (value: number, index: number, obj: this) => boolean, thisArg?: any): number | undefined;
    findIndex(predicate: (value: number, index: number, obj: this) => boolean, thisArg?: any): number;
    forEach(callbackfn: (value: number, index: number, array: this) => void, thisArg?: any): void;
    indexOf(searchElement: number, fromIndex?: number): number;
    join(separator?: string): string;
    lastIndexOf(searchElement: number, fromIndex?: number): number;
    readonly length: number;
    map(callbackfn: (value: number, index: number, array: this) => number, thisArg?: any): Float32Array<ArrayBuffer>;
    reduce(callbackfn: (previousValue: number, currentValue: number, currentIndex: number, array: this) => number): number;
    reduce(callbackfn: (previousValue: number, currentValue: number, currentIndex: number, array: this) => number, initialValue: number): number;
    reduce<U>(callbackfn: (previousValue: U, currentValue: number, currentIndex: number, array: this) => U, initialValue: U): U;
    reduceRight(callbackfn: (previousValue: number, currentValue: number, currentIndex: number, array: this) => number): number;
    reduceRight(callbackfn: (previousValue: number, currentValue: number, currentIndex: number, array: this) => number, initialValue: number): number;
    reduceRight<U>(callbackfn: (previousValue: U, currentValue: number, currentIndex: number, array: this) => U, initialValue: U): U;
    reverse(): this;
    set(array: ArrayLike<number>, offset?: number): void;
    slice(start?: number, end?: number): Float32Array<ArrayBuffer>;
    some(predicate: (value: number, index: number, array: this) => unknown, thisArg?: any): boolean;
    sort(compareFn?: (a: number, b: number) => number): this;
    subarray(begin?: number, end?: number): Float32Array<TArrayBuffer>;
    toLocaleString(): string;
    toString(): string;
    valueOf(): this;
    [index: number]: number;
}
interface Float32ArrayConstructor {
    readonly prototype: Float32Array<ArrayBufferLike>;
    new (length: number): Float32Array<ArrayBuffer>;
    new (array: ArrayLike<number>): Float32Array<ArrayBuffer>;
    new <TArrayBuffer extends ArrayBufferLike = ArrayBuffer>(buffer: TArrayBuffer, byteOffset?: number, length?: number): Float32Array<TArrayBuffer>;
    new (buffer: ArrayBuffer, byteOffset?: number, length?: number): Float32Array<ArrayBuffer>;
    new (array: ArrayLike<number> | ArrayBuffer): Float32Array<ArrayBuffer>;
    readonly BYTES_PER_ELEMENT: number;
    of(...items: number[]): Float32Array<ArrayBuffer>;
    from(arrayLike: ArrayLike<number>): Float32Array<ArrayBuffer>;
    from<T>(arrayLike: ArrayLike<T>, mapfn: (v: T, k: number) => number, thisArg?: any): Float32Array<ArrayBuffer>;
}
declare var Float32Array: Float32ArrayConstructor;
interface Float64Array<TArrayBuffer extends ArrayBufferLike = ArrayBufferLike> {
    readonly BYTES_PER_ELEMENT: number;
    readonly buffer: TArrayBuffer;
    readonly byteLength: number;
    readonly byteOffset: number;
    copyWithin(target: number, start: number, end?: number): this;
    every(predicate: (value: number, index: number, array: this) => unknown, thisArg?: any): boolean;
    fill(value: number, start?: number, end?: number): this;
    filter(predicate: (value: number, index: number, array: this) => any, thisArg?: any): Float64Array<ArrayBuffer>;
    find(predicate: (value: number, index: number, obj: this) => boolean, thisArg?: any): number | undefined;
    findIndex(predicate: (value: number, index: number, obj: this) => boolean, thisArg?: any): number;
    forEach(callbackfn: (value: number, index: number, array: this) => void, thisArg?: any): void;
    indexOf(searchElement: number, fromIndex?: number): number;
    join(separator?: string): string;
    lastIndexOf(searchElement: number, fromIndex?: number): number;
    readonly length: number;
    map(callbackfn: (value: number, index: number, array: this) => number, thisArg?: any): Float64Array<ArrayBuffer>;
    reduce(callbackfn: (previousValue: number, currentValue: number, currentIndex: number, array: this) => number): number;
    reduce(callbackfn: (previousValue: number, currentValue: number, currentIndex: number, array: this) => number, initialValue: number): number;
    reduce<U>(callbackfn: (previousValue: U, currentValue: number, currentIndex: number, array: this) => U, initialValue: U): U;
    reduceRight(callbackfn: (previousValue: number, currentValue: number, currentIndex: number, array: this) => number): number;
    reduceRight(callbackfn: (previousValue: number, currentValue: number, currentIndex: number, array: this) => number, initialValue: number): number;
    reduceRight<U>(callbackfn: (previousValue: U, currentValue: number, currentIndex: number, array: this) => U, initialValue: U): U;
    reverse(): this;
    set(array: ArrayLike<number>, offset?: number): void;
    slice(start?: number, end?: number): Float64Array<ArrayBuffer>;
    some(predicate: (value: number, index: number, array: this) => unknown, thisArg?: any): boolean;
    sort(compareFn?: (a: number, b: number) => number): this;
    subarray(begin?: number, end?: number): Float64Array<TArrayBuffer>;
    toLocaleString(): string;
    toString(): string;
    valueOf(): this;
    [index: number]: number;
}
interface Float64ArrayConstructor {
    readonly prototype: Float64Array<ArrayBufferLike>;
    new (length: number): Float64Array<ArrayBuffer>;
    new (array: ArrayLike<number>): Float64Array<ArrayBuffer>;
    new <TArrayBuffer extends ArrayBufferLike = ArrayBuffer>(buffer: TArrayBuffer, byteOffset?: number, length?: number): Float64Array<TArrayBuffer>;
    new (buffer: ArrayBuffer, byteOffset?: number, length?: number): Float64Array<ArrayBuffer>;
    new (array: ArrayLike<number> | ArrayBuffer): Float64Array<ArrayBuffer>;
    readonly BYTES_PER_ELEMENT: number;
    of(...items: number[]): Float64Array<ArrayBuffer>;
    from(arrayLike: ArrayLike<number>): Float64Array<ArrayBuffer>;
    from<T>(arrayLike: ArrayLike<T>, mapfn: (v: T, k: number) => number, thisArg?: any): Float64Array<ArrayBuffer>;
}
declare var Float64Array: Float64ArrayConstructor;
interface BigInt64Array<TArrayBuffer extends ArrayBufferLike = ArrayBufferLike> {
    readonly BYTES_PER_ELEMENT: number;
    readonly buffer: TArrayBuffer;
    readonly byteLength: number;
    readonly byteOffset: number;
    copyWithin(target: number, start: number, end?: number): this;
    entries(): ArrayIterator<[number, bigint]>;
    every(predicate: (value: bigint, index: number, array: BigInt64Array<TArrayBuffer>) => boolean, thisArg?: any): boolean;
    fill(value: bigint, start?: number, end?: number): this;
    filter(predicate: (value: bigint, index: number, array: BigInt64Array<TArrayBuffer>) => any, thisArg?: any): BigInt64Array<ArrayBuffer>;
    find(predicate: (value: bigint, index: number, array: BigInt64Array<TArrayBuffer>) => boolean, thisArg?: any): bigint | undefined;
    findIndex(predicate: (value: bigint, index: number, array: BigInt64Array<TArrayBuffer>) => boolean, thisArg?: any): number;
    forEach(callbackfn: (value: bigint, index: number, array: BigInt64Array<TArrayBuffer>) => void, thisArg?: any): void;
    includes(searchElement: bigint, fromIndex?: number): boolean;
    indexOf(searchElement: bigint, fromIndex?: number): number;
    join(separator?: string): string;
    keys(): ArrayIterator<number>;
    lastIndexOf(searchElement: bigint, fromIndex?: number): number;
    readonly length: number;
    map(callbackfn: (value: bigint, index: number, array: BigInt64Array<TArrayBuffer>) => bigint, thisArg?: any): BigInt64Array<ArrayBuffer>;
    reduce(callbackfn: (previousValue: bigint, currentValue: bigint, currentIndex: number, array: BigInt64Array<TArrayBuffer>) => bigint): bigint;
    reduce<U>(callbackfn: (previousValue: U, currentValue: bigint, currentIndex: number, array: BigInt64Array<TArrayBuffer>) => U, initialValue: U): U;
    reduceRight(callbackfn: (previousValue: bigint, currentValue: bigint, currentIndex: number, array: BigInt64Array<TArrayBuffer>) => bigint): bigint;
    reduceRight<U>(callbackfn: (previousValue: U, currentValue: bigint, currentIndex: number, array: BigInt64Array<TArrayBuffer>) => U, initialValue: U): U;
    reverse(): this;
    set(array: ArrayLike<bigint>, offset?: number): void;
    slice(start?: number, end?: number): BigInt64Array<ArrayBuffer>;
    some(predicate: (value: bigint, index: number, array: BigInt64Array<TArrayBuffer>) => boolean, thisArg?: any): boolean;
    sort(compareFn?: (a: bigint, b: bigint) => number | bigint): this;
    subarray(begin?: number, end?: number): BigInt64Array<TArrayBuffer>;
    toLocaleString(locales?: string | string[], options?: Intl.NumberFormatOptions): string;
    toString(): string;
    valueOf(): BigInt64Array<TArrayBuffer>;
    values(): ArrayIterator<bigint>;
    [Symbol.iterator](): ArrayIterator<bigint>;
    readonly [Symbol.toStringTag]: "BigInt64Array";
    [index: number]: bigint;
}
interface BigInt64ArrayConstructor {
    readonly prototype: BigInt64Array<ArrayBufferLike>;
    new (length?: number): BigInt64Array<ArrayBuffer>;
    new (array: ArrayLike<bigint> | Iterable<bigint>): BigInt64Array<ArrayBuffer>;
    new <TArrayBuffer extends ArrayBufferLike = ArrayBuffer>(buffer: TArrayBuffer, byteOffset?: number, length?: number): BigInt64Array<TArrayBuffer>;
    new (buffer: ArrayBuffer, byteOffset?: number, length?: number): BigInt64Array<ArrayBuffer>;
    new (array: ArrayLike<bigint> | ArrayBuffer): BigInt64Array<ArrayBuffer>;
    readonly BYTES_PER_ELEMENT: number;
    of(...items: bigint[]): BigInt64Array<ArrayBuffer>;
    from(arrayLike: ArrayLike<bigint>): BigInt64Array<ArrayBuffer>;
    from<U>(arrayLike: ArrayLike<U>, mapfn: (v: U, k: number) => bigint, thisArg?: any): BigInt64Array<ArrayBuffer>;
    from(elements: Iterable<bigint>): BigInt64Array<ArrayBuffer>;
    from<T>(elements: Iterable<T>, mapfn?: (v: T, k: number) => bigint, thisArg?: any): BigInt64Array<ArrayBuffer>;
}
declare var BigInt64Array: BigInt64ArrayConstructor;
interface BigUint64Array<TArrayBuffer extends ArrayBufferLike = ArrayBufferLike> {
    readonly BYTES_PER_ELEMENT: number;
    readonly buffer: TArrayBuffer;
    readonly byteLength: number;
    readonly byteOffset: number;
    copyWithin(target: number, start: number, end?: number): this;
    entries(): ArrayIterator<[number, bigint]>;
    every(predicate: (value: bigint, index: number, array: BigUint64Array<TArrayBuffer>) => boolean, thisArg?: any): boolean;
    fill(value: bigint, start?: number, end?: number): this;
    filter(predicate: (value: bigint, index: number, array: BigUint64Array<TArrayBuffer>) => any, thisArg?: any): BigUint64Array<ArrayBuffer>;
    find(predicate: (value: bigint, index: number, array: BigUint64Array<TArrayBuffer>) => boolean, thisArg?: any): bigint | undefined;
    findIndex(predicate: (value: bigint, index: number, array: BigUint64Array<TArrayBuffer>) => boolean, thisArg?: any): number;
    forEach(callbackfn: (value: bigint, index: number, array: BigUint64Array<TArrayBuffer>) => void, thisArg?: any): void;
    includes(searchElement: bigint, fromIndex?: number): boolean;
    indexOf(searchElement: bigint, fromIndex?: number): number;
    join(separator?: string): string;
    keys(): ArrayIterator<number>;
    lastIndexOf(searchElement: bigint, fromIndex?: number): number;
    readonly length: number;
    map(callbackfn: (value: bigint, index: number, array: BigUint64Array<TArrayBuffer>) => bigint, thisArg?: any): BigUint64Array<ArrayBuffer>;
    reduce(callbackfn: (previousValue: bigint, currentValue: bigint, currentIndex: number, array: BigUint64Array<TArrayBuffer>) => bigint): bigint;
    reduce<U>(callbackfn: (previousValue: U, currentValue: bigint, currentIndex: number, array: BigUint64Array<TArrayBuffer>) => U, initialValue: U): U;
    reduceRight(callbackfn: (previousValue: bigint, currentValue: bigint, currentIndex: number, array: BigUint64Array<TArrayBuffer>) => bigint): bigint;
    reduceRight<U>(callbackfn: (previousValue: U, currentValue: bigint, currentIndex: number, array: BigUint64Array<TArrayBuffer>) => U, initialValue: U): U;
    reverse(): this;
    set(array: ArrayLike<bigint>, offset?: number): void;
    slice(start?: number, end?: number): BigUint64Array<ArrayBuffer>;
    some(predicate: (value: bigint, index: number, array: BigUint64Array<TArrayBuffer>) => boolean, thisArg?: any): boolean;
    sort(compareFn?: (a: bigint, b: bigint) => number | bigint): this;
    subarray(begin?: number, end?: number): BigUint64Array<TArrayBuffer>;
    toLocaleString(locales?: string | string[], options?: Intl.NumberFormatOptions): string;
    toString(): string;
    valueOf(): BigUint64Array<TArrayBuffer>;
    values(): ArrayIterator<bigint>;
    [Symbol.iterator](): ArrayIterator<bigint>;
    readonly [Symbol.toStringTag]: "BigUint64Array";
    [index: number]: bigint;
}
interface BigUint64ArrayConstructor {
    readonly prototype: BigUint64Array<ArrayBufferLike>;
    new (length?: number): BigUint64Array<ArrayBuffer>;
    new (array: ArrayLike<bigint> | Iterable<bigint>): BigUint64Array<ArrayBuffer>;
    new <TArrayBuffer extends ArrayBufferLike = ArrayBuffer>(buffer: TArrayBuffer, byteOffset?: number, length?: number): BigUint64Array<TArrayBuffer>;
    new (buffer: ArrayBuffer, byteOffset?: number, length?: number): BigUint64Array<ArrayBuffer>;
    new (array: ArrayLike<bigint> | ArrayBuffer): BigUint64Array<ArrayBuffer>;
    readonly BYTES_PER_ELEMENT: number;
    of(...items: bigint[]): BigUint64Array<ArrayBuffer>;
    from(arrayLike: ArrayLike<bigint>): BigUint64Array<ArrayBuffer>;
    from<U>(arrayLike: ArrayLike<U>, mapfn: (v: U, k: number) => bigint, thisArg?: any): BigUint64Array<ArrayBuffer>;
    from(elements: Iterable<bigint>): BigUint64Array<ArrayBuffer>;
    from<T>(elements: Iterable<T>, mapfn?: (v: T, k: number) => bigint, thisArg?: any): BigUint64Array<ArrayBuffer>;
}
declare var BigUint64Array: BigUint64ArrayConstructor;

// Their later members (lib.es2015.*.d.ts through lib.es2023.array.d.ts).
interface Int8Array<TArrayBuffer extends ArrayBufferLike> {
    toLocaleString(locales: string | string[], options?: Intl.NumberFormatOptions): string;
}
interface Uint8Array<TArrayBuffer extends ArrayBufferLike> {
    toLocaleString(locales: string | string[], options?: Intl.NumberFormatOptions): string;
}
interface Uint8ClampedArray<TArrayBuffer extends ArrayBufferLike> {
    toLocaleString(locales: string | string[], options?: Intl.NumberFormatOptions): string;
}
interface Int16Array<TArrayBuffer extends ArrayBufferLike> {
    toLocaleString(locales: string | string[], options?: Intl.NumberFormatOptions): string;
}
interface Uint16Array<TArrayBuffer extends ArrayBufferLike> {
    toLocaleString(locales: string | string[], options?: Intl.NumberFormatOptions): string;
}
interface Int32Array<TArrayBuffer extends ArrayBufferLike> {
    toLocaleString(locales: string | string[], options?: Intl.NumberFormatOptions): string;
}
interface Uint32Array<TArrayBuffer extends ArrayBufferLike> {
    toLocaleString(locales: string | string[], options?: Intl.NumberFormatOptions): string;
}
interface Float32Array<TArrayBuffer extends ArrayBufferLike> {
    toLocaleString(locales: string | string[], options?: Intl.NumberFormatOptions): string;
}
interface Float64Array<TArrayBuffer extends ArrayBufferLike> {
    toLocaleString(locales: string | string[], options?: Intl.NumberFormatOptions): string;
}
interface Int8Array<TArrayBuffer extends ArrayBufferLike> {
    [Symbol.iterator](): ArrayIterator<number>;
    entries(): ArrayIterator<[number, number]>;
    keys(): ArrayIterator<number>;
    values(): ArrayIterator<number>;
}
interface Int8ArrayConstructor {
    new (elements: Iterable<number>): Int8Array<ArrayBuffer>;
    from(elements: Iterable<number>): Int8Array<ArrayBuffer>;
    from<T>(elements: Iterable<T>, mapfn?: (v: T, k: number) => number, thisArg?: any): Int8Array<ArrayBuffer>;
}
interface Uint8Array<TArrayBuffer extends ArrayBufferLike> {
    [Symbol.iterator](): ArrayIterator<number>;
    entries(): ArrayIterator<[number, number]>;
    keys(): ArrayIterator<number>;
    values(): ArrayIterator<number>;
}
interface Uint8ArrayConstructor {
    new (elements: Iterable<number>): Uint8Array<ArrayBuffer>;
    from(elements: Iterable<number>): Uint8Array<ArrayBuffer>;
    from<T>(elements: Iterable<T>, mapfn?: (v: T, k: number) => number, thisArg?: any): Uint8Array<ArrayBuffer>;
}
interface Uint8ClampedArray<TArrayBuffer extends ArrayBufferLike> {
    [Symbol.iterator](): ArrayIterator<number>;
    entries(): ArrayIterator<[number, number]>;
    keys(): ArrayIterator<number>;
    values(): ArrayIterator<number>;
}
interface Uint8ClampedArrayConstructor {
    new (elements: Iterable<number>): Uint8ClampedArray<ArrayBuffer>;
    from(elements: Iterable<number>): Uint8ClampedArray<ArrayBuffer>;
    from<T>(elements: Iterable<T>, mapfn?: (v: T, k: number) => number, thisArg?: any): Uint8ClampedArray<ArrayBuffer>;
}
interface Int16Array<TArrayBuffer extends ArrayBufferLike> {
    [Symbol.iterator](): ArrayIterator<number>;
    entries(): ArrayIterator<[number, number]>;
    keys(): ArrayIterator<number>;
    values(): ArrayIterator<number>;
}
interface Int16ArrayConstructor {
    new (elements: Iterable<number>): Int16Array<ArrayBuffer>;
    from(elements: Iterable<number>): Int16Array<ArrayBuffer>;
    from<T>(elements: Iterable<T>, mapfn?: (v: T, k: number) => number, thisArg?: any): Int16Array<ArrayBuffer>;
}
interface Uint16Array<TArrayBuffer extends ArrayBufferLike> {
    [Symbol.iterator](): ArrayIterator<number>;
    entries(): ArrayIterator<[number, number]>;
    keys(): ArrayIterator<number>;
    values(): ArrayIterator<number>;
}
interface Uint16ArrayConstructor {
    new (elements: Iterable<number>): Uint16Array<ArrayBuffer>;
    from(elements: Iterable<number>): Uint16Array<ArrayBuffer>;
    from<T>(elements: Iterable<T>, mapfn?: (v: T, k: number) => number, thisArg?: any): Uint16Array<ArrayBuffer>;
}
interface Int32Array<TArrayBuffer extends ArrayBufferLike> {
    [Symbol.iterator](): ArrayIterator<number>;
    entries(): ArrayIterator<[number, number]>;
    keys(): ArrayIterator<number>;
    values(): ArrayIterator<number>;
}
interface Int32ArrayConstructor {
    new (elements: Iterable<number>): Int32Array<ArrayBuffer>;
    from(elements: Iterable<number>): Int32Array<ArrayBuffer>;
    from<T>(elements: Iterable<T>, mapfn?: (v: T, k: number) => number, thisArg?: any): Int32Array<ArrayBuffer>;
}
interface Uint32Array<TArrayBuffer extends ArrayBufferLike> {
    [Symbol.iterator](): ArrayIterator<number>;
    entries(): ArrayIterator<[number, number]>;
    keys(): ArrayIterator<number>;
    values(): ArrayIterator<number>;
}
interface Uint32ArrayConstructor {
    new (elements: Iterable<number>): Uint32Array<ArrayBuffer>;
    from(elements: Iterable<number>): Uint32Array<ArrayBuffer>;
    from<T>(elements: Iterable<T>, mapfn?: (v: T, k: number) => number, thisArg?: any): Uint32Array<ArrayBuffer>;
}
interface Float32Array<TArrayBuffer extends ArrayBufferLike> {
    [Symbol.iterator](): ArrayIterator<number>;
    entries(): ArrayIterator<[number, number]>;
    keys(): ArrayIterator<number>;
    values(): ArrayIterator<number>;
}
interface Float32ArrayConstructor {
    new (elements: Iterable<number>): Float32Array<ArrayBuffer>;
    from(elements: Iterable<number>): Float32Array<ArrayBuffer>;
    from<T>(elements: Iterable<T>, mapfn?: (v: T, k: number) => number, thisArg?: any): Float32Array<ArrayBuffer>;
}
interface Float64Array<TArrayBuffer extends ArrayBufferLike> {
    [Symbol.iterator](): ArrayIterator<number>;
    entries(): ArrayIterator<[number, number]>;
    keys(): ArrayIterator<number>;
    values(): ArrayIterator<number>;
}
interface Float64ArrayConstructor {
    new (elements: Iterable<number>): Float64Array<ArrayBuffer>;
    from(elements: Iterable<number>): Float64Array<ArrayBuffer>;
    from<T>(elements: Iterable<T>, mapfn?: (v: T, k: number) => number, thisArg?: any): Float64Array<ArrayBuffer>;
}
interface Int8Array<TArrayBuffer extends ArrayBufferLike> {
    readonly [Symbol.toStringTag]: "Int8Array";
}
interface Uint8Array<TArrayBuffer extends ArrayBufferLike> {
    readonly [Symbol.toStringTag]: "Uint8Array";
}
interface Uint8ClampedArray<TArrayBuffer extends ArrayBufferLike> {
    readonly [Symbol.toStringTag]: "Uint8ClampedArray";
}
interface Int16Array<TArrayBuffer extends ArrayBufferLike> {
    readonly [Symbol.toStringTag]: "Int16Array";
}
interface Uint16Array<TArrayBuffer extends ArrayBufferLike> {
    readonly [Symbol.toStringTag]: "Uint16Array";
}
interface Int32Array<TArrayBuffer extends ArrayBufferLike> {
    readonly [Symbol.toStringTag]: "Int32Array";
}
interface Uint32Array<TArrayBuffer extends ArrayBufferLike> {
    readonly [Symbol.toStringTag]: "Uint32Array";
}
interface Float32Array<TArrayBuffer extends ArrayBufferLike> {
    readonly [Symbol.toStringTag]: "Float32Array";
}
interface Float64Array<TArrayBuffer extends ArrayBufferLike> {
    readonly [Symbol.toStringTag]: "Float64Array";
}
interface Int8Array<TArrayBuffer extends ArrayBufferLike> {
    includes(searchElement: number, fromIndex?: number): boolean;
}
interface Uint8Array<TArrayBuffer extends ArrayBufferLike> {
    includes(searchElement: number, fromIndex?: number): boolean;
}
interface Uint8ClampedArray<TArrayBuffer extends ArrayBufferLike> {
    includes(searchElement: number, fromIndex?: number): boolean;
}
interface Int16Array<TArrayBuffer extends ArrayBufferLike> {
    includes(searchElement: number, fromIndex?: number): boolean;
}
interface Uint16Array<TArrayBuffer extends ArrayBufferLike> {
    includes(searchElement: number, fromIndex?: number): boolean;
}
interface Int32Array<TArrayBuffer extends ArrayBufferLike> {
    includes(searchElement: number, fromIndex?: number): boolean;
}
interface Uint32Array<TArrayBuffer extends ArrayBufferLike> {
    includes(searchElement: number, fromIndex?: number): boolean;
}
interface Float32Array<TArrayBuffer extends ArrayBufferLike> {
    includes(searchElement: number, fromIndex?: number): boolean;
}
interface Float64Array<TArrayBuffer extends ArrayBufferLike> {
    includes(searchElement: number, fromIndex?: number): boolean;
}
interface Int8ArrayConstructor {
    new (): Int8Array<ArrayBuffer>;
}
interface Uint8ArrayConstructor {
    new (): Uint8Array<ArrayBuffer>;
}
interface Uint8ClampedArrayConstructor {
    new (): Uint8ClampedArray<ArrayBuffer>;
}
interface Int16ArrayConstructor {
    new (): Int16Array<ArrayBuffer>;
}
interface Uint16ArrayConstructor {
    new (): Uint16Array<ArrayBuffer>;
}
interface Int32ArrayConstructor {
    new (): Int32Array<ArrayBuffer>;
}
interface Uint32ArrayConstructor {
    new (): Uint32Array<ArrayBuffer>;
}
interface Float32ArrayConstructor {
    new (): Float32Array<ArrayBuffer>;
}
interface Float64ArrayConstructor {
    new (): Float64Array<ArrayBuffer>;
}
interface Int8Array<TArrayBuffer extends ArrayBufferLike> {
    at(index: number): number | undefined;
}
interface Uint8Array<TArrayBuffer extends ArrayBufferLike> {
    at(index: number): number | undefined;
}
interface Uint8ClampedArray<TArrayBuffer extends ArrayBufferLike> {
    at(index: number): number | undefined;
}
interface Int16Array<TArrayBuffer extends ArrayBufferLike> {
    at(index: number): number | undefined;
}
interface Uint16Array<TArrayBuffer extends ArrayBufferLike> {
    at(index: number): number | undefined;
}
interface Int32Array<TArrayBuffer extends ArrayBufferLike> {
    at(index: number): number | undefined;
}
interface Uint32Array<TArrayBuffer extends ArrayBufferLike> {
    at(index: number): number | undefined;
}
interface Float32Array<TArrayBuffer extends ArrayBufferLike> {
    at(index: number): number | undefined;
}
interface Float64Array<TArrayBuffer extends ArrayBufferLike> {
    at(index: number): number | undefined;
}
interface BigInt64Array<TArrayBuffer extends ArrayBufferLike> {
    at(index: number): bigint | undefined;
}
interface BigUint64Array<TArrayBuffer extends ArrayBufferLike> {
    at(index: number): bigint | undefined;
}
interface Int8Array<TArrayBuffer extends ArrayBufferLike> {
    findLast<S extends number>(
        predicate: (
            value: number,
            index: number,
            array: this,
        ) => value is S,
        thisArg?: any,
    ): S | undefined;
    findLast(
        predicate: (value: number, index: number, array: this) => unknown,
        thisArg?: any,
    ): number | undefined;
    findLastIndex(
        predicate: (value: number, index: number, array: this) => unknown,
        thisArg?: any,
    ): number;
    toReversed(): Int8Array<ArrayBuffer>;
    toSorted(compareFn?: (a: number, b: number) => number): Int8Array<ArrayBuffer>;
    with(index: number, value: number): Int8Array<ArrayBuffer>;
}
interface Uint8Array<TArrayBuffer extends ArrayBufferLike> {
    findLast<S extends number>(
        predicate: (
            value: number,
            index: number,
            array: this,
        ) => value is S,
        thisArg?: any,
    ): S | undefined;
    findLast(
        predicate: (value: number, index: number, array: this) => unknown,
        thisArg?: any,
    ): number | undefined;
    findLastIndex(
        predicate: (value: number, index: number, array: this) => unknown,
        thisArg?: any,
    ): number;
    toReversed(): Uint8Array<ArrayBuffer>;
    toSorted(compareFn?: (a: number, b: number) => number): Uint8Array<ArrayBuffer>;
    with(index: number, value: number): Uint8Array<ArrayBuffer>;
}
interface Uint8ClampedArray<TArrayBuffer extends ArrayBufferLike> {
    findLast<S extends number>(
        predicate: (
            value: number,
            index: number,
            array: this,
        ) => value is S,
        thisArg?: any,
    ): S | undefined;
    findLast(
        predicate: (
            value: number,
            index: number,
            array: this,
        ) => unknown,
        thisArg?: any,
    ): number | undefined;
    findLastIndex(
        predicate: (
            value: number,
            index: number,
            array: this,
        ) => unknown,
        thisArg?: any,
    ): number;
    toReversed(): Uint8ClampedArray<ArrayBuffer>;
    toSorted(compareFn?: (a: number, b: number) => number): Uint8ClampedArray<ArrayBuffer>;
    with(index: number, value: number): Uint8ClampedArray<ArrayBuffer>;
}
interface Int16Array<TArrayBuffer extends ArrayBufferLike> {
    findLast<S extends number>(
        predicate: (
            value: number,
            index: number,
            array: this,
        ) => value is S,
        thisArg?: any,
    ): S | undefined;
    findLast(
        predicate: (value: number, index: number, array: this) => unknown,
        thisArg?: any,
    ): number | undefined;
    findLastIndex(
        predicate: (value: number, index: number, array: this) => unknown,
        thisArg?: any,
    ): number;
    toReversed(): Int16Array<ArrayBuffer>;
    toSorted(compareFn?: (a: number, b: number) => number): Int16Array<ArrayBuffer>;
    with(index: number, value: number): Int16Array<ArrayBuffer>;
}
interface Uint16Array<TArrayBuffer extends ArrayBufferLike> {
    findLast<S extends number>(
        predicate: (
            value: number,
            index: number,
            array: this,
        ) => value is S,
        thisArg?: any,
    ): S | undefined;
    findLast(
        predicate: (
            value: number,
            index: number,
            array: this,
        ) => unknown,
        thisArg?: any,
    ): number | undefined;
    findLastIndex(
        predicate: (
            value: number,
            index: number,
            array: this,
        ) => unknown,
        thisArg?: any,
    ): number;
    toReversed(): Uint16Array<ArrayBuffer>;
    toSorted(compareFn?: (a: number, b: number) => number): Uint16Array<ArrayBuffer>;
    with(index: number, value: number): Uint16Array<ArrayBuffer>;
}
interface Int32Array<TArrayBuffer extends ArrayBufferLike> {
    findLast<S extends number>(
        predicate: (
            value: number,
            index: number,
            array: this,
        ) => value is S,
        thisArg?: any,
    ): S | undefined;
    findLast(
        predicate: (value: number, index: number, array: this) => unknown,
        thisArg?: any,
    ): number | undefined;
    findLastIndex(
        predicate: (value: number, index: number, array: this) => unknown,
        thisArg?: any,
    ): number;
    toReversed(): Int32Array<ArrayBuffer>;
    toSorted(compareFn?: (a: number, b: number) => number): Int32Array<ArrayBuffer>;
    with(index: number, value: number): Int32Array<ArrayBuffer>;
}
interface Uint32Array<TArrayBuffer extends ArrayBufferLike> {
    findLast<S extends number>(
        predicate: (
            value: number,
            index: number,
            array: this,
        ) => value is S,
        thisArg?: any,
    ): S | undefined;
    findLast(
        predicate: (
            value: number,
            index: number,
            array: this,
        ) => unknown,
        thisArg?: any,
    ): number | undefined;
    findLastIndex(
        predicate: (
            value: number,
            index: number,
            array: this,
        ) => unknown,
        thisArg?: any,
    ): number;
    toReversed(): Uint32Array<ArrayBuffer>;
    toSorted(compareFn?: (a: number, b: number) => number): Uint32Array<ArrayBuffer>;
    with(index: number, value: number): Uint32Array<ArrayBuffer>;
}
interface Float32Array<TArrayBuffer extends ArrayBufferLike> {
    findLast<S extends number>(
        predicate: (
            value: number,
            index: number,
            array: this,
        ) => value is S,
        thisArg?: any,
    ): S | undefined;
    findLast(
        predicate: (
            value: number,
            index: number,
            array: this,
        ) => unknown,
        thisArg?: any,
    ): number | undefined;
    findLastIndex(
        predicate: (
            value: number,
            index: number,
            array: this,
        ) => unknown,
        thisArg?: any,
    ): number;
    toReversed(): Float32Array<ArrayBuffer>;
    toSorted(compareFn?: (a: number, b: number) => number): Float32Array<ArrayBuffer>;
    with(index: number, value: number): Float32Array<ArrayBuffer>;
}
interface Float64Array<TArrayBuffer extends ArrayBufferLike> {
    findLast<S extends number>(
        predicate: (
            value: number,
            index: number,
            array: this,
        ) => value is S,
        thisArg?: any,
    ): S | undefined;
    findLast(
        predicate: (
            value: number,
            index: number,
            array: this,
        ) => unknown,
        thisArg?: any,
    ): number | undefined;
    findLastIndex(
        predicate: (
            value: number,
            index: number,
            array: this,
        ) => unknown,
        thisArg?: any,
    ): number;
    toReversed(): Float64Array<ArrayBuffer>;
    toSorted(compareFn?: (a: number, b: number) => number): Float64Array<ArrayBuffer>;
    with(index: number, value: number): Float64Array<ArrayBuffer>;
}
interface BigInt64Array<TArrayBuffer extends ArrayBufferLike> {
    findLast<S extends bigint>(
        predicate: (
            value: bigint,
            index: number,
            array: this,
        ) => value is S,
        thisArg?: any,
    ): S | undefined;
    findLast(
        predicate: (
            value: bigint,
            index: number,
            array: this,
        ) => unknown,
        thisArg?: any,
    ): bigint | undefined;
    findLastIndex(
        predicate: (
            value: bigint,
            index: number,
            array: this,
        ) => unknown,
        thisArg?: any,
    ): number;
    toReversed(): BigInt64Array<ArrayBuffer>;
    toSorted(compareFn?: (a: bigint, b: bigint) => number): BigInt64Array<ArrayBuffer>;
    with(index: number, value: bigint): BigInt64Array<ArrayBuffer>;
}
interface BigUint64Array<TArrayBuffer extends ArrayBufferLike> {
    findLast<S extends bigint>(
        predicate: (
            value: bigint,
            index: number,
            array: this,
        ) => value is S,
        thisArg?: any,
    ): S | undefined;
    findLast(
        predicate: (
            value: bigint,
            index: number,
            array: this,
        ) => unknown,
        thisArg?: any,
    ): bigint | undefined;
    findLastIndex(
        predicate: (
            value: bigint,
            index: number,
            array: this,
        ) => unknown,
        thisArg?: any,
    ): number;
    toReversed(): BigUint64Array<ArrayBuffer>;
    toSorted(compareFn?: (a: bigint, b: bigint) => number): BigUint64Array<ArrayBuffer>;
    with(index: number, value: bigint): BigUint64Array<ArrayBuffer>;
}

// DataView (lib.es5.d.ts, with lib.es2020.bigint.d.ts's members).
interface DataView<TArrayBuffer extends ArrayBufferLike = ArrayBufferLike> {
    readonly buffer: TArrayBuffer;
    readonly byteLength: number;
    readonly byteOffset: number;
    getFloat32(byteOffset: number, littleEndian?: boolean): number;
    getFloat64(byteOffset: number, littleEndian?: boolean): number;
    getInt8(byteOffset: number): number;
    getInt16(byteOffset: number, littleEndian?: boolean): number;
    getInt32(byteOffset: number, littleEndian?: boolean): number;
    getUint8(byteOffset: number): number;
    getUint16(byteOffset: number, littleEndian?: boolean): number;
    getUint32(byteOffset: number, littleEndian?: boolean): number;
    setFloat32(byteOffset: number, value: number, littleEndian?: boolean): void;
    setFloat64(byteOffset: number, value: number, littleEndian?: boolean): void;
    setInt8(byteOffset: number, value: number): void;
    setInt16(byteOffset: number, value: number, littleEndian?: boolean): void;
    setInt32(byteOffset: number, value: number, littleEndian?: boolean): void;
    setUint8(byteOffset: number, value: number): void;
    setUint16(byteOffset: number, value: number, littleEndian?: boolean): void;
    setUint32(byteOffset: number, value: number, littleEndian?: boolean): void;
}
interface DataViewConstructor {
    readonly prototype: DataView<ArrayBufferLike>;
    new <TArrayBuffer extends ArrayBufferLike & { BYTES_PER_ELEMENT?: never; }>(buffer: TArrayBuffer, byteOffset?: number, byteLength?: number): DataView<TArrayBuffer>;
}
declare var DataView: DataViewConstructor;
interface DataView<TArrayBuffer extends ArrayBufferLike> {
    getBigInt64(byteOffset: number, littleEndian?: boolean): bigint;
    getBigUint64(byteOffset: number, littleEndian?: boolean): bigint;
    setBigInt64(byteOffset: number, value: bigint, littleEndian?: boolean): void;
    setBigUint64(byteOffset: number, value: bigint, littleEndian?: boolean): void;
}
