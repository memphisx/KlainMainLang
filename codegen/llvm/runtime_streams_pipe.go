// runtime_streams_pipe.go — TDD-00097 Stage 3: the pipeTo state machine,
// TransformStream coupling, and tee(), all driven purely by promise reactions
// (no fiber). They live in streamssrc/streams.c (TDD-00240); this file holds
// the context layouts C checks its structs against and declares the entry
// points.
//
// %kml.pipe (80 B): src rstream · dst wstream · pipe promise · decodeFn
// ({i64,i64,i64}(ptr rec), compiler-emitted per chunk type) · flags
// (1 preventClose · 2 preventAbort · 4 preventCancel) · sigA / sigR (the
// AbortSignal's aborted flag and reason slot, or null) · pending chunk words ·
// the promise the current reaction fires on.
//
// %kml.tee (72 B): src · b1 · b2 · decodeFn · reading · canceled1 · canceled2 ·
// reason1 · reason2.
//
// %kml.ts (72 B): readable · writable · transClo · flushClo · parked flag ·
// parked chunk words · parkedProm (the in-flight writable write promise while
// a chunk waits for readable capacity) · closeProm (settled once flush +
// readable close complete).
package llvm

const (
	pipeCtxStructIR = "{ " + irPtr + ", " + irPtr + ", " + irPtr + ", " + irPtr + ", i64, " + irPtr + ", " + irPtr + ", i64, i64, " + irPtr + " }"
	teeCtxStructIR  = "{ " + irPtr + ", " + irPtr + ", " + irPtr + ", " + irPtr + ", i64, i64, i64, i64, i64, " + irPtr + " }"
	tsCtxStructIR   = "{ " + irPtr + ", " + irPtr + ", " + irPtr + ", " + irPtr + ", i64, i64, i64, " + irPtr + ", " + irPtr + " }"
)
