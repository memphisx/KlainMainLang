// kml:global
// The Web Crypto global read as a value (`const c = crypto`,
// `crypto.getRandomValues` taken by value): a Crypto whose methods, like
// Node's, check their receiver. A call straight off the name
// (`crypto.getRandomValues(a)`) never comes here: code generation compiles
// it directly.

import { _kmlSubtle } from './internal_crypto_webcrypto';

function invalidThis(): TypeError {
    const e: any = new TypeError('Value of "this" must be of type Crypto');
    e.code = 'ERR_INVALID_THIS';
    return e;
}

let creating = false;

// The global `Crypto`: Node's constructor is illegal to call.
export class Crypto {
    constructor() {
        if (!creating) {
            const e: any = new TypeError('Illegal constructor');
            e.code = 'ERR_ILLEGAL_CONSTRUCTOR';
            throw e;
        }
    }

    get [Symbol.toStringTag](): string {
        return 'Crypto';
    }

    get subtle(): any {
        if (!(this instanceof Crypto)) throw invalidThis();
        return _kmlSubtle();
    }

    getRandomValues(array: any): any {
        if (!(this instanceof Crypto)) throw invalidThis();
        return globalThis.crypto.getRandomValues(array);
    }

    randomUUID(): string {
        if (!(this instanceof Crypto)) throw invalidThis();
        return globalThis.crypto.randomUUID();
    }
}

let cryptoObj: any = null;

export function _kmlCrypto(): any {
    if (cryptoObj === null) {
        // WebIDL members are enumerable.
        for (const k of ['subtle', 'getRandomValues', 'randomUUID']) {
            Object.defineProperty(Crypto.prototype, k, { enumerable: true });
        }
        creating = true;
        cryptoObj = new Crypto();
        creating = false;
    }
    return cryptoObj;
}

