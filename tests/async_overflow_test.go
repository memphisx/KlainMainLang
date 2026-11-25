package tests

import "testing"

// Unbounded async recursion overflows the stack and throws Node's RangeError
// at the call site, as a synchronous recursion does (ADR-02077).
func TestE2EAsyncRecursionStackOverflow(t *testing.T) {
	assertSameAsNode(t, `let depth = 0;
async function test(): Promise<void> {
  depth++;
  await test();
}
(async () => {
  try {
    await test();
  } catch (err: any) {
    console.log(err instanceof RangeError, err.message, depth > 100);
  }
  async function ok(n: number): Promise<number> { return n === 0 ? 0 : 1 + await ok(n - 1); }
  console.log(await ok(500));
})().then(() => console.log('done'));
`)
}
