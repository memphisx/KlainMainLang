// process.hrtime and process.hrtime.bigint (Node's
// lib/internal/process/per_thread.js), a module of their own so that a
// program not reading the clock this way compiles no bigint arithmetic.

class NodeTypeError extends TypeError {
    code: string;
    constructor(code: string, message: string) {
        super(message);
        this.code = code;
    }
}

class NodeRangeError extends RangeError {
    code: string;
    constructor(code: string, message: string) {
        super(message);
        this.code = code;
    }
}

function received(value: any): string {
    if (value === null || value === undefined) return 'Received ' + String(value);
    if (typeof value === 'function') return 'Received function ' + (value as Function).name;
    if (typeof value === 'object') return 'Received an instance of ' + (Array.isArray(value) ? 'Array' : 'Object');
    const shown = typeof value === 'string' ? "'" + (value as string) + "'" : String(value);
    return 'Received type ' + typeof value + ' (' + shown + ')';
}

function invalidArgType(name: string, expected: string, value: any): NodeTypeError {
    return new NodeTypeError('ERR_INVALID_ARG_TYPE', 'The "' + name + '" argument must be ' + expected + '. ' + received(value));
}

export function hrtime(time?: [number, number]): [number, number];
export function hrtime(time?: any): [number, number] {
    __kml_native.processHrtime();
    const sec = __kml_native.processHrtimeRead(0);
    const nsec = __kml_native.processHrtimeRead(1);
    if (time !== undefined) {
        if (!Array.isArray(time)) throw invalidArgType('time', 'an instance of Array', time);
        if (time.length !== 2) {
            throw new NodeRangeError('ERR_OUT_OF_RANGE', 'The value of "time" is out of range. It must be 2. Received ' + String(time.length));
        }
        const dsec = sec - (time[0] as number);
        const dnsec = nsec - (time[1] as number);
        const needsBorrow = dnsec < 0;
        return [needsBorrow ? dsec - 1 : dsec, needsBorrow ? dnsec + 1e9 : dnsec];
    }
    return [sec, nsec];
}

export function hrtimeBigint(): bigint {
    __kml_native.processHrtime();
    return BigInt(__kml_native.processHrtimeRead(0)) * 1000000000n + BigInt(__kml_native.processHrtimeRead(1));
}
