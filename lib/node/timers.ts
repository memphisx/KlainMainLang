// Node's `timers` (lib/timers.js): the timer functions the globals are, and
// the promise forms as `promises`.
//
// kml:default-namespace — `import timers from 'timers'` reads this module's
// exports, as Node's default export carries them.

import { startTimeout, stopTimeout, startInterval, stopInterval, startImmediate, stopImmediate } from './internal_timers';
import { setTimeout as promiseTimeout, setImmediate as promiseImmediate, setInterval as promiseInterval, scheduler } from './timers_promises';

export function setTimeout(callback: (...args: any[]) => void, delay?: number, ...args: any[]): NodeJS.Timeout {
    return startTimeout(() => { callback(...args); }, delay === undefined ? 0 : delay);
}

export function clearTimeout(timeout: any): void {
    if (timeout === undefined || timeout === null) return;
    stopTimeout(timeout as NodeJS.Timeout);
}

export function setInterval(callback: (...args: any[]) => void, delay?: number, ...args: any[]): NodeJS.Timeout {
    return startInterval(() => { callback(...args); }, delay === undefined ? 0 : delay);
}

export function clearInterval(timeout: any): void {
    if (timeout === undefined || timeout === null) return;
    stopInterval(timeout as NodeJS.Timeout);
}

export function setImmediate(callback: (...args: any[]) => void, ...args: any[]): NodeJS.Immediate {
    return startImmediate(() => { callback(...args); });
}

export function clearImmediate(immediate: any): void {
    if (immediate === undefined || immediate === null) return;
    stopImmediate(immediate as NodeJS.Immediate);
}

// `timers.promises` is `timers/promises`.
export const promises: any = {
    setTimeout: (delay?: number, value?: any, options?: any): Promise<any> => promiseTimeout<any>(delay, value, options),
    setImmediate: (value?: any, options?: any): Promise<any> => promiseImmediate<any>(value, options),
    setInterval: (delay?: number, value?: any, options?: any): AsyncGenerator<any> => promiseInterval<any>(delay, value, options),
    scheduler: scheduler,
};
