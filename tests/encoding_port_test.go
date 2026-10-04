package tests

import "testing"

// TextEncoder/TextDecoder as Node's lib/internal/encoding.js in TypeScript
// (ADR-01348): UTF-8, UTF-16LE/BE and windows-1252 decoding, fatal, stream,
// BOM handling, labels, errors and inspect. Checked against the local Node.
func TestE2ETextEncoderDecoderNodeEncoding(t *testing.T) {
	assertSameAsNodeImports(t, `
import util from 'util';
const t = (label: string, f: () => any) => { try { const r = f(); console.log(label, 'ok', typeof r === 'string' ? JSON.stringify(r) : r); } catch (e: any) { console.log(label, e.name, e.code, JSON.stringify(e.message)); } };
const enc = new TextEncoder();
t('encode', () => enc.encode('héllo €'));
t('encode-empty', () => enc.encode());
t('encoding', () => [enc.encoding, Object.prototype.toString.call(enc.encode('a'))]);
t('encodeInto', () => { const d = new Uint8Array(5); return [enc.encodeInto('a€b😀', d), Array.from(d)]; });
t('encodeInto-full', () => { const d = new Uint8Array(10); return enc.encodeInto('a😀b', d); });
const dec = new TextDecoder();
t('props', () => [dec.encoding, dec.fatal, dec.ignoreBOM]);
t('utf8', () => dec.decode(new Uint8Array([0xEF, 0xBB, 0xBF, 0x68, 0xC3, 0xA9, 0xFF, 0x21])));
t('ignoreBOM', () => new TextDecoder('utf-8', { ignoreBOM: true }).decode(new Uint8Array([0xEF, 0xBB, 0xBF, 0x68])) === '\uFEFFh');
t('fatal', () => new TextDecoder('utf-8', { fatal: true }).decode(new Uint8Array([0x68, 0xFF])));
t('stream', () => { const d = new TextDecoder(); return [d.decode(new Uint8Array([0xE2, 0x82]), { stream: true }), d.decode(new Uint8Array([0xAC, 0x21]))]; });
t('stream-flush', () => { const d = new TextDecoder(); return [d.decode(new Uint8Array([0xE2, 0x82]), { stream: true }), d.decode()]; });
t('utf16le', () => new TextDecoder('utf-16le').decode(new Uint8Array([0x68, 0x00, 0xAC, 0x20, 0x3D, 0xD8, 0x00, 0xDE])));
t('utf16le-bom', () => new TextDecoder('utf-16').decode(new Uint8Array([0xFF, 0xFE, 0x68, 0x00])));
t('utf16be', () => new TextDecoder('utf-16be').decode(new Uint8Array([0x00, 0x68, 0x20, 0xAC])));
t('latin1', () => { const d = new TextDecoder('latin1'); return [d.encoding, d.decode(new Uint8Array([0x63, 0x61, 0x66, 0xE9, 0x80, 0x9F, 0xFF]))]; });
t('ascii', () => new TextDecoder('ascii').encoding);
t('label-trim', () => new TextDecoder('  UTF8 ').encoding);
t('bad-label', () => new TextDecoder('nope'));
t('bad-input', () => dec.decode(5 as any));
const ab = new ArrayBuffer(2); new Uint8Array(ab).set([104, 105]);
t('dataview', () => dec.decode(new DataView(ab)));
t('arraybuffer', () => dec.decode(ab));
t('inspect', () => [util.inspect(enc), util.inspect(dec)]);
t('fatal-utf16', () => new TextDecoder('utf-16le', { fatal: true }).decode(new Uint8Array([0x00, 0xD8])));
t('odd-utf16', () => new TextDecoder('utf-16le').decode(new Uint8Array([0x68, 0x00, 0x69])));
`)
}

// Every single-byte WHATWG encoding (tables generated from Node's) and
// x-user-defined, over all 256 bytes and with fatal, and the Greek labels
// (ADR-01348).
func TestE2ETextDecoderSingleByteEncodings(t *testing.T) {
	assertSameAsNode(t, `
const names = ['ibm866', 'iso-8859-2', 'iso-8859-3', 'iso-8859-4', 'iso-8859-5', 'iso-8859-6', 'iso-8859-7', 'iso-8859-8', 'iso-8859-8-i', 'iso-8859-10', 'iso-8859-13', 'iso-8859-14', 'iso-8859-15', 'iso-8859-16', 'koi8-r', 'koi8-u', 'macintosh', 'windows-874', 'windows-1250', 'windows-1251', 'windows-1252', 'windows-1253', 'windows-1254', 'windows-1255', 'windows-1256', 'windows-1257', 'windows-1258', 'x-mac-cyrillic', 'x-user-defined'];
const all = new Uint8Array(256);
for (let i = 0; i < 256; i++) all[i] = i;
for (const n of names) {
  const d = new TextDecoder(n);
  const s = d.decode(all);
  let cps = '';
  for (const ch of s) cps += ch.codePointAt(0)!.toString(16) + ' ';
  let fatal = 'ok';
  try { new TextDecoder(n, { fatal: true }).decode(all); } catch (e: any) { fatal = e.code; }
  console.log(d.encoding, fatal, cps.length, cps.slice(380, 520));
}
console.log(new TextDecoder('greek').decode(new Uint8Array([0xc8, 0xe5, 0xf3, 0xf3, 0xe1, 0xeb, 0xef, 0xed, 0xdf, 0xea, 0xe7])));
for (const l of ['cp1253', 'elot_928', 'csisolatingreek', 'l2', 'cyrillic', 'koi', 'dos-874', 'iso-2022-kr']) { try { console.log(l, new TextDecoder(l).encoding); } catch (e: any) { console.log(l, e.code, e.message); } }
`)
}
