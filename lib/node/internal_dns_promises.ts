// Node's lib/internal/dns/promises.js, in TypeScript: lookup, lookupService
// and the Resolver whose methods return promises; `dns/promises` and
// `dns.promises` are this module.

import { isIP } from './internal_net';
import {
    ResolverBase, DNSException, runQuery, caresName, errorCodes, validateString, validateStringWithoutNullBytes,
    validateNumber, validateBoolean, validateOneOf, validatePort, validateHints, emitInvalidHostnameWarning,
    getDefaultResultOrder, setDefaultResultOrder as setOrder, validDnsOrders, validFamilies, invalidArgType,
    invalidArgValue, missingArgs,
} from './internal_dns_utils';
import { resolveMap } from './internal_dns_callback_resolver';
import type {
    LookupOptions, LookupOneOptions, LookupAllOptions, LookupAddress, ResolveOptions, ResolveWithTtlOptions,
    RecordWithTtl, CaaRecord, MxRecord, NaptrRecord, SoaRecord, SrvRecord, TlsaRecord, AnyRecord,
} from './internal_dns_types';

export * from './internal_dns_types';

function createLookupPromise(family: number, hostname: any, all: boolean, hints: number, dnsOrder: string): Promise<any> {
    return new Promise((resolve, reject) => {
        if (!hostname) {
            emitInvalidHostnameWarning(hostname);
            resolve(all ? [] : { address: null, family: family === 6 ? 6 : 4 });
            return;
        }

        const matchedFamily = isIP(hostname);

        if (matchedFamily !== 0) {
            const result = { address: hostname, family: matchedFamily };
            resolve(all ? [result] : result);
            return;
        }

        const order = dnsOrder === 'ipv4first' ? 1 : dnsOrder === 'ipv6first' ? 2 : 0;
        __kml_native.dnsGetaddrinfo(hostname, family, hints, order, (err: number, unused: number) => {
            if (err !== 0) {
                reject(new DNSException(err, 'getaddrinfo', hostname));
                return;
            }
            const addresses: string[] = JSON.parse(__kml_native.lastString());
            if (all) {
                resolve(addresses.map((address: string) => ({ address, family: family || isIP(address) })));
            } else {
                resolve({ address: addresses[0], family: family || isIP(addresses[0]) });
            }
        });
    });
}

export function lookup(hostname: string, family: number): Promise<LookupAddress>;
export function lookup(hostname: string, options: LookupOneOptions): Promise<LookupAddress>;
export function lookup(hostname: string, options: LookupAllOptions): Promise<LookupAddress[]>;
export function lookup(hostname: string, options: LookupOptions): Promise<LookupAddress | LookupAddress[]>;
export function lookup(hostname: string): Promise<LookupAddress>;
export function lookup(hostname: any, options?: any): Promise<any> {
    let hints = 0;
    let family = 0;
    let all = false;
    let dnsOrder = getDefaultResultOrder();

    // Parse arguments
    if (hostname) {
        validateStringWithoutNullBytes(hostname, 'hostname');
    }

    if (typeof options === 'number') {
        validateOneOf(options, 'family', validFamilies);
        // Coerce -0 to +0.
        family = options + 0;
    } else if (options !== undefined && typeof options !== 'object') {
        throw invalidArgType('options', 'of type number or an instance of Object', options);
    } else {
        if (options?.hints != null) {
            validateNumber(options.hints, 'options.hints');
            hints = options.hints >>> 0;
            validateHints(hints);
        }
        if (options?.family != null) {
            validateOneOf(options.family, 'options.family', validFamilies);
            // Coerce -0 to +0.
            family = options.family + 0;
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

    return createLookupPromise(family, hostname, all, hints, dnsOrder);
}

export function lookupService(address: string, port: number): Promise<{ hostname: string; service: string }>;
export function lookupService(address: any, port: any): Promise<any> {
    if (address === undefined || port === undefined)
        throw missingArgs(['address', 'port']);

    if (isIP(address) === 0)
        throw invalidArgValue('address', address);

    validatePort(port);

    return new Promise((resolve, reject) => {
        __kml_native.dnsGetnameinfo(address, +port + 0, (err: number, unused: number) => {
            if (err !== 0) {
                reject(new DNSException(err, 'getnameinfo', address));
                return;
            }
            const r: string[] = JSON.parse(__kml_native.lastString());
            resolve({ hostname: r[0], service: r[1] });
        });
    });
}

function createResolverPromise(resolver: ResolverBase, binding: string, hostname: string, ttl: boolean): Promise<any> {
    return new Promise((resolve, reject) => {
        runQuery(resolver._handle, binding, hostname, (status: any, result: any, ttls: any) => {
            if (ttls && ttl) {
                result = result.map((address: any, index: number) => ({ address, ttl: ttls[index] }));
            }
            if (status !== 0) {
                reject(new DNSException(caresName(status), binding, hostname));
            } else {
                resolve(result);
            }
        });
    });
}

function query(self: ResolverBase, binding: string, name: any, options: any): Promise<any> {
    validateString(name, 'name');
    return createResolverPromise(self, binding, name, !!(options?.ttl));
}

export class Resolver extends ResolverBase {
    constructor(options?: any) {
        super(options);
    }

    resolveAny(name: any, options?: any): Promise<any> { return query(this, 'queryAny', name, options); }
    resolve4(name: any, options?: any): Promise<any> { return query(this, 'queryA', name, options); }
    resolve6(name: any, options?: any): Promise<any> { return query(this, 'queryAaaa', name, options); }
    resolveCaa(name: any, options?: any): Promise<any> { return query(this, 'queryCaa', name, options); }
    resolveCname(name: any, options?: any): Promise<any> { return query(this, 'queryCname', name, options); }
    resolveMx(name: any, options?: any): Promise<any> { return query(this, 'queryMx', name, options); }
    resolveNs(name: any, options?: any): Promise<any> { return query(this, 'queryNs', name, options); }
    resolveTlsa(name: any, options?: any): Promise<any> { return query(this, 'queryTlsa', name, options); }
    resolveTxt(name: any, options?: any): Promise<any> { return query(this, 'queryTxt', name, options); }
    resolveSrv(name: any, options?: any): Promise<any> { return query(this, 'querySrv', name, options); }
    resolvePtr(name: any, options?: any): Promise<any> { return query(this, 'queryPtr', name, options); }
    resolveNaptr(name: any, options?: any): Promise<any> { return query(this, 'queryNaptr', name, options); }
    resolveSoa(name: any, options?: any): Promise<any> { return query(this, 'querySoa', name, options); }
    reverse(name: any, options?: any): Promise<any> { return query(this, 'getHostByAddr', name, options); }

    resolve(hostname: any, rrtype?: any): Promise<any> {
        let binding: string | undefined;
        if (rrtype !== undefined) {
            validateString(rrtype, 'rrtype');
            binding = resolveMap[rrtype];
            if (binding === undefined) throw invalidArgValue('rrtype', rrtype);
        } else {
            binding = 'queryA';
        }
        return query(this, binding, hostname, undefined);
    }
}

// The default resolver the module's own resolve functions use.
let defaultResolver: Resolver | undefined;

function getDefaultResolver(): Resolver {
    if (defaultResolver === undefined) defaultResolver = new Resolver();
    return defaultResolver;
}

export function getServers(): string[] { return getDefaultResolver().getServers(); }
export function setServers(servers: readonly string[]): void;
export function setServers(servers: any): void {
    const resolver = new Resolver();
    resolver.setServers(servers);
    defaultResolver = resolver;
}
export function resolve(hostname: string): Promise<string[]>;
export function resolve(hostname: string, rrtype: 'A' | 'AAAA' | 'CNAME' | 'NS' | 'PTR'): Promise<string[]>;
export function resolve(hostname: string, rrtype: 'ANY'): Promise<AnyRecord[]>;
export function resolve(hostname: string, rrtype: 'CAA'): Promise<CaaRecord[]>;
export function resolve(hostname: string, rrtype: 'MX'): Promise<MxRecord[]>;
export function resolve(hostname: string, rrtype: 'NAPTR'): Promise<NaptrRecord[]>;
export function resolve(hostname: string, rrtype: 'SOA'): Promise<SoaRecord>;
export function resolve(hostname: string, rrtype: 'SRV'): Promise<SrvRecord[]>;
export function resolve(hostname: string, rrtype: 'TLSA'): Promise<TlsaRecord[]>;
export function resolve(hostname: string, rrtype: 'TXT'): Promise<string[][]>;
export function resolve(hostname: string, rrtype: string): Promise<string[] | CaaRecord[] | MxRecord[] | NaptrRecord[] | SoaRecord | SrvRecord[] | TlsaRecord[] | string[][] | AnyRecord[]>;
export function resolve(hostname: any, rrtype?: any): Promise<any> { return getDefaultResolver().resolve(hostname, rrtype); }
export function resolve4(hostname: string): Promise<string[]>;
export function resolve4(hostname: string, options: ResolveWithTtlOptions): Promise<RecordWithTtl[]>;
export function resolve4(hostname: string, options: ResolveOptions): Promise<string[] | RecordWithTtl[]>;
export function resolve4(name: any, options?: any): Promise<any> { return getDefaultResolver().resolve4(name, options); }
export function resolve6(hostname: string): Promise<string[]>;
export function resolve6(hostname: string, options: ResolveWithTtlOptions): Promise<RecordWithTtl[]>;
export function resolve6(hostname: string, options: ResolveOptions): Promise<string[] | RecordWithTtl[]>;
export function resolve6(name: any, options?: any): Promise<any> { return getDefaultResolver().resolve6(name, options); }
export function resolveAny(hostname: string): Promise<AnyRecord[]>;
export function resolveAny(name: any, options?: any): Promise<any> { return getDefaultResolver().resolveAny(name, options); }
export function resolveCaa(hostname: string): Promise<CaaRecord[]>;
export function resolveCaa(name: any, options?: any): Promise<any> { return getDefaultResolver().resolveCaa(name, options); }
export function resolveCname(hostname: string): Promise<string[]>;
export function resolveCname(name: any, options?: any): Promise<any> { return getDefaultResolver().resolveCname(name, options); }
export function resolveMx(hostname: string): Promise<MxRecord[]>;
export function resolveMx(name: any, options?: any): Promise<any> { return getDefaultResolver().resolveMx(name, options); }
export function resolveNaptr(hostname: string): Promise<NaptrRecord[]>;
export function resolveNaptr(name: any, options?: any): Promise<any> { return getDefaultResolver().resolveNaptr(name, options); }
export function resolveNs(hostname: string): Promise<string[]>;
export function resolveNs(name: any, options?: any): Promise<any> { return getDefaultResolver().resolveNs(name, options); }
export function resolvePtr(hostname: string): Promise<string[]>;
export function resolvePtr(name: any, options?: any): Promise<any> { return getDefaultResolver().resolvePtr(name, options); }
export function resolveSoa(hostname: string): Promise<SoaRecord>;
export function resolveSoa(name: any, options?: any): Promise<any> { return getDefaultResolver().resolveSoa(name, options); }
export function resolveSrv(hostname: string): Promise<SrvRecord[]>;
export function resolveSrv(name: any, options?: any): Promise<any> { return getDefaultResolver().resolveSrv(name, options); }
export function resolveTlsa(hostname: string): Promise<TlsaRecord[]>;
export function resolveTlsa(name: any, options?: any): Promise<any> { return getDefaultResolver().resolveTlsa(name, options); }
export function resolveTxt(hostname: string): Promise<string[][]>;
export function resolveTxt(name: any, options?: any): Promise<any> { return getDefaultResolver().resolveTxt(name, options); }
export function reverse(ip: string): Promise<string[]>;
export function reverse(name: any, options?: any): Promise<any> { return getDefaultResolver().reverse(name, options); }

export { getDefaultResultOrder } from './internal_dns_utils';
export function setDefaultResultOrder(order: 'ipv4first' | 'ipv6first' | 'verbatim'): void;
export function setDefaultResultOrder(value: any): void { setOrder(value); }

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
