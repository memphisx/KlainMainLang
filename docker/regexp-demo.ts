// A tiny smoke-test program for docker/Dockerfile.regexp-test's --static
// build: proves a RegExp-using program links and runs correctly when
// statically linked (PCRE2 included) inside a `scratch` container — no
// libc, no dynamic linker, nothing else at all. See docs/tdd/TDD-00035.md
// Stage 6 / docs/adr/ADR-00120.md.
const r = /(\d+)-(\d+)/g
const s = "range: 12-34, 56-78 end"

const m = s.match(r)
if (m !== null) {
  console.log(m.length)
  console.log(m[0])
  console.log(m[1])
}

console.log(s.replace(/\d+/g, "N"))
console.log(s.split(/,\s*/).length)
console.log(s.search(/end/))

for (const each of s.matchAll(/(\d+)-(\d+)/g)) {
  console.log(each[1] + "/" + each[2])
}

console.log("statically-linked RegExp works inside scratch")
