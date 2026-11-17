package llvm

import (
	"fmt"
	"os"
	"reflect"
	"sync/atomic"

	"KlainMainLang/ast"
)

// oracle_cache.go — the front oracle's answers kept across outermost
// queries. ExprType's memo lasts one outermost query; emission asks about
// the same nodes again and again (four in five asks of a Test262 file
// repeat one already answered), so each answer is kept, under the same key
// the memo uses (the node and the scope stack it was asked under), until
// the emitter state it can read changes.
//
// An answer reads the AST and, through the oracle's codegen callbacks, the
// emitter's scopes and module globals. Emission changes them:
//   - a function whose body reads `arguments` gains its rest parameter when
//     its signature is first built (setArgumentsRestParams), in place;
//   - registerModuleGlobals settles the globals' storage, which an answer
//     computed while it sizes them reads unsettled;
//   - a scope is pushed or popped, or a name defined in one. The memo key
//     names a scope stack by its depth and its outermost scope only, so two
//     sibling scopes (a callback's parameters, pushed to infer its result,
//     then its body's) share a key.
//
// Each change bumps the emitter's oracle generation (oracleChanged); an
// answer kept under an older one is not used, and a query a change ran
// during keeps nothing.
//
// KML_ORACLE_CACHE=off disables the cache; =verify recomputes every answer
// it would have served and reports one that differs, on stderr.
var oracleCacheMode = os.Getenv("KML_ORACLE_CACHE")

var oracleCacheBad atomic.Int64

// oracleChanged records an emitter change a kept oracle answer can read.
func (e *Emitter) oracleChanged() { e.oracleGen++ }

// setArgumentsRestParams is maybeAddArgumentsRestParams for a caller that
// stores the result back into the node.
func (e *Emitter) setArgumentsRestParams(params []ast.Param, body []ast.Statement) []ast.Param {
	out := maybeAddArgumentsRestParams(params, body)
	if len(out) != len(params) {
		e.oracleChanged()
	}
	return out
}

// gen is the emitter's oracle generation (0 for an oracle with no emitter).
func (r reprCtx) gen() int64 {
	if r.oracleGen == nil {
		return 0
	}
	return r.oracleGen()
}

// oracleCacheLookup is the kept answer for key, if there is a current one.
func (o frontOracle) oracleCacheLookup(key oracleKey) (oracleAnswer, bool) {
	if oracleCacheMode == "off" || o.q.persist == nil {
		return oracleAnswer{}, false
	}
	a, ok := o.q.persist[key]
	return a, ok && a.gen == o.r.gen()
}

// oracleCacheVerify reports a kept answer that differs from the one just
// computed (KML_ORACLE_CACHE=verify).
func (o frontOracle) oracleCacheVerify(key oracleKey, kept, fresh oracleAnswer) {
	if kept.ok == fresh.ok && reflect.DeepEqual(kept.t, fresh.t) {
		return
	}
	if oracleCacheBad.Add(1) <= 1000 {
		fmt.Fprintf(os.Stderr, "oracle cache: stale answer for %T at %v: kept ok=%v %s, now ok=%v %s\n", key.e, key.e.GetPos(), kept.ok, kept.t.IR+"/"+kept.t.ClassName, fresh.ok, fresh.t.IR+"/"+fresh.t.ClassName)
	}
}

// oracleCacheKeep keeps the answers of the outermost query that just ended,
// unless the emitter changed during it.
func (o frontOracle) oracleCacheKeep() {
	if oracleCacheMode == "off" {
		return
	}
	g := o.r.gen()
	if g != o.q.startGen {
		return
	}
	if o.q.persist == nil || o.q.persistGen != g {
		// Every kept answer is older than g: drop them rather than let the
		// map grow over a program's emission.
		o.q.persist = map[oracleKey]oracleAnswer{}
		o.q.persistGen = g
	}
	for k, v := range o.q.memo {
		if !v.ok {
			// No answer yet is not final: a class the program declares is
			// answerable once emission has reached its declaration.
			continue
		}
		v.gen = g
		o.q.persist[k] = v
	}
}

// OracleCacheStale is how many stale answers KML_ORACLE_CACHE=verify found.
func OracleCacheStale() int64 { return oracleCacheBad.Load() }
