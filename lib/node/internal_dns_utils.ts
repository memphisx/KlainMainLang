// Node's lib/internal/dns/utils.js, in TypeScript, with what lib/dns.js and
// internal/errors.js share with it: DNSException, the argument checks, the
// default result order, ResolverBase over a native channel (klaindns.c,
// standing in for c-ares' ChannelWrap), and the query that shapes an
// answer's records the way cares_wrap.cc's parsers do.

import { isIP } from './internal_net';
import { idnaToASCII } from './kml_url';
import { NodeError, NodeTypeError, NodeRangeError } from './internal_errors';

export function received(value: any): string {
    if (value === null || value === undefined) return ' Received ' + String(value);
    if (typeof value === 'function') return ' Received function ' + (value as Function).name;
    if (typeof value === 'object') {
        if (value.constructor && value.constructor.name) return ' Received an instance of ' + value.constructor.name;
        return ' Received ' + String(value);
    }
    let shown = typeof value === 'string' ? "'" + value + "'" : String(value);
    if (shown.length > 28) shown = shown.slice(0, 25) + '...';
    return ' Received type ' + typeof value + ' (' + shown + ')';
}

export function invalidArgType(name: string, expected: string, value: any): Error {
    return new NodeTypeError('ERR_INVALID_ARG_TYPE', 'The "' + name + '" argument must be ' + expected + '.' + received(value));
}

export function invalidArgValue(name: string, value: any, reason?: string): Error {
    const shown = typeof value === 'string' ? "'" + value + "'" : String(value);
    const kind = name.includes('.') ? 'property' : 'argument';
    return new NodeTypeError('ERR_INVALID_ARG_VALUE', "The " + kind + " '" + name + "' " + (reason ?? 'is invalid') + '. Received ' + shown);
}

export function missingArgs(names: string[]): Error {
    let msg = 'The ';
    const quoted = names.map((n) => '"' + n + '"');
    if (quoted.length === 1) msg += quoted[0] + ' argument';
    else if (quoted.length === 2) msg += quoted[0] + ' and ' + quoted[1] + ' arguments';
    else msg += quoted.slice(0, -1).join(', ') + ', and ' + quoted[quoted.length - 1] + ' arguments';
    return new NodeTypeError('ERR_MISSING_ARGS', msg + ' must be specified');
}

// lib/internal/errors.js's DNSException: a libuv error number (getaddrinfo,
// getnameinfo) or a c-ares code. Its constructor reads as Error.
export class DNSException extends Error {
    code: string;
    syscall: string;
    hostname: any;
    constructor(code: any, syscall: string, hostname?: string) {
        let errno: any;
        let name: string;
        if (typeof code === 'number') {
            errno = code;
            // UV_EAI_NODATA and UV_EAI_NONAME: Node's fabricated ENOTFOUND.
            if (code === -3007 || code === -3008) name = 'ENOTFOUND';
            else name = systemErrorName(code);
        } else {
            name = code;
        }
        super(syscall + ' ' + name + (hostname ? ' ' + hostname : ''));
        // The layout's errno slot is numeric: a c-ares error leaves it unset.
        if (errno !== undefined) (this as any).errno = errno;
        this.code = name;
        this.syscall = syscall;
        if (hostname) {
            this.hostname = hostname;
        }
    }
}

const uvEaiNames: string[] = ['EAI_ADDRFAMILY', 'EAI_AGAIN', 'EAI_BADFLAGS', 'EAI_CANCELED', 'EAI_FAIL', 'EAI_FAMILY',
    'EAI_MEMORY', 'EAI_NODATA', 'EAI_NONAME', 'EAI_OVERFLOW', 'EAI_SERVICE', 'EAI_SOCKTYPE', '', 'EAI_BADHINTS', 'EAI_PROTOCOL'];

function systemErrorName(err: number): string {
    if (err <= -3000 && err >= -3014 && uvEaiNames[-3000 - err] !== '') return uvEaiNames[-3000 - err];
    return __kml_native.errnoName(-err);
}

export function validateString(value: any, name: string): void {
    if (typeof value !== 'string') throw invalidArgType(name, 'of type string', value);
}

export function validateStringWithoutNullBytes(value: any, name: string): void {
    validateString(value, name);
    if ((value as string).includes('\u0000')) {
        throw invalidArgValue(name, value, 'must be a string without null bytes');
    }
}

export function validateFunction(value: any, name: string): void {
    if (typeof value !== 'function') throw invalidArgType(name, 'of type function', value);
}

export function validateBoolean(value: any, name: string): void {
    if (typeof value !== 'boolean') throw invalidArgType(name, 'of type boolean', value);
}

export function validateNumber(value: any, name: string): void {
    if (typeof value !== 'number') throw invalidArgType(name, 'of type number', value);
}

export function validateArray(value: any, name: string): void {
    if (!Array.isArray(value)) throw invalidArgType(name, 'an instance of Array', value);
}

function outOfRange(name: string, range: string, value: any): Error {
    return new NodeRangeError('ERR_OUT_OF_RANGE', 'The value of "' + name + '" is out of range. It must be ' + range + '. Received ' + String(value));
}

function validateInt32(value: any, name: string, min: number): void {
    validateNumber(value, name);
    if (!Number.isInteger(value)) throw outOfRange(name, 'an integer', value);
    if (value < min || value > 2147483647) throw outOfRange(name, '>= ' + min + ' && <= 2147483647', value);
}

function validateUint32(value: any, name: string): void {
    validateNumber(value, name);
    if (!Number.isInteger(value)) throw outOfRange(name, 'an integer', value);
    if (value < 0 || value > 4294967295) throw outOfRange(name, '>= 0 && <= 4294967295', value);
}

export function validateOneOf(value: any, name: string, oneOf: any[]): void {
    if (!oneOf.includes(value)) {
        const allowed = oneOf.map((v) => (typeof v === 'string' ? "'" + v + "'" : String(v))).join(', ');
        const shown = typeof value === 'string' ? "'" + value + "'" : String(value);
        throw new NodeTypeError('ERR_INVALID_ARG_VALUE',
            "The " + (name.includes('.') ? 'property' : 'argument') + " '" + name + "' must be one of: " + allowed + '. Received ' + shown);
    }
}

export function validatePort(port: any, name: string = 'Port'): void {
    const ok = (typeof port === 'number' || (typeof port === 'string' && port.trim().length !== 0)) &&
        +port === (+port >>> 0) && +port <= 0xFFFF;
    if (!ok) {
        throw new NodeRangeError('ERR_SOCKET_BAD_PORT', name + ' should be >= 0 and < 65536. Received ' +
            (typeof port === 'string' ? "type string ('" + port + "')" : 'type ' + typeof port + ' (' + String(port) + ')') + '.');
    }
}

function validateTimeout(options: any): number {
    const timeout = options !== undefined && options !== null && options.timeout !== undefined ? options.timeout : -1;
    validateInt32(timeout, 'options.timeout', -1);
    return timeout + 0;
}

function validateMaxTimeout(options: any): number {
    const maxTimeout = options !== undefined && options !== null && options.maxTimeout !== undefined ? options.maxTimeout : 0;
    validateUint32(maxTimeout, 'options.maxTimeout');
    return maxTimeout + 0;
}

function validateTries(options: any): number {
    const tries = options !== undefined && options !== null && options.tries !== undefined ? options.tries : 4;
    validateInt32(tries, 'options.tries', 1);
    return tries;
}

const IANA_DNS_PORT = 53;
const IPv6RE = /^\[([^[\]]*)\]/;
const addrSplitRE = /(^.+?)(?::(\d+))?$/;

// Resolver instances correspond 1:1 to native channels.
export class ResolverBase {
    _handle: number;

    constructor(options?: any) {
        const timeout = validateTimeout(options);
        const tries = validateTries(options);
        const maxTimeout = validateMaxTimeout(options);
        this._handle = __kml_native.dnsChannelNew(timeout, tries, maxTimeout);
    }

    cancel(): void {
        __kml_native.dnsChannelCancel(this._handle);
    }

    getServers(): string[] {
        const servers: any[] = JSON.parse(__kml_native.dnsChannelServers(this._handle));
        return servers.map((val: any) => {
            if (!val[1] || val[1] === IANA_DNS_PORT)
                return val[0];
            const host = isIP(val[0]) === 6 ? '[' + val[0] + ']' : val[0];
            return host + ':' + val[1];
        });
    }

    setServers(servers: any): void {
        validateArray(servers, 'servers');
        const newSet: any[] = [];
        (servers as any[]).forEach((serv: any, index: number) => {
            validateString(serv, 'servers[' + index + ']');
            let ipVersion = isIP(serv);

            if (ipVersion !== 0) {
                newSet.push([ipVersion, serv, IANA_DNS_PORT]);
                return;
            }

            const match = IPv6RE.exec(serv);

            // Check for an IPv6 in brackets.
            if (match) {
                ipVersion = isIP(match[1]);

                if (ipVersion !== 0) {
                    const port = parseInt((serv as string).replace(addrSplitRE, '$2')) || IANA_DNS_PORT;
                    newSet.push([ipVersion, match[1], port]);
                    return;
                }
            }

            // addr::port
            const addrSplitMatch = addrSplitRE.exec(serv);

            if (addrSplitMatch) {
                const hostIP = addrSplitMatch[1];
                const port = addrSplitMatch[2] || IANA_DNS_PORT;

                ipVersion = isIP(hostIP);

                if (ipVersion !== 0) {
                    newSet.push([ipVersion, hostIP, parseInt(String(port))]);
                    return;
                }
            }

            throw new NodeTypeError('ERR_INVALID_IP_ADDRESS', 'Invalid IP address: ' + serv);
        });

        const spec = newSet.map((s: any) => s[0] + ' ' + s[1] + ' ' + s[2]).join('\n');
        const errorNumber = __kml_native.dnsChannelSetServers(this._handle, spec);
        if (errorNumber !== 0) {
            throw new NodeError('ERR_DNS_SET_SERVERS_FAILED', 'c-ares failed to set servers: "' + __kml_native.dnsStrerror(errorNumber) + '" [' + (servers as any[]).join(',') + ']');
        }
    }

    setLocalAddress(ipv4: any, ipv6?: any): void {
        validateString(ipv4, 'ipv4');
        if (ipv6 !== undefined) {
            validateString(ipv6, 'ipv6');
        }
        const r = __kml_native.dnsChannelSetLocal(this._handle, ipv4, ipv6 === undefined ? '' : ipv6);
        if (r !== 0) {
            throw new NodeTypeError('ERR_INVALID_ARG_VALUE',
                r === 1 ? 'Invalid IP address.' : r === 2 ? 'Cannot specify two IPv4 addresses.' : 'Cannot specify two IPv6 addresses.');
        }
    }
}

let defaultResolver: any;
let dnsOrder = 'verbatim';
export const validDnsOrders = ['verbatim', 'ipv4first', 'ipv6first'];
export const validFamilies = [0, 4, 6];

export function getDefaultResolver(): any {
    return defaultResolver;
}

export function setDefaultResolver(resolver: any): void {
    defaultResolver = resolver;
}

// The getaddrinfo flags Node accepts in `hints`.
export const AI_ADDRCONFIG: number = __kml_native.dnsAiFlag(0);
export const AI_ALL: number = __kml_native.dnsAiFlag(1);
export const AI_V4MAPPED: number = __kml_native.dnsAiFlag(2);

export function validateHints(hints: number): void {
    if ((hints & ~(AI_ADDRCONFIG | AI_ALL | AI_V4MAPPED)) !== 0) {
        throw invalidArgValue('hints', hints);
    }
}

let invalidHostnameWarningEmitted = false;
export function emitInvalidHostnameWarning(hostname: any): void {
    if (!invalidHostnameWarningEmitted) {
        process.emitWarning(
            'The provided hostname "' + String(hostname) + '" is not a valid ' +
            'hostname, and is supported in the dns module solely for compatibility.',
            'DeprecationWarning',
            'DEP0118',
        );
        invalidHostnameWarningEmitted = true;
    }
}

export function setDefaultResultOrder(value: any): void {
    validateOneOf(value, 'dnsOrder', validDnsOrders);
    dnsOrder = value;
}

export function getDefaultResultOrder(): string {
    return dnsOrder;
}

// ERROR CODES
export const errorCodes = {
    NODATA: 'ENODATA',
    FORMERR: 'EFORMERR',
    SERVFAIL: 'ESERVFAIL',
    NOTFOUND: 'ENOTFOUND',
    NOTIMP: 'ENOTIMP',
    REFUSED: 'EREFUSED',
    BADQUERY: 'EBADQUERY',
    BADNAME: 'EBADNAME',
    BADFAMILY: 'EBADFAMILY',
    BADRESP: 'EBADRESP',
    CONNREFUSED: 'ECONNREFUSED',
    TIMEOUT: 'ETIMEOUT',
    EOF: 'EOF',
    FILE: 'EFILE',
    NOMEM: 'ENOMEM',
    DESTRUCTION: 'EDESTRUCTION',
    BADSTR: 'EBADSTR',
    BADFLAGS: 'EBADFLAGS',
    NONAME: 'ENONAME',
    BADHINTS: 'EBADHINTS',
    NOTINITIALIZED: 'ENOTINITIALIZED',
    LOADIPHLPAPI: 'ELOADIPHLPAPI',
    ADDRGETNETWORKPARAMS: 'EADDRGETNETWORKPARAMS',
    CANCELLED: 'ECANCELLED',
};

// A c-ares status's name (ARES_ENODATA is 1, … ARES_ECANCELLED 24).
const caresNames = ['', 'ENODATA', 'EFORMERR', 'ESERVFAIL', 'ENOTFOUND', 'ENOTIMP', 'EREFUSED', 'EBADQUERY',
    'EBADNAME', 'EBADFAMILY', 'EBADRESP', 'ECONNREFUSED', 'ETIMEOUT', 'EOF', 'EFILE', 'ENOMEM', 'EDESTRUCTION',
    'EBADSTR', 'EBADFLAGS', 'ENONAME', 'EBADHINTS', 'ENOTINITIALIZED', 'ELOADIPHLPAPI', 'EADDRGETNETWORKPARAMS',
    'ECANCELLED'];

export function caresName(status: number): string {
    return status > 0 && status < caresNames.length ? caresNames[status] : 'UNKNOWN';
}

// ---- the query: a c-ares query and cares_wrap's parse of its answer ----

// The record type each query asks for.
export const queryTypes: { [binding: string]: number } = {
    queryAny: 255, queryA: 1, queryAaaa: 28, queryCaa: 257, queryCname: 5, queryMx: 15, queryNs: 2,
    queryTlsa: 52, queryTxt: 16, querySrv: 33, queryPtr: 12, queryNaptr: 35, querySoa: 6, getHostByAddr: 12,
};

// A TLSA record's data arrives as hex; cares_wrap hands an ArrayBuffer.
function tlsaData(rec: any): any {
    if (rec === null || typeof rec !== 'object' || rec.certUsage === undefined) return rec;
    const hex: string = rec.data.hex;
    const data = new ArrayBuffer(hex.length / 2);
    const bytes = new Uint8Array(data);
    for (let i = 0; i < bytes.length; i++) bytes[i] = parseInt(hex.slice(i * 2, i * 2 + 2), 16);
    rec.data = data;
    return rec;
}

// Runs binding's query for name on the channel; the callback gets (status,
// result, ttls): status 0, a c-ares status, or a c-ares name for a bad
// address.
export function runQuery(channel: number, binding: string, name: string, done: (status: any, result: any, ttls: any) => void): void {
    const type = binding === 'getHostByAddr' ? 0 : queryTypes[binding];
    const err = __kml_native.dnsQuery(channel, type, idnaToASCII(name), (status: number, unused: number) => {
        if (status !== 0) {
            done(status, undefined, undefined);
            return;
        }
        const answer = JSON.parse(__kml_native.lastString());
        let result = answer.r;
        if (binding === 'queryTlsa' || binding === 'queryAny') result = result.map(tlsaData);
        // The record template's type slot, left undefined without need_type.
        if (binding === 'querySoa') result.type = undefined;
        if (binding === 'queryNaptr') for (const rec of result) rec.type = undefined;
        done(0, result, answer.x);
    });
    if (err !== 0) {
        // The binding refuses a non-address: UV_EINVAL, thrown.
        throw new DNSException(__kml_native.uvErrno(22), binding, name);
    }
}
