// frontview.go — the read-only view of the checker that codegen sees
// (TDD-00230 P3.5): emission asks the front end questions and never
// mutates its state.
package llvm

import (
	"KlainMainLang/ast"
	"KlainMainLang/binder"
	"KlainMainLang/checker"
)

// frontView is what codegen may ask of the checker.
type frontView interface {
	AnyArgOverloadCall(e *ast.CallExpression) bool
	AnyType() *checker.Type
	Binding() *binder.Binding
	BodyReturnType(body *ast.BlockStatement) *checker.Type
	CallTypeArgs(e *ast.CallExpression) map[string]*checker.Type
	ChainNonOptional(e ast.Expression) *checker.Type
	ClassValueOf(cls *ast.ClassDeclaration) *checker.Type
	ConstLiteralKey(id *ast.Identifier) (value string, number, ok bool)
	ContextualSignature(fn ast.Expression) *checker.Type
	ExpandoMember(e *ast.MemberExpression) bool
	FieldType(cd *ast.ClassDeclaration, name string) *checker.Type
	GlobalInterface(name string) *checker.Type
	InProgram(n ast.Node) bool
	IteratedType(t *checker.Type) *checker.Type
	JSCallSiteParam(fn ast.Node, i int) *checker.Type
	JSConstructorFields(decl *ast.ClassDeclaration) []*checker.Property
	LibraryMethod(e *ast.MemberExpression) bool
	MaybeUndefined(t *checker.Type) bool
	MemberDecl(e *ast.MemberExpression) ast.Node
	MemberInLibrary(e *ast.MemberExpression) bool
	MemberOverloadDecls(e *ast.MemberExpression) []ast.Node
	ModuleVarType(v *ast.VarDeclaration) *checker.Type
	NamespaceMember(e *ast.MemberExpression) *binder.Symbol
	NonNullable(t *checker.Type) *checker.Type
	NonUndefined(t *checker.Type) *checker.Type
	OverloadIndex(e *ast.CallExpression, fn *checker.Type) int
	OverloadResult(e *ast.CallExpression, fn *checker.Type) *checker.Type
	ParentOf(n ast.Node) ast.Node
	PatternDefaultType(p ast.Param) *checker.Type
	PatternParamType(fd *ast.FunctionDeclaration, i int) *checker.Type
	PrimitiveMember(str bool, name string) (*checker.Type, ast.Node, []ast.Node)
	StorageTypeOf(e ast.Expression) *checker.Type
	ErasedSignature(t *checker.Type) *checker.Type
	StandsFor(syn, orig ast.Expression)
	ImplementationSignature(fd *ast.FunctionDeclaration) *checker.Type
	FunctionSignature(fd *ast.FunctionDeclaration) *checker.Type
	TupleAsArray(t *checker.Type) *checker.Type
	ImpliedPatternType(p ast.Param) *checker.Type
	WarmDecl(n ast.Node)
	TypeFromNode(n ast.TypeNode, scope *binder.Scope) *checker.Type
	TypeFromNodeWithParams(n ast.TypeNode, scope *binder.Scope, names []string) *checker.Type
	TypeOf(e ast.Expression) *checker.Type
	Unanswered(t *checker.Type) bool
	WithoutFalsy(t *checker.Type) *checker.Type
}

var _ frontView = (*checker.Checker)(nil)
