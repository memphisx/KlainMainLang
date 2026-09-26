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

// ── new Date(year, month, day?, hours?, minutes?, seconds?, ms?) ───────────
// Multi-argument calendar form. month is 0-indexed (0 = January), matching
// real JS's convention (and getMonth()'s) — NOT the 1-indexed month ISO date
// strings use. Fields are treated as UTC directly here, same as every other
// Date operation in this compiler (a deliberate, documented deviation from
// real JS, where this specific constructor form interprets its fields as
// local time — see docs/adr/ADR-00014.md).
const fromFields: Date = new Date(2023, 10, 14)
console.log(fromFields.toISOString())   // 2023-11-14T00:00:00.000Z
console.log(fromFields.getMonth())      // 10 (round-trips the 0-indexed value back)

const fromFieldsFull: Date = new Date(2023, 10, 14, 22, 13, 20, 500)
console.log(fromFieldsFull.toISOString())  // 2023-11-14T22:13:20.500Z

// Omitted trailing fields default like real JS: day defaults to 1, every
// field after that defaults to 0.
const fromFieldsPartial: Date = new Date(2023, 0)
console.log(fromFieldsPartial.toISOString())  // 2023-01-01T00:00:00.000Z

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
// A Date is a point in time; arithmetic goes through its millisecond count
// (getTime()), as TypeScript requires — a Date is not a number.
const start: Date = new Date(0)
const oneDayMs: number = 24 * 60 * 60 * 1000

const tomorrow: Date = new Date(start.getTime() + oneDayMs)
console.log(tomorrow.toISOString())        // 1970-01-02T00:00:00.000Z

const yesterday: Date = new Date(start.getTime() - oneDayMs)
console.log(yesterday.toISOString())       // 1969-12-31T00:00:00.000Z

const elapsedMs: number = tomorrow.getTime() - start.getTime()
console.log(elapsedMs)                      // 86400000

// Moving a Date in place goes through setTime.
let clock: Date = new Date(0)
clock.setTime(clock.getTime() + oneDayMs)
console.log(clock.toISOString())            // 1970-01-02T00:00:00.000Z
clock.setTime(clock.getTime() - 60 * 60 * 1000)
console.log(clock.toISOString())            // 1970-01-01T23:00:00.000Z

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
