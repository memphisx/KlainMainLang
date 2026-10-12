// kml:global
// The timer globals — setTimeout, setInterval, setImmediate and their clear
// functions — as Node defines them (lib/timers.js), over internal_timers.ts
// (TDD-00245). A program naming one imports this module.

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

export function __kml_setTimeout(callback: (...args: any[]) => void, after?: any, ...args: any[]): Timeout { // kml:lower setTimeout
    return nodeSetTimeout(callback, after, ...args);
}

export function __kml_setInterval(callback: (...args: any[]) => void, repeat?: any, ...args: any[]): Timeout { // kml:lower setInterval
    return nodeSetInterval(callback, repeat, ...args);
}

export function __kml_setImmediate(callback: (...args: any[]) => void, ...args: any[]): Immediate { // kml:lower setImmediate
    return nodeSetImmediate(callback, ...args);
}

export function __kml_clearTimeout(timer: any): void { // kml:lower clearTimeout
    nodeClearTimeout(timer);
}

export function __kml_clearInterval(timer: any): void { // kml:lower clearInterval
    nodeClearInterval(timer);
}

export function __kml_clearImmediate(immediate: any): void { // kml:lower clearImmediate
    nodeClearImmediate(immediate);
}
