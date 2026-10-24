// Generates codegen/llvm/collatesrc/collate_tables.h, the collation weights
// String.prototype.localeCompare compares by, from the running Node's own ICU
// collator (root order, as the `en` locale has no tailoring):
//   node tools/gencollate/gen.cjs > codegen/llvm/collatesrc/collate_tables.h
//
// Each covered character maps to collation elements (primary, secondary,
// tertiary), read off Intl.Collator at its three sensitivities:
//   - primary: the order of the characters compared at `base` sensitivity,
//     equal ones sharing a weight; a character equal to two letters there
//     (ß and "ss", æ and "ae") expands to theirs;
//   - secondary: a decomposable character is its NFD base followed by its
//     combining marks, each mark a secondary-only element ordered by
//     `accent` comparison; another accent variant of a letter (ø of o) gets
//     its rank among its primary group;
//   - tertiary: the rank by `variant` comparison among a group's members of
//     one secondary class (lowercase before uppercase);
//   - case: whether the character is uppercase, for `case` sensitivity.
// A character the table does not cover sorts after every covered one, by
// code point. Control characters ICU ignores map to no element.
'use strict';

const base = new Intl.Collator('en', { sensitivity: 'base' });
const accent = new Intl.Collator('en', { sensitivity: 'accent' });
const variant = new Intl.Collator('en', { sensitivity: 'variant' });

// Every Basic Multilingual Plane script, symbol and presentation form, plus
// mathematical letters and emoji. Han ideographs get their own rank table
// below; Hangul syllables decompose to their jamo at run time, as the
// collation algorithm defines them.
const ranges = [
  [0x00, 0x33ff], [0xa000, 0xabff], [0xd7b0, 0xd7ff], [0xfb00, 0xffef],
  [0x1d400, 0x1d7ff], [0x1f000, 0x1faff],
];
const chars = [];
for (const [a, b] of ranges) for (let c = a; c <= b; c++) {
  if (c >= 0xd800 && c <= 0xdfff) continue;
  chars.push(String.fromCodePoint(c));
}
const isMark = (s) => /^\p{M}$/u.test(s);
const ignorable = (s) => variant.compare(s, '') === 0;

// Combining marks that only add a secondary difference, ordered.
const marks = chars.filter((m) => isMark(m) && !ignorable(m) && base.compare('a' + m, 'a') === 0);
marks.sort((x, y) => accent.compare('a' + x, 'a' + y));
const markS = new Map();
{
  let s = 1, prev = null;
  for (const m of marks) {
    if (prev === null || accent.compare('a' + m, 'a' + prev) !== 0) s++;
    markS.set(m, s);
    prev = m;
  }
}
const firstVariantS = Math.max(1, ...markS.values()) + 1;

// Atoms: characters that are their own NFD, neither ignorable nor a
// secondary-only mark.
const atoms = chars.filter((c) => c.normalize('NFD') === c && !ignorable(c) && !markS.has(c));
const letters = 'abcdefghijklmnopqrstuvwxyz';
const expansion = new Map();
for (const c of atoms) {
  if (!/^\p{L}$/u.test(c) || letters.includes(c.toLowerCase())) continue;
  search: for (const x of letters) {
    for (const y of letters) {
      if (base.compare(c, x + y) === 0) {
        expansion.set(c, x + y);
        break search;
      }
    }
  }
}

const singles = atoms.filter((c) => !expansion.has(c));
singles.sort((x, y) => base.compare(x, y) || accent.compare(x, y) || variant.compare(x, y) || (x < y ? -1 : 1));
const isUpper = (c) => /^[\p{Lu}\p{Lt}]$/u.test(c);
const ce = new Map(); // char -> [[p, s, t, case], ...]
{
  let p = 0, i = 0;
  while (i < singles.length) {
    let j = i + 1;
    while (j < singles.length && base.compare(singles[i], singles[j]) === 0) j++;
    p++;
    const group = singles.slice(i, j);
    // Secondary classes within the group, then tertiary ranks within each.
    let s = 0, k = 0;
    while (k < group.length) {
      let l = k + 1;
      while (l < group.length && accent.compare(group[k], group[l]) === 0) l++;
      const sw = s === 0 ? 1 : firstVariantS + s - 1;
      let t = 0, prev = null;
      for (const c of group.slice(k, l)) {
        if (prev === null || variant.compare(c, prev) !== 0) t++;
        ce.set(c, [[p, sw, t * 4, isUpper(c) ? 1 : 0]]);
        prev = c;
      }
      s++;
      k = l;
    }
    i = j;
  }
}
// An expansion differs from its letters at the secondary level (ß, æ, œ:
// after every accent) or, a compatibility ligature (ĳ, ﬁ), at the tertiary
// one, between lowercase and uppercase.
const expS = firstVariantS + 64;
for (const [c, xy] of expansion) {
  const upper = c !== c.toLowerCase();
  const els = [...xy].map((ch) => ce.get(upper ? ch.toUpperCase() : ch)[0].slice());
  if (accent.compare(c, xy) !== 0) els[0][1] = expS;
  else for (const e of els) e[2] = e[2] * 4 - 2;
  ce.set(c, els);
}
for (const [m, s] of markS) ce.set(m, [[0, s, 4, 0]]);
for (const c of chars) {
  if (ce.has(c) || ignorable(c)) continue;
  const d = c.normalize('NFD');
  const head = d.codePointAt(0) <= 0xffff ? d[0] : String.fromCodePoint(d.codePointAt(0));
  const rest = [...d.slice(head.length)];
  if (!ce.has(head) || rest.some((m) => !ce.has(m))) continue;
  ce.set(c, [...ce.get(head).map((e) => e.slice()), ...rest.map((m) => ce.get(m)[0].slice())]);
}

// Han: the BMP ideographs (Extension A, the unified block, the compatibility
// ideographs as their canonical equivalents) ranked in ICU's radical-stroke
// order, primaries after every table entry.
const hanCps = [];
for (let c = 0x3400; c <= 0x4dbf; c++) hanCps.push(c);
for (let c = 0x4e00; c <= 0x9fff; c++) hanCps.push(c);
const hanSorted = hanCps.map((c) => String.fromCodePoint(c)).sort(variant.compare);
const hanRank = new Map(hanSorted.map((c, i) => [c.codePointAt(0), i + 1]));
const hanOf = (c) => hanRank.get(String.fromCodePoint(c).normalize('NFD').codePointAt(0)) || 0;

const out = [];
out.push('/* collate_tables.h — generated by tools/gencollate/gen.cjs from Node ' + process.version + "'s ICU");
out.push(' * (' + process.versions.icu + ', root order). Do not edit. */');
const pool = [], index = [];
for (const c of chars) {
  const cp = c.codePointAt(0);
  if (ignorable(c)) { index.push([cp, 0, 0]); continue; }
  const els = ce.get(c);
  if (!els) continue;
  index.push([cp, pool.length, els.length]);
  for (const e of els) pool.push(e);
}
out.push('static const KmlCE kml_ce_pool[' + pool.length + '] = {');
for (let i = 0; i < pool.length; i += 8) out.push('    ' + pool.slice(i, i + 8).map(([p, s, t, k]) => `{${p}, ${s}, ${t}, ${k}}`).join(', ') + ',');
out.push('};');
out.push('static const KmlCEIndex kml_ce_index[' + index.length + '] = {');
for (let i = 0; i < index.length; i += 6) out.push('    ' + index.slice(i, i + 6).map(([cp, o, n]) => `{0x${cp.toString(16)}, ${o}, ${n}}`).join(', ') + ',');
out.push('};');
out.push('#define KML_CE_MAX_PRIMARY ' + Math.max(...pool.map((e) => e[0])));
const emitRanks = (name, lo, hi) => {
  const r = [];
  for (let c = lo; c <= hi; c++) r.push(hanOf(c));
  out.push(`static const unsigned short ${name}[${r.length}] = { /* U+${lo.toString(16)}..U+${hi.toString(16)} */`);
  for (let i = 0; i < r.length; i += 16) out.push('    ' + r.slice(i, i + 16).join(', ') + ',');
  out.push('};');
};
emitRanks('kml_han_a', 0x3400, 0x4dbf);
emitRanks('kml_han_u', 0x4e00, 0x9fff);
emitRanks('kml_han_c', 0xf900, 0xfaff);
out.push('#define KML_HAN_COUNT ' + hanSorted.length);
process.stdout.write(out.join('\n') + '\n');
