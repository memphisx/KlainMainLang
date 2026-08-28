// The node:test runner (TDD-00140) — real unit tests, portable with Node.
// Run the compiled binary: per-test TAP-shaped lines, a summary, and a
// non-zero exit code when anything fails.
import { test, describe, it, after, beforeEach } from 'node:test';
import assert from 'assert';

function slugify(s: string): string {
  return s.toLowerCase().trim().split(" ").join("-");
}

let checked = 0;
beforeEach(() => { checked = checked + 1; });
after(() => { console.log("# ran", checked, "checks"); });

describe('slugify', () => {
  it('lowercases and joins', () => {
    assert.strictEqual(slugify("Kalimera Kosme"), "kalimera-kosme");
  });
  it('trims edges', () => {
    assert.strictEqual(slugify("  Thessaloniki  "), "thessaloniki");
  });
});

test('async work', async () => {
  const v = await Promise.resolve("ok");
  assert.strictEqual(v, "ok");
});

test('context helpers', (t) => {
  t.after(() => { console.log("# cleanup ran"); });
  assert.ok(slugify("A B").includes("-"));
});

test('not written yet', { todo: true }, () => {});
