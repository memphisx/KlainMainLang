package ast

import "reflect"

//go:generate go run ./gen

// Inspect walks the tree rooted at n in depth-first pre-order: fn is called on
// each node, and its children are walked only when fn returns true. A walker
// that must treat some kinds specially switches on those and returns false
// after handling them; every other kind is traversed by the generated
// ForEachChild, so a new node kind can never be silently skipped.
func Inspect(n Node, fn func(Node) bool) {
	if !present(n) || !fn(n) {
		return
	}
	ForEachChild(n, func(c Node) bool {
		Inspect(c, fn)
		return true
	})
}

// present reports whether an interface-typed child holds a node: nil, or a
// typed nil pointer stored in the interface, is an absent child.
func present(n Node) bool {
	if n == nil {
		return false
	}
	v := reflect.ValueOf(n)
	return v.Kind() != reflect.Pointer || !v.IsNil()
}
