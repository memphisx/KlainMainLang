// CommonJS modules: a file that uses `module`/`exports` is loaded as Node
// loads one. `require` gives back whatever its module.exports holds once the
// file has run; an extensionless path finds the .js file.
const assert = require('assert');
const add = require('./commonjs_modules/add');
const { inc, get } = require('./commonjs_modules/counter');
const counter = require('./commonjs_modules/counter.js');

assert.strictEqual(add(2, 3), 5);
assert.strictEqual(add.twice(4), 8);
inc();
counter.inc();
assert.strictEqual(get(), 2);

// This file has a module object of its own, and `global` is globalThis.
assert.strictEqual(module.exports, exports);
assert.strictEqual(global, globalThis);
console.log(`add(2, 3) = ${add(2, 3)}, count = ${get()}`);
