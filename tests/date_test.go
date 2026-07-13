package tests

import (
	"testing"
)

// --- Date (UTC only, not local time — see docs/adr/ADR-00014.md) ---

func TestE2EDateEpoch(t *testing.T) {
	assertOutput(t, `
const d: Date = new Date(0)
console.log(d.getFullYear())
console.log(d.getMonth())
console.log(d.getDate())
console.log(d.getDay())
console.log(d.getHours())
console.log(d.getMinutes())
console.log(d.getSeconds())
console.log(d.getMilliseconds())
console.log(d.getTime())
console.log(d.valueOf())
console.log(d.toISOString())
`, "1970\n0\n1\n4\n0\n0\n0\n0\n0\n0\n1970-01-01T00:00:00.000Z")
}

func TestE2EDateFromTimestamp(t *testing.T) {
	assertOutput(t, `
const d: Date = new Date(1700000000000)
console.log(d.toISOString())
console.log(d.getFullYear())
console.log(d.getMonth())
console.log(d.getDate())
`, "2023-11-14T22:13:20.000Z\n2023\n10\n14")
}

func TestE2EDateFromStringLiteral(t *testing.T) {
	assertOutput(t, `
const d: Date = new Date("2023-11-14T00:00:00.000Z")
console.log(d.getTime())
console.log(d.toISOString())
`, "1699920000000\n2023-11-14T00:00:00.000Z")
}

func TestE2EDateFromInvalidStringLiteral(t *testing.T) {
	assertOutput(t, `
const d: Date = new Date("not a date")
console.log(d.getTime())
`, "-1")
}

func TestE2EDateMultiArgConstructor(t *testing.T) {
	assertOutput(t, `
const d: Date = new Date(2023, 10, 14)
console.log(d.toISOString())
console.log(d.getFullYear())
console.log(d.getMonth())
console.log(d.getDate())
`, "2023-11-14T00:00:00.000Z\n2023\n10\n14")
}

func TestE2EDateMultiArgConstructorFullFields(t *testing.T) {
	assertOutput(t, `
const d: Date = new Date(2023, 10, 14, 22, 13, 20, 500)
console.log(d.toISOString())
`, "2023-11-14T22:13:20.500Z")
}

func TestE2EDateMultiArgConstructorDefaultsDay(t *testing.T) {
	assertOutput(t, `
const d: Date = new Date(2023, 0)
console.log(d.toISOString())
`, "2023-01-01T00:00:00.000Z")
}

func TestE2EDateNow(t *testing.T) {
	assertOutput(t, `
const now: number = Date.now()
console.log(now > 1700000000000)
const d: Date = new Date()
console.log(d.getTime() > 1700000000000)
`, "1\n1")
}

func TestE2EDateUntypedInference(t *testing.T) {
	// Date must be recognized when inferred from an untyped declaration, not
	// just via an explicit `: Date` annotation.
	assertOutput(t, `
const d = new Date(0)
console.log(d.getFullYear())

function epoch(): Date {
    return new Date(0)
}
const e = epoch()
console.log(e.getFullYear())
`, "1970\n1970")
}

func TestE2EDateAsFunctionParam(t *testing.T) {
	assertOutput(t, `
function year(d: Date): number {
    return d.getFullYear()
}
const d: Date = new Date(0)
console.log(year(d))
`, "1970")
}

func TestE2EDateAsObjectField(t *testing.T) {
	assertOutput(t, `
interface Event { name: string; when: Date }
const ev: Event = { name: 'launch', when: new Date(0) }
console.log(ev.name)
console.log(ev.when.getFullYear())
`, "launch\n1970")
}

func TestE2EDateParseFullISO(t *testing.T) {
	assertOutput(t, `
const ms: number = Date.parse("1970-01-01T00:00:00.000Z")
console.log(ms)
const ms2: number = Date.parse("2023-11-14T22:13:20.000Z")
console.log(ms2)
`, "0\n1700000000000")
}

func TestE2EDateParseWithoutMillis(t *testing.T) {
	assertOutput(t, `
console.log(Date.parse("2023-11-14T22:13:20Z"))
`, "1700000000000")
}

func TestE2EDateParseDateOnly(t *testing.T) {
	assertOutput(t, `
console.log(Date.parse("2023-11-14"))
`, "1699920000000")
}

func TestE2EDateParseInvalid(t *testing.T) {
	assertOutput(t, `
console.log(Date.parse("not a date"))
`, "-1")
}

func TestE2EDateParseRoundTrip(t *testing.T) {
	assertOutput(t, `
const d: Date = new Date(Date.parse("2023-11-14T22:13:20.000Z"))
console.log(d.toISOString())
console.log(d.getFullYear())
`, "2023-11-14T22:13:20.000Z\n2023")
}

func TestE2EDateParseOffsetWithMillis(t *testing.T) {
	assertOutput(t, `
console.log(Date.parse("2023-11-14T22:13:20.000+02:00"))
console.log(Date.parse("2023-11-14T22:13:20.000-05:00"))
`, "1699992800000\n1700018000000")
}

func TestE2EDateParseOffsetWithoutMillis(t *testing.T) {
	assertOutput(t, `
console.log(Date.parse("2023-11-14T22:13:20+02:00"))
console.log(Date.parse("2023-11-14T22:13:20-05:00"))
`, "1699992800000\n1700018000000")
}

func TestE2EDateParseOffsetZeroMatchesZ(t *testing.T) {
	assertOutput(t, `
console.log(Date.parse("2023-11-14T22:13:20.000+00:00"))
console.log(Date.parse("2023-11-14T22:13:20.000Z"))
`, "1700000000000\n1700000000000")
}

func TestE2EDateParseOffsetHalfHour(t *testing.T) {
	assertOutput(t, `
console.log(Date.parse("2023-11-14T22:13:20.000+05:30"))
`, "1699980200000")
}

func TestE2EDateParseOffsetNegativeZeroHour(t *testing.T) {
	// "-00:30": zero-hour part with a negative sign — a real edge case where
	// naively parsing the hour field as a signed integer loses the sign
	// (-0 == 0), silently misinterpreting this as a positive/zero offset.
	assertOutput(t, `
console.log(Date.parse("2023-11-14T22:13:20.000-00:30"))
`, "1700001800000")
}

func TestE2EDateParseOffsetRoundTrip(t *testing.T) {
	assertOutput(t, `
const d: Date = new Date(Date.parse("2023-11-14T22:13:20.000+02:00"))
console.log(d.toISOString())
`, "2023-11-14T20:13:20.000Z")
}

func TestE2EDateParseUntypedInference(t *testing.T) {
	assertOutput(t, `
const a = Date.parse("1970-01-01T00:00:00.000Z")
console.log(a)

function parseIt(s: string): number {
    return Date.parse(s)
}
console.log(parseIt("2023-11-14T22:13:20.000Z"))
`, "0\n1700000000000")
}

func TestE2EDateSetFullYear(t *testing.T) {
	assertOutput(t, `
const d: Date = new Date(0)
const returned: number = d.setFullYear(2020)
console.log(returned)
console.log(d.getFullYear())
console.log(d.toISOString())
`, "1577836800000\n2020\n2020-01-01T00:00:00.000Z")
}

func TestE2EDateSetAllFields(t *testing.T) {
	assertOutput(t, `
const d: Date = new Date(0)
d.setFullYear(2020)
d.setMonth(5)
d.setDate(15)
d.setHours(12)
d.setMinutes(30)
d.setSeconds(45)
d.setMilliseconds(500)
console.log(d.toISOString())
`, "2020-06-15T12:30:45.500Z")
}

func TestE2EDateSetTime(t *testing.T) {
	assertOutput(t, `
const d: Date = new Date(0)
d.setFullYear(2020)
const t: number = d.setTime(0)
console.log(t)
console.log(d.toISOString())
`, "0\n1970-01-01T00:00:00.000Z")
}

func TestE2EDateSetterOverflowRollsOverLikeRealJS(t *testing.T) {
	assertOutput(t, `
const d1: Date = new Date(0)
d1.setMonth(12)
console.log(d1.toISOString())

const d2: Date = new Date(0)
d2.setDate(32)
console.log(d2.toISOString())
`, "1971-01-01T00:00:00.000Z\n1970-02-01T00:00:00.000Z")
}

func TestE2EDateSetterOnObjectFieldRejected(t *testing.T) {
	_, err := parseAndCompile(`
interface Box { when: Date }
const b: Box = { when: new Date(0) }
b.when.setFullYear(2020)
`)
	if err == nil {
		t.Fatal("expected a compile error for a Date setter on a non-identifier receiver, got none")
	}
}

func TestE2EDateSetterMultiArgRejected(t *testing.T) {
	_, err := parseAndCompile(`
const d: Date = new Date(0)
d.setFullYear(2020, 5)
`)
	if err == nil {
		t.Fatal("expected a compile error for a multi-argument Date setter overload, got none")
	}
}

func TestE2EDateMinusDateGivesMillisDifference(t *testing.T) {
	assertOutput(t, `
const d1: Date = new Date(1000)
const d2: Date = new Date(3000)
const diff: number = d1 - d2
console.log(diff)
console.log(d2 - d1)
`, "-2000\n2000")
}

func TestE2EDatePlusNumberGivesNewDate(t *testing.T) {
	assertOutput(t, `
const d1: Date = new Date(1000)
console.log((d1 + 500).getTime())
console.log((500 + d1).getTime())
console.log((d1 + 86400000).toISOString())
`, "1500\n1500\n1970-01-02T00:00:01.000Z")
}

func TestE2EDateMinusNumberGivesNewDate(t *testing.T) {
	assertOutput(t, `
const d1: Date = new Date(10000)
console.log((d1 - 500).getTime())
console.log((d1 - 10000).toISOString())
`, "9500\n1970-01-01T00:00:00.000Z")
}

func TestE2EDateArithmeticUntypedInference(t *testing.T) {
	assertOutput(t, `
const d1: Date = new Date(0)
const later = d1 + 86400000
console.log(later.getFullYear())
console.log(later.toISOString())

const diff = later - d1
console.log(diff)
`, "1970\n1970-01-02T00:00:00.000Z\n86400000")
}

func TestE2EDateCompoundAssignAddsDuration(t *testing.T) {
	assertOutput(t, `
let d: Date = new Date(0)
d += 86400000
console.log(d.toISOString())
d -= 3600000
console.log(d.toISOString())
`, "1970-01-02T00:00:00.000Z\n1970-01-01T23:00:00.000Z")
}

func TestE2EDatePlusDateRejected(t *testing.T) {
	_, err := parseAndCompile(`
const d1: Date = new Date(1000)
const d2: Date = new Date(3000)
console.log(d1 + d2)
`)
	if err == nil {
		t.Fatal("expected a compile error for adding two Dates together, got none")
	}
}

func TestE2ENumberMinusDateRejected(t *testing.T) {
	_, err := parseAndCompile(`
const d1: Date = new Date(1000)
console.log(500 - d1)
`)
	if err == nil {
		t.Fatal("expected a compile error for subtracting a Date from a number, got none")
	}
}

func TestE2EDateCompoundAssignDateRejected(t *testing.T) {
	_, err := parseAndCompile(`
let d1: Date = new Date(1000)
const d2: Date = new Date(3000)
d1 += d2
`)
	if err == nil {
		t.Fatal("expected a compile error for compound-assigning a Date into a Date, got none")
	}
}

func TestE2EDateToDateString(t *testing.T) {
	assertOutput(t, `
const d1: Date = new Date(0)
console.log(d1.toDateString())
const d2: Date = new Date(1700000000000)
console.log(d2.toDateString())
`, "Thu Jan 01 1970\nTue Nov 14 2023")
}

func TestE2EDateToLocaleDateString(t *testing.T) {
	assertOutput(t, `
const d1: Date = new Date(0)
console.log(d1.toLocaleDateString())
const d2: Date = new Date(1700000000000)
console.log(d2.toLocaleDateString())
`, "1/1/1970\n11/14/2023")
}

func TestE2EDateFormattingUntypedInferenceAndFunctionChain(t *testing.T) {
	assertOutput(t, `
const d: Date = new Date(0)
const s = d.toDateString()
console.log(s)

function fmt(x: Date): string {
    return x.toLocaleDateString()
}
console.log(fmt(d))
`, "Thu Jan 01 1970\n1/1/1970")
}
