// fetch + wttr.in — a real-world REST API example: a console-friendly
// weather report for a city, over plain HTTP GET, no API key required.
//
// Needs real network access to run, same as fetch.ts in this same directory
// — `make examples` will report this one as FAILED on a machine with no
// network access, or if wttr.in itself is temporarily down, which is
// expected, not a bug in this compiler.
//
// Query options used: `0` asks for today's conditions only (no multi-day
// forecast), which keeps the response short and its shape stable; `T`
// disables ANSI terminal color codes, so the printed text stays plain.
//
// The weather itself isn't static, so only the first two lines are actually
// checked below: a fixed header that just echoes the city name back, then a
// blank line that always follows it. Everything after that (the ASCII art,
// temperature, wind, precipitation) is printed for a human to read, not
// asserted against, since it changes with the real weather.

const r = await fetch('https://wttr.in/Thessaloniki?0T')
console.log(r.status)  // 200
console.log(r.ok)      // 1 (true)

const body: string = r.text()
const lines: string[] = body.split('\n')
console.log(lines[0])          // Weather report: Thessaloniki
console.log(lines[1] === '')   // 1 (true) — a blank line always follows the header

console.log('--- current conditions (changes with the real weather, not asserted) ---')
console.log(body)
