// Node's `timers` (lib/timers.js): the timer functions the globals are, and
// the promise forms as `promises`.
//
// kml:default-namespace — `import timers from 'timers'` reads this module's
// exports, as Node's default export carries them.

import {
    setTimeout as nodeSetTimeout,
    setInterval as nodeSetInterval,
    setImmediate as nodeSetImmediate,
    clearTimeout as nodeClearTimeout,
    clearInterval as nodeClearInterval,
    clearImmediate as nodeClearImmediate,
    Timeout,
    Immediate,
} from './internal_timers';
import { setTimeout as promiseTimeout, setImmediate as promiseImmediate, setInterval as promiseInterval, scheduler } from './timers_promises';

export function setTimeout(callback: (...args: any[]) => void, delay?: any, ...args: any[]): Timeout {
    return nodeSetTimeout(callback, delay, ...args);
}

export function clearTimeout(timeout: any): void {
    nodeClearTimeout(timeout);
}

export function setInterval(callback: (...args: any[]) => void, delay?: any, ...args: any[]): Timeout {
    return nodeSetInterval(callback, delay, ...args);
}

export function clearInterval(timeout: any): void {
    nodeClearInterval(timeout);
}

export function setImmediate(callback: (...args: any[]) => void, ...args: any[]): Immediate {
    return nodeSetImmediate(callback, ...args);
}

export function clearImmediate(immediate: any): void {
    nodeClearImmediate(immediate);
}

// `timers.promises` is `timers/promises`.
export const promises: any = {
    setTimeout: (delay?: number, value?: any, options?: any): Promise<any> => promiseTimeout<any>(delay, value, options),
    setImmediate: (value?: any, options?: any): Promise<any> => promiseImmediate<any>(value, options),
    setInterval: (delay?: number, value?: any, options?: any): AsyncGenerator<any> => promiseInterval<any>(delay, value, options),
    scheduler: scheduler,
};
