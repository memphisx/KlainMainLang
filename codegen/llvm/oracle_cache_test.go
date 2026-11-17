package llvm

import (
	"testing"

	"KlainMainLang/ast"
)

// TestOracleCacheInvalidation: an answer kept before an emitter change (an
// in-place parameter rewrite, a scope pushed, a name defined) is not served
// after it; a query a change ran during keeps nothing; an unanswered ask
// is never kept.
func TestOracleCacheInvalidation(t *testing.T) {
	if oracleCacheMode == "off" {
		t.Skip("KML_ORACLE_CACHE=off")
	}
	e := NewEmitter()
	key := oracleKey{e: &ast.Identifier{Name: "x"}}
	o := frontOracle{q: &oracleQuery{}, r: reprCtx{oracleGen: func() int64 { return e.oracleGen }}}
	keep := func() {
		o.q.startGen = o.r.gen()
		o.q.memo = map[oracleKey]oracleAnswer{
			key:                             {ok: true},
			{e: &ast.Identifier{Name: "y"}}: {ok: false},
		}
		o.oracleCacheKeep()
	}

	keep()
	if _, hit := o.oracleCacheLookup(key); !hit {
		t.Fatal("answer kept with the emitter unchanged is not served")
	}
	if len(o.q.persist) != 1 {
		t.Fatalf("kept %d answers, want 1 (an unanswered ask is not kept)", len(o.q.persist))
	}

	// A body reading `arguments` gains its rest parameter: a rewrite.
	body := []ast.Statement{&ast.ExpressionStatement{Expr: &ast.Identifier{Name: "arguments"}}}
	if out := e.setArgumentsRestParams(nil, body); len(out) != 1 {
		t.Fatalf("rest parameter not added: %d params", len(out))
	}
	if _, hit := o.oracleCacheLookup(key); hit {
		t.Fatal("answer kept before a parameter rewrite is served after it")
	}

	keep()
	e.pushScope()
	if _, hit := o.oracleCacheLookup(key); hit {
		t.Fatal("answer kept before a scope push is served after it")
	}
	keep()
	e.define("z", Symbol{Ty: TypeI64})
	if _, hit := o.oracleCacheLookup(key); hit {
		t.Fatal("answer kept before a define is served after it")
	}

	// A query a change ran during keeps nothing.
	o.q.persist = nil
	o.q.startGen = o.r.gen()
	e.popScope()
	o.q.memo = map[oracleKey]oracleAnswer{key: {ok: true}}
	o.oracleCacheKeep()
	if len(o.q.persist) != 0 {
		t.Fatal("a query a change ran during kept its answers")
	}
}
