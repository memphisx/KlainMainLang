// Node's lib/internal/net.js: the IP address checks net and dns share.

const v4Seg = '(?:25[0-5]|2[0-4][0-9]|1[0-9][0-9]|[1-9][0-9]|[0-9])';
const v4Str = '(?:' + v4Seg + '\\.){3}' + v4Seg;
const IPv4Reg = new RegExp('^' + v4Str + '$');
const v6Seg = '(?:[0-9a-fA-F]{1,4})';
const IPv6Reg = new RegExp('^(?:' +
    '(?:' + v6Seg + ':){7}(?:' + v6Seg + '|:)|' +
    '(?:' + v6Seg + ':){6}(?:' + v4Str + '|:' + v6Seg + '|:)|' +
    '(?:' + v6Seg + ':){5}(?::' + v4Str + '|(?::' + v6Seg + '){1,2}|:)|' +
    '(?:' + v6Seg + ':){4}(?:(?::' + v6Seg + '){0,1}:' + v4Str + '|(?::' + v6Seg + '){1,3}|:)|' +
    '(?:' + v6Seg + ':){3}(?:(?::' + v6Seg + '){0,2}:' + v4Str + '|(?::' + v6Seg + '){1,4}|:)|' +
    '(?:' + v6Seg + ':){2}(?:(?::' + v6Seg + '){0,3}:' + v4Str + '|(?::' + v6Seg + '){1,5}|:)|' +
    '(?:' + v6Seg + ':){1}(?:(?::' + v6Seg + '){0,4}:' + v4Str + '|(?::' + v6Seg + '){1,6}|:)|' +
    '(?::(?:(?::' + v6Seg + '){0,5}:' + v4Str + '|(?::' + v6Seg + '){1,7}|:))' +
    ')(?:%[0-9a-zA-Z-.:]{1,})?$');

// Node tests `${input}`: a JavaScript caller may pass any value.
export function isIPv4(input: string): boolean;
export function isIPv4(input?: any): boolean {
    return IPv4Reg.test(typeof input === 'string' ? input : `${input}`);
}

export function isIPv6(input: string): boolean;
export function isIPv6(input?: any): boolean {
    return IPv6Reg.test(typeof input === 'string' ? input : `${input}`);
}

export function isIP(input: string): number;
export function isIP(input?: any): number {
    const s: string = typeof input === 'string' ? input : `${input}`;
    if (isIPv4(s)) return 4;
    if (isIPv6(s)) return 6;
    return 0;
}
