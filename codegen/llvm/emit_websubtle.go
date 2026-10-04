package llvm

import "KlainMainLang/ast"

// crypto.subtle is lib/node/internal_crypto_webcrypto.ts's SubtleCrypto
// (Node's lib/internal/crypto/webcrypto.js over node:crypto): `crypto.subtle`
// and a `const { subtle } = crypto` alias read as its one instance.

// tsSubtle reports whether crypto.subtle is the TypeScript SubtleCrypto
// (lib/node/internal_crypto_webcrypto.ts), linked into this program.
func (e *Emitter) tsSubtle() bool {
	_, ok := e.libExports["internal_crypto_webcrypto:_kmlSubtle"]
	return ok
}

// tsSubtleRef is `crypto.subtle` (or `globalThis.crypto.subtle`) as the call
// of the module's _kmlSubtle, when the TypeScript SubtleCrypto is linked.
func (e *Emitter) tsSubtleRef(mem *ast.MemberExpression) (*ast.CallExpression, bool) {
	m, ok := e.libExports["internal_crypto_webcrypto:_kmlSubtle"]
	if !ok || mem.Property != "subtle" || mem.Optional {
		return nil, false
	}
	isGlobal := false
	if inner, ok := mem.Object.(*ast.MemberExpression); ok {
		isGlobal, _ = cryptoGlobalExpr(inner)
	} else if id, ok := mem.Object.(*ast.Identifier); ok {
		isGlobal = id.Name == "crypto" && !e.isShadowedByLocal("crypto")
	}
	if !isGlobal {
		return nil, false
	}
	return ast.NewCallExpression(ast.NewIdentifier(m, mem.GetPos()), nil, mem.GetPos()), true
}

// tsSubtleAlias is a `const { subtle } = crypto` alias read as the call of
// the module's _kmlSubtle, when the TypeScript SubtleCrypto is linked and
// no local shadows the name.
func (e *Emitter) tsSubtleAlias(id *ast.Identifier) (*ast.CallExpression, bool) {
	m, ok := e.libExports["internal_crypto_webcrypto:_kmlSubtle"]
	if !ok || !e.cryptoSubtleAliases[id.Name] {
		return nil, false
	}
	if _, bound := e.lookup(id.Name); bound {
		return nil, false
	}
	return ast.NewCallExpression(ast.NewIdentifier(m, id.GetPos()), nil, id.GetPos()), true
}
