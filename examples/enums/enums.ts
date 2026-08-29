// =============================================================================
// Enums
// =============================================================================

// ── Numeric enum (auto-increments from 0) ────────────────────────────────────
enum Direction {
  North,
  East,
  South,
  West,
}

console.log(Direction.North)  // 0
console.log(Direction.East)   // 1
console.log(Direction.South)  // 2
console.log(Direction.West)   // 3

// Enums work in switch statements
const dir = Direction.East
switch (dir) {
  case Direction.North: console.log('heading north'); break
  case Direction.East:  console.log('heading east');  break
  case Direction.South: console.log('heading south'); break
  case Direction.West:  console.log('heading west');  break
}

// ── Enum with explicit values ─────────────────────────────────────────────────
enum StatusCode {
  OK = 200,
  NotFound = 404,
  ServerError = 500,
}

console.log(StatusCode.OK)           // 200
console.log(StatusCode.NotFound)     // 404
console.log(StatusCode.ServerError)  // 500

function describe(code: number): string {
  if (code === StatusCode.OK) return 'success'
  if (code === StatusCode.NotFound) return 'not found'
  return 'error'
}

console.log(describe(StatusCode.OK))        // success
console.log(describe(StatusCode.NotFound))  // not found

// ── Enum with mixed values (auto-increment from last explicit) ────────────────
enum Priority {
  Low = 1,
  Medium,     // 2
  High,       // 3
  Critical = 10,
}

console.log(Priority.Low)      // 1
console.log(Priority.Medium)   // 2
console.log(Priority.High)     // 3
console.log(Priority.Critical) // 10

// ── const enum (inlined at compile time — no runtime object) ─────────────────
const enum Color {
  Red,
  Green,
  Blue,
}

const c = Color.Green
console.log(c)  // 1

// const enums are erased; values are substituted directly at compile time
if (Color.Red === 0) console.log('red is 0')  // red is 0

// ── String enum (members are string values) ──────────────────────────────────
enum Suit {
  Clubs = 'C',
  Diamonds = 'D',
  Hearts = 'H',
  Spades = 'S',
}

// A variable/param/field/array explicitly typed with the enum stores the
// member's string value.
const picked: Suit = Suit.Hearts
console.log(picked)                 // H
console.log(picked === Suit.Hearts) // true

function suitName(s: Suit): string {
  switch (s) {
    case Suit.Clubs:    return 'clubs'
    case Suit.Diamonds: return 'diamonds'
    case Suit.Hearts:   return 'hearts'
    default:            return 'spades'
  }
}
console.log(suitName(Suit.Spades))  // spades

const hand: Suit[] = [Suit.Clubs, Suit.Spades]
console.log(hand[1])                // S

// Bracket access and the numeric reverse mapping.
enum Size { S, M, L }
console.log(Size["M"]);   // 1
console.log(Size[2]);     // L
console.log(Size[42]);    // undefined
