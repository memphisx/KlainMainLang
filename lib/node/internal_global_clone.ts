// globalThis.structuredClone as a value: its implementation reaches every
// class it can clone, so it is in only the programs that can read it.

function globalThisClone(value: any): any {
    return structuredClone(value);
}

export function _kmlGlobalStructuredClone(value: any, options?: any): any {
    if (options !== undefined && options !== null && options.transfer !== undefined && options.transfer.length > 0) {
        throw new TypeError("structuredClone's transfer option is not supported");
    }
    return globalThisClone(value);
}
