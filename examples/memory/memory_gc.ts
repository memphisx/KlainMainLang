// -mm=gc — the Boehm-Demers-Weiser garbage collector as an alternative to
// this compiler's default "manual" memory mode (see memory_free.ts and
// docs/tdd/TDD-00001.md). Compile this file with:
//
//   klainmain -mm=gc examples/memory/memory_gc.ts
//
// In manual mode (the default `make examples` uses to compile and run every
// file, including this one), every allocation below is simply never freed
// — harmless for a program this short, but exactly the "leaks forever"
// behavior TDD-00001 tracks. Under -mm=gc, the same program's heap stays
// small and bounded throughout, because the collector reclaims each
// throwaway string as soon as nothing references it anymore. The printed
// output is identical either way — gc mode changes *when memory is
// reclaimed*, never what the program computes.
//
// Memory.free(x) (memory_free.ts) still works under -mm=gc too: it just
// lowers to an early "release this now" hint to the collector instead of a
// raw libc free(), rather than being disabled.

let total = 0;
for (let i = 0; i < 500000; i++) {
    // Each iteration's concatenation result is reachable only for this one
    // iteration — a fresh throwaway allocation every time, exactly the
    // pattern a long-running request handler produces once per request.
    let s: string = "abcdefghijklmnopqrstuvwxyz0123456789" + "abcdefghijklmnopqrstuvwxyz0123456789";
    total = total + s.length;
}
console.log(total); // 36000000
