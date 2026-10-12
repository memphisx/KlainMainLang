// A typed port of Node's test/common/dns.js: a naive DNS packet parser and
// writer for tests that answer queries from a local UDP server, plus the
// lookup mocks. Same exports, same wire format.
import assert from 'assert';
import { isIP } from 'net';

const types: Record<string, number> = {
  A: 1,
  AAAA: 28,
  NS: 2,
  CNAME: 5,
  SOA: 6,
  PTR: 12,
  MX: 15,
  TXT: 16,
  ANY: 255,
  CAA: 257,
};

const classes: Record<string, number> = {
  IN: 1,
};

function readDomainFromPacket(buffer: Buffer, offset: number): any {
  assert.ok(offset < buffer.length);
  const length: number = buffer[offset];
  if (length === 0) {
    return { nread: 1, domain: '' };
  } else if ((length & 0xC0) === 0) {
    offset += 1;
    const chunk: string = buffer.toString('ascii', offset, offset + length);
    const rest: any = readDomainFromPacket(buffer, offset + length);
    return {
      nread: 1 + length + rest.nread,
      domain: rest.domain ? `${chunk}.${rest.domain}` : chunk,
    };
  }
  assert.strictEqual(length & 0xC0, 0xC0);
  const pointeeOffset: number = buffer.readUInt16BE(offset) & ~0xC000;
  return {
    nread: 2,
    domain: readDomainFromPacket(buffer, pointeeOffset),
  };
}

function parseDNSPacket(buffer: Buffer): any {
  assert.ok(buffer.length > 12);

  const parsed: any = {
    id: buffer.readUInt16BE(0),
    flags: buffer.readUInt16BE(2),
  };

  const counts: [string, number][] = [
    ['questions', buffer.readUInt16BE(4)],
    ['answers', buffer.readUInt16BE(6)],
    ['authorityAnswers', buffer.readUInt16BE(8)],
    ['additionalRecords', buffer.readUInt16BE(10)],
  ];

  let offset: number = 12;
  for (const [sectionName, count] of counts) {
    parsed[sectionName] = [];
    for (let i = 0; i < count; ++i) {
      const name: any = readDomainFromPacket(buffer, offset);
      offset += name.nread;

      const type: number = buffer.readUInt16BE(offset);

      const rr: any = {
        domain: name.domain,
        cls: buffer.readUInt16BE(offset + 2),
      };
      offset += 4;

      for (const typeName in types) {
        if (types[typeName] === type)
          rr.type = typeName;
      }

      if (sectionName !== 'questions') {
        rr.ttl = buffer.readInt32BE(offset);
        const dataLength: number = buffer.readUInt16BE(offset);
        offset += 6;

        switch (type) {
          case types.A:
            assert.strictEqual(dataLength, 4);
            rr.address = `${buffer[offset + 0]}.${buffer[offset + 1]}.` +
                         `${buffer[offset + 2]}.${buffer[offset + 3]}`;
            break;
          case types.AAAA:
            assert.strictEqual(dataLength, 16);
            rr.address = buffer.toString('hex', offset, offset + 16)
                               .replace(/(.{4}(?!$))/g, '$1:');
            break;
          case types.TXT:
          {
            let position: number = offset;
            rr.entries = [];
            while (position < offset + dataLength) {
              const txtLength: number = buffer[offset];
              rr.entries.push(buffer.toString('utf8',
                                              position + 1,
                                              position + 1 + txtLength));
              position += 1 + txtLength;
            }
            assert.strictEqual(position, offset + dataLength);
            break;
          }
          case types.MX:
          {
            rr.priority = buffer.readInt16BE(offset);
            offset += 2;
            const ex: any = readDomainFromPacket(buffer, offset);
            rr.exchange = ex.domain;
            assert.strictEqual(ex.nread, dataLength);
            break;
          }
          case types.NS:
          case types.CNAME:
          case types.PTR:
          {
            const v: any = readDomainFromPacket(buffer, offset);
            rr.value = v.domain;
            assert.strictEqual(v.nread, dataLength);
            break;
          }
          case types.SOA:
          {
            const mname: any = readDomainFromPacket(buffer, offset);
            const rname: any = readDomainFromPacket(buffer, offset + mname.nread);
            rr.nsname = mname.domain;
            rr.hostmaster = rname.domain;
            const trailerOffset: number = offset + mname.nread + rname.nread;
            rr.serial = buffer.readUInt32BE(trailerOffset);
            rr.refresh = buffer.readUInt32BE(trailerOffset + 4);
            rr.retry = buffer.readUInt32BE(trailerOffset + 8);
            rr.expire = buffer.readUInt32BE(trailerOffset + 12);
            rr.minttl = buffer.readUInt32BE(trailerOffset + 16);

            assert.strictEqual(trailerOffset + 20, dataLength);
            break;
          }
          default:
            throw new Error(`Unknown RR type ${rr.type}`);
        }
        offset += dataLength;
      }

      parsed[sectionName].push(rr);

      assert.ok(offset <= buffer.length);
    }
  }

  assert.strictEqual(offset, buffer.length);
  return parsed;
}

function writeIPv6(ip: string): Buffer {
  const parts: string[] = ip.replace(/^:|:$/g, '').split(':');
  const buf: Buffer = Buffer.alloc(16);

  let offset: number = 0;
  for (const part of parts) {
    if (part === '') {
      offset += 16 - 2 * (parts.length - 1);
    } else {
      buf.writeUInt16BE(parseInt(part, 16), offset);
      offset += 2;
    }
  }

  return buf;
}

function writeDomainName(domain: string): Buffer {
  const pieces: Buffer[] = domain.split('.').map((label: string): Buffer => {
    assert(label.length < 64);
    return Buffer.concat([
      Buffer.from([label.length]),
      Buffer.from(label, 'ascii'),
    ]);
  });
  pieces.push(Buffer.alloc(1));
  return Buffer.concat(pieces);
}

// Big-endian bytes of 16-bit and 32-bit fields, as the original's typed
// arrays byte-swapped on a little-endian host produce.
function u16(values: number[]): Buffer {
  const b: Buffer = Buffer.alloc(values.length * 2);
  for (let i = 0; i < values.length; i++) b.writeUInt16BE((values[i] | 0) & 0xFFFF, i * 2);
  return b;
}

function u32(values: number[]): Buffer {
  const b: Buffer = Buffer.alloc(values.length * 4);
  for (let i = 0; i < values.length; i++) b.writeUInt32BE(values[i] >>> 0, i * 4);
  return b;
}

function i32(value: number): Buffer {
  const b: Buffer = Buffer.alloc(4);
  b.writeInt32BE(value | 0, 0);
  return b;
}

function sectionLength(section: any): number {
  return section === undefined || section === null ? 0 : section.length;
}

function writeDNSPacket(parsed: any): Buffer {
  const buffers: Buffer[] = [];
  const kStandardResponseFlags: number = 0x8180;

  buffers.push(u16([
    parsed.id,
    parsed.flags ?? kStandardResponseFlags,
    sectionLength(parsed.questions),
    sectionLength(parsed.answers),
    sectionLength(parsed.authorityAnswers),
    sectionLength(parsed.additionalRecords),
  ]));

  for (const q of parsed.questions) {
    assert(types[q.type]);
    buffers.push(writeDomainName(q.domain));
    buffers.push(u16([
      types[q.type],
      q.cls === undefined ? classes.IN : q.cls,
    ]));
  }

  const records: any[] = [];
  for (const section of [parsed.answers, parsed.authorityAnswers, parsed.additionalRecords]) {
    if (section === undefined || section === null) continue;
    for (const rr of section) records.push(rr);
  }

  for (const rr of records) {
    if (!rr) continue;

    assert(types[rr.type]);
    buffers.push(writeDomainName(rr.domain));
    buffers.push(u16([
      types[rr.type],
      rr.cls === undefined ? classes.IN : rr.cls,
    ]));
    buffers.push(i32(rr.ttl === undefined ? 0 : rr.ttl));

    let rdata: Buffer[] = [];
    switch (rr.type) {
      case 'A': {
        const octets: number[] = rr.address.split('.').map((s: string): number => Number(s) & 0xFF);
        rdata.push(Buffer.from(octets));
        break;
      }
      case 'AAAA':
        rdata.push(writeIPv6(rr.address));
        break;
      case 'TXT': {
        for (const txt of rr.entries) {
          rdata.push(Buffer.from([Buffer.byteLength(txt) & 0xFF]));
          rdata.push(Buffer.from(txt));
        }
        break;
      }
      case 'MX':
        rdata.push(u16([rr.priority]));
        rdata.push(writeDomainName(rr.exchange));
        break;
      case 'NS':
      case 'CNAME':
      case 'PTR':
        rdata.push(writeDomainName(rr.exchange || rr.value));
        break;
      case 'SOA':
        rdata.push(writeDomainName(rr.nsname));
        rdata.push(writeDomainName(rr.hostmaster));
        rdata.push(u32([rr.serial, rr.refresh, rr.retry, rr.expire, rr.minttl]));
        break;
      case 'CAA':
        rdata.push(Buffer.from([Number(rr.critical) & 0xFF]));
        rdata.push(Buffer.from([5]));
        rdata.push(Buffer.from('issue' + rr.issue));
        break;
      default:
        throw new Error(`Unknown RR type ${rr.type}`);
    }
    const body: Buffer = Buffer.concat(rdata);
    // The original's TXT length counts characters, not bytes; keep its value.
    let rdLength: number = body.length;
    if (rr.type === 'TXT') {
      let total: number = 0;
      for (const s of rr.entries) total += s.length;
      rdLength = rr.entries.length + total;
    }
    buffers.push(u16([rdLength]));
    buffers.push(body);
  }

  return Buffer.concat(buffers);
}

const mockedErrorCode: string = 'ENOTFOUND';
const mockedSysCall: string = 'getaddrinfo';

function errorLookupMock(code: string = mockedErrorCode, syscall: string = mockedSysCall): any {
  return function lookupWithError(hostname: string, dnsopts: any, cb: (err: any) => void): void {
    const err: any = new Error(`${syscall} ${code} ${hostname}`);
    err.code = code;
    err.errno = code;
    err.syscall = syscall;
    err.hostname = hostname;
    cb(err);
  };
}

function createMockedLookup(...addresses: string[]): any {
  const resolved: any[] = addresses.map((address: string): any => ({ address: address, family: isIP(address) }));

  return function lookup(hostname: string, options: any, cb: (...args: any[]) => void): void {
    if (options.all === true) {
      process.nextTick(() => {
        cb(null, resolved);
      });
      return;
    }

    process.nextTick(() => {
      cb(null, resolved[0].address, resolved[0].family);
    });
  };
}

const dnstools = {
  types: types,
  classes: classes,
  writeDNSPacket: writeDNSPacket,
  parseDNSPacket: parseDNSPacket,
  errorLookupMock: errorLookupMock,
  mockedErrorCode: mockedErrorCode,
  mockedSysCall: mockedSysCall,
  createMockedLookup: createMockedLookup,
};

export default dnstools;
