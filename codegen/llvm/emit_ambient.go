package llvm

import (
	"sync"

	"KlainMainLang/ast"
	"KlainMainLang/lib"
)

// ambientInterfaceType is the type an interface or type alias the builtin
// declarations (lib/*.d.ts) declare at top level, registered on first use
// as a program interface is: a parameter typed `RequestInit` holds the
// object passed, read through the checked view (TDD-00233).
func (e *Emitter) ambientInterfaceType(name string) (Type, bool) {
	if ty, ok := e.interfaces[name]; ok {
		return ty, true
	}
	if e.ambientMiss[name] {
		return Type{}, false
	}
	if alias := ambientAliasDecl(name); alias != nil && len(alias.TypeParams) == 0 && alias.Type != nil {
		// A type alias (`type BodyInit = …`): what it names.
		ph := ObjectType(nil)
		ph.RefName = name
		e.interfaces[name] = ph
		ty := e.annotationType(alias.Type)
		e.interfaces[name] = ty
		return ty, true
	}
	decl := ambientInterfaceDecl(name)
	if decl == nil || decl.IndexSig != nil || decl.CallSig != nil || len(decl.TypeParams) > 0 {
		if e.ambientMiss == nil {
			e.ambientMiss = map[string]bool{}
		}
		e.ambientMiss[name] = true
		return Type{}, false
	}
	// A placeholder breaks a self-reference while the members resolve.
	ph := ObjectType(nil)
	ph.RefName = name
	e.interfaces[name] = ph
	if e.ambientBusy == nil {
		e.ambientBusy = map[string]bool{}
	}
	e.ambientBusy[name] = true
	defer delete(e.ambientBusy, name)
	fields := make([]Field, 0, len(decl.Fields))
	for _, f := range decl.Fields {
		fty := e.annotationType(f.Type)
		if f.Optional {
			fty = optionalFieldType(fty)
		}
		fields = append(fields, Field{Name: f.Name, Ty: fty, Optional: f.Optional})
	}
	fields = e.withMethodFields(fields, decl.Methods, e.annotationType)
	ty := ObjectType(fields)
	ty.RefName = name
	e.interfaces[name] = ty
	return ty, true
}

// ambientBuilding says whether the builtin interface name's members are
// resolving, its placeholder standing for it meanwhile.
func (e *Emitter) ambientBuilding(name string) bool { return e.ambientBusy[name] }

// ambientInterfaceDecl finds a top-level interface named name in the
// builtin declarations (the first, when several merge).
func ambientInterfaceDecl(name string) *ast.InterfaceDeclaration {
	return ambientDecls().ifaces[name]
}

// ambientAliasDecl finds a top-level type alias named name in the builtin
// declarations.
func ambientAliasDecl(name string) *ast.TypeAliasDeclaration {
	return ambientDecls().aliases[name]
}

// ambientIndex is the builtin declarations' top-level interfaces and type
// aliases by name, built once: the declarations never change in a process.
type ambientIndex struct {
	ifaces  map[string]*ast.InterfaceDeclaration
	aliases map[string]*ast.TypeAliasDeclaration
}

var ambientDecls = sync.OnceValue(func() ambientIndex {
	ix := ambientIndex{ifaces: map[string]*ast.InterfaceDeclaration{}, aliases: map[string]*ast.TypeAliasDeclaration{}}
	progs, err := lib.Programs()
	if err != nil {
		return ix
	}
	for _, p := range progs {
		for _, st := range p.Body {
			switch d := st.(type) {
			case *ast.InterfaceDeclaration:
				if _, seen := ix.ifaces[d.Name]; !seen {
					ix.ifaces[d.Name] = d
				}
			case *ast.TypeAliasDeclaration:
				if _, seen := ix.aliases[d.Name]; !seen {
					ix.aliases[d.Name] = d
				}
			}
		}
	}
	return ix
})

// ambientDataShape reports a builtin generic interface that is only a data
// shape (`IteratorYieldResult<T>`), instantiable as a program's own
// generic is.
func ambientDataShape(name string) bool {
	decl := ambientInterfaceDecl(name)
	return decl != nil && len(decl.TypeParams) > 0 && decl.IndexSig == nil && decl.CallSig == nil &&
		len(decl.Methods) == 0 && len(decl.Extends) == 0 && len(decl.Fields) > 0
}
