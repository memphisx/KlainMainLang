// kml:global
// process.nextTick as Node defines it (lib/internal/process/task_queues.js):
// the callback, validated, runs with its arguments and the async context it
// was queued in, on the tick queue ahead of the promise jobs. One function:
// a call through the declaration lowers to it, and the process object
// (internal_process.ts) holds it.

// Only internal_errors: a module that queues ticks itself would import this
// one back.
import { NodeTypeError } from './internal_errors';

// lib/internal/validators.js validateFunction, with
// lib/internal/errors.js determineSpecificType's reading of the value.
function validateFunction(value: any, name: string): void {
    if (typeof value === 'function') return;
    let shown: string;
    if (value === null || value === undefined) shown = 'Received ' + String(value);
    else if (typeof value === 'object') shown = 'Received an instance of ' + (Array.isArray(value) ? 'Array' : 'Object');
    else {
        let v = typeof value === 'string' ? "'" + (value as string) + "'" : String(value);
        if (typeof value === 'string' && (value as string).length > 25) v = "'" + (value as string).slice(0, 25) + "'...";
        shown = 'Received type ' + typeof value + ' (' + v + ')';
    }
    throw NodeTypeError('ERR_INVALID_ARG_TYPE', 'The "' + name + '" argument must be of type function. ' + shown);
}

export function __kml_process_nextTick(callback: Function, ...args: any[]): void { // kml:lower process.nextTick
    validateFunction(callback, 'callback');
    const frame = __kml_native.asyncContextGet();
    __kml_native.nextTick(() => {
        const prior = __kml_native.asyncContextGet();
        __kml_native.asyncContextSet(frame);
        try {
            (callback as any)(...args);
        } finally {
            __kml_native.asyncContextSet(prior);
        }
    });
}
