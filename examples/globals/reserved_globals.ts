// -globals=strict|permissive (TDD-00050, default strict) — whether a
// program may declare its own binding named the same as an ambient global
// (Math/JSON/console/process/fetch/parseInt/... — "Tier 1"). Real
// browsers/Node genuinely allow `const Math = {}`; this compiler's default
// is deliberately stricter, closing the same class of silent-miscompile
// bug TDD-00049 fixed for fs/path/etc. (a same-named local variable
// silently routing to the built-in instead of the user's own value).
//
// This file only exercises the *default* (`strict`) behavior — `make
// examples` always compiles with default flags, and a real shadowing
// declaration would fail to compile under strict mode by design, so it
// can't appear as live top-level code here. To see the permissive escape
// hatch actually run, save this next to this file as shadow_demo.ts:
//
//   function useFakeMath(): number {
//     let Math = { random: (): number => 42 }
//     return Math.random()
//   }
//   console.log(useFakeMath())
//
// then: `klainmain -globals=permissive shadow_demo.ts && ./shadow_demo`
// prints 42 (the user's own value, not a real random number) — versus the
// same file compiled with no flag at all (or explicit -globals=strict):
// `klainmain shadow_demo.ts` fails with:
//
//   klainmain: parse error: 2:3: 'Math' is a reserved built-in name —
//   pass -globals=permissive to allow shadowing it (matches real
//   JS/browser behavior)
//
// Constructor-style built-ins (Map/Set/Date/RegExp/URL/EventEmitter/... —
// "Tier 2") stay reserved under *either* mode, always — decided directly,
// see docs/tdd/TDD-00050.md's Context: the parser commits to the built-in
// meaning of `new Map()` from the bare token text alone, before any scope
// information exists, so there's no notion of "shadow it" to opt into.

// Ordinary, unshadowed use of a reserved name is completely unaffected by
// -globals=strict being the default — only *declaring* a colliding binding
// is ever rejected.
console.log(Math.floor(3.7))          // 3
console.log(parseInt('42') + 1)       // 43
console.log(process.argv.length >= 1) // true
