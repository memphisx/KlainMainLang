// Date — a thin wrapper over milliseconds-since-epoch.
//
// All calendar-field getters (getFullYear, getMonth, getDate, getDay,
// getHours, getMinutes, getSeconds, getMilliseconds) report UTC, not the
// local system timezone — deliberately, so output is deterministic on any
// machine/CI runner regardless of timezone. This deviates from real JS,
// where these getters default to local time.

// ── construct from an explicit timestamp ────────────────────────────────────
const epoch: Date = new Date(0)
console.log(epoch.getFullYear())       // 1970
console.log(epoch.getMonth())          // 0 (January — 0-indexed, like real JS)
console.log(epoch.getDate())           // 1
console.log(epoch.getDay())            // 4 (Thursday — 0=Sunday..6=Saturday)
console.log(epoch.getHours())          // 0
console.log(epoch.getMinutes())        // 0
console.log(epoch.getSeconds())        // 0
console.log(epoch.getMilliseconds())   // 0
console.log(epoch.getTime())           // 0
console.log(epoch.toISOString())       // 1970-01-01T00:00:00.000Z

// ── a later timestamp ────────────────────────────────────────────────────────
const later: Date = new Date(1700000000000)
console.log(later.toISOString())       // 2023-11-14T22:13:20.000Z
console.log(later.getFullYear())       // 2023
console.log(later.getMonth())          // 10 (November)
console.log(later.getDate())           // 14

// ── Date.now() / new Date() — current time ──────────────────────────────────
const nowMs: number = Date.now()
console.log(nowMs > 1700000000000)     // 1 (true, unless run before Nov 2023)

const now: Date = new Date()
console.log(now.getTime() > 1700000000000)   // 1

// ── Date works as a function parameter, return type, and object field ──────
function year(d: Date): number {
    return d.getFullYear()
}
console.log(year(epoch))   // 1970

function makeEpoch(): Date {
    return new Date(0)
}
const e2 = makeEpoch()
console.log(e2.getFullYear())   // 1970

interface LogEntry { message: string; when: Date }
const entry: LogEntry = { message: 'started', when: new Date(0) }
console.log(entry.message)             // started
console.log(entry.when.getFullYear())  // 1970

// ── Date.parse(string) — ISO 8601 UTC strings only ──────────────────────────
console.log(Date.parse('1970-01-01T00:00:00.000Z'))   // 0
console.log(Date.parse('2023-11-14T22:13:20.000Z'))   // 1700000000000
console.log(Date.parse('2023-11-14T22:13:20Z'))       // 1700000000000 (millis optional)
console.log(Date.parse('2023-11-14'))                 // 1699920000000 (date-only == UTC midnight)
console.log(Date.parse('not a date'))                 // -1 (unparseable: real JS returns NaN, but
                                                       //     this compiler's Date is a plain number
                                                       //     with no NaN, so -1 is the sentinel)

const parsed: Date = new Date(Date.parse('2023-11-14T22:13:20.000Z'))
console.log(parsed.toISOString())      // 2023-11-14T22:13:20.000Z

// ── new Date(aStringLiteral) — parses the string directly, no Date.parse() needed ──
const fromString: Date = new Date('2023-11-14T00:00:00.000Z')
console.log(fromString.getTime())        // 1699920000000
console.log(fromString.toISOString())    // 2023-11-14T00:00:00.000Z

const invalidFromString: Date = new Date('not a date')
console.log(invalidFromString.getTime()) // -1 (same unparseable sentinel as Date.parse)

// ── Date.parse(string) — with a "+HH:MM" / "-HH:MM" timezone offset ────────
// The offset is converted to UTC (subtracted for "+", added for "-"), with
// or without milliseconds present.
console.log(Date.parse('2023-11-14T22:13:20.000+02:00'))   // 1699992800000
console.log(Date.parse('2023-11-14T22:13:20.000-05:00'))   // 1700018000000
console.log(Date.parse('2023-11-14T22:13:20+02:00'))       // 1699992800000 (millis optional here too)
console.log(Date.parse('2023-11-14T22:13:20.000+00:00'))   // 1700000000000 (same as ...Z)
console.log(Date.parse('2023-11-14T22:13:20.000+05:30'))   // 1699980200000 (half-hour offsets work)

const withOffset: Date = new Date(Date.parse('2023-11-14T22:13:20.000+02:00'))
console.log(withOffset.toISOString())  // 2023-11-14T20:13:20.000Z

// ── Date setters — mutate in place, and return the new timestamp ───────────
// Setters require a named variable receiver (not a field access or a call
// result): this compiler's Date is a plain number, not a heap-allocated
// reference object like real JS's, so "mutate in place" only makes sense
// for a variable's own storage. Only the single-argument form of each
// setter is supported (no setFullYear(y, m, d)-style multi-arg overloads).
const editable: Date = new Date(0)
const newTimestamp: number = editable.setFullYear(2020)
console.log(newTimestamp)              // 1577836800000
console.log(editable.toISOString())    // 2020-01-01T00:00:00.000Z

editable.setMonth(5)
editable.setDate(15)
editable.setHours(12)
editable.setMinutes(30)
editable.setSeconds(45)
editable.setMilliseconds(500)
console.log(editable.toISOString())    // 2020-06-15T12:30:45.500Z

editable.setTime(0)
console.log(editable.toISOString())    // 1970-01-01T00:00:00.000Z

// Out-of-range values roll over into adjacent months/years, matching real JS
const rollover: Date = new Date(0)
rollover.setMonth(12)                  // month 12 (0-indexed) == January of next year
console.log(rollover.toISOString())    // 1971-01-01T00:00:00.000Z

// ── Date arithmetic — adding/subtracting durations ──────────────────────────
// This compiler's Date is a plain number (ms since epoch) under the hood, so
// +/- with a number operand does duration arithmetic directly, producing a
// new Date you can keep chaining methods on. This is a deliberate deviation
// from real JS, where `+` on a Date coerces it to a string (its default
// ToPrimitive hint) rather than adding numerically — treating it as plain
// numeric duration arithmetic is far more useful here. `Date - Date` (unlike
// `Date + Date`, which is rejected as meaningless) stays a real, meaningful
// operation matching real JS: the difference in milliseconds, as a number.
const start: Date = new Date(0)
const oneDayMs: number = 24 * 60 * 60 * 1000

const tomorrow: Date = start + oneDayMs
console.log(tomorrow.toISOString())        // 1970-01-02T00:00:00.000Z

const yesterday: Date = start - oneDayMs
console.log(yesterday.toISOString())       // 1969-12-31T00:00:00.000Z

const elapsedMs: number = tomorrow - start
console.log(elapsedMs)                      // 86400000

// Compound assignment mutates the variable in place, same as the setters
let clock: Date = new Date(0)
clock += oneDayMs
console.log(clock.toISOString())            // 1970-01-02T00:00:00.000Z
clock -= 60 * 60 * 1000
console.log(clock.toISOString())            // 1970-01-01T23:00:00.000Z

// Adding two Dates together, or compound-assigning a Date into a Date, is
// rejected at compile time rather than silently producing a nonsense value:
//   tomorrow + yesterday        // error: cannot add two Dates together
//   clock += tomorrow           // error: cannot compound-assign a Date with '+='

// ── Date formatting — toDateString() / toLocaleDateString() ────────────────
// Both are always UTC (like every other Date method here). toDateString
// matches real JS's fixed "Www Mon DD YYYY" shape exactly. toLocaleDateString
// mimics real JS's default (en-US, no explicit locale) "M/D/YYYY" output —
// full Intl-style locale support isn't implemented, and no locale argument
// is accepted; this is the one fixed format always produced.
const launch: Date = new Date(1700000000000)
console.log(launch.toDateString())          // Tue Nov 14 2023
console.log(launch.toLocaleDateString())    // 11/14/2023
console.log(epoch.toDateString())           // Thu Jan 01 1970
console.log(epoch.toLocaleDateString())     // 1/1/1970
