// The part of Node's `util` written in TypeScript: promisify, ported from
// Node v24's lib/internal/util.js. The rest of `util` is the compiler's own;
// a program importing `util` imports this module too, and `util.X` names its
// export X when it has one.

class NodeTypeError extends TypeError {
    code: string;
    constructor(code: string, message: string) {
        super(message);
        this.code = code;
    }
}

function received(v: any): string {
    if (v === null) return ' Received null';
    if (v === undefined) return ' Received undefined';
    if (typeof v === 'string') return " Received type string ('" + v + "')";
    return ' Received type ' + typeof v + ' (' + String(v) + ')';
}

// The @types/node overloads: a callback-last function of up to four
// arguments, typed; any other function, untyped.
export function promisify<TResult>(fn: (callback: (err: any, result: TResult) => void) => void): () => Promise<TResult>;
export function promisify(fn: (callback: (err?: any) => void) => void): () => Promise<void>;
export function promisify<T1, TResult>(fn: (arg1: T1, callback: (err: any, result: TResult) => void) => void): (arg1: T1) => Promise<TResult>;
export function promisify<T1>(fn: (arg1: T1, callback: (err?: any) => void) => void): (arg1: T1) => Promise<void>;
export function promisify<T1, T2, TResult>(fn: (arg1: T1, arg2: T2, callback: (err: any, result: TResult) => void) => void): (arg1: T1, arg2: T2) => Promise<TResult>;
export function promisify<T1, T2>(fn: (arg1: T1, arg2: T2, callback: (err?: any) => void) => void): (arg1: T1, arg2: T2) => Promise<void>;
export function promisify<T1, T2, T3, TResult>(fn: (arg1: T1, arg2: T2, arg3: T3, callback: (err: any, result: TResult) => void) => void): (arg1: T1, arg2: T2, arg3: T3) => Promise<TResult>;
export function promisify<T1, T2, T3>(fn: (arg1: T1, arg2: T2, arg3: T3, callback: (err?: any) => void) => void): (arg1: T1, arg2: T2, arg3: T3) => Promise<void>;
export function promisify(fn: Function): Function;
export function promisify(original: any): (...args: any[]) => Promise<any> {
    if (typeof original !== 'function') {
        throw new NodeTypeError('ERR_INVALID_ARG_TYPE', 'The "original" argument must be of type function.' + received(original));
    }
    return (...args: any[]): Promise<any> => new Promise<any>((resolve, reject) => {
        original(...args, (err: any, value: any) => {
            if (err) {
                reject(err);
                return;
            }
            resolve(value);
        });
    });
}
