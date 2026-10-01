// The global timer functions, for a module that exports its own functions of
// the same names (timers/promises) and so cannot name the globals.

export function startTimeout(fn: () => void, ms: number): NodeJS.Timeout {
    return setTimeout(fn, ms);
}

export function stopTimeout(timer: NodeJS.Timeout): void {
    clearTimeout(timer);
}

export function startInterval(fn: () => void, ms: number): NodeJS.Timeout {
    return setInterval(fn, ms);
}

export function stopInterval(timer: NodeJS.Timeout): void {
    clearInterval(timer);
}

export function startImmediate(fn: () => void): NodeJS.Immediate {
    return setImmediate(fn);
}

export function stopImmediate(immediate: NodeJS.Immediate): void {
    clearImmediate(immediate);
}
