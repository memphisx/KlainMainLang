// Smoke-test program for docker/Dockerfile.gc's -mm=gc build: allocates
// enough throwaway heap churn to force several real Boehm GC collections
// (same shape as examples/memory/memory_gc.ts and
// tests/memory_gc_test.go's forced-collection test), then prints a value
// that only comes out correct if none of those collections corrupted
// anything — proof the shim + GC_stackbottom fiber fix (see
// docs/adr/ADR-00071.md) work in a real Linux/Alpine/musl build, not just
// on this project's macOS dev machine.
let total = 0;
for (let i = 0; i < 500000; i++) {
    let s: string = "abcdefghijklmnopqrstuvwxyz0123456789" + "abcdefghijklmnopqrstuvwxyz0123456789";
    total = total + s.length;
}
console.log("Hello from a -mm=gc KlainMainLang binary!");
console.log("Churn checksum: " + total); // 36000000
