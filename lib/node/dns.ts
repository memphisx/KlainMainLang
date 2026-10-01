// Node's lib/dns.js, in TypeScript: lookup and lookupService over the
// pool's getaddrinfo/getnameinfo, the callback Resolver over the native DNS
// client (klaindns.c), and the default resolver's functions.
// kml:default-namespace — `import dns from 'dns'` reads this module's
// exports.

import { isIP } from './internal_net';
import {
    DNSException, errorCodes, validateFunction, validateStringWithoutNullBytes, validateNumber, validateBoolean,
    validateOneOf, validatePort, validateHints, emitInvalidHostnameWarning, getDefaultResultOrder as getOrder,
    setDefaultResultOrder as setOrder, validDnsOrders, validFamilies, invalidArgType, invalidArgValue, missingArgs,
    AI_ADDRCONFIG, AI_ALL, AI_V4MAPPED,
} from './internal_dns_utils';
import { Resolver } from './internal_dns_callback_resolver';
import type {
    LookupOptions, LookupOneOptions, LookupAllOptions, LookupAddress, ResolveOptions, ResolveWithTtlOptions,
    RecordWithTtl, CaaRecord, MxRecord, NaptrRecord, SoaRecord, SrvRecord, TlsaRecord, AnyRecord,
} from './internal_dns_types';

export * from './internal_dns_types';
import {
    lookup as pLookup, lookupService as pLookupService, Resolver as PResolver, getDefaultResultOrder as pGetOrder,
    setDefaultResultOrder as pSetOrder, setServers as pSetServers, getServers as pGetServers, resolve as pResolve,
    resolve4 as pResolve4, resolve6 as pResolve6, resolveAny as pResolveAny, resolveCaa as pResolveCaa,
    resolveCname as pResolveCname, resolveMx as pResolveMx, resolveNaptr as pResolveNaptr, resolveNs as pResolveNs,
    resolvePtr as pResolvePtr, resolveSoa as pResolveSoa, resolveSrv as pResolveSrv, resolveTlsa as pResolveTlsa,
    resolveTxt as pResolveTxt, reverse as pReverse,
} from './internal_dns_promises';

export { Resolver } from './internal_dns_callback_resolver';

// Easy DNS A/AAAA look up
// lookup(hostname, [options,] callback)
export function lookup(hostname: string, family: number, callback: (err: NodeJS.ErrnoException | null, address: string, family: number) => void): void;
export function lookup(hostname: string, options: LookupOneOptions, callback: (err: NodeJS.ErrnoException | null, address: string, family: number) => void): void;
export function lookup(hostname: string, options: LookupAllOptions, callback: (err: NodeJS.ErrnoException | null, addresses: LookupAddress[]) => void): void;
export function lookup(hostname: string, options: LookupOptions, callback: (err: NodeJS.ErrnoException | null, address: string | LookupAddress[], family: number) => void): void;
export function lookup(hostname: string, callback: (err: NodeJS.ErrnoException | null, address: string, family: number) => void): void;
export function lookup(hostname: any, options: any, callback?: any): object {
    let hints = 0;
    let family = 0;
    let all = false;
    let dnsOrder = getOrder();

    // Parse arguments
    if (hostname) {
        validateStringWithoutNullBytes(hostname, 'hostname');
    }

    if (typeof options === 'function') {
        callback = options;
        family = 0;
    } else if (typeof options === 'number') {
        validateFunction(callback, 'callback');

        validateOneOf(options, 'family', validFamilies);
        // Coerce -0 to +0.
        family = options + 0;
    } else if (options !== undefined && typeof options !== 'object') {
        validateFunction(callback === undefined ? options : callback, 'callback');
        throw invalidArgType('options', 'of type number or an instance of Object', options);
    } else {
        validateFunction(callback, 'callback');

        if (options?.hints != null) {
            validateNumber(options.hints, 'options.hints');
            hints = options.hints >>> 0;
            validateHints(hints);
        }
        if (options?.family != null) {
            switch (options.family) {
                case 'IPv4':
                    family = 4;
                    break;
                case 'IPv6':
                    family = 6;
                    break;
                default:
                    validateOneOf(options.family, 'options.family', validFamilies);
                    // Coerce -0 to +0.
                    family = options.family + 0;
                    break;
            }
        }
        if (options?.all != null) {
            validateBoolean(options.all, 'options.all');
            all = options.all;
        }
        if (options?.verbatim != null) {
            validateBoolean(options.verbatim, 'options.verbatim');
            dnsOrder = options.verbatim ? 'verbatim' : 'ipv4first';
        }
        if (options?.order != null) {
            validateOneOf(options.order, 'options.order', validDnsOrders);
            dnsOrder = options.order;
        }
    }

    if (!hostname) {
        emitInvalidHostnameWarning(hostname);
        if (all) {
            process.nextTick(() => callback(null, []));
        } else {
            process.nextTick(() => callback(null, null, family === 6 ? 6 : 4));
        }
        return {};
    }

    const matchedFamily = isIP(hostname);
    if (matchedFamily) {
        if (all) {
            process.nextTick(() => callback(null, [{ address: hostname, family: matchedFamily }]));
        } else {
            process.nextTick(() => callback(null, hostname, matchedFamily));
        }
        return {};
    }

    const req: any = { callback: callback, family: family, hostname: hostname };
    const order = dnsOrder === 'ipv4first' ? 1 : dnsOrder === 'ipv6first' ? 2 : 0;
    __kml_native.dnsGetaddrinfo(hostname, family, hints, order, (err: number, unused: number) => {
        if (err !== 0) {
            callback(new DNSException(err, 'getaddrinfo', hostname));
            return;
        }
        const addresses: any[] = JSON.parse(__kml_native.lastString());
        if (all) {
            for (let i = 0; i < addresses.length; i++) {
                const addr = addresses[i];
                addresses[i] = {
                    address: addr,
                    family: family || isIP(addr),
                };
            }
            callback(null, addresses);
        } else {
            callback(null, addresses[0], family || isIP(addresses[0]));
        }
    });
    return req;
}

export function lookupService(address: string, port: number, callback: (err: NodeJS.ErrnoException | null, hostname: string, service: string) => void): void;
export function lookupService(address: any, port: any, callback: any): object {
    if (address === undefined || port === undefined || callback === undefined)
        throw missingArgs(['address', 'port', 'callback']);

    if (isIP(address) === 0)
        throw invalidArgValue('address', address);

    validatePort(port);

    validateFunction(callback, 'callback');

    // Coerce -0 to +0.
    port = +port + 0;

    const req: any = { callback: callback, hostname: address, port: port };
    __kml_native.dnsGetnameinfo(address, port, (err: number, unused: number) => {
        if (err !== 0) {
            callback(new DNSException(err, 'getnameinfo', address));
            return;
        }
        const r: string[] = JSON.parse(__kml_native.lastString());
        callback(null, r[0], r[1]);
    });
    return req;
}

// The default resolver: the module's resolve functions and getServers are
// its methods; setServers replaces it.
let defaultResolver: Resolver | undefined;

function getDefaultResolver(): Resolver {
    if (defaultResolver === undefined) defaultResolver = new Resolver();
    return defaultResolver;
}

export function setServers(servers: readonly string[]): void;
export function setServers(servers: any): void {
    const resolver = new Resolver();
    resolver.setServers(servers);
    defaultResolver = resolver;
}

export function getServers(): string[] { return getDefaultResolver().getServers(); }
export function resolve(hostname: string, callback: (err: NodeJS.ErrnoException | null, addresses: string[]) => void): void;
export function resolve(hostname: string, rrtype: 'A' | 'AAAA' | 'CNAME' | 'NS' | 'PTR', callback: (err: NodeJS.ErrnoException | null, addresses: string[]) => void): void;
export function resolve(hostname: string, rrtype: 'ANY', callback: (err: NodeJS.ErrnoException | null, addresses: AnyRecord[]) => void): void;
export function resolve(hostname: string, rrtype: 'CAA', callback: (err: NodeJS.ErrnoException | null, address: CaaRecord[]) => void): void;
export function resolve(hostname: string, rrtype: 'MX', callback: (err: NodeJS.ErrnoException | null, addresses: MxRecord[]) => void): void;
export function resolve(hostname: string, rrtype: 'NAPTR', callback: (err: NodeJS.ErrnoException | null, addresses: NaptrRecord[]) => void): void;
export function resolve(hostname: string, rrtype: 'SOA', callback: (err: NodeJS.ErrnoException | null, addresses: SoaRecord) => void): void;
export function resolve(hostname: string, rrtype: 'SRV', callback: (err: NodeJS.ErrnoException | null, addresses: SrvRecord[]) => void): void;
export function resolve(hostname: string, rrtype: 'TLSA', callback: (err: NodeJS.ErrnoException | null, addresses: TlsaRecord[]) => void): void;
export function resolve(hostname: string, rrtype: 'TXT', callback: (err: NodeJS.ErrnoException | null, addresses: string[][]) => void): void;
export function resolve(hostname: string, rrtype: string, callback: (err: NodeJS.ErrnoException | null, addresses: string[] | CaaRecord[] | MxRecord[] | NaptrRecord[] | SoaRecord | SrvRecord[] | TlsaRecord[] | string[][] | AnyRecord[]) => void): void;
export function resolve(hostname: any, rrtype?: any, callback?: any): object {
    return getDefaultResolver().resolve(hostname, rrtype, callback);
}
export function resolve4(hostname: string, callback: (err: NodeJS.ErrnoException | null, addresses: string[]) => void): void;
export function resolve4(hostname: string, options: ResolveWithTtlOptions, callback: (err: NodeJS.ErrnoException | null, addresses: RecordWithTtl[]) => void): void;
export function resolve4(hostname: string, options: ResolveOptions, callback: (err: NodeJS.ErrnoException | null, addresses: string[] | RecordWithTtl[]) => void): void;
export function resolve4(name: any, options?: any, callback?: any): object {
    return getDefaultResolver().resolve4(name, options, callback);
}
export function resolve6(hostname: string, callback: (err: NodeJS.ErrnoException | null, addresses: string[]) => void): void;
export function resolve6(hostname: string, options: ResolveWithTtlOptions, callback: (err: NodeJS.ErrnoException | null, addresses: RecordWithTtl[]) => void): void;
export function resolve6(hostname: string, options: ResolveOptions, callback: (err: NodeJS.ErrnoException | null, addresses: string[] | RecordWithTtl[]) => void): void;
export function resolve6(name: any, options?: any, callback?: any): object {
    return getDefaultResolver().resolve6(name, options, callback);
}
export function resolveAny(hostname: string, callback: (err: NodeJS.ErrnoException | null, addresses: AnyRecord[]) => void): void;
export function resolveAny(name: any, options?: any, callback?: any): object {
    return getDefaultResolver().resolveAny(name, options, callback);
}
export function resolveCaa(hostname: string, callback: (err: NodeJS.ErrnoException | null, records: CaaRecord[]) => void): void;
export function resolveCaa(name: any, options?: any, callback?: any): object {
    return getDefaultResolver().resolveCaa(name, options, callback);
}
export function resolveCname(hostname: string, callback: (err: NodeJS.ErrnoException | null, addresses: string[]) => void): void;
export function resolveCname(name: any, options?: any, callback?: any): object {
    return getDefaultResolver().resolveCname(name, options, callback);
}
export function resolveMx(hostname: string, callback: (err: NodeJS.ErrnoException | null, addresses: MxRecord[]) => void): void;
export function resolveMx(name: any, options?: any, callback?: any): object {
    return getDefaultResolver().resolveMx(name, options, callback);
}
export function resolveNaptr(hostname: string, callback: (err: NodeJS.ErrnoException | null, addresses: NaptrRecord[]) => void): void;
export function resolveNaptr(name: any, options?: any, callback?: any): object {
    return getDefaultResolver().resolveNaptr(name, options, callback);
}
export function resolveNs(hostname: string, callback: (err: NodeJS.ErrnoException | null, addresses: string[]) => void): void;
export function resolveNs(name: any, options?: any, callback?: any): object {
    return getDefaultResolver().resolveNs(name, options, callback);
}
export function resolvePtr(hostname: string, callback: (err: NodeJS.ErrnoException | null, addresses: string[]) => void): void;
export function resolvePtr(name: any, options?: any, callback?: any): object {
    return getDefaultResolver().resolvePtr(name, options, callback);
}
export function resolveSoa(hostname: string, callback: (err: NodeJS.ErrnoException | null, address: SoaRecord) => void): void;
export function resolveSoa(name: any, options?: any, callback?: any): object {
    return getDefaultResolver().resolveSoa(name, options, callback);
}
export function resolveSrv(hostname: string, callback: (err: NodeJS.ErrnoException | null, addresses: SrvRecord[]) => void): void;
export function resolveSrv(name: any, options?: any, callback?: any): object {
    return getDefaultResolver().resolveSrv(name, options, callback);
}
export function resolveTlsa(hostname: string, callback: (err: NodeJS.ErrnoException | null, addresses: TlsaRecord[]) => void): void;
export function resolveTlsa(name: any, options?: any, callback?: any): object {
    return getDefaultResolver().resolveTlsa(name, options, callback);
}
export function resolveTxt(hostname: string, callback: (err: NodeJS.ErrnoException | null, addresses: string[][]) => void): void;
export function resolveTxt(name: any, options?: any, callback?: any): object {
    return getDefaultResolver().resolveTxt(name, options, callback);
}
export function reverse(ip: string, callback: (err: NodeJS.ErrnoException | null, hostnames: string[]) => void): void;
export function reverse(name: any, options?: any, callback?: any): object {
    return getDefaultResolver().reverse(name, options, callback);
}

export function getDefaultResultOrder(): 'ipv4first' | 'ipv6first' | 'verbatim' { return getOrder() as any; }
export function setDefaultResultOrder(order: 'ipv4first' | 'ipv6first' | 'verbatim'): void;
export function setDefaultResultOrder(value: any): void { setOrder(value); }

// uv_getaddrinfo flags
export const ADDRCONFIG: number = AI_ADDRCONFIG;
export const ALL: number = AI_ALL;
export const V4MAPPED: number = AI_V4MAPPED;

// ERROR CODES (internal/dns/utils.js's errorCodes)
export const NODATA = 'ENODATA';
export const FORMERR = 'EFORMERR';
export const SERVFAIL = 'ESERVFAIL';
export const NOTFOUND = 'ENOTFOUND';
export const NOTIMP = 'ENOTIMP';
export const REFUSED = 'EREFUSED';
export const BADQUERY = 'EBADQUERY';
export const BADNAME = 'EBADNAME';
export const BADFAMILY = 'EBADFAMILY';
export const BADRESP = 'EBADRESP';
export const CONNREFUSED = 'ECONNREFUSED';
export const TIMEOUT = 'ETIMEOUT';
export const EOF = 'EOF';
export const FILE = 'EFILE';
export const NOMEM = 'ENOMEM';
export const DESTRUCTION = 'EDESTRUCTION';
export const BADSTR = 'EBADSTR';
export const BADFLAGS = 'EBADFLAGS';
export const NONAME = 'ENONAME';
export const BADHINTS = 'EBADHINTS';
export const NOTINITIALIZED = 'ENOTINITIALIZED';
export const LOADIPHLPAPI = 'ELOADIPHLPAPI';
export const ADDRGETNETWORKPARAMS = 'EADDRGETNETWORKPARAMS';
export const CANCELLED = 'ECANCELLED';

// dns.promises: lib/internal/dns/promises.js's exports.
export const promises = {
    lookup: pLookup,
    lookupService: pLookupService,
    Resolver: PResolver,
    getDefaultResultOrder: pGetOrder,
    setDefaultResultOrder: pSetOrder,
    setServers: pSetServers,
    getServers: pGetServers,
    resolve: pResolve,
    resolve4: pResolve4,
    resolve6: pResolve6,
    resolveAny: pResolveAny,
    resolveCaa: pResolveCaa,
    resolveCname: pResolveCname,
    resolveMx: pResolveMx,
    resolveNaptr: pResolveNaptr,
    resolveNs: pResolveNs,
    resolvePtr: pResolvePtr,
    resolveSoa: pResolveSoa,
    resolveSrv: pResolveSrv,
    resolveTlsa: pResolveTlsa,
    resolveTxt: pResolveTxt,
    reverse: pReverse,
    NODATA, FORMERR, SERVFAIL, NOTFOUND, NOTIMP, REFUSED, BADQUERY, BADNAME, BADFAMILY, BADRESP,
    CONNREFUSED, TIMEOUT, EOF, FILE, NOMEM, DESTRUCTION, BADSTR, BADFLAGS, NONAME, BADHINTS,
    NOTINITIALIZED, LOADIPHLPAPI, ADDRGETNETWORKPARAMS, CANCELLED,
};
