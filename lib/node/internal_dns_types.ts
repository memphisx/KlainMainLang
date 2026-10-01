// The dns module's record and option types (@types/node's dns.d.ts), which
// `dns` and `dns/promises` both export.

export interface LookupOptions {
    family?: number | 'IPv4' | 'IPv6' | undefined;
    hints?: number | undefined;
    all?: boolean | undefined;
    order?: 'ipv4first' | 'ipv6first' | 'verbatim' | undefined;
    verbatim?: boolean | undefined;
}
export interface LookupOneOptions extends LookupOptions {
    all?: false | undefined;
}
export interface LookupAllOptions extends LookupOptions {
    all: true;
}
export interface LookupAddress {
    address: string;
    family: number;
}
export interface ResolveOptions {
    ttl: boolean;
}
export interface ResolveWithTtlOptions extends ResolveOptions {
    ttl: true;
}
export interface RecordWithTtl {
    address: string;
    ttl: number;
}
export interface AnyARecord extends RecordWithTtl {
    type: 'A';
}
export interface AnyAaaaRecord extends RecordWithTtl {
    type: 'AAAA';
}
export interface CaaRecord {
    critical: number;
    issue?: string | undefined;
    issuewild?: string | undefined;
    iodef?: string | undefined;
    contactemail?: string | undefined;
    contactphone?: string | undefined;
}
export interface AnyCaaRecord extends CaaRecord {
    type: 'CAA';
}
export interface MxRecord {
    priority: number;
    exchange: string;
}
export interface AnyMxRecord extends MxRecord {
    type: 'MX';
}
export interface NaptrRecord {
    flags: string;
    service: string;
    regexp: string;
    replacement: string;
    order: number;
    preference: number;
}
export interface AnyNaptrRecord extends NaptrRecord {
    type: 'NAPTR';
}
export interface SoaRecord {
    nsname: string;
    hostmaster: string;
    serial: number;
    refresh: number;
    retry: number;
    expire: number;
    minttl: number;
}
export interface AnySoaRecord extends SoaRecord {
    type: 'SOA';
}
export interface SrvRecord {
    priority: number;
    weight: number;
    port: number;
    name: string;
}
export interface AnySrvRecord extends SrvRecord {
    type: 'SRV';
}
export interface TlsaRecord {
    certUsage: number;
    selector: number;
    match: number;
    data: ArrayBuffer;
}
export interface AnyTlsaRecord extends TlsaRecord {
    type: 'TLSA';
}
export interface AnyTxtRecord {
    type: 'TXT';
    entries: string[];
}
export interface AnyNsRecord {
    type: 'NS';
    value: string;
}
export interface AnyPtrRecord {
    type: 'PTR';
    value: string;
}
export interface AnyCnameRecord {
    type: 'CNAME';
    value: string;
}
export type AnyRecord =
    | AnyARecord
    | AnyAaaaRecord
    | AnyCaaRecord
    | AnyCnameRecord
    | AnyMxRecord
    | AnyNaptrRecord
    | AnyNsRecord
    | AnyPtrRecord
    | AnySoaRecord
    | AnySrvRecord
    | AnyTlsaRecord
    | AnyTxtRecord;
export interface ResolverOptions {
    timeout?: number | undefined;
    tries?: number | undefined;
    maxTimeout?: number | undefined;
}
