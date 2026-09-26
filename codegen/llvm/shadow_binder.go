// shadow_binder.go — the new front end's side of the shadow comparison
// (TDD-00230 P2.1–P2.3): the binder answers name resolution, the checker the
// type questions, its types mapped to codegen's representation by reprOf.
package llvm

import (
	"KlainMainLang/ast"
	"KlainMainLang/binder"
	"KlainMainLang/checker"
	"KlainMainLang/lib"
	"KlainMainLang/options"
)

type frontOracle struct {
	b *binder.Binding
	c *checker.Checker
}

func init() {
	NewShadowOracle = func(prog *ast.Program, opts options.Options) ShadowOracle {
		// The lane's own rules: Annex B binding and the checker's js mode.
		libProgs, _ := lib.Programs() // a parse error in them fails the strict lane's check first
		b := binder.BindWith(prog, binder.Options{AnnexB: opts.CompatJS(), Lib: libProgs})
		return frontOracle{b: b, c: checker.NewWith(b, opts)}
	}
}

// ExprType answers inferExprType, which gives a variable or property read
// the representation of its storage: the checker's storage type, not the
// type the read narrows it to.
func (o frontOracle) ExprType(e ast.Expression) (Type, bool) {
	t := o.c.StorageTypeOf(e)
	if o.c.Unanswered(t) {
		return Type{}, false
	}
	return reprOf(t)
}

func (o frontOracle) AnnotationType(ta *ast.TypeAnnotation) (Type, bool) {
	n := ta.TypeNode()
	if n == nil {
		return Type{}, false
	}
	t := o.c.TypeFromNode(n, o.b.Module)
	if o.c.Unanswered(t) {
		return Type{}, false
	}
	if t.Flags&checker.StringLiteral != 0 {
		// An annotation keeps its string literal (a discriminant's tag).
		return Type{IR: "ptr", IsStrLiteral: true, LitValue: t.Value}, true
	}
	return reprOf(t)
}

func (frontOracle) GlobalType(*ast.VarDeclaration) (Type, bool, bool) { return Type{}, false, false }
func (frontOracle) ReturnType(*ast.BlockStatement) (Type, bool, bool) { return Type{}, false, false }

func (o frontOracle) Reference(id *ast.Identifier) (bool, bool) {
	sym, ok := o.b.Resolve(id)
	return sym != nil, ok
}

// reprOf is the representation codegen gives a checker type — the first
// cut of TDD-00230 P3.3's closed set: the primitives, the native widths and
// arrays of them. A type outside it has no answer yet.
func reprOf(t *checker.Type) (Type, bool) {
	switch {
	case t.Flags&(checker.Any|checker.Unknown) != 0:
		return TypeAny, true
	case t.Flags&(checker.Number|checker.NumberLiteral) != 0:
		return TypeF64, true
	case t.Flags&checker.Float32 != 0:
		return TypeF32, true
	case t.Flags&checker.Int != 0:
		return Type{IR: "i" + itoa(t.Bits), Signed: t.Signed}, true
	case t.Flags&(checker.String|checker.StringLiteral) != 0:
		return TypePtr, true
	case t.Flags&(checker.Boolean|checker.BooleanLiteral) != 0:
		return TypeBool, true
	case t.Flags&(checker.BigInt|checker.BigIntLiteral) != 0:
		return BigIntType(), true
	case t.Flags&checker.Void != 0:
		return TypeVoid, true
	case t.Flags&checker.Null != 0:
		return TypeNull, true
	case t.Flags&checker.Undefined != 0:
		return TypeUndefined, true
	case t.Flags&checker.Object != 0 && t.Kind == checker.Array:
		if e, ok := reprOf(t.Elem); ok {
			return ArrayOf(e), true
		}
	}
	return Type{}, false
}

func itoa(n int) string {
	switch n {
	case 8:
		return "8"
	case 16:
		return "16"
	case 32:
		return "32"
	}
	return "64"
}
