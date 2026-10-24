// kml:global
// Promise's statics written in TypeScript, reached through their `@lower`
// declarations in lib/es.d.ts.

// Promise.withResolvers (ECMA-262 §27.2.4.8): a new promise and the
// functions that resolve and reject it.
export function __kml_Promise_withResolvers<T>(): PromiseWithResolvers<T> { // kml:lower Promise.withResolvers
    let resolve: (value: T | PromiseLike<T>) => void = () => {};
    let reject: (reason?: any) => void = () => {};
    const promise = new Promise<T>((res, rej) => {
        resolve = res;
        reject = rej;
    });
    return { promise, resolve, reject };
}
