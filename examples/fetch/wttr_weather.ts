// fetch + wttr.in — a real-world REST API example: a console-friendly
// weather report for a city, over plain HTTP GET, no API key required.
//
// Needs real network access to run, same as fetch.ts in this same directory.
// Unlike fetch.ts, this one talks to a single third-party service (wttr.in,
// not a dedicated test host like httpbin.org) that occasionally has a bad
// moment — a transient timeout or a momentary outage — with no fault on
// either end. That's a network-level failure, which this compiler surfaces
// as a catchable Error (see examples/fetch/fetch.ts's own "a network-level
// failure throws" section), so it's wrapped in try/catch here: a rare
// hiccup on wttr.in's side prints a message instead of crashing the whole
// example (and, by extension, failing `make examples`/CI over something
// outside this compiler's control). Still requires real network access —
// a machine with none will still fail, that's not something a try/catch
// around a single flaky host can paper over.
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

try {
    const r = await fetch('https://wttr.in/Thessaloniki?0T')
    console.log(r.status)  // 200
    console.log(r.ok)      // 1 (true)

    const body: string = r.text()
    const lines: string[] = body.split('\n')
    console.log(lines[0])          // Weather report: Thessaloniki
    console.log(lines[1] === '')   // 1 (true) — a blank line always follows the header

    console.log('--- current conditions (changes with the real weather, not asserted) ---')
    console.log(body)
} catch (e) {
    console.log('wttr.in was unreachable just now (transient network/service issue, not a compiler bug)')
}
