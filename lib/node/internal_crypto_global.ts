// The Web Crypto global read as a value (`const c = crypto`,
// `crypto.getRandomValues` taken by value): a Crypto whose methods, like
// Node's, check their receiver. A call straight off the name
// (`crypto.getRandomValues(a)`) never comes here: code generation compiles
// it directly.

function invalidThis(): TypeError {
    const e: any = new TypeError('Value of "this" must be of type Crypto');
    e.code = 'ERR_INVALID_THIS';
    return e;
}

class Crypto {
    get [Symbol.toStringTag](): string {
        return 'Crypto';
    }

    getRandomValues(array: any): any {
        if (!(this instanceof Crypto)) throw invalidThis();
        return crypto.getRandomValues(array);
    }

    randomUUID(): string {
        if (!(this instanceof Crypto)) throw invalidThis();
        return crypto.randomUUID();
    }
}

let cryptoObj: any = null;

export function _kmlCrypto(): any {
    if (cryptoObj === null) cryptoObj = new Crypto();
    return cryptoObj;
}

