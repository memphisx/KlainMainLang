// Node's lib/internal/dns/callback_resolver.js, in TypeScript: the Resolver
// whose methods take a callback.

import {
    ResolverBase, DNSException, runQuery, caresName, validateString, validateFunction,
    invalidArgType, invalidArgValue,
} from './internal_dns_utils';

// query(name, [options,] callback) for one binding: the callback gets (err,
// result), the result as { address, ttl } records with options.ttl.
function query(self: ResolverBase, binding: string, name: any, options: any, callback: any): object {
    // query(name, [options,] callback)
    if (callback === undefined) {
        callback = options;
        options = undefined;
    }

    validateString(name, 'name');
    validateFunction(callback, 'callback');

    const req: any = { bindingName: binding, hostname: name, ttl: !!(options?.ttl) };
    runQuery(self._handle, binding, name, (status: any, result: any, ttls: any) => {
        if (ttls && req.ttl) {
            result = result.map((address: any, index: number) => ({ address, ttl: ttls[index] }));
        }
        if (status !== 0) {
            callback(new DNSException(caresName(status), binding, name));
        } else {
            callback(null, result);
        }
    });
    return req;
}

export class Resolver extends ResolverBase {
    constructor(options?: any) {
        super(options);
    }

    resolveAny(name: any, options?: any, callback?: any): object {
        return query(this, 'queryAny', name, options, callback);
    }
    resolve4(name: any, options?: any, callback?: any): object {
        return query(this, 'queryA', name, options, callback);
    }
    resolve6(name: any, options?: any, callback?: any): object {
        return query(this, 'queryAaaa', name, options, callback);
    }
    resolveCaa(name: any, options?: any, callback?: any): object {
        return query(this, 'queryCaa', name, options, callback);
    }
    resolveCname(name: any, options?: any, callback?: any): object {
        return query(this, 'queryCname', name, options, callback);
    }
    resolveMx(name: any, options?: any, callback?: any): object {
        return query(this, 'queryMx', name, options, callback);
    }
    resolveNs(name: any, options?: any, callback?: any): object {
        return query(this, 'queryNs', name, options, callback);
    }
    resolveTlsa(name: any, options?: any, callback?: any): object {
        return query(this, 'queryTlsa', name, options, callback);
    }
    resolveTxt(name: any, options?: any, callback?: any): object {
        return query(this, 'queryTxt', name, options, callback);
    }
    resolveSrv(name: any, options?: any, callback?: any): object {
        return query(this, 'querySrv', name, options, callback);
    }
    resolvePtr(name: any, options?: any, callback?: any): object {
        return query(this, 'queryPtr', name, options, callback);
    }
    resolveNaptr(name: any, options?: any, callback?: any): object {
        return query(this, 'queryNaptr', name, options, callback);
    }
    resolveSoa(name: any, options?: any, callback?: any): object {
        return query(this, 'querySoa', name, options, callback);
    }
    reverse(name: any, options?: any, callback?: any): object {
        return query(this, 'getHostByAddr', name, options, callback);
    }

    resolve(hostname: any, rrtype?: any, callback?: any): object {
        let binding: string | undefined;
        if (typeof rrtype === 'string') {
            binding = resolveMap[rrtype];
        } else if (typeof rrtype === 'function') {
            binding = 'queryA';
            callback = rrtype;
        } else {
            throw invalidArgType('rrtype', 'of type string', rrtype);
        }

        if (binding !== undefined) {
            return query(this, binding, hostname, callback, undefined);
        }
        throw invalidArgValue('rrtype', rrtype);
    }
}

// The binding each rrtype resolves with.
export const resolveMap: { [rrtype: string]: string } = {
    ANY: 'queryAny', A: 'queryA', AAAA: 'queryAaaa', CAA: 'queryCaa', CNAME: 'queryCname', MX: 'queryMx',
    NS: 'queryNs', TLSA: 'queryTlsa', TXT: 'queryTxt', SRV: 'querySrv', PTR: 'queryPtr', NAPTR: 'queryNaptr',
    SOA: 'querySoa',
};
