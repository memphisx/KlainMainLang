package tests

import "testing"

// util.parseArgs as Node's lib/internal/util/parse_args in TypeScript
// (ADR-01349): short groups, values, multiple, defaults, negation, tokens,
// positionals and every strict-mode error. Checked against the local Node.
func TestE2EUtilParseArgs(t *testing.T) {
	assertSameAsNodeImports(t, `
import { parseArgs } from 'util';
import util from 'util';
const t = (label: string, f: () => any) => { try { const r = f(); console.log(label, 'ok', r); } catch (e: any) { console.log(label, e.name, e.code, JSON.stringify(e.message)); } };
const options = { verbose: { type: 'boolean' as const, short: 'v' }, port: { type: 'string' as const, short: 'p', default: '8080' }, tag: { type: 'string' as const, multiple: true } };
t('basic', () => parseArgs({ args: ['-v', '--port', '3000', '--tag', 'a', '--tag=b'], options }));
t('group', () => parseArgs({ args: ['-vp9000'], options }).values);
t('default', () => parseArgs({ args: [], options }).values);
t('positionals', () => parseArgs({ args: ['a', '-v', '--', '-x'], options, allowPositionals: true }));
t('unknown', () => parseArgs({ args: ['--nope'], options }));
t('unknown-pos', () => parseArgs({ args: ['--nope'], options, allowPositionals: true }));
t('missing', () => parseArgs({ args: ['--port'], options }));
t('bool-arg', () => parseArgs({ args: ['--verbose=yes'], options }));
t('ambiguous', () => parseArgs({ args: ['--port', '--verbose'], options }));
t('unexpected', () => parseArgs({ args: ['x'], options }));
t('nonstrict', () => parseArgs({ args: ['--foo', 'x', '-ab'], strict: false }));
t('negative', () => parseArgs({ args: ['--no-verbose'], options, allowNegative: true }).values);
t('tokens', () => parseArgs({ args: ['-v', 'x'], options, allowPositionals: true, tokens: true }).tokens);
t('bad-type', () => parseArgs({ args: [], options: { a: { type: 'number' } } as any }));
t('bad-short', () => parseArgs({ args: [], options: { a: { type: 'string', short: 'ab' } } }));
t('util', () => typeof util.parseArgs);
`)
}

// arr.push(...items) / arr.unshift(...items) with spreads (ADR-01349).
func TestE2EArrayPushUnshiftSpread(t *testing.T) {
	assertSameAsNode(t, `
const a = [1, 2];
a.push(0, ...a, 9);
console.log(a, a.length);
const o: { n: number }[] = [{ n: 1 }];
o.push(...[{ n: 2 }, { n: 3 }]);
console.log(o);
const m: any[] = ['x'];
m.push(...['y', 1], ...[true]);
m.unshift(...[null, { k: 1 }]);
console.log(m);
const nest: number[][] = [[1]];
nest.push(...[[2, 3], [4]]);
nest[1].push(5);
console.log(nest, nest.unshift(...[]));
`)
}

// An array literal mixing one kind with null is that kind, nullable
// (ADR-01349).
func TestE2EArrayLiteralNullableElements(t *testing.T) {
	assertSameAsNode(t, `
const z: any[] = [...[null, { k: 1 }]];
console.log(z);
const inner = [null, { k: 2 }];
const y: any[] = [...inner];
console.log(y);
const w: any[] = [...[{ k: 3 }]];
console.log(w);
const inner2 = [null, { k: 2 }];
console.log(inner2, inner2[1]);
const i2 = [{ k: 2 }, null];
console.log(i2);
`)
}

// util.inspect's configured depth reaches a custom [inspect.custom] method,
// as %o's depth 4 does (ADR-01349).
func TestE2EUtilInspectCustomDepth(t *testing.T) {
	assertSameAsNodeImports(t, `
import util from 'util';
class K { [util.inspect.custom](depth: number, options: any): string { return 'K!' + depth + '/' + options.depth; } }
console.log(util.inspect(new K(), { depth: 4 }), util.inspect([new K()], { depth: 4 }), util.inspect(new K()));
console.log(util.format('%o', new K()), util.inspect({ a: { b: new K() } }, { depth: 1 }));
`)
}
