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
    /** @lower __kml_Object_is */
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
    /** @intrinsic String.prototype.localeCompare */
    localeCompare(that: string, locales?: string | string[], options?: any): number;
    normalize(form?: string): string;
    /** @lower __kml_String_padStart @link string */
    padStart(maxLength: number, fillString?: string): string;
    /** @lower __kml_String_padEnd @link string */
    padEnd(maxLength: number, fillString?: string): string;
    /** @lower __kml_String_repeat @link string */
    repeat(count: number): string;
    /** @intrinsic String.prototype.replace */
    replace(searchValue: string | RegExp, replaceValue: string): string;
    /** @intrinsic String.prototype.replace */
    replace(searchValue: string | RegExp, replacer: (substring: string, ...args: any[]) => string): string;
    /** @intrinsic String.prototype.replaceAll */
    replaceAll(searchValue: string | RegExp, replaceValue: string): string;
    /** @intrinsic String.prototype.replaceAll */
    replaceAll(searchValue: string | RegExp, replacer: (substring: string, ...args: any[]) => string): string;
    /** @intrinsic String.prototype.search */
    search(regexp: string | RegExp): number;
    /** @lower __kml_String_slice @link string */
    slice(start?: number, end?: number): string;
    /** @lower __kml_String_split @link string */
    split(separator: string, limit?: number): string[];
    /** @intrinsic String.prototype.split */
    split(separator: RegExp, limit?: number): string[];
    /** @intrinsic String.prototype.split */
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
    /** @intrinsic String.fromCharCode */
    fromCharCode(...codes: number[]): string;
    /** @intrinsic String.fromCodePoint */
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
    /** @intrinsic Number.isFinite */
    isFinite(number: unknown): boolean;
    /** @intrinsic Number.isInteger */
    isInteger(number: unknown): boolean;
    /** @intrinsic Number.isNaN */
    isNaN(number: unknown): boolean;
    /** @intrinsic Number.isSafeInteger */
    isSafeInteger(number: unknown): boolean;
    /** @intrinsic parseFloat */
    parseFloat(string: string): number;
    /** @intrinsic parseInt */
    parseInt(string: string, radix?: number): number;
}

declare var Number: NumberConstructor;

// A method with `@lower f` compiles to a call of the libm function f, whose
// result is the one ECMAScript's Math specifies (TDD-00230 P3.2); one with
// `@intrinsic Math.f` has JavaScript semantics of its own (Math.round's
// ties, pow's NaN cases, an integer kept an integer, …) and is emitted
// inline (emit_intrinsics.go).
interface Math {
    readonly E: number;
    readonly LN10: number;
    readonly LN2: number;
    readonly LOG2E: number;
    readonly LOG10E: number;
    readonly PI: number;
    readonly SQRT1_2: number;
    readonly SQRT2: number;
    /** @intrinsic Math.abs */
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
    /** @intrinsic Math.cbrt */
    cbrt(x: number): number;
    /** @intrinsic Math.ceil */
    ceil(x: number): number;
    /** @intrinsic Math.clz32 */
    clz32(x: number): number;
    /** @lower cos @link m */
    cos(x: number): number;
    /** @lower cosh @link m */
    cosh(x: number): number;
    /** @lower exp @link m */
    exp(x: number): number;
    /** @lower expm1 @link m */
    expm1(x: number): number;
    /** @intrinsic Math.floor */
    floor(x: number): number;
    /** @intrinsic Math.fround */
    fround(x: number): number;
    /** @intrinsic Math.hypot */
    hypot(...values: number[]): number;
    /** @intrinsic Math.imul */
    imul(x: number, y: number): number;
    /** @lower log @link m */
    log(x: number): number;
    /** @lower log10 @link m */
    log10(x: number): number;
    /** @lower log1p @link m */
    log1p(x: number): number;
    /** @lower log2 @link m */
    log2(x: number): number;
    /** @intrinsic Math.max */
    max(...values: number[]): number;
    /** @intrinsic Math.min */
    min(...values: number[]): number;
    /** @intrinsic Math.pow */
    pow(x: number, y: number): number;
    /** @intrinsic Math.random */
    random(): number;
    /** @intrinsic Math.round */
    round(x: number): number;
    /** @intrinsic Math.sign */
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
    /** @intrinsic Math.trunc */
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
    [Symbol.iterator](): ArrayIterator<T>;
    /** @intrinsic Array.prototype.entries */
    entries(): ArrayIterator<[number, T]>;
    /** @intrinsic Array.prototype.keys */
    keys(): ArrayIterator<number>;
    /** @intrinsic Array.prototype.values */
    values(): ArrayIterator<T>;
    readonly length: number;
    readonly [n: number]: T;
    toString(): string;
    /** @intrinsic Array.prototype.join */
    join(separator?: string): string;
    /** @intrinsic Array.prototype.slice */
    slice(start?: number, end?: number): T[];
    /** @intrinsic Array.prototype.indexOf */
    indexOf(searchElement: T, fromIndex?: number): number;
    /** @intrinsic Array.prototype.lastIndexOf */
    lastIndexOf(searchElement: T, fromIndex?: number): number;
    /** @intrinsic Array.prototype.includes */
    includes(searchElement: T, fromIndex?: number): boolean;
    /** @intrinsic Array.prototype.every */
    every<S extends T>(predicate: (value: T, index: number, array: readonly T[]) => value is S, thisArg?: any): this is readonly S[];
    /** @intrinsic Array.prototype.every */
    every(predicate: (value: T, index: number, array: readonly T[]) => unknown, thisArg?: any): boolean;
    /** @intrinsic Array.prototype.some */
    some(predicate: (value: T, index: number, array: readonly T[]) => unknown, thisArg?: any): boolean;
    /** @intrinsic Array.prototype.forEach */
    forEach(callbackfn: (value: T, index: number, array: readonly T[]) => void, thisArg?: any): void;
    /** @intrinsic Array.prototype.map */
    map<U>(callbackfn: (value: T, index: number, array: readonly T[]) => U, thisArg?: any): U[];
    /** @intrinsic Array.prototype.filter */
    filter<S extends T>(predicate: (value: T, index: number, array: readonly T[]) => value is S, thisArg?: any): S[];
    /** @intrinsic Array.prototype.filter */
    filter(predicate: (value: T, index: number, array: readonly T[]) => unknown, thisArg?: any): T[];
    /** @intrinsic Array.prototype.find */
    find<S extends T>(predicate: (value: T, index: number, obj: readonly T[]) => value is S, thisArg?: any): S | undefined;
    /** @intrinsic Array.prototype.find */
    find(predicate: (value: T, index: number, obj: readonly T[]) => unknown, thisArg?: any): T | undefined;
    /** @intrinsic Array.prototype.findIndex */
    findIndex(predicate: (value: T, index: number, obj: readonly T[]) => unknown, thisArg?: any): number;
    /** @intrinsic Array.prototype.at */
    at(index: number): T | undefined;
}

interface Array<T> {
    length: number;
    [n: number]: T;
    [Symbol.iterator](): ArrayIterator<T>;
    /** @intrinsic Array.prototype.entries */
    entries(): ArrayIterator<[number, T]>;
    /** @intrinsic Array.prototype.keys */
    keys(): ArrayIterator<number>;
    /** @intrinsic Array.prototype.values */
    values(): ArrayIterator<T>;
    toString(): string;
    /** @intrinsic Array.prototype.pop */
    pop(): T | undefined;
    /** @intrinsic Array.prototype.push */
    push(...items: T[]): number;
    /** @intrinsic Array.prototype.concat */
    concat(...items: (T | T[])[]): T[];
    /** @intrinsic Array.prototype.join */
    join(separator?: string): string;
    /** @intrinsic Array.prototype.reverse */
    reverse(): T[];
    /** @intrinsic Array.prototype.shift */
    shift(): T | undefined;
    /** @intrinsic Array.prototype.slice */
    slice(start?: number, end?: number): T[];
    /** @intrinsic Array.prototype.sort */
    sort(compareFn?: (a: T, b: T) => number): this;
    /** @intrinsic Array.prototype.splice */
    splice(start: number, deleteCount?: number, ...items: T[]): T[];
    /** @intrinsic Array.prototype.unshift */
    unshift(...items: T[]): number;
    /** @intrinsic Array.prototype.indexOf */
    indexOf(searchElement: T, fromIndex?: number): number;
    /** @intrinsic Array.prototype.lastIndexOf */
    lastIndexOf(searchElement: T, fromIndex?: number): number;
    /** @intrinsic Array.prototype.includes */
    includes(searchElement: T, fromIndex?: number): boolean;
    /** @intrinsic Array.prototype.every */
    every<S extends T>(predicate: (value: T, index: number, array: T[]) => value is S, thisArg?: any): this is S[];
    /** @intrinsic Array.prototype.every */
    every(predicate: (value: T, index: number, array: T[]) => unknown, thisArg?: any): boolean;
    /** @intrinsic Array.prototype.some */
    some(predicate: (value: T, index: number, array: T[]) => unknown, thisArg?: any): boolean;
    /** @intrinsic Array.prototype.forEach */
    forEach(callbackfn: (value: T, index: number, array: T[]) => void, thisArg?: any): void;
    /** @intrinsic Array.prototype.map */
    map<U>(callbackfn: (value: T, index: number, array: T[]) => U, thisArg?: any): U[];
    /** @intrinsic Array.prototype.filter */
    filter<S extends T>(predicate: (value: T, index: number, array: T[]) => value is S, thisArg?: any): S[];
    /** @intrinsic Array.prototype.filter */
    filter(predicate: (value: T, index: number, array: T[]) => unknown, thisArg?: any): T[];
    /** @intrinsic Array.prototype.reduce */
    reduce(callbackfn: (previousValue: T, currentValue: T, currentIndex: number, array: T[]) => T): T;
    /** @intrinsic Array.prototype.reduce */
    reduce(callbackfn: (previousValue: T, currentValue: T, currentIndex: number, array: T[]) => T, initialValue: T): T;
    /** @intrinsic Array.prototype.reduce */
    reduce<U>(callbackfn: (previousValue: U, currentValue: T, currentIndex: number, array: T[]) => U, initialValue: U): U;
    /** @intrinsic Array.prototype.reduceRight */
    reduceRight(callbackfn: (previousValue: T, currentValue: T, currentIndex: number, array: T[]) => T): T;
    /** @intrinsic Array.prototype.reduceRight */
    reduceRight(callbackfn: (previousValue: T, currentValue: T, currentIndex: number, array: T[]) => T, initialValue: T): T;
    /** @intrinsic Array.prototype.reduceRight */
    reduceRight<U>(callbackfn: (previousValue: U, currentValue: T, currentIndex: number, array: T[]) => U, initialValue: U): U;
    /** @intrinsic Array.prototype.find */
    find<S extends T>(predicate: (value: T, index: number, obj: T[]) => value is S, thisArg?: any): S | undefined;
    /** @intrinsic Array.prototype.find */
    find(predicate: (value: T, index: number, obj: T[]) => unknown, thisArg?: any): T | undefined;
    /** @intrinsic Array.prototype.findIndex */
    findIndex(predicate: (value: T, index: number, obj: T[]) => unknown, thisArg?: any): number;
    /** @intrinsic Array.prototype.findLast */
    findLast<S extends T>(predicate: (value: T, index: number, array: T[]) => value is S, thisArg?: any): S | undefined;
    /** @intrinsic Array.prototype.findLast */
    findLast(predicate: (value: T, index: number, obj: T[]) => unknown, thisArg?: any): T | undefined;
    /** @intrinsic Array.prototype.findLastIndex */
    findLastIndex(predicate: (value: T, index: number, obj: T[]) => unknown, thisArg?: any): number;
    /** @intrinsic Array.prototype.fill */
    fill(value: T, start?: number, end?: number): this;
    /** @intrinsic Array.prototype.copyWithin */
    copyWithin(target: number, start: number, end?: number): this;
    /** @intrinsic Array.prototype.flatMap */
    flatMap<U>(callback: (value: T, index: number, array: T[]) => U | readonly U[], thisArg?: any): U[];
    /** @intrinsic Array.prototype.at */
    at(index: number): T | undefined;
    /** @intrinsic Array.prototype.toReversed */
    toReversed(): T[];
    /** @intrinsic Array.prototype.toSorted */
    toSorted(compareFn?: (a: T, b: T) => number): T[];
    /** @intrinsic Array.prototype.toSpliced */
    toSpliced(start: number, deleteCount?: number, ...items: T[]): T[];
    /** @intrinsic Array.prototype.with */
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
    from<T>(arrayLike: ArrayLike<T>): T[];
    from<T, U>(arrayLike: ArrayLike<T>, mapfn: (v: T, k: number) => U, thisArg?: any): U[];
    from<T>(iterable: Iterable<T> | ArrayLike<T>): T[];
    from<T, U>(iterable: Iterable<T> | ArrayLike<T>, mapfn: (v: T, k: number) => U, thisArg?: any): U[];
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
    /** @intrinsic Date.prototype.toString */
    toString(): string;
    /** @intrinsic Date.prototype.toDateString */
    toDateString(): string;
    toTimeString(): string;
    toLocaleString(locales?: string | string[], options?: any): string;
    /** @intrinsic Date.prototype.toLocaleDateString */
    toLocaleDateString(locales?: string | string[], options?: any): string;
    toLocaleTimeString(locales?: string | string[], options?: any): string;
    /** @intrinsic Date.prototype.valueOf */
    valueOf(): number;
    /** @intrinsic Date.prototype.getTime */
    getTime(): number;
    /** @intrinsic Date.prototype.getFullYear */
    getFullYear(): number;
    /** @intrinsic Date.prototype.getUTCFullYear */
    getUTCFullYear(): number;
    /** @intrinsic Date.prototype.getMonth */
    getMonth(): number;
    /** @intrinsic Date.prototype.getUTCMonth */
    getUTCMonth(): number;
    /** @intrinsic Date.prototype.getDate */
    getDate(): number;
    /** @intrinsic Date.prototype.getUTCDate */
    getUTCDate(): number;
    /** @intrinsic Date.prototype.getDay */
    getDay(): number;
    /** @intrinsic Date.prototype.getUTCDay */
    getUTCDay(): number;
    /** @intrinsic Date.prototype.getHours */
    getHours(): number;
    /** @intrinsic Date.prototype.getUTCHours */
    getUTCHours(): number;
    /** @intrinsic Date.prototype.getMinutes */
    getMinutes(): number;
    /** @intrinsic Date.prototype.getUTCMinutes */
    getUTCMinutes(): number;
    /** @intrinsic Date.prototype.getSeconds */
    getSeconds(): number;
    /** @intrinsic Date.prototype.getUTCSeconds */
    getUTCSeconds(): number;
    /** @intrinsic Date.prototype.getMilliseconds */
    getMilliseconds(): number;
    /** @intrinsic Date.prototype.getUTCMilliseconds */
    getUTCMilliseconds(): number;
    /** @intrinsic Date.prototype.getTimezoneOffset */
    getTimezoneOffset(): number;
    /** @intrinsic Date.prototype.setTime */
    setTime(time: number): number;
    /** @intrinsic Date.prototype.setMilliseconds */
    setMilliseconds(ms: number): number;
    /** @intrinsic Date.prototype.setSeconds */
    setSeconds(sec: number, ms?: number): number;
    /** @intrinsic Date.prototype.setMinutes */
    setMinutes(min: number, sec?: number, ms?: number): number;
    /** @intrinsic Date.prototype.setHours */
    setHours(hours: number, min?: number, sec?: number, ms?: number): number;
    /** @intrinsic Date.prototype.setDate */
    setDate(date: number): number;
    /** @intrinsic Date.prototype.setMonth */
    setMonth(month: number, date?: number): number;
    /** @intrinsic Date.prototype.setFullYear */
    setFullYear(year: number, month?: number, date?: number): number;
    /** @intrinsic Date.prototype.toUTCString */
    toUTCString(): string;
    /** @intrinsic Date.prototype.toISOString */
    toISOString(): string;
    toJSON(key?: any): string;
    /** @intrinsic Date.prototype.setUTCMilliseconds */
    setUTCMilliseconds(ms: number): number;
    /** @intrinsic Date.prototype.setUTCSeconds */
    setUTCSeconds(sec: number, ms?: number): number;
    /** @intrinsic Date.prototype.setUTCMinutes */
    setUTCMinutes(min: number, sec?: number, ms?: number): number;
    /** @intrinsic Date.prototype.setUTCHours */
    setUTCHours(hours: number, min?: number, sec?: number, ms?: number): number;
    /** @intrinsic Date.prototype.setUTCDate */
    setUTCDate(date: number): number;
    /** @intrinsic Date.prototype.setUTCMonth */
    setUTCMonth(month: number, date?: number): number;
    /** @intrinsic Date.prototype.setUTCFullYear */
    setUTCFullYear(year: number, month?: number, date?: number): number;
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
    groups?: { [key: string]: string };
}

interface RegExp {
    /** @intrinsic RegExp.prototype.exec */
    exec(string: string): RegExpExecArray | null;
    /** @intrinsic RegExp.prototype.test */
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

interface IteratorYieldResult<TYield> {
    done?: false;
    value: TYield;
}
interface IteratorReturnResult<TReturn> {
    done: true;
    value: TReturn;
}
type IteratorResult<T, TReturn = any> = IteratorYieldResult<T> | IteratorReturnResult<TReturn>;

interface Iterable<T> {}
interface Iterator<T, TReturn = any, TNext = any> {
    next(...[value]: [] | [TNext]): IteratorResult<T, TReturn>;
    return?(value?: TReturn): IteratorResult<T, TReturn>;
    throw?(e?: any): IteratorResult<T, TReturn>;
}
interface IterableIterator<T> extends Iterator<T> {}

interface IteratorObject<T, TReturn = unknown, TNext = unknown> extends Iterator<T, TReturn, TNext> {
    [Symbol.iterator](): IteratorObject<T, TReturn, TNext>;
    toArray(): T[];
}

type BuiltinIteratorReturn = any;

interface ArrayIterator<T> extends IteratorObject<T, BuiltinIteratorReturn, unknown> {
    [Symbol.iterator](): ArrayIterator<T>;
}

interface MapIterator<T> extends IteratorObject<T, BuiltinIteratorReturn, unknown> {
    [Symbol.iterator](): MapIterator<T>;
}

interface SetIterator<T> extends IteratorObject<T, BuiltinIteratorReturn, unknown> {
    [Symbol.iterator](): SetIterator<T>;
}
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
    /** @intrinsic Map.prototype.clear */
    clear(): void;
    /** @intrinsic Map.prototype.delete */
    delete(key: K): boolean;
    /** @intrinsic Map.prototype.forEach */
    forEach(callbackfn: (value: V, key: K, map: Map<K, V>) => void, thisArg?: any): void;
    /** @intrinsic Map.prototype.get */
    get(key: K): V | undefined;
    /** @intrinsic Map.prototype.has */
    has(key: K): boolean;
    /** @intrinsic Map.prototype.set */
    set(key: K, value: V): this;
    readonly size: number;
    [Symbol.iterator](): MapIterator<[K, V]>;
    /** @intrinsic Map.prototype.entries */
    entries(): MapIterator<[K, V]>;
    /** @intrinsic Map.prototype.keys */
    keys(): MapIterator<K>;
    /** @intrinsic Map.prototype.values */
    values(): MapIterator<V>;
}

interface MapConstructor {
    new (): Map<any, any>;
    new <K, V>(entries?: readonly (readonly [K, V])[] | null): Map<K, V>;
    new <K, V>(iterable?: Iterable<readonly [K, V]> | null): Map<K, V>;
    readonly prototype: Map<any, any>;
}

declare var Map: MapConstructor;

interface Set<T> {
    /** @intrinsic Set.prototype.add */
    add(value: T): this;
    /** @intrinsic Set.prototype.clear */
    clear(): void;
    /** @intrinsic Set.prototype.delete */
    delete(value: T): boolean;
    /** @intrinsic Set.prototype.forEach */
    forEach(callbackfn: (value: T, value2: T, set: Set<T>) => void, thisArg?: any): void;
    /** @intrinsic Set.prototype.has */
    has(value: T): boolean;
    readonly size: number;
    [Symbol.iterator](): SetIterator<T>;
    /** @intrinsic Set.prototype.entries */
    entries(): SetIterator<[T, T]>;
    /** @intrinsic Set.prototype.keys */
    keys(): SetIterator<T>;
    /** @intrinsic Set.prototype.values */
    values(): SetIterator<T>;
}

interface SetConstructor {
    new <T = any>(values?: readonly T[] | null): Set<T>;
    new <T>(iterable?: Iterable<T> | null): Set<T>;
    readonly prototype: Set<any>;
}

declare var Set: SetConstructor;

interface WeakMap<K extends object, V> {
    /** @intrinsic WeakMap.prototype.delete */
    delete(key: K): boolean;
    /** @intrinsic WeakMap.prototype.get */
    get(key: K): V | undefined;
    /** @intrinsic WeakMap.prototype.has */
    has(key: K): boolean;
    /** @intrinsic WeakMap.prototype.set */
    set(key: K, value: V): this;
}

interface WeakMapConstructor {
    new <K extends object = object, V = any>(entries?: readonly (readonly [K, V])[] | null): WeakMap<K, V>;
    new <K extends object, V>(iterable: Iterable<readonly [K, V]>): WeakMap<K, V>;
    readonly prototype: WeakMap<object, any>;
}

declare var WeakMap: WeakMapConstructor;

interface WeakSet<T extends object> {
    /** @intrinsic WeakSet.prototype.add */
    add(value: T): this;
    /** @intrinsic WeakSet.prototype.delete */
    delete(value: T): boolean;
    /** @intrinsic WeakSet.prototype.has */
    has(value: T): boolean;
}

interface WeakSetConstructor {
    new <T extends object = object>(values?: readonly T[] | null): WeakSet<T>;
    new <T extends object>(iterable: Iterable<T>): WeakSet<T>;
    readonly prototype: WeakSet<object>;
}

declare var WeakSet: WeakSetConstructor;

interface WeakRef<T extends object> {
    deref(): T | undefined;
}
interface WeakRefConstructor {
    readonly prototype: WeakRef<any>;
    new <T extends object>(target: T): WeakRef<T>;
}
declare var WeakRef: WeakRefConstructor;

interface FinalizationRegistry<T> {
    register(target: object, heldValue: T, unregisterToken?: object): void;
    unregister(unregisterToken: object): boolean;
}
interface FinalizationRegistryConstructor {
    readonly prototype: FinalizationRegistry<any>;
    new <T>(cleanupCallback: (heldValue: T) => void): FinalizationRegistry<T>;
}
declare var FinalizationRegistry: FinalizationRegistryConstructor;

interface ProxyHandler<T extends object> {
    apply?(target: T, thisArg: any, argArray: any[]): any;
    construct?(target: T, argArray: any[], newTarget: Function): object;
    defineProperty?(target: T, property: string | symbol, attributes: PropertyDescriptor): boolean;
    deleteProperty?(target: T, p: string | symbol): boolean;
    get?(target: T, p: string | symbol, receiver: any): any;
    getOwnPropertyDescriptor?(target: T, p: string | symbol): PropertyDescriptor | undefined;
    getPrototypeOf?(target: T): object | null;
    has?(target: T, p: string | symbol): boolean;
    isExtensible?(target: T): boolean;
    ownKeys?(target: T): ArrayLike<string | symbol>;
    preventExtensions?(target: T): boolean;
    set?(target: T, p: string | symbol, newValue: any, receiver: any): boolean;
    setPrototypeOf?(target: T, v: object | null): boolean;
}
interface ProxyConstructor {
    revocable<T extends object>(target: T, handler: ProxyHandler<T>): { proxy: T; revoke: () => void; };
    new <T extends object>(target: T, handler: ProxyHandler<T>): T;
}
declare var Proxy: ProxyConstructor;

declare var NaN: number;
declare var Infinity: number;
/** @intrinsic parseInt */
declare function parseInt(string: string, radix?: number): number;
/** @intrinsic parseFloat */
declare function parseFloat(string: string): number;
/** @intrinsic isNaN */
declare function isNaN(number: number): boolean;
/** @intrinsic isFinite */
declare function isFinite(number: number): boolean;
/** @intrinsic decodeURI */
declare function decodeURI(encodedURI: string): string;
/** @intrinsic decodeURIComponent */
declare function decodeURIComponent(encodedURIComponent: string): string;
/** @intrinsic encodeURI */
declare function encodeURI(uri: string): string;
/** @intrinsic encodeURIComponent */
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
    /** @intrinsic Array.prototype.copyWithin */
    copyWithin(target: number, start: number, end?: number): this;
    /** @intrinsic Array.prototype.every */
    every(predicate: (value: number, index: number, array: this) => unknown, thisArg?: any): boolean;
    /** @intrinsic Array.prototype.fill */
    fill(value: number, start?: number, end?: number): this;
    /** @intrinsic Array.prototype.filter */
    filter(predicate: (value: number, index: number, array: this) => any, thisArg?: any): Int8Array<ArrayBuffer>;
    /** @intrinsic Array.prototype.find */
    find(predicate: (value: number, index: number, obj: this) => boolean, thisArg?: any): number | undefined;
    /** @intrinsic Array.prototype.findIndex */
    findIndex(predicate: (value: number, index: number, obj: this) => boolean, thisArg?: any): number;
    /** @intrinsic Array.prototype.forEach */
    forEach(callbackfn: (value: number, index: number, array: this) => void, thisArg?: any): void;
    /** @intrinsic Array.prototype.indexOf */
    indexOf(searchElement: number, fromIndex?: number): number;
    /** @intrinsic Array.prototype.join */
    join(separator?: string): string;
    /** @intrinsic Array.prototype.lastIndexOf */
    lastIndexOf(searchElement: number, fromIndex?: number): number;
    readonly length: number;
    /** @intrinsic Array.prototype.map */
    map(callbackfn: (value: number, index: number, array: this) => number, thisArg?: any): Int8Array<ArrayBuffer>;
    /** @intrinsic Array.prototype.reduce */
    reduce(callbackfn: (previousValue: number, currentValue: number, currentIndex: number, array: this) => number): number;
    /** @intrinsic Array.prototype.reduce */
    reduce(callbackfn: (previousValue: number, currentValue: number, currentIndex: number, array: this) => number, initialValue: number): number;
    /** @intrinsic Array.prototype.reduce */
    reduce<U>(callbackfn: (previousValue: U, currentValue: number, currentIndex: number, array: this) => U, initialValue: U): U;
    /** @intrinsic Array.prototype.reduceRight */
    reduceRight(callbackfn: (previousValue: number, currentValue: number, currentIndex: number, array: this) => number): number;
    /** @intrinsic Array.prototype.reduceRight */
    reduceRight(callbackfn: (previousValue: number, currentValue: number, currentIndex: number, array: this) => number, initialValue: number): number;
    /** @intrinsic Array.prototype.reduceRight */
    reduceRight<U>(callbackfn: (previousValue: U, currentValue: number, currentIndex: number, array: this) => U, initialValue: U): U;
    /** @intrinsic Array.prototype.reverse */
    reverse(): this;
    set(array: ArrayLike<number>, offset?: number): void;
    /** @intrinsic Array.prototype.slice */
    slice(start?: number, end?: number): Int8Array<ArrayBuffer>;
    /** @intrinsic Array.prototype.some */
    some(predicate: (value: number, index: number, array: this) => unknown, thisArg?: any): boolean;
    /** @intrinsic Array.prototype.sort */
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
    /** @intrinsic Array.prototype.copyWithin */
    copyWithin(target: number, start: number, end?: number): this;
    /** @intrinsic Array.prototype.every */
    every(predicate: (value: number, index: number, array: this) => unknown, thisArg?: any): boolean;
    /** @intrinsic Array.prototype.fill */
    fill(value: number, start?: number, end?: number): this;
    /** @intrinsic Array.prototype.filter */
    filter(predicate: (value: number, index: number, array: this) => any, thisArg?: any): Uint8Array<ArrayBuffer>;
    /** @intrinsic Array.prototype.find */
    find(predicate: (value: number, index: number, obj: this) => boolean, thisArg?: any): number | undefined;
    /** @intrinsic Array.prototype.findIndex */
    findIndex(predicate: (value: number, index: number, obj: this) => boolean, thisArg?: any): number;
    /** @intrinsic Array.prototype.forEach */
    forEach(callbackfn: (value: number, index: number, array: this) => void, thisArg?: any): void;
    /** @intrinsic Array.prototype.indexOf */
    indexOf(searchElement: number, fromIndex?: number): number;
    /** @intrinsic Array.prototype.join */
    join(separator?: string): string;
    /** @intrinsic Array.prototype.lastIndexOf */
    lastIndexOf(searchElement: number, fromIndex?: number): number;
    readonly length: number;
    /** @intrinsic Array.prototype.map */
    map(callbackfn: (value: number, index: number, array: this) => number, thisArg?: any): Uint8Array<ArrayBuffer>;
    /** @intrinsic Array.prototype.reduce */
    reduce(callbackfn: (previousValue: number, currentValue: number, currentIndex: number, array: this) => number): number;
    /** @intrinsic Array.prototype.reduce */
    reduce(callbackfn: (previousValue: number, currentValue: number, currentIndex: number, array: this) => number, initialValue: number): number;
    /** @intrinsic Array.prototype.reduce */
    reduce<U>(callbackfn: (previousValue: U, currentValue: number, currentIndex: number, array: this) => U, initialValue: U): U;
    /** @intrinsic Array.prototype.reduceRight */
    reduceRight(callbackfn: (previousValue: number, currentValue: number, currentIndex: number, array: this) => number): number;
    /** @intrinsic Array.prototype.reduceRight */
    reduceRight(callbackfn: (previousValue: number, currentValue: number, currentIndex: number, array: this) => number, initialValue: number): number;
    /** @intrinsic Array.prototype.reduceRight */
    reduceRight<U>(callbackfn: (previousValue: U, currentValue: number, currentIndex: number, array: this) => U, initialValue: U): U;
    /** @intrinsic Array.prototype.reverse */
    reverse(): this;
    set(array: ArrayLike<number>, offset?: number): void;
    /** @intrinsic Array.prototype.slice */
    slice(start?: number, end?: number): Uint8Array<ArrayBuffer>;
    /** @intrinsic Array.prototype.some */
    some(predicate: (value: number, index: number, array: this) => unknown, thisArg?: any): boolean;
    /** @intrinsic Array.prototype.sort */
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
    /** @intrinsic Array.prototype.copyWithin */
    copyWithin(target: number, start: number, end?: number): this;
    /** @intrinsic Array.prototype.every */
    every(predicate: (value: number, index: number, array: this) => unknown, thisArg?: any): boolean;
    /** @intrinsic Array.prototype.fill */
    fill(value: number, start?: number, end?: number): this;
    /** @intrinsic Array.prototype.filter */
    filter(predicate: (value: number, index: number, array: this) => any, thisArg?: any): Uint8ClampedArray<ArrayBuffer>;
    /** @intrinsic Array.prototype.find */
    find(predicate: (value: number, index: number, obj: this) => boolean, thisArg?: any): number | undefined;
    /** @intrinsic Array.prototype.findIndex */
    findIndex(predicate: (value: number, index: number, obj: this) => boolean, thisArg?: any): number;
    /** @intrinsic Array.prototype.forEach */
    forEach(callbackfn: (value: number, index: number, array: this) => void, thisArg?: any): void;
    /** @intrinsic Array.prototype.indexOf */
    indexOf(searchElement: number, fromIndex?: number): number;
    /** @intrinsic Array.prototype.join */
    join(separator?: string): string;
    /** @intrinsic Array.prototype.lastIndexOf */
    lastIndexOf(searchElement: number, fromIndex?: number): number;
    readonly length: number;
    /** @intrinsic Array.prototype.map */
    map(callbackfn: (value: number, index: number, array: this) => number, thisArg?: any): Uint8ClampedArray<ArrayBuffer>;
    /** @intrinsic Array.prototype.reduce */
    reduce(callbackfn: (previousValue: number, currentValue: number, currentIndex: number, array: this) => number): number;
    /** @intrinsic Array.prototype.reduce */
    reduce(callbackfn: (previousValue: number, currentValue: number, currentIndex: number, array: this) => number, initialValue: number): number;
    /** @intrinsic Array.prototype.reduce */
    reduce<U>(callbackfn: (previousValue: U, currentValue: number, currentIndex: number, array: this) => U, initialValue: U): U;
    /** @intrinsic Array.prototype.reduceRight */
    reduceRight(callbackfn: (previousValue: number, currentValue: number, currentIndex: number, array: this) => number): number;
    /** @intrinsic Array.prototype.reduceRight */
    reduceRight(callbackfn: (previousValue: number, currentValue: number, currentIndex: number, array: this) => number, initialValue: number): number;
    /** @intrinsic Array.prototype.reduceRight */
    reduceRight<U>(callbackfn: (previousValue: U, currentValue: number, currentIndex: number, array: this) => U, initialValue: U): U;
    /** @intrinsic Array.prototype.reverse */
    reverse(): this;
    set(array: ArrayLike<number>, offset?: number): void;
    /** @intrinsic Array.prototype.slice */
    slice(start?: number, end?: number): Uint8ClampedArray<ArrayBuffer>;
    /** @intrinsic Array.prototype.some */
    some(predicate: (value: number, index: number, array: this) => unknown, thisArg?: any): boolean;
    /** @intrinsic Array.prototype.sort */
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
    /** @intrinsic Array.prototype.copyWithin */
    copyWithin(target: number, start: number, end?: number): this;
    /** @intrinsic Array.prototype.every */
    every(predicate: (value: number, index: number, array: this) => unknown, thisArg?: any): boolean;
    /** @intrinsic Array.prototype.fill */
    fill(value: number, start?: number, end?: number): this;
    /** @intrinsic Array.prototype.filter */
    filter(predicate: (value: number, index: number, array: this) => any, thisArg?: any): Int16Array<ArrayBuffer>;
    /** @intrinsic Array.prototype.find */
    find(predicate: (value: number, index: number, obj: this) => boolean, thisArg?: any): number | undefined;
    /** @intrinsic Array.prototype.findIndex */
    findIndex(predicate: (value: number, index: number, obj: this) => boolean, thisArg?: any): number;
    /** @intrinsic Array.prototype.forEach */
    forEach(callbackfn: (value: number, index: number, array: this) => void, thisArg?: any): void;
    /** @intrinsic Array.prototype.indexOf */
    indexOf(searchElement: number, fromIndex?: number): number;
    /** @intrinsic Array.prototype.join */
    join(separator?: string): string;
    /** @intrinsic Array.prototype.lastIndexOf */
    lastIndexOf(searchElement: number, fromIndex?: number): number;
    readonly length: number;
    /** @intrinsic Array.prototype.map */
    map(callbackfn: (value: number, index: number, array: this) => number, thisArg?: any): Int16Array<ArrayBuffer>;
    /** @intrinsic Array.prototype.reduce */
    reduce(callbackfn: (previousValue: number, currentValue: number, currentIndex: number, array: this) => number): number;
    /** @intrinsic Array.prototype.reduce */
    reduce(callbackfn: (previousValue: number, currentValue: number, currentIndex: number, array: this) => number, initialValue: number): number;
    /** @intrinsic Array.prototype.reduce */
    reduce<U>(callbackfn: (previousValue: U, currentValue: number, currentIndex: number, array: this) => U, initialValue: U): U;
    /** @intrinsic Array.prototype.reduceRight */
    reduceRight(callbackfn: (previousValue: number, currentValue: number, currentIndex: number, array: this) => number): number;
    /** @intrinsic Array.prototype.reduceRight */
    reduceRight(callbackfn: (previousValue: number, currentValue: number, currentIndex: number, array: this) => number, initialValue: number): number;
    /** @intrinsic Array.prototype.reduceRight */
    reduceRight<U>(callbackfn: (previousValue: U, currentValue: number, currentIndex: number, array: this) => U, initialValue: U): U;
    /** @intrinsic Array.prototype.reverse */
    reverse(): this;
    set(array: ArrayLike<number>, offset?: number): void;
    /** @intrinsic Array.prototype.slice */
    slice(start?: number, end?: number): Int16Array<ArrayBuffer>;
    /** @intrinsic Array.prototype.some */
    some(predicate: (value: number, index: number, array: this) => unknown, thisArg?: any): boolean;
    /** @intrinsic Array.prototype.sort */
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
    /** @intrinsic Array.prototype.copyWithin */
    copyWithin(target: number, start: number, end?: number): this;
    /** @intrinsic Array.prototype.every */
    every(predicate: (value: number, index: number, array: this) => unknown, thisArg?: any): boolean;
    /** @intrinsic Array.prototype.fill */
    fill(value: number, start?: number, end?: number): this;
    /** @intrinsic Array.prototype.filter */
    filter(predicate: (value: number, index: number, array: this) => any, thisArg?: any): Uint16Array<ArrayBuffer>;
    /** @intrinsic Array.prototype.find */
    find(predicate: (value: number, index: number, obj: this) => boolean, thisArg?: any): number | undefined;
    /** @intrinsic Array.prototype.findIndex */
    findIndex(predicate: (value: number, index: number, obj: this) => boolean, thisArg?: any): number;
    /** @intrinsic Array.prototype.forEach */
    forEach(callbackfn: (value: number, index: number, array: this) => void, thisArg?: any): void;
    /** @intrinsic Array.prototype.indexOf */
    indexOf(searchElement: number, fromIndex?: number): number;
    /** @intrinsic Array.prototype.join */
    join(separator?: string): string;
    /** @intrinsic Array.prototype.lastIndexOf */
    lastIndexOf(searchElement: number, fromIndex?: number): number;
    readonly length: number;
    /** @intrinsic Array.prototype.map */
    map(callbackfn: (value: number, index: number, array: this) => number, thisArg?: any): Uint16Array<ArrayBuffer>;
    /** @intrinsic Array.prototype.reduce */
    reduce(callbackfn: (previousValue: number, currentValue: number, currentIndex: number, array: this) => number): number;
    /** @intrinsic Array.prototype.reduce */
    reduce(callbackfn: (previousValue: number, currentValue: number, currentIndex: number, array: this) => number, initialValue: number): number;
    /** @intrinsic Array.prototype.reduce */
    reduce<U>(callbackfn: (previousValue: U, currentValue: number, currentIndex: number, array: this) => U, initialValue: U): U;
    /** @intrinsic Array.prototype.reduceRight */
    reduceRight(callbackfn: (previousValue: number, currentValue: number, currentIndex: number, array: this) => number): number;
    /** @intrinsic Array.prototype.reduceRight */
    reduceRight(callbackfn: (previousValue: number, currentValue: number, currentIndex: number, array: this) => number, initialValue: number): number;
    /** @intrinsic Array.prototype.reduceRight */
    reduceRight<U>(callbackfn: (previousValue: U, currentValue: number, currentIndex: number, array: this) => U, initialValue: U): U;
    /** @intrinsic Array.prototype.reverse */
    reverse(): this;
    set(array: ArrayLike<number>, offset?: number): void;
    /** @intrinsic Array.prototype.slice */
    slice(start?: number, end?: number): Uint16Array<ArrayBuffer>;
    /** @intrinsic Array.prototype.some */
    some(predicate: (value: number, index: number, array: this) => unknown, thisArg?: any): boolean;
    /** @intrinsic Array.prototype.sort */
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
    /** @intrinsic Array.prototype.copyWithin */
    copyWithin(target: number, start: number, end?: number): this;
    /** @intrinsic Array.prototype.every */
    every(predicate: (value: number, index: number, array: this) => unknown, thisArg?: any): boolean;
    /** @intrinsic Array.prototype.fill */
    fill(value: number, start?: number, end?: number): this;
    /** @intrinsic Array.prototype.filter */
    filter(predicate: (value: number, index: number, array: this) => any, thisArg?: any): Int32Array<ArrayBuffer>;
    /** @intrinsic Array.prototype.find */
    find(predicate: (value: number, index: number, obj: this) => boolean, thisArg?: any): number | undefined;
    /** @intrinsic Array.prototype.findIndex */
    findIndex(predicate: (value: number, index: number, obj: this) => boolean, thisArg?: any): number;
    /** @intrinsic Array.prototype.forEach */
    forEach(callbackfn: (value: number, index: number, array: this) => void, thisArg?: any): void;
    /** @intrinsic Array.prototype.indexOf */
    indexOf(searchElement: number, fromIndex?: number): number;
    /** @intrinsic Array.prototype.join */
    join(separator?: string): string;
    /** @intrinsic Array.prototype.lastIndexOf */
    lastIndexOf(searchElement: number, fromIndex?: number): number;
    readonly length: number;
    /** @intrinsic Array.prototype.map */
    map(callbackfn: (value: number, index: number, array: this) => number, thisArg?: any): Int32Array<ArrayBuffer>;
    /** @intrinsic Array.prototype.reduce */
    reduce(callbackfn: (previousValue: number, currentValue: number, currentIndex: number, array: this) => number): number;
    /** @intrinsic Array.prototype.reduce */
    reduce(callbackfn: (previousValue: number, currentValue: number, currentIndex: number, array: this) => number, initialValue: number): number;
    /** @intrinsic Array.prototype.reduce */
    reduce<U>(callbackfn: (previousValue: U, currentValue: number, currentIndex: number, array: this) => U, initialValue: U): U;
    /** @intrinsic Array.prototype.reduceRight */
    reduceRight(callbackfn: (previousValue: number, currentValue: number, currentIndex: number, array: this) => number): number;
    /** @intrinsic Array.prototype.reduceRight */
    reduceRight(callbackfn: (previousValue: number, currentValue: number, currentIndex: number, array: this) => number, initialValue: number): number;
    /** @intrinsic Array.prototype.reduceRight */
    reduceRight<U>(callbackfn: (previousValue: U, currentValue: number, currentIndex: number, array: this) => U, initialValue: U): U;
    /** @intrinsic Array.prototype.reverse */
    reverse(): this;
    set(array: ArrayLike<number>, offset?: number): void;
    /** @intrinsic Array.prototype.slice */
    slice(start?: number, end?: number): Int32Array<ArrayBuffer>;
    /** @intrinsic Array.prototype.some */
    some(predicate: (value: number, index: number, array: this) => unknown, thisArg?: any): boolean;
    /** @intrinsic Array.prototype.sort */
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
    /** @intrinsic Array.prototype.copyWithin */
    copyWithin(target: number, start: number, end?: number): this;
    /** @intrinsic Array.prototype.every */
    every(predicate: (value: number, index: number, array: this) => unknown, thisArg?: any): boolean;
    /** @intrinsic Array.prototype.fill */
    fill(value: number, start?: number, end?: number): this;
    /** @intrinsic Array.prototype.filter */
    filter(predicate: (value: number, index: number, array: this) => any, thisArg?: any): Uint32Array<ArrayBuffer>;
    /** @intrinsic Array.prototype.find */
    find(predicate: (value: number, index: number, obj: this) => boolean, thisArg?: any): number | undefined;
    /** @intrinsic Array.prototype.findIndex */
    findIndex(predicate: (value: number, index: number, obj: this) => boolean, thisArg?: any): number;
    /** @intrinsic Array.prototype.forEach */
    forEach(callbackfn: (value: number, index: number, array: this) => void, thisArg?: any): void;
    /** @intrinsic Array.prototype.indexOf */
    indexOf(searchElement: number, fromIndex?: number): number;
    /** @intrinsic Array.prototype.join */
    join(separator?: string): string;
    /** @intrinsic Array.prototype.lastIndexOf */
    lastIndexOf(searchElement: number, fromIndex?: number): number;
    readonly length: number;
    /** @intrinsic Array.prototype.map */
    map(callbackfn: (value: number, index: number, array: this) => number, thisArg?: any): Uint32Array<ArrayBuffer>;
    /** @intrinsic Array.prototype.reduce */
    reduce(callbackfn: (previousValue: number, currentValue: number, currentIndex: number, array: this) => number): number;
    /** @intrinsic Array.prototype.reduce */
    reduce(callbackfn: (previousValue: number, currentValue: number, currentIndex: number, array: this) => number, initialValue: number): number;
    /** @intrinsic Array.prototype.reduce */
    reduce<U>(callbackfn: (previousValue: U, currentValue: number, currentIndex: number, array: this) => U, initialValue: U): U;
    /** @intrinsic Array.prototype.reduceRight */
    reduceRight(callbackfn: (previousValue: number, currentValue: number, currentIndex: number, array: this) => number): number;
    /** @intrinsic Array.prototype.reduceRight */
    reduceRight(callbackfn: (previousValue: number, currentValue: number, currentIndex: number, array: this) => number, initialValue: number): number;
    /** @intrinsic Array.prototype.reduceRight */
    reduceRight<U>(callbackfn: (previousValue: U, currentValue: number, currentIndex: number, array: this) => U, initialValue: U): U;
    /** @intrinsic Array.prototype.reverse */
    reverse(): this;
    set(array: ArrayLike<number>, offset?: number): void;
    /** @intrinsic Array.prototype.slice */
    slice(start?: number, end?: number): Uint32Array<ArrayBuffer>;
    /** @intrinsic Array.prototype.some */
    some(predicate: (value: number, index: number, array: this) => unknown, thisArg?: any): boolean;
    /** @intrinsic Array.prototype.sort */
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
    /** @intrinsic Array.prototype.copyWithin */
    copyWithin(target: number, start: number, end?: number): this;
    /** @intrinsic Array.prototype.every */
    every(predicate: (value: number, index: number, array: this) => unknown, thisArg?: any): boolean;
    /** @intrinsic Array.prototype.fill */
    fill(value: number, start?: number, end?: number): this;
    /** @intrinsic Array.prototype.filter */
    filter(predicate: (value: number, index: number, array: this) => any, thisArg?: any): Float32Array<ArrayBuffer>;
    /** @intrinsic Array.prototype.find */
    find(predicate: (value: number, index: number, obj: this) => boolean, thisArg?: any): number | undefined;
    /** @intrinsic Array.prototype.findIndex */
    findIndex(predicate: (value: number, index: number, obj: this) => boolean, thisArg?: any): number;
    /** @intrinsic Array.prototype.forEach */
    forEach(callbackfn: (value: number, index: number, array: this) => void, thisArg?: any): void;
    /** @intrinsic Array.prototype.indexOf */
    indexOf(searchElement: number, fromIndex?: number): number;
    /** @intrinsic Array.prototype.join */
    join(separator?: string): string;
    /** @intrinsic Array.prototype.lastIndexOf */
    lastIndexOf(searchElement: number, fromIndex?: number): number;
    readonly length: number;
    /** @intrinsic Array.prototype.map */
    map(callbackfn: (value: number, index: number, array: this) => number, thisArg?: any): Float32Array<ArrayBuffer>;
    /** @intrinsic Array.prototype.reduce */
    reduce(callbackfn: (previousValue: number, currentValue: number, currentIndex: number, array: this) => number): number;
    /** @intrinsic Array.prototype.reduce */
    reduce(callbackfn: (previousValue: number, currentValue: number, currentIndex: number, array: this) => number, initialValue: number): number;
    /** @intrinsic Array.prototype.reduce */
    reduce<U>(callbackfn: (previousValue: U, currentValue: number, currentIndex: number, array: this) => U, initialValue: U): U;
    /** @intrinsic Array.prototype.reduceRight */
    reduceRight(callbackfn: (previousValue: number, currentValue: number, currentIndex: number, array: this) => number): number;
    /** @intrinsic Array.prototype.reduceRight */
    reduceRight(callbackfn: (previousValue: number, currentValue: number, currentIndex: number, array: this) => number, initialValue: number): number;
    /** @intrinsic Array.prototype.reduceRight */
    reduceRight<U>(callbackfn: (previousValue: U, currentValue: number, currentIndex: number, array: this) => U, initialValue: U): U;
    /** @intrinsic Array.prototype.reverse */
    reverse(): this;
    set(array: ArrayLike<number>, offset?: number): void;
    /** @intrinsic Array.prototype.slice */
    slice(start?: number, end?: number): Float32Array<ArrayBuffer>;
    /** @intrinsic Array.prototype.some */
    some(predicate: (value: number, index: number, array: this) => unknown, thisArg?: any): boolean;
    /** @intrinsic Array.prototype.sort */
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
    /** @intrinsic Array.prototype.copyWithin */
    copyWithin(target: number, start: number, end?: number): this;
    /** @intrinsic Array.prototype.every */
    every(predicate: (value: number, index: number, array: this) => unknown, thisArg?: any): boolean;
    /** @intrinsic Array.prototype.fill */
    fill(value: number, start?: number, end?: number): this;
    /** @intrinsic Array.prototype.filter */
    filter(predicate: (value: number, index: number, array: this) => any, thisArg?: any): Float64Array<ArrayBuffer>;
    /** @intrinsic Array.prototype.find */
    find(predicate: (value: number, index: number, obj: this) => boolean, thisArg?: any): number | undefined;
    /** @intrinsic Array.prototype.findIndex */
    findIndex(predicate: (value: number, index: number, obj: this) => boolean, thisArg?: any): number;
    /** @intrinsic Array.prototype.forEach */
    forEach(callbackfn: (value: number, index: number, array: this) => void, thisArg?: any): void;
    /** @intrinsic Array.prototype.indexOf */
    indexOf(searchElement: number, fromIndex?: number): number;
    /** @intrinsic Array.prototype.join */
    join(separator?: string): string;
    /** @intrinsic Array.prototype.lastIndexOf */
    lastIndexOf(searchElement: number, fromIndex?: number): number;
    readonly length: number;
    /** @intrinsic Array.prototype.map */
    map(callbackfn: (value: number, index: number, array: this) => number, thisArg?: any): Float64Array<ArrayBuffer>;
    /** @intrinsic Array.prototype.reduce */
    reduce(callbackfn: (previousValue: number, currentValue: number, currentIndex: number, array: this) => number): number;
    /** @intrinsic Array.prototype.reduce */
    reduce(callbackfn: (previousValue: number, currentValue: number, currentIndex: number, array: this) => number, initialValue: number): number;
    /** @intrinsic Array.prototype.reduce */
    reduce<U>(callbackfn: (previousValue: U, currentValue: number, currentIndex: number, array: this) => U, initialValue: U): U;
    /** @intrinsic Array.prototype.reduceRight */
    reduceRight(callbackfn: (previousValue: number, currentValue: number, currentIndex: number, array: this) => number): number;
    /** @intrinsic Array.prototype.reduceRight */
    reduceRight(callbackfn: (previousValue: number, currentValue: number, currentIndex: number, array: this) => number, initialValue: number): number;
    /** @intrinsic Array.prototype.reduceRight */
    reduceRight<U>(callbackfn: (previousValue: U, currentValue: number, currentIndex: number, array: this) => U, initialValue: U): U;
    /** @intrinsic Array.prototype.reverse */
    reverse(): this;
    set(array: ArrayLike<number>, offset?: number): void;
    /** @intrinsic Array.prototype.slice */
    slice(start?: number, end?: number): Float64Array<ArrayBuffer>;
    /** @intrinsic Array.prototype.some */
    some(predicate: (value: number, index: number, array: this) => unknown, thisArg?: any): boolean;
    /** @intrinsic Array.prototype.sort */
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
    /** @intrinsic Array.prototype.copyWithin */
    copyWithin(target: number, start: number, end?: number): this;
    /** @intrinsic Array.prototype.entries */
    entries(): ArrayIterator<[number, bigint]>;
    /** @intrinsic Array.prototype.every */
    every(predicate: (value: bigint, index: number, array: BigInt64Array<TArrayBuffer>) => boolean, thisArg?: any): boolean;
    /** @intrinsic Array.prototype.fill */
    fill(value: bigint, start?: number, end?: number): this;
    /** @intrinsic Array.prototype.filter */
    filter(predicate: (value: bigint, index: number, array: BigInt64Array<TArrayBuffer>) => any, thisArg?: any): BigInt64Array<ArrayBuffer>;
    /** @intrinsic Array.prototype.find */
    find(predicate: (value: bigint, index: number, array: BigInt64Array<TArrayBuffer>) => boolean, thisArg?: any): bigint | undefined;
    /** @intrinsic Array.prototype.findIndex */
    findIndex(predicate: (value: bigint, index: number, array: BigInt64Array<TArrayBuffer>) => boolean, thisArg?: any): number;
    /** @intrinsic Array.prototype.forEach */
    forEach(callbackfn: (value: bigint, index: number, array: BigInt64Array<TArrayBuffer>) => void, thisArg?: any): void;
    /** @intrinsic Array.prototype.includes */
    includes(searchElement: bigint, fromIndex?: number): boolean;
    /** @intrinsic Array.prototype.indexOf */
    indexOf(searchElement: bigint, fromIndex?: number): number;
    /** @intrinsic Array.prototype.join */
    join(separator?: string): string;
    /** @intrinsic Array.prototype.keys */
    keys(): ArrayIterator<number>;
    /** @intrinsic Array.prototype.lastIndexOf */
    lastIndexOf(searchElement: bigint, fromIndex?: number): number;
    readonly length: number;
    /** @intrinsic Array.prototype.map */
    map(callbackfn: (value: bigint, index: number, array: BigInt64Array<TArrayBuffer>) => bigint, thisArg?: any): BigInt64Array<ArrayBuffer>;
    /** @intrinsic Array.prototype.reduce */
    reduce(callbackfn: (previousValue: bigint, currentValue: bigint, currentIndex: number, array: BigInt64Array<TArrayBuffer>) => bigint): bigint;
    /** @intrinsic Array.prototype.reduce */
    reduce<U>(callbackfn: (previousValue: U, currentValue: bigint, currentIndex: number, array: BigInt64Array<TArrayBuffer>) => U, initialValue: U): U;
    /** @intrinsic Array.prototype.reduceRight */
    reduceRight(callbackfn: (previousValue: bigint, currentValue: bigint, currentIndex: number, array: BigInt64Array<TArrayBuffer>) => bigint): bigint;
    /** @intrinsic Array.prototype.reduceRight */
    reduceRight<U>(callbackfn: (previousValue: U, currentValue: bigint, currentIndex: number, array: BigInt64Array<TArrayBuffer>) => U, initialValue: U): U;
    /** @intrinsic Array.prototype.reverse */
    reverse(): this;
    set(array: ArrayLike<bigint>, offset?: number): void;
    /** @intrinsic Array.prototype.slice */
    slice(start?: number, end?: number): BigInt64Array<ArrayBuffer>;
    /** @intrinsic Array.prototype.some */
    some(predicate: (value: bigint, index: number, array: BigInt64Array<TArrayBuffer>) => boolean, thisArg?: any): boolean;
    /** @intrinsic Array.prototype.sort */
    sort(compareFn?: (a: bigint, b: bigint) => number | bigint): this;
    subarray(begin?: number, end?: number): BigInt64Array<TArrayBuffer>;
    toLocaleString(locales?: string | string[], options?: Intl.NumberFormatOptions): string;
    toString(): string;
    valueOf(): BigInt64Array<TArrayBuffer>;
    /** @intrinsic Array.prototype.values */
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
    /** @intrinsic Array.prototype.copyWithin */
    copyWithin(target: number, start: number, end?: number): this;
    /** @intrinsic Array.prototype.entries */
    entries(): ArrayIterator<[number, bigint]>;
    /** @intrinsic Array.prototype.every */
    every(predicate: (value: bigint, index: number, array: BigUint64Array<TArrayBuffer>) => boolean, thisArg?: any): boolean;
    /** @intrinsic Array.prototype.fill */
    fill(value: bigint, start?: number, end?: number): this;
    /** @intrinsic Array.prototype.filter */
    filter(predicate: (value: bigint, index: number, array: BigUint64Array<TArrayBuffer>) => any, thisArg?: any): BigUint64Array<ArrayBuffer>;
    /** @intrinsic Array.prototype.find */
    find(predicate: (value: bigint, index: number, array: BigUint64Array<TArrayBuffer>) => boolean, thisArg?: any): bigint | undefined;
    /** @intrinsic Array.prototype.findIndex */
    findIndex(predicate: (value: bigint, index: number, array: BigUint64Array<TArrayBuffer>) => boolean, thisArg?: any): number;
    /** @intrinsic Array.prototype.forEach */
    forEach(callbackfn: (value: bigint, index: number, array: BigUint64Array<TArrayBuffer>) => void, thisArg?: any): void;
    /** @intrinsic Array.prototype.includes */
    includes(searchElement: bigint, fromIndex?: number): boolean;
    /** @intrinsic Array.prototype.indexOf */
    indexOf(searchElement: bigint, fromIndex?: number): number;
    /** @intrinsic Array.prototype.join */
    join(separator?: string): string;
    /** @intrinsic Array.prototype.keys */
    keys(): ArrayIterator<number>;
    /** @intrinsic Array.prototype.lastIndexOf */
    lastIndexOf(searchElement: bigint, fromIndex?: number): number;
    readonly length: number;
    /** @intrinsic Array.prototype.map */
    map(callbackfn: (value: bigint, index: number, array: BigUint64Array<TArrayBuffer>) => bigint, thisArg?: any): BigUint64Array<ArrayBuffer>;
    /** @intrinsic Array.prototype.reduce */
    reduce(callbackfn: (previousValue: bigint, currentValue: bigint, currentIndex: number, array: BigUint64Array<TArrayBuffer>) => bigint): bigint;
    /** @intrinsic Array.prototype.reduce */
    reduce<U>(callbackfn: (previousValue: U, currentValue: bigint, currentIndex: number, array: BigUint64Array<TArrayBuffer>) => U, initialValue: U): U;
    /** @intrinsic Array.prototype.reduceRight */
    reduceRight(callbackfn: (previousValue: bigint, currentValue: bigint, currentIndex: number, array: BigUint64Array<TArrayBuffer>) => bigint): bigint;
    /** @intrinsic Array.prototype.reduceRight */
    reduceRight<U>(callbackfn: (previousValue: U, currentValue: bigint, currentIndex: number, array: BigUint64Array<TArrayBuffer>) => U, initialValue: U): U;
    /** @intrinsic Array.prototype.reverse */
    reverse(): this;
    set(array: ArrayLike<bigint>, offset?: number): void;
    /** @intrinsic Array.prototype.slice */
    slice(start?: number, end?: number): BigUint64Array<ArrayBuffer>;
    /** @intrinsic Array.prototype.some */
    some(predicate: (value: bigint, index: number, array: BigUint64Array<TArrayBuffer>) => boolean, thisArg?: any): boolean;
    /** @intrinsic Array.prototype.sort */
    sort(compareFn?: (a: bigint, b: bigint) => number | bigint): this;
    subarray(begin?: number, end?: number): BigUint64Array<TArrayBuffer>;
    toLocaleString(locales?: string | string[], options?: Intl.NumberFormatOptions): string;
    toString(): string;
    valueOf(): BigUint64Array<TArrayBuffer>;
    /** @intrinsic Array.prototype.values */
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
    /** @intrinsic Array.prototype.entries */
    entries(): ArrayIterator<[number, number]>;
    /** @intrinsic Array.prototype.keys */
    keys(): ArrayIterator<number>;
    /** @intrinsic Array.prototype.values */
    values(): ArrayIterator<number>;
}
interface Int8ArrayConstructor {
    new (elements: Iterable<number>): Int8Array<ArrayBuffer>;
    from(elements: Iterable<number>): Int8Array<ArrayBuffer>;
    from<T>(elements: Iterable<T>, mapfn?: (v: T, k: number) => number, thisArg?: any): Int8Array<ArrayBuffer>;
}
interface Uint8Array<TArrayBuffer extends ArrayBufferLike> {
    [Symbol.iterator](): ArrayIterator<number>;
    /** @intrinsic Array.prototype.entries */
    entries(): ArrayIterator<[number, number]>;
    /** @intrinsic Array.prototype.keys */
    keys(): ArrayIterator<number>;
    /** @intrinsic Array.prototype.values */
    values(): ArrayIterator<number>;
}
interface Uint8ArrayConstructor {
    new (elements: Iterable<number>): Uint8Array<ArrayBuffer>;
    from(elements: Iterable<number>): Uint8Array<ArrayBuffer>;
    from<T>(elements: Iterable<T>, mapfn?: (v: T, k: number) => number, thisArg?: any): Uint8Array<ArrayBuffer>;
}
interface Uint8ClampedArray<TArrayBuffer extends ArrayBufferLike> {
    [Symbol.iterator](): ArrayIterator<number>;
    /** @intrinsic Array.prototype.entries */
    entries(): ArrayIterator<[number, number]>;
    /** @intrinsic Array.prototype.keys */
    keys(): ArrayIterator<number>;
    /** @intrinsic Array.prototype.values */
    values(): ArrayIterator<number>;
}
interface Uint8ClampedArrayConstructor {
    new (elements: Iterable<number>): Uint8ClampedArray<ArrayBuffer>;
    from(elements: Iterable<number>): Uint8ClampedArray<ArrayBuffer>;
    from<T>(elements: Iterable<T>, mapfn?: (v: T, k: number) => number, thisArg?: any): Uint8ClampedArray<ArrayBuffer>;
}
interface Int16Array<TArrayBuffer extends ArrayBufferLike> {
    [Symbol.iterator](): ArrayIterator<number>;
    /** @intrinsic Array.prototype.entries */
    entries(): ArrayIterator<[number, number]>;
    /** @intrinsic Array.prototype.keys */
    keys(): ArrayIterator<number>;
    /** @intrinsic Array.prototype.values */
    values(): ArrayIterator<number>;
}
interface Int16ArrayConstructor {
    new (elements: Iterable<number>): Int16Array<ArrayBuffer>;
    from(elements: Iterable<number>): Int16Array<ArrayBuffer>;
    from<T>(elements: Iterable<T>, mapfn?: (v: T, k: number) => number, thisArg?: any): Int16Array<ArrayBuffer>;
}
interface Uint16Array<TArrayBuffer extends ArrayBufferLike> {
    [Symbol.iterator](): ArrayIterator<number>;
    /** @intrinsic Array.prototype.entries */
    entries(): ArrayIterator<[number, number]>;
    /** @intrinsic Array.prototype.keys */
    keys(): ArrayIterator<number>;
    /** @intrinsic Array.prototype.values */
    values(): ArrayIterator<number>;
}
interface Uint16ArrayConstructor {
    new (elements: Iterable<number>): Uint16Array<ArrayBuffer>;
    from(elements: Iterable<number>): Uint16Array<ArrayBuffer>;
    from<T>(elements: Iterable<T>, mapfn?: (v: T, k: number) => number, thisArg?: any): Uint16Array<ArrayBuffer>;
}
interface Int32Array<TArrayBuffer extends ArrayBufferLike> {
    [Symbol.iterator](): ArrayIterator<number>;
    /** @intrinsic Array.prototype.entries */
    entries(): ArrayIterator<[number, number]>;
    /** @intrinsic Array.prototype.keys */
    keys(): ArrayIterator<number>;
    /** @intrinsic Array.prototype.values */
    values(): ArrayIterator<number>;
}
interface Int32ArrayConstructor {
    new (elements: Iterable<number>): Int32Array<ArrayBuffer>;
    from(elements: Iterable<number>): Int32Array<ArrayBuffer>;
    from<T>(elements: Iterable<T>, mapfn?: (v: T, k: number) => number, thisArg?: any): Int32Array<ArrayBuffer>;
}
interface Uint32Array<TArrayBuffer extends ArrayBufferLike> {
    [Symbol.iterator](): ArrayIterator<number>;
    /** @intrinsic Array.prototype.entries */
    entries(): ArrayIterator<[number, number]>;
    /** @intrinsic Array.prototype.keys */
    keys(): ArrayIterator<number>;
    /** @intrinsic Array.prototype.values */
    values(): ArrayIterator<number>;
}
interface Uint32ArrayConstructor {
    new (elements: Iterable<number>): Uint32Array<ArrayBuffer>;
    from(elements: Iterable<number>): Uint32Array<ArrayBuffer>;
    from<T>(elements: Iterable<T>, mapfn?: (v: T, k: number) => number, thisArg?: any): Uint32Array<ArrayBuffer>;
}
interface Float32Array<TArrayBuffer extends ArrayBufferLike> {
    [Symbol.iterator](): ArrayIterator<number>;
    /** @intrinsic Array.prototype.entries */
    entries(): ArrayIterator<[number, number]>;
    /** @intrinsic Array.prototype.keys */
    keys(): ArrayIterator<number>;
    /** @intrinsic Array.prototype.values */
    values(): ArrayIterator<number>;
}
interface Float32ArrayConstructor {
    new (elements: Iterable<number>): Float32Array<ArrayBuffer>;
    from(elements: Iterable<number>): Float32Array<ArrayBuffer>;
    from<T>(elements: Iterable<T>, mapfn?: (v: T, k: number) => number, thisArg?: any): Float32Array<ArrayBuffer>;
}
interface Float64Array<TArrayBuffer extends ArrayBufferLike> {
    [Symbol.iterator](): ArrayIterator<number>;
    /** @intrinsic Array.prototype.entries */
    entries(): ArrayIterator<[number, number]>;
    /** @intrinsic Array.prototype.keys */
    keys(): ArrayIterator<number>;
    /** @intrinsic Array.prototype.values */
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
    /** @intrinsic Array.prototype.includes */
    includes(searchElement: number, fromIndex?: number): boolean;
}
interface Uint8Array<TArrayBuffer extends ArrayBufferLike> {
    /** @intrinsic Array.prototype.includes */
    includes(searchElement: number, fromIndex?: number): boolean;
}
interface Uint8ClampedArray<TArrayBuffer extends ArrayBufferLike> {
    /** @intrinsic Array.prototype.includes */
    includes(searchElement: number, fromIndex?: number): boolean;
}
interface Int16Array<TArrayBuffer extends ArrayBufferLike> {
    /** @intrinsic Array.prototype.includes */
    includes(searchElement: number, fromIndex?: number): boolean;
}
interface Uint16Array<TArrayBuffer extends ArrayBufferLike> {
    /** @intrinsic Array.prototype.includes */
    includes(searchElement: number, fromIndex?: number): boolean;
}
interface Int32Array<TArrayBuffer extends ArrayBufferLike> {
    /** @intrinsic Array.prototype.includes */
    includes(searchElement: number, fromIndex?: number): boolean;
}
interface Uint32Array<TArrayBuffer extends ArrayBufferLike> {
    /** @intrinsic Array.prototype.includes */
    includes(searchElement: number, fromIndex?: number): boolean;
}
interface Float32Array<TArrayBuffer extends ArrayBufferLike> {
    /** @intrinsic Array.prototype.includes */
    includes(searchElement: number, fromIndex?: number): boolean;
}
interface Float64Array<TArrayBuffer extends ArrayBufferLike> {
    /** @intrinsic Array.prototype.includes */
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
    /** @intrinsic Array.prototype.at */
    at(index: number): number | undefined;
}
interface Uint8Array<TArrayBuffer extends ArrayBufferLike> {
    /** @intrinsic Array.prototype.at */
    at(index: number): number | undefined;
}
interface Uint8ClampedArray<TArrayBuffer extends ArrayBufferLike> {
    /** @intrinsic Array.prototype.at */
    at(index: number): number | undefined;
}
interface Int16Array<TArrayBuffer extends ArrayBufferLike> {
    /** @intrinsic Array.prototype.at */
    at(index: number): number | undefined;
}
interface Uint16Array<TArrayBuffer extends ArrayBufferLike> {
    /** @intrinsic Array.prototype.at */
    at(index: number): number | undefined;
}
interface Int32Array<TArrayBuffer extends ArrayBufferLike> {
    /** @intrinsic Array.prototype.at */
    at(index: number): number | undefined;
}
interface Uint32Array<TArrayBuffer extends ArrayBufferLike> {
    /** @intrinsic Array.prototype.at */
    at(index: number): number | undefined;
}
interface Float32Array<TArrayBuffer extends ArrayBufferLike> {
    /** @intrinsic Array.prototype.at */
    at(index: number): number | undefined;
}
interface Float64Array<TArrayBuffer extends ArrayBufferLike> {
    /** @intrinsic Array.prototype.at */
    at(index: number): number | undefined;
}
interface BigInt64Array<TArrayBuffer extends ArrayBufferLike> {
    /** @intrinsic Array.prototype.at */
    at(index: number): bigint | undefined;
}
interface BigUint64Array<TArrayBuffer extends ArrayBufferLike> {
    /** @intrinsic Array.prototype.at */
    at(index: number): bigint | undefined;
}
interface Int8Array<TArrayBuffer extends ArrayBufferLike> {
    /** @intrinsic Array.prototype.findLast */
    findLast<S extends number>(
        predicate: (
            value: number,
            index: number,
            array: this,
        ) => value is S,
        thisArg?: any,
    ): S | undefined;
    /** @intrinsic Array.prototype.findLast */
    findLast(
        predicate: (value: number, index: number, array: this) => unknown,
        thisArg?: any,
    ): number | undefined;
    /** @intrinsic Array.prototype.findLastIndex */
    findLastIndex(
        predicate: (value: number, index: number, array: this) => unknown,
        thisArg?: any,
    ): number;
    /** @intrinsic Array.prototype.toReversed */
    toReversed(): Int8Array<ArrayBuffer>;
    /** @intrinsic Array.prototype.toSorted */
    toSorted(compareFn?: (a: number, b: number) => number): Int8Array<ArrayBuffer>;
    /** @intrinsic Array.prototype.with */
    with(index: number, value: number): Int8Array<ArrayBuffer>;
}
interface Uint8Array<TArrayBuffer extends ArrayBufferLike> {
    /** @intrinsic Array.prototype.findLast */
    findLast<S extends number>(
        predicate: (
            value: number,
            index: number,
            array: this,
        ) => value is S,
        thisArg?: any,
    ): S | undefined;
    /** @intrinsic Array.prototype.findLast */
    findLast(
        predicate: (value: number, index: number, array: this) => unknown,
        thisArg?: any,
    ): number | undefined;
    /** @intrinsic Array.prototype.findLastIndex */
    findLastIndex(
        predicate: (value: number, index: number, array: this) => unknown,
        thisArg?: any,
    ): number;
    /** @intrinsic Array.prototype.toReversed */
    toReversed(): Uint8Array<ArrayBuffer>;
    /** @intrinsic Array.prototype.toSorted */
    toSorted(compareFn?: (a: number, b: number) => number): Uint8Array<ArrayBuffer>;
    /** @intrinsic Array.prototype.with */
    with(index: number, value: number): Uint8Array<ArrayBuffer>;
}
interface Uint8ClampedArray<TArrayBuffer extends ArrayBufferLike> {
    /** @intrinsic Array.prototype.findLast */
    findLast<S extends number>(
        predicate: (
            value: number,
            index: number,
            array: this,
        ) => value is S,
        thisArg?: any,
    ): S | undefined;
    /** @intrinsic Array.prototype.findLast */
    findLast(
        predicate: (
            value: number,
            index: number,
            array: this,
        ) => unknown,
        thisArg?: any,
    ): number | undefined;
    /** @intrinsic Array.prototype.findLastIndex */
    findLastIndex(
        predicate: (
            value: number,
            index: number,
            array: this,
        ) => unknown,
        thisArg?: any,
    ): number;
    /** @intrinsic Array.prototype.toReversed */
    toReversed(): Uint8ClampedArray<ArrayBuffer>;
    /** @intrinsic Array.prototype.toSorted */
    toSorted(compareFn?: (a: number, b: number) => number): Uint8ClampedArray<ArrayBuffer>;
    /** @intrinsic Array.prototype.with */
    with(index: number, value: number): Uint8ClampedArray<ArrayBuffer>;
}
interface Int16Array<TArrayBuffer extends ArrayBufferLike> {
    /** @intrinsic Array.prototype.findLast */
    findLast<S extends number>(
        predicate: (
            value: number,
            index: number,
            array: this,
        ) => value is S,
        thisArg?: any,
    ): S | undefined;
    /** @intrinsic Array.prototype.findLast */
    findLast(
        predicate: (value: number, index: number, array: this) => unknown,
        thisArg?: any,
    ): number | undefined;
    /** @intrinsic Array.prototype.findLastIndex */
    findLastIndex(
        predicate: (value: number, index: number, array: this) => unknown,
        thisArg?: any,
    ): number;
    /** @intrinsic Array.prototype.toReversed */
    toReversed(): Int16Array<ArrayBuffer>;
    /** @intrinsic Array.prototype.toSorted */
    toSorted(compareFn?: (a: number, b: number) => number): Int16Array<ArrayBuffer>;
    /** @intrinsic Array.prototype.with */
    with(index: number, value: number): Int16Array<ArrayBuffer>;
}
interface Uint16Array<TArrayBuffer extends ArrayBufferLike> {
    /** @intrinsic Array.prototype.findLast */
    findLast<S extends number>(
        predicate: (
            value: number,
            index: number,
            array: this,
        ) => value is S,
        thisArg?: any,
    ): S | undefined;
    /** @intrinsic Array.prototype.findLast */
    findLast(
        predicate: (
            value: number,
            index: number,
            array: this,
        ) => unknown,
        thisArg?: any,
    ): number | undefined;
    /** @intrinsic Array.prototype.findLastIndex */
    findLastIndex(
        predicate: (
            value: number,
            index: number,
            array: this,
        ) => unknown,
        thisArg?: any,
    ): number;
    /** @intrinsic Array.prototype.toReversed */
    toReversed(): Uint16Array<ArrayBuffer>;
    /** @intrinsic Array.prototype.toSorted */
    toSorted(compareFn?: (a: number, b: number) => number): Uint16Array<ArrayBuffer>;
    /** @intrinsic Array.prototype.with */
    with(index: number, value: number): Uint16Array<ArrayBuffer>;
}
interface Int32Array<TArrayBuffer extends ArrayBufferLike> {
    /** @intrinsic Array.prototype.findLast */
    findLast<S extends number>(
        predicate: (
            value: number,
            index: number,
            array: this,
        ) => value is S,
        thisArg?: any,
    ): S | undefined;
    /** @intrinsic Array.prototype.findLast */
    findLast(
        predicate: (value: number, index: number, array: this) => unknown,
        thisArg?: any,
    ): number | undefined;
    /** @intrinsic Array.prototype.findLastIndex */
    findLastIndex(
        predicate: (value: number, index: number, array: this) => unknown,
        thisArg?: any,
    ): number;
    /** @intrinsic Array.prototype.toReversed */
    toReversed(): Int32Array<ArrayBuffer>;
    /** @intrinsic Array.prototype.toSorted */
    toSorted(compareFn?: (a: number, b: number) => number): Int32Array<ArrayBuffer>;
    /** @intrinsic Array.prototype.with */
    with(index: number, value: number): Int32Array<ArrayBuffer>;
}
interface Uint32Array<TArrayBuffer extends ArrayBufferLike> {
    /** @intrinsic Array.prototype.findLast */
    findLast<S extends number>(
        predicate: (
            value: number,
            index: number,
            array: this,
        ) => value is S,
        thisArg?: any,
    ): S | undefined;
    /** @intrinsic Array.prototype.findLast */
    findLast(
        predicate: (
            value: number,
            index: number,
            array: this,
        ) => unknown,
        thisArg?: any,
    ): number | undefined;
    /** @intrinsic Array.prototype.findLastIndex */
    findLastIndex(
        predicate: (
            value: number,
            index: number,
            array: this,
        ) => unknown,
        thisArg?: any,
    ): number;
    /** @intrinsic Array.prototype.toReversed */
    toReversed(): Uint32Array<ArrayBuffer>;
    /** @intrinsic Array.prototype.toSorted */
    toSorted(compareFn?: (a: number, b: number) => number): Uint32Array<ArrayBuffer>;
    /** @intrinsic Array.prototype.with */
    with(index: number, value: number): Uint32Array<ArrayBuffer>;
}
interface Float32Array<TArrayBuffer extends ArrayBufferLike> {
    /** @intrinsic Array.prototype.findLast */
    findLast<S extends number>(
        predicate: (
            value: number,
            index: number,
            array: this,
        ) => value is S,
        thisArg?: any,
    ): S | undefined;
    /** @intrinsic Array.prototype.findLast */
    findLast(
        predicate: (
            value: number,
            index: number,
            array: this,
        ) => unknown,
        thisArg?: any,
    ): number | undefined;
    /** @intrinsic Array.prototype.findLastIndex */
    findLastIndex(
        predicate: (
            value: number,
            index: number,
            array: this,
        ) => unknown,
        thisArg?: any,
    ): number;
    /** @intrinsic Array.prototype.toReversed */
    toReversed(): Float32Array<ArrayBuffer>;
    /** @intrinsic Array.prototype.toSorted */
    toSorted(compareFn?: (a: number, b: number) => number): Float32Array<ArrayBuffer>;
    /** @intrinsic Array.prototype.with */
    with(index: number, value: number): Float32Array<ArrayBuffer>;
}
interface Float64Array<TArrayBuffer extends ArrayBufferLike> {
    /** @intrinsic Array.prototype.findLast */
    findLast<S extends number>(
        predicate: (
            value: number,
            index: number,
            array: this,
        ) => value is S,
        thisArg?: any,
    ): S | undefined;
    /** @intrinsic Array.prototype.findLast */
    findLast(
        predicate: (
            value: number,
            index: number,
            array: this,
        ) => unknown,
        thisArg?: any,
    ): number | undefined;
    /** @intrinsic Array.prototype.findLastIndex */
    findLastIndex(
        predicate: (
            value: number,
            index: number,
            array: this,
        ) => unknown,
        thisArg?: any,
    ): number;
    /** @intrinsic Array.prototype.toReversed */
    toReversed(): Float64Array<ArrayBuffer>;
    /** @intrinsic Array.prototype.toSorted */
    toSorted(compareFn?: (a: number, b: number) => number): Float64Array<ArrayBuffer>;
    /** @intrinsic Array.prototype.with */
    with(index: number, value: number): Float64Array<ArrayBuffer>;
}
interface BigInt64Array<TArrayBuffer extends ArrayBufferLike> {
    /** @intrinsic Array.prototype.findLast */
    findLast<S extends bigint>(
        predicate: (
            value: bigint,
            index: number,
            array: this,
        ) => value is S,
        thisArg?: any,
    ): S | undefined;
    /** @intrinsic Array.prototype.findLast */
    findLast(
        predicate: (
            value: bigint,
            index: number,
            array: this,
        ) => unknown,
        thisArg?: any,
    ): bigint | undefined;
    /** @intrinsic Array.prototype.findLastIndex */
    findLastIndex(
        predicate: (
            value: bigint,
            index: number,
            array: this,
        ) => unknown,
        thisArg?: any,
    ): number;
    /** @intrinsic Array.prototype.toReversed */
    toReversed(): BigInt64Array<ArrayBuffer>;
    /** @intrinsic Array.prototype.toSorted */
    toSorted(compareFn?: (a: bigint, b: bigint) => number): BigInt64Array<ArrayBuffer>;
    /** @intrinsic Array.prototype.with */
    with(index: number, value: bigint): BigInt64Array<ArrayBuffer>;
}
interface BigUint64Array<TArrayBuffer extends ArrayBufferLike> {
    /** @intrinsic Array.prototype.findLast */
    findLast<S extends bigint>(
        predicate: (
            value: bigint,
            index: number,
            array: this,
        ) => value is S,
        thisArg?: any,
    ): S | undefined;
    /** @intrinsic Array.prototype.findLast */
    findLast(
        predicate: (
            value: bigint,
            index: number,
            array: this,
        ) => unknown,
        thisArg?: any,
    ): bigint | undefined;
    /** @intrinsic Array.prototype.findLastIndex */
    findLastIndex(
        predicate: (
            value: bigint,
            index: number,
            array: this,
        ) => unknown,
        thisArg?: any,
    ): number;
    /** @intrinsic Array.prototype.toReversed */
    toReversed(): BigUint64Array<ArrayBuffer>;
    /** @intrinsic Array.prototype.toSorted */
    toSorted(compareFn?: (a: bigint, b: bigint) => number): BigUint64Array<ArrayBuffer>;
    /** @intrinsic Array.prototype.with */
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
