// navigator: the host as Node describes it. The language follows LC_ALL,
// LC_MESSAGES or LANG (en-US when none is set).
import assert from 'assert';

assert.ok(navigator.hardwareConcurrency >= 1);
assert.deepStrictEqual(navigator.languages, [navigator.language]);
assert.ok(navigator.userAgent.startsWith('Node.js/'));
console.log(typeof navigator.platform, navigator.language.length > 0);

// Navigator itself cannot be constructed.
assert.throws(() => new (Navigator as any)(), { name: 'TypeError', message: 'Illegal constructor' });
