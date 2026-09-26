package diag

// The message table. A message whose code is below FirstOwnCode reports the
// condition TypeScript reports under that code (the wording is this
// compiler's own). Messages are grouped by the phase that reports them.

func syntax(code int, text string) *Message {
	return &Message{Code: code, Kind: SyntaxError, Phase: PhaseParse, Text: text}
}

// unsupported is a construct valid in TypeScript that this compiler does not
// implement.
func unsupported(code int, text string) *Message {
	return &Message{Code: code, Kind: Unsupported, Phase: PhaseParse, Text: text}
}

// annotation is a misuse of one of this compiler's own JSDoc annotations.
func annotation(code int, text string) *Message {
	return &Message{Code: code, Kind: Unsupported, Phase: PhaseParse, Text: text}
}

// Scanner.
var (
	UnexpectedCharacter       = syntax(1127, "unexpected character %q")
	NumericSeparatorPlacement = syntax(6188, "numeric separator '_' must be between two digits")
	NumericSeparatorLegacy    = syntax(6188, "a numeric separator '_' is not allowed in a legacy octal or non-octal-decimal literal")
	BigIntDecimalPoint        = syntax(1353, "a BigInt literal cannot contain a decimal point")
	BigIntExponent            = syntax(1352, "a BigInt literal cannot contain an exponent")
	BigIntLegacyOctal         = syntax(1121, "a BigInt literal cannot use a legacy-octal / non-octal-decimal form")
	UnterminatedString        = syntax(1002, "unterminated string literal")
	InvalidHexEscape          = syntax(1125, "invalid hexadecimal escape sequence")
	InvalidUnicodeEscape      = syntax(1125, "invalid Unicode escape sequence")
	CodePointOutOfRange       = syntax(1198, "Unicode code point out of range")
	UnterminatedRegExp        = syntax(1161, "unterminated regular expression literal")
	UnterminatedTemplate      = syntax(1160, "unterminated template literal")
)

// Parser: tokens the grammar requires.
var (
	// ExpectedToken is the generic "'X' expected" (TS1005).
	ExpectedToken         = syntax(1005, "expected %s, got %s")
	ExpectedSemicolon     = syntax(1005, "expected ';', got %s")
	ExpectedColon         = syntax(1005, "expected :, got %s")
	ExpectedArrow         = syntax(1005, "expected =>, got %s")
	ExpectedCloseAngle    = syntax(1005, "expected '>' to close %s")
	ExpectedFromImport    = syntax(1005, "expected 'from' after import specifier list, got %s")
	ExpectedFromExport    = syntax(1005, "expected 'from' after export specifier list, got %s")
	ExpectedParamName     = syntax(1138, "expected a parameter name, got %s")
	PrivateNameUndeclared = syntax(18016, "the private name '%s' is not declared in an enclosing class")
	DeletePrivateName     = syntax(18011, "the operand of a 'delete' operator cannot be a private identifier")
	ModuleDeclNotTopLevel = syntax(1233, "an import or export declaration can only be used at the top level of a module")
	CoverInitializedName  = syntax(1312, "a shorthand property with a default ('{ x = 1 }') is only valid in a destructuring assignment")
	ReservedWordIdent     = syntax(1359, "'%s' is a reserved word that cannot be used here")
	StrictDeleteName      = syntax(1102, "'delete' cannot be called on an identifier in strict mode")
	UndefinedLabel        = syntax(1116, "a '%s' statement can only jump to a label of an enclosing statement ('%s' is not one)")
	YieldOutsideGenerator = syntax(1163, "a 'yield' expression is only allowed in a generator body")
	InvalidAssignTarget   = syntax(2364, "the left-hand side of an assignment expression must be a variable or a property access")
	InvalidUpdateTarget   = syntax(2357, "the operand of an increment or decrement operator must be a variable or a property access")
	ExpectedAsNamespace   = syntax(1005, "expected 'as' after '*' in namespace import, got %s")
	ExpectedOfDestructure = syntax(1005, "expected 'of' after a for-of destructuring pattern, got %s")
	ExpectedImportDotOr   = syntax(1005, "expected '.' or '(' after 'import' in an expression, got %s")
	ExpectedCaseOrDefault = syntax(1130, "expected 'case' or 'default' in switch")
	ExpectedCatchFinally  = syntax(1472, "try statement requires at least a catch or finally clause")
	ExpectedTemplateCont  = syntax(1160, "expected template continuation, got %s")
	ExpectedImportMeta    = syntax(17012, "expected 'meta' after 'import.'")
	ForAwaitNotForIn      = syntax(1005, "'for await' requires a for-of loop, not for-in")
	ForAwaitNotForOf      = syntax(1005, "'for await' requires a for-of loop over an async iterable")
	MethodMissingParens   = syntax(1005, "'%s %s' must be a method (missing '()')")
	ModifierNeedsMethod   = syntax(1005, "'%s' is not a method (an async/generator modifier requires a '()' method body)")
)

// Parser: an element of the wrong kind.
var (
	UnexpectedTokenInExpr = syntax(1109, "unexpected token %s in expression")
	ExpectedConstructor   = syntax(1109, "expected constructor name after 'new'")
	ExpectedTypeName      = syntax(1110, "expected type name, got %s")
	ExpectedPropertyName  = syntax(1131, "expected property name, got %s")
	ExpectedMemberName    = syntax(1131, "expected member name, got %s")
	ExpectedMethodName    = syntax(1068, "expected method name, got %s")
	ExportNotDeclaration  = syntax(1128, "'export' can only precede a function, variable, interface, type alias, enum, or class declaration")
	ExpectedParamBeforeIs = syntax(1003, "expected a parameter name before 'is'")
	ExpectedFunctionType  = syntax(1005, "expected a function type after a type parameter list")
	ExpectedCtorArrow     = syntax(1005, "expected => in a constructor type")
	ExpectedCloseArray    = syntax(1005, "expected ] in array type annotation")
	ExpectedCloseIndex    = syntax(1005, "expected ] in indexed access type")
	ExpectedMapComma      = syntax(1005, "expected ',' in Map<K,V>")
	MalformedTemplateType = syntax(1160, "malformed template literal type (expected `${...}` continuation, got %s)")
)

// Parser: early errors.
var (
	LineBreakBeforeArrow      = syntax(1200, "line break not allowed before '=>'")
	LineBreakAfterThrow       = syntax(1142, "line break not allowed after 'throw'")
	ConstMustBeInitialized    = syntax(1155, "'const' declaration '%s' must be initialized")
	DuplicateParameter        = syntax(2300, "duplicate parameter name '%s'")
	StrictBindingName         = syntax(1100, "'%s' cannot be used as a binding name in strict mode")
	StrictParameterName       = syntax(1100, "'%s' cannot be a parameter name in strict mode")
	StrictClassParameterName  = syntax(1100, "'%s' cannot be a parameter name in a class method (strict mode)")
	StrictNonSimpleParams     = syntax(1347, "a strict-mode function cannot have a non-simple parameter list")
	ExponentUnaryAmbiguous    = syntax(17006, "unary operator '%s' before '**' is ambiguous — parenthesize as '(%s x) ** y' or '%s(x ** y)'")
	RestParamLastFunctionType = syntax(1014, "a rest parameter must be last in a function type")
	RestParamLastMethodSig    = syntax(1014, "a rest parameter must be last in a method signature")
	RestElementLast           = syntax(2462, "a rest element must be the last property in an object pattern, got %s")
	ConstructorAsyncGenerator = syntax(1089, "a constructor cannot be async or a generator")
	ConstructorAccessor       = syntax(1341, "a constructor cannot be a getter/setter")
	DuplicateConstructor      = syntax(2392, "class '%s' declares more than one constructor")
	StaticPrototype           = syntax(2699, "a static class member cannot be named 'prototype'")
	HashConstructor           = syntax(18012, "'#constructor' is a reserved class member name")
	AccessibilityOnPrivate    = syntax(18010, "an accessibility modifier cannot be used with a private identifier")
	ParamPropertyOutsideCtor  = syntax(2369, "a parameter property (public/private/protected/readonly) is only allowed in a class constructor")
	ParamPropertyPattern      = syntax(1317, "a parameter property cannot be a rest or destructured parameter")
	AccessorOnMethod          = syntax(1275, "'accessor' is only valid on a class field, not a method")
	DecoratorsNotValid        = syntax(1206, "decorators can only be applied to a class declaration or its members")
	DecoratorOnStaticBlock    = syntax(1206, "decorators are not allowed on a static initializer block")
	OverloadNoImplementation  = syntax(2391, "overload signature for '%s' must be followed by another overload signature or its implementation")
	OverloadClassNoImpl       = syntax(2391, "overload signature for '%s' in class '%s' has no implementation")
	OverloadWrongName         = syntax(2389, "expected the implementation of overloaded function '%s', got 'function %s'")
	ImportRequireAssignment   = syntax(1202, "`import %s = require(...)` is not supported — use an ES import declaration instead")
	RedeclaredBlockScoped     = syntax(2451, "identifier '%s' has already been declared")
	DuplicateIdentifier       = syntax(2300, "identifier '%s' has already been declared")
	UsedBeforeDeclaration     = &Message{Code: 2448, Kind: ReferenceError, Phase: PhaseCheck, Text: "cannot access '%s' before initialization"}
	UsedBeforeAssigned        = &Message{Code: 2454, Kind: Unsupported, Phase: PhaseCheck, Text: "variable '%s' is used before being assigned"}
	// The checker's type errors (TDD-00230 P2.7).
	PropertyNotIndex      = &Message{Code: 2411, Kind: TypeScriptError, Phase: PhaseCheck, Text: "property '%s' of type '%s' is not assignable to 'string' index type '%s'"}
	NotAssignable         = &Message{Code: 2322, Kind: TypeScriptError, Phase: PhaseCheck, Text: "type '%s' is not assignable to type '%s'"}
	ClassNotCallable      = &Message{Code: 2348, Kind: TypeScriptError, Phase: PhaseCheck, Text: "value of type '%s' is not callable; did you mean to include 'new'?"}
	ObjectToFewTypes      = &Message{Code: 2696, Kind: TypeScriptError, Phase: PhaseCheck, Text: "the 'Object' type is assignable to very few other types; did you mean to use the 'any' type instead?"}
	ArgNotAssignable      = &Message{Code: 2345, Kind: TypeScriptError, Phase: PhaseCheck, Text: "argument of type '%s' is not assignable to parameter of type '%s'"}
	ArgCount              = &Message{Code: 2554, Kind: TypeScriptError, Phase: PhaseCheck, Text: "expected %s arguments, but got %d"}
	ArgCountAtLeast       = &Message{Code: 2555, Kind: TypeScriptError, Phase: PhaseCheck, Text: "expected at least %d arguments, but got %d"}
	PropertyNotExist      = &Message{Code: 2339, Kind: TypeScriptError, Phase: PhaseCheck, Text: "property '%s' does not exist on type '%s'"}
	AssignToConst         = &Message{Code: 2588, Kind: TypeScriptError, Phase: PhaseCheck, Text: "cannot assign to '%s' because it is a constant"}
	AssignToFunction      = &Message{Code: 2630, Kind: TypeScriptError, Phase: PhaseCheck, Text: "cannot assign to '%s' because it is a function"}
	AssignToClass         = &Message{Code: 2629, Kind: TypeScriptError, Phase: PhaseCheck, Text: "cannot assign to '%s' because it is a class"}
	AssignToEnum          = &Message{Code: 2628, Kind: TypeScriptError, Phase: PhaseCheck, Text: "cannot assign to '%s' because it is an enum"}
	ExcessProperty        = &Message{Code: 2353, Kind: TypeScriptError, Phase: PhaseCheck, Text: "object literal may only specify known properties, and '%s' does not exist in type '%s'"}
	PropertyMissing       = &Message{Code: 2741, Kind: TypeScriptError, Phase: PhaseCheck, Text: "property '%s' is missing in type '%s' but required in type '%s'"}
	PropertiesMissing     = &Message{Code: 2739, Kind: TypeScriptError, Phase: PhaseCheck, Text: "type '%s' is missing the following properties from type '%s': %s"}
	PropertiesMissingMore = &Message{Code: 2740, Kind: TypeScriptError, Phase: PhaseCheck, Text: "type '%s' is missing the following properties from type '%s': %s, and %d more"}
	ArithmeticLeft        = &Message{Code: 2362, Kind: TypeScriptError, Phase: PhaseCheck, Text: "the left-hand side of an arithmetic operation must be of type 'any', 'number', 'bigint' or an enum type"}
	ArithmeticRight       = &Message{Code: 2363, Kind: TypeScriptError, Phase: PhaseCheck, Text: "the right-hand side of an arithmetic operation must be of type 'any', 'number', 'bigint' or an enum type"}
	OperatorNotApplied    = &Message{Code: 2365, Kind: TypeScriptError, Phase: PhaseCheck, Text: "operator '%s' cannot be applied to types '%s' and '%s'"}
	PossiblyNull          = &Message{Code: 18047, Kind: TypeScriptError, Phase: PhaseCheck, Text: "'%s' is possibly 'null'"}
	PossiblyUndefined     = &Message{Code: 18048, Kind: TypeScriptError, Phase: PhaseCheck, Text: "'%s' is possibly 'undefined'"}
	PossiblyNullish       = &Message{Code: 18049, Kind: TypeScriptError, Phase: PhaseCheck, Text: "'%s' is possibly 'null' or 'undefined'"}
	ObjectPossiblyNull    = &Message{Code: 2531, Kind: TypeScriptError, Phase: PhaseCheck, Text: "object is possibly 'null'"}
	ObjectPossiblyUndef   = &Message{Code: 2532, Kind: TypeScriptError, Phase: PhaseCheck, Text: "object is possibly 'undefined'"}
	ObjectPossiblyNullish = &Message{Code: 2533, Kind: TypeScriptError, Phase: PhaseCheck, Text: "object is possibly 'null' or 'undefined'"}
	ValueCannotBeUsed     = &Message{Code: 18050, Kind: TypeScriptError, Phase: PhaseCheck, Text: "the value '%s' cannot be used here"}
	IsOfTypeUnknown       = &Message{Code: 18046, Kind: TypeScriptError, Phase: PhaseCheck, Text: "'%s' is of type 'unknown'"}
	ObjectOfTypeUnknown   = &Message{Code: 2571, Kind: TypeScriptError, Phase: PhaseCheck, Text: "object is of type 'unknown'"}
	InvokePossiblyNull    = &Message{Code: 2721, Kind: TypeScriptError, Phase: PhaseCheck, Text: "cannot invoke an object which is possibly 'null'"}
	InvokePossiblyUndef   = &Message{Code: 2722, Kind: TypeScriptError, Phase: PhaseCheck, Text: "cannot invoke an object which is possibly 'undefined'"}
	InvokePossiblyNullish = &Message{Code: 2723, Kind: TypeScriptError, Phase: PhaseCheck, Text: "cannot invoke an object which is possibly 'null' or 'undefined'"}
	PrivateMember         = &Message{Code: 2341, Kind: TypeScriptError, Phase: PhaseCheck, Text: "property '%s' is private and only accessible within class '%s'"}
	ProtectedMember       = &Message{Code: 2445, Kind: TypeScriptError, Phase: PhaseCheck, Text: "property '%s' is protected and only accessible within class '%s' and its subclasses"}
	ProtectedReceiver     = &Message{Code: 2446, Kind: TypeScriptError, Phase: PhaseCheck, Text: "property '%s' is protected and only accessible through an instance of class '%s'. This is an instance of class '%s'"}
	PrivateConstructor    = &Message{Code: 2673, Kind: TypeScriptError, Phase: PhaseCheck, Text: "constructor of class '%s' is private and only accessible within the class declaration"}
	ProtectedConstructor  = &Message{Code: 2674, Kind: TypeScriptError, Phase: PhaseCheck, Text: "constructor of class '%s' is protected and only accessible within the class declaration"}
	ThisContext           = &Message{Code: 2684, Kind: TypeScriptError, Phase: PhaseCheck, Text: "the 'this' context of type '%s' is not assignable to method's 'this' of type '%s'"}
	SuperField            = &Message{Code: 2855, Kind: TypeScriptError, Phase: PhaseCheck, Text: "class field '%s' defined by the parent class is not accessible in the child class via super"}
	SuperTypeArgs         = &Message{Code: 2754, Kind: TypeScriptError, Phase: PhaseCheck, Text: "'super' may not use type arguments"}
	TypeArgCount          = &Message{Code: 2558, Kind: TypeScriptError, Phase: PhaseCheck, Text: "expected %s type arguments, but got %d"}
	NoOverloadMatches     = &Message{Code: 2769, Kind: TypeScriptError, Phase: PhaseCheck, Text: "no overload matches this call"}
	CannotFindName        = &Message{Code: 2304, Kind: TypeScriptError, Phase: PhaseCheck, Text: "cannot find name '%s'"}
	// The forms of TS2304 tsc gives when it knows more about the name.
	CannotFindNameDidYouMean = &Message{Code: 2552, Kind: TypeScriptError, Phase: PhaseCheck, Text: "cannot find name '%s'; did you mean '%s'?"}
	MissingStaticPrefix      = &Message{Code: 2662, Kind: TypeScriptError, Phase: PhaseCheck, Text: "cannot find name '%s'; did you mean the static member '%s.%[1]s'?"}
	MissingThisPrefix        = &Message{Code: 2663, Kind: TypeScriptError, Phase: PhaseCheck, Text: "cannot find name '%s'; did you mean the instance member 'this.%[1]s'?"}
	CtorLocalInInitializer   = &Message{Code: 2301, Kind: TypeScriptError, Phase: PhaseCheck, Text: "initializer of instance member variable '%s' cannot reference identifier '%s' declared in the constructor"}
	TypeAsValue              = &Message{Code: 2693, Kind: TypeScriptError, Phase: PhaseCheck, Text: "'%s' only refers to a type, but is being used as a value here"}
	NamespaceAsValue         = &Message{Code: 2708, Kind: TypeScriptError, Phase: PhaseCheck, Text: "cannot use namespace '%s' as a value"}
	CannotFindTestRunner     = &Message{Code: 2593, Kind: TypeScriptError, Phase: PhaseCheck, Text: "cannot find name '%s'; do you need to install type definitions for a test runner? Try `npm i --save-dev @types/jest` or `npm i --save-dev @types/mocha` and then add 'jest' or 'mocha' to the types field in your tsconfig"}
	CannotFindJQuery         = &Message{Code: 2592, Kind: TypeScriptError, Phase: PhaseCheck, Text: "cannot find name '%s'; do you need to install type definitions for jQuery? Try `npm i --save-dev @types/jquery` and then add 'jquery' to the types field in your tsconfig"}
	ShorthandNoValue         = &Message{Code: 18004, Kind: TypeScriptError, Phase: PhaseCheck, Text: "no value exists in scope for the shorthand property '%s'; either declare one or provide an initializer"}
	TypeOnlyImportValue      = &Message{Code: 1361, Kind: TypeScriptError, Phase: PhaseCheck, Text: "'%s' cannot be used as a value because it was imported using 'import type'"}
	NoOverlap                = &Message{Code: 2367, Kind: TypeScriptError, Phase: PhaseCheck, Text: "this comparison appears to be unintentional because the types '%s' and '%s' have no overlap"}
	IndexSignatureKey        = syntax(1268, "only a string or number index signature `[k: string]: T` / `[i: number]: T` is supported (a `%s` key is not yet supported)")
)

// Parser and type conversion: valid TypeScript this compiler does not
// implement.
var (
	RestParamPattern            = unsupported(90001, "a rest parameter cannot be a destructuring pattern")
	DestructuredParamDefault    = unsupported(90002, "a default value on a destructured parameter is not yet supported")
	DestructuredParamType       = unsupported(90003, "a destructured parameter requires an explicit type annotation")
	ComputedKeyBinding          = unsupported(90004, "a computed destructuring key must bind through `: name`")
	ComputedKeyConstant         = unsupported(90005, "a computed destructuring key must be a constant string or number literal, got %s")
	LiteralKeyBinding           = unsupported(90006, "a string or numeric destructuring key ('%s') must be bound with `: name`, got %s")
	ComputedClassMemberName     = unsupported(90007, "a computed class member name must be a constant string or number literal — a dynamic key (identifier, call, Symbol, or interpolation) is not yet supported")
	ImportMetaOnlyURL           = unsupported(90008, "'import.meta' is only supported as 'import.meta.url'")
	NamespaceReexport           = unsupported(90009, "namespace re-exports ('export * as ns from') are not supported yet")
	NamespaceMemberBindings     = unsupported(90010, "a namespace const/let member must declare exactly one binding")
	ExportInNamespace           = syntax(1194, "export declarations are not permitted in a namespace")
	RequireDestructure          = unsupported(90011, "only simple `{ a, b: c }` destructuring is supported when binding a require('...') import (no defaults, nested patterns, or rest)")
	DynamicRequire              = unsupported(90012, "dynamic require(...) with a non-string-literal module path is not supported — use a string-literal path (e.g. require('path')); runtime/lazy module loading is a separate capability")
	InterfaceIndexSigTwice      = unsupported(90013, "at most one index signature is supported per interface")
	ObjectIndexSigTwice         = unsupported(90014, "at most one index signature is supported per object type")
	ObjectCallSigTwice          = unsupported(90015, "at most one call signature is supported per object type")
	IndexSigWithProperties      = unsupported(90016, "combining named properties with an index signature is not yet supported — use an index signature alone")
	InterfaceCallSigMixed       = unsupported(90017, "a call signature combined with other interface members is not supported — a callable object value has no runtime shape here")
	ObjectCallSigMixed          = unsupported(90018, "a call signature combined with other object-type members is not supported — a callable object value has no runtime shape here; use a plain function type or split the members")
	OptionalTupleElement        = unsupported(90020, "an optional tuple element is not yet supported")
	RestTupleElement            = unsupported(90021, "a rest tuple element is not yet supported")
	UnsupportedDeclarationMerge = unsupported(90022, "merging the declarations of '%s' is not yet supported")
	ThisTypeUnsupported         = unsupported(90024, "the 'this' type is not yet supported here")
	NamespaceTypeMemberClash    = unsupported(90023, "'%s' is declared as a class, interface, type or enum in more than one namespace; namespace type members share one top-level name, so each name must be unique")
)

// Parser: this compiler's own JSDoc annotations.
var (
	PureNotFunction      = annotation(91001, "@pure applies only to a function — '%s' is not a function binding")
	PureAsyncGenerator   = annotation(91002, "@pure cannot be applied to an async or generator function ('%s') — it isn't side-effect-free")
	PureAsyncGenExpr     = annotation(91002, "@pure cannot be applied to an async or generator function expression ('%s')")
	PureAsyncArrow       = annotation(91002, "@pure cannot be applied to an async arrow function ('%s')")
	OwnedUnknownParam    = annotation(91003, "@owned names unknown parameter '%s' on function '%s'")
	OwnedRestParam       = annotation(91004, "@owned cannot be applied to rest parameter '...%s'")
	OwnedAsyncGenerator  = annotation(91005, "@owned cannot be applied to an async or generator function ('%s') — a free across a suspension point is unsupported")
	FreeOwnedSingleVar   = annotation(91006, "@free/@owned applies to a single-variable declaration — annotate one variable per declaration statement")
	FreeOwnedExclusive   = annotation(91007, "@free and @owned are mutually exclusive on '%s' — @free frees at block exit, @owned at last use; pick one")
	ValueSingleVar       = annotation(91008, "@value applies to a single-variable declaration — annotate one variable per declaration statement")
	ValueWithFreeOwned   = annotation(91009, "@value cannot be combined with @free/@owned on '%s'")
	ErasedNeedsTypeParam = annotation(91010, "@erased requires '%s' to declare a type parameter, e.g. 'function %s<T>(...)'")
)
