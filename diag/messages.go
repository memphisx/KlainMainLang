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
	IdentAfterNumeric         = syntax(1351, "an identifier or keyword cannot immediately follow a numeric literal")
	NumericSeparatorLegacy    = syntax(6188, "a numeric separator '_' is not allowed in a legacy octal or non-octal-decimal literal")
	BigIntDecimalPoint        = syntax(1353, "a BigInt literal cannot contain a decimal point")
	BigIntExponent            = syntax(1352, "a BigInt literal cannot contain an exponent")
	RegexFlags                = syntax(1499, "invalid regular expression flags")
	BigIntLegacyOctal         = syntax(1121, "a BigInt literal cannot use a legacy-octal / non-octal-decimal form")
	UnterminatedString        = syntax(1002, "unterminated string literal")
	InvalidHexEscape          = syntax(1125, "invalid hexadecimal escape sequence")
	InvalidUnicodeEscape      = syntax(1125, "invalid Unicode escape sequence")
	HexDigitExpected          = syntax(1125, "hexadecimal digit expected")
	BinaryDigitExpected       = syntax(1177, "binary digit expected")
	OctalDigitExpected        = syntax(1178, "octal digit expected")
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
	// SuperPrivateName is JavaScript's early error for `super.#x` (a private
	// name is never reached through super); tsc reports it as TS2339.
	SuperPrivateName       = syntax(2339, "a private name cannot be read through 'super'")
	ModuleDeclNotTopLevel  = syntax(1233, "an import or export declaration can only be used at the top level of a module")
	CoverInitializedName   = syntax(1312, "a shorthand property with a default ('{ x = 1 }') is only valid in a destructuring assignment")
	ReservedWordIdent      = syntax(1359, "'%s' is a reserved word that cannot be used here")
	DeclInSingleStatement  = syntax(1156, "'%s' declarations can only be declared inside a block")
	AwaitInParamInit       = syntax(2524, "'await' expressions cannot be used in a parameter initializer")
	StrictDeleteName       = syntax(1102, "'delete' cannot be called on an identifier in strict mode")
	SuperNotFollowed       = syntax(1034, "'super' must be followed by an argument list or member access")
	BreakOutsideLoop       = syntax(1105, "a 'break' statement can only be used within an enclosing iteration or switch statement")
	ContinueOutsideLoop    = syntax(1104, "a 'continue' statement can only be used within an enclosing iteration statement")
	JumpCrossesFunction    = syntax(1107, "jump target cannot cross function boundary")
	ContinueNonLoopLabel   = syntax(1115, "a 'continue' statement can only jump to a label of an enclosing iteration statement")
	UndefinedLabel         = syntax(1116, "a '%s' statement can only jump to a label of an enclosing statement ('%s' is not one)")
	YieldOutsideGenerator  = syntax(1163, "a 'yield' expression is only allowed in a generator body")
	YieldInGeneratorParams = syntax(2523, "'yield' expressions cannot be used in a parameter initializer")
	AwaitOutsideAsync      = syntax(1308, "'await' expressions are only allowed within async functions and at the top levels of modules")
	NewTargetNotAllowed    = syntax(17013, "new.target expression is not allowed here")
	InvalidAssignTarget    = syntax(2364, "the left-hand side of an assignment expression must be a variable or a property access")
	InvalidUpdateTarget    = syntax(2357, "the operand of an increment or decrement operator must be a variable or a property access")
	ExpectedAsNamespace    = syntax(1005, "expected 'as' after '*' in namespace import, got %s")
	ExpectedOfDestructure  = syntax(1005, "expected 'of' after a for-of destructuring pattern, got %s")
	ExpectedImportDotOr    = syntax(1005, "expected '.' or '(' after 'import' in an expression, got %s")
	ExpectedCaseOrDefault  = syntax(1130, "expected 'case' or 'default' in switch")
	ExpectedCatchFinally   = syntax(1472, "try statement requires at least a catch or finally clause")
	ExpectedTemplateCont   = syntax(1160, "expected template continuation, got %s")
	ExpectedImportMeta     = syntax(17012, "expected 'meta' after 'import.'")
	ForAwaitNotForIn       = syntax(1005, "'for await' requires a for-of loop, not for-in")
	ForAwaitNotForOf       = syntax(1005, "'for await' requires a for-of loop over an async iterable")
	MethodMissingParens    = syntax(1005, "'%s %s' must be a method (missing '()')")
	ModifierNeedsMethod    = syntax(1005, "'%s' is not a method (an async/generator modifier requires a '()' method body)")
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
	LineBreakBeforeArrow        = syntax(1200, "line break not allowed before '=>'")
	LineBreakAfterThrow         = syntax(1142, "line break not allowed after 'throw'")
	ConstMustBeInitialized      = syntax(1155, "'const' declaration '%s' must be initialized")
	DefiniteWithInitializer     = syntax(1263, "Declarations with initializers cannot also have definite assignment assertions.")
	DefiniteNeedsAnnotation     = syntax(1264, "Declarations with definite assignment assertions must also have type annotations.")
	DuplicateParameter          = syntax(2300, "duplicate parameter name '%s'")
	StrictBindingName           = syntax(1100, "'%s' cannot be used as a binding name in strict mode")
	StrictParameterName         = syntax(1100, "'%s' cannot be a parameter name in strict mode")
	StrictAssignTargetName      = syntax(1100, "'%s' cannot be assigned to in strict mode")
	StrictClassParameterName    = syntax(1100, "'%s' cannot be a parameter name in a class method (strict mode)")
	StrictNonSimpleParams       = syntax(1347, "a strict-mode function cannot have a non-simple parameter list")
	ExponentUnaryAmbiguous      = syntax(17006, "unary operator '%s' before '**' is ambiguous — parenthesize as '(%s x) ** y' or '%s(x ** y)'")
	RestParamLastFunctionType   = syntax(1014, "a rest parameter must be last in a function type")
	RestParamLastMethodSig      = syntax(1014, "a rest parameter must be last in a method signature")
	RestElementLast             = syntax(2462, "a rest element must be the last property in an object pattern, got %s")
	ConstructorAsyncGenerator   = syntax(1089, "a constructor cannot be async or a generator")
	ConstructorAccessor         = syntax(1341, "a constructor cannot be a getter/setter")
	GetterParams                = syntax(1054, "a 'get' accessor cannot have parameters")
	SetterArity                 = syntax(1049, "a 'set' accessor must have exactly one parameter")
	SetterRest                  = syntax(1053, "a 'set' accessor cannot have a rest parameter")
	DuplicateConstructor        = syntax(2392, "class '%s' declares more than one constructor")
	StaticPrototype             = syntax(2699, "a static class member cannot be named 'prototype'")
	FieldConstructor            = syntax(18006, "classes may not have a field named 'constructor'")
	SuperCallOutsideConstructor = syntax(2337, "super calls are not permitted outside constructors or in nested functions inside constructors")
	SuperPropertyOutsideMethod  = syntax(2660, "'super' can only be referenced in members of derived classes or object literal expressions")
	ArgumentsInFieldInit        = syntax(2815, "'arguments' cannot be referenced in property initializers")
	DuplicatePrivateName        = syntax(2300, "duplicate identifier '%s'")
	HashConstructor             = syntax(18012, "'#constructor' is a reserved class member name")
	AccessibilityOnPrivate      = syntax(18010, "an accessibility modifier cannot be used with a private identifier")
	ParamPropertyOutsideCtor    = syntax(2369, "a parameter property (public/private/protected/readonly) is only allowed in a class constructor")
	ParamPropertyPattern        = syntax(1317, "a parameter property cannot be a rest or destructured parameter")
	AccessorOnMethod            = syntax(1275, "'accessor' is only valid on a class field, not a method")
	DecoratorsNotValid          = syntax(1206, "decorators can only be applied to a class declaration or its members")
	DecoratorOnStaticBlock      = syntax(1206, "decorators are not allowed on a static initializer block")
	OverloadNoImplementation    = syntax(2391, "overload signature for '%s' must be followed by another overload signature or its implementation")
	OverloadClassNoImpl         = syntax(2391, "overload signature for '%s' in class '%s' has no implementation")
	OverloadWrongName           = syntax(2389, "expected the implementation of overloaded function '%s', got 'function %s'")
	ImportRequireAssignment     = syntax(1202, "`import %s = require(...)` is not supported — use an ES import declaration instead")
	RedeclaredBlockScoped       = syntax(2451, "identifier '%s' has already been declared")
	DuplicateIdentifier         = syntax(2300, "identifier '%s' has already been declared")
	DuplicateFunctionImpl       = syntax(2393, "duplicate function implementation of '%s'")
	UsedBeforeDeclaration       = &Message{Code: 2448, Kind: ReferenceError, Phase: PhaseCheck, Text: "cannot access '%s' before initialization"}
	UsedBeforeAssigned          = &Message{Code: 2454, Kind: TypeScriptError, Phase: PhaseCheck, Text: "variable '%s' is used before being assigned"}
	// The checker's type errors (TDD-00230 P2.7).
	PropertyNotIndex         = &Message{Code: 2411, Kind: TypeScriptError, Phase: PhaseCheck, Text: "property '%s' of type '%s' is not assignable to 'string' index type '%s'"}
	DerivedNoSuper           = &Message{Code: 2377, Kind: TypeScriptError, Phase: PhaseCheck, Text: "constructors for derived classes must contain a 'super' call"}
	VarRedeclaredType        = &Message{Code: 2403, Kind: TypeScriptError, Phase: PhaseCheck, Text: "subsequent variable declarations must have the same type. Variable '%s' must be of type '%s', but here has type '%s'"}
	SymbolToStringImplicit   = &Message{Code: 2731, Kind: TypeScriptError, Phase: PhaseCheck, Text: "implicit conversion of a 'symbol' to a 'string' will fail at runtime. Consider wrapping this expression in 'String(...)'"}
	NotIterable              = &Message{Code: 2488, Kind: TypeScriptError, Phase: PhaseCheck, Text: "type '%s' must have a '[Symbol.iterator]()' method that returns an iterator"}
	BadAwaitOperand          = &Message{Code: 1320, Kind: TypeScriptError, Phase: PhaseCheck, Text: "type of 'await' operand must either be a valid promise or must not contain a callable 'then' member"}
	NextResultNoValue        = &Message{Code: 2490, Kind: TypeScriptError, Phase: PhaseCheck, Text: "the type returned by the 'next()' method of an iterator must have a 'value' property"}
	AsyncNextResultNoValue   = &Message{Code: 2547, Kind: TypeScriptError, Phase: PhaseCheck, Text: "the type returned by the 'next()' method of an async iterator must be a promise for a type with a 'value' property"}
	InvalidSpreadType        = &Message{Code: 2698, Kind: TypeScriptError, Phase: PhaseCheck, Text: "spread types may only be created from object types"}
	NotAsyncIterable         = &Message{Code: 2504, Kind: TypeScriptError, Phase: PhaseCheck, Text: "type '%s' must have a '[Symbol.asyncIterator]()' method that returns an async iterator"}
	TupleNoElement           = &Message{Code: 2493, Kind: TypeScriptError, Phase: PhaseCheck, Text: "tuple type '%s' of length '%d' has no element at index '%d'"}
	NotAssignable            = &Message{Code: 2322, Kind: TypeScriptError, Phase: PhaseCheck, Text: "type '%s' is not assignable to type '%s'"}
	ParamsIncompatible       = &Message{Code: 2328, Kind: TypeScriptError, Phase: PhaseCheck, Text: "types of parameters '%s' and '%s' are incompatible"}
	GetterMustReturn         = &Message{Code: 2378, Kind: TypeScriptError, Phase: PhaseCheck, Text: "a 'get' accessor must return a value"}
	MustReturnValue          = &Message{Code: 2355, Kind: TypeScriptError, Phase: PhaseCheck, Text: "a function whose declared type is neither 'undefined', 'void', nor 'any' must return a value"}
	LacksEndingReturn        = &Message{Code: 2366, Kind: TypeScriptError, Phase: PhaseCheck, Text: "function lacks ending return statement and return type does not include 'undefined'"}
	NeverReachableEnd        = &Message{Code: 2534, Kind: TypeScriptError, Phase: PhaseCheck, Text: "a function returning 'never' cannot have a reachable end point"}
	NotCallable              = &Message{Code: 2349, Kind: TypeScriptError, Phase: PhaseCheck, Text: "This expression is not callable."}
	ClassNotCallable         = &Message{Code: 2348, Kind: TypeScriptError, Phase: PhaseCheck, Text: "value of type '%s' is not callable; did you mean to include 'new'?"}
	ObjectToFewTypes         = &Message{Code: 2696, Kind: TypeScriptError, Phase: PhaseCheck, Text: "the 'Object' type is assignable to very few other types; did you mean to use the 'any' type instead?"}
	NoCommonProperties       = &Message{Code: 2559, Kind: TypeScriptError, Phase: PhaseCheck, Text: "type '%s' has no properties in common with type '%s'"}
	TypeArgConstraint        = &Message{Code: 2344, Kind: TypeScriptError, Phase: PhaseCheck, Text: "type '%s' does not satisfy the constraint '%s'"}
	ArgNotAssignable         = &Message{Code: 2345, Kind: TypeScriptError, Phase: PhaseCheck, Text: "argument of type '%s' is not assignable to parameter of type '%s'"}
	SpreadArgument           = &Message{Code: 2556, Kind: TypeScriptError, Phase: PhaseCheck, Text: "a spread argument must either have a tuple type or be passed to a rest parameter"}
	ArgCount                 = &Message{Code: 2554, Kind: TypeScriptError, Phase: PhaseCheck, Text: "expected %s arguments, but got %d"}
	SuperNotDerived          = &Message{Code: 2335, Kind: TypeScriptError, Phase: PhaseCheck, Text: "'super' can only be referenced in a derived class"}
	ReadonlyProperty         = &Message{Code: 2540, Kind: TypeScriptError, Phase: PhaseCheck, Text: "cannot assign to '%s' because it is a read-only property"}
	PromiseResolveArity      = &Message{Code: 2794, Kind: TypeScriptError, Phase: PhaseCheck, Text: "expected %s arguments, but got %d. Did you forget to include 'void' in your type argument to 'Promise'?"}
	ArgCountAtLeast          = &Message{Code: 2555, Kind: TypeScriptError, Phase: PhaseCheck, Text: "expected at least %d arguments, but got %d"}
	ParamSelfReference       = &Message{Code: 2372, Kind: TypeScriptError, Phase: PhaseCheck, Text: "parameter '%s' cannot reference itself"}
	ParamForwardReference    = &Message{Code: 2373, Kind: TypeScriptError, Phase: PhaseCheck, Text: "parameter '%s' cannot reference identifier '%s' declared after it"}
	ImplicitAnyOwnInit       = &Message{Code: 7022, Kind: TypeScriptError, Phase: PhaseCheck, Text: "'%s' implicitly has type 'any' because it does not have a type annotation and is referenced directly or indirectly in its own initializer"}
	PrivateMethodAssign      = &Message{Code: 2803, Kind: TypeScriptError, Phase: PhaseCheck, Text: "cannot assign to private method '%s'. Private methods are not writable"}
	NoMatchingIndexSig       = &Message{Code: 2537, Kind: TypeScriptError, Phase: PhaseCheck, Text: "type '%s' has no matching index signature for type '%s'"}
	PropertyNotExist         = &Message{Code: 2339, Kind: TypeScriptError, Phase: PhaseCheck, Text: "property '%s' does not exist on type '%s'"}
	PropertyDidYouMean       = &Message{Code: 2551, Kind: TypeScriptError, Phase: PhaseCheck, Text: "property '%s' does not exist on type '%s'; did you mean '%s'?"}
	AssignToConst            = &Message{Code: 2588, Kind: TypeScriptError, Phase: PhaseCheck, Text: "cannot assign to '%s' because it is a constant"}
	AssignToFunction         = &Message{Code: 2630, Kind: TypeScriptError, Phase: PhaseCheck, Text: "cannot assign to '%s' because it is a function"}
	AssignToClass            = &Message{Code: 2629, Kind: TypeScriptError, Phase: PhaseCheck, Text: "cannot assign to '%s' because it is a class"}
	EnumAmbientComputed      = &Message{Code: 1066, Kind: TypeScriptError, Phase: PhaseCheck, Text: "in ambient enum declarations member initializer must be constant expression"}
	ConstEnumIndexKey        = &Message{Code: 2476, Kind: TypeScriptError, Phase: PhaseCheck, Text: "a const enum member can only be accessed using a string literal"}
	GeneratorVoidAnnotation  = &Message{Code: 2505, Kind: TypeScriptError, Phase: PhaseCheck, Text: "a generator cannot have a 'void' type annotation"}
	ConstEnumComputed        = &Message{Code: 2474, Kind: TypeScriptError, Phase: PhaseCheck, Text: "const enum member initializers must be constant expressions"}
	EnumMemberNeedsInit      = &Message{Code: 1061, Kind: TypeScriptError, Phase: PhaseCheck, Text: "enum member must have initializer"}
	CommaLeftUnused          = &Message{Code: 2695, Kind: TypeScriptError, Phase: PhaseCheck, Text: "left side of comma operator is unused and has no side effects"}
	AlwaysTruthy             = &Message{Code: 2872, Kind: TypeScriptError, Phase: PhaseCheck, Text: "this kind of expression is always truthy"}
	AlwaysFalsy              = &Message{Code: 2873, Kind: TypeScriptError, Phase: PhaseCheck, Text: "this kind of expression is always falsy"}
	EnumComputedNotNumber    = &Message{Code: 18033, Kind: TypeScriptError, Phase: PhaseCheck, Text: "type '%s' is not assignable to type 'number' as required for computed enum member values"}
	ConstEnumNonFinite       = &Message{Code: 2477, Kind: TypeScriptError, Phase: PhaseCheck, Text: "'const' enum member initializer was evaluated to a non-finite value"}
	ConstEnumNaN             = &Message{Code: 2478, Kind: TypeScriptError, Phase: PhaseCheck, Text: "'const' enum member initializer was evaluated to disallowed value 'NaN'"}
	AssignToEnum             = &Message{Code: 2628, Kind: TypeScriptError, Phase: PhaseCheck, Text: "cannot assign to '%s' because it is an enum"}
	ExcessProperty           = &Message{Code: 2353, Kind: TypeScriptError, Phase: PhaseCheck, Text: "object literal may only specify known properties, and '%s' does not exist in type '%s'"}
	PropertyMissing          = &Message{Code: 2741, Kind: TypeScriptError, Phase: PhaseCheck, Text: "property '%s' is missing in type '%s' but required in type '%s'"}
	PropertiesMissing        = &Message{Code: 2739, Kind: TypeScriptError, Phase: PhaseCheck, Text: "type '%s' is missing the following properties from type '%s': %s"}
	PropertiesMissingMore    = &Message{Code: 2740, Kind: TypeScriptError, Phase: PhaseCheck, Text: "type '%s' is missing the following properties from type '%s': %s, and %d more"}
	ArithmeticOperand        = &Message{Code: 2356, Kind: TypeScriptError, Phase: PhaseCheck, Text: "an arithmetic operand must be of type 'any', 'number', 'bigint' or an enum type"}
	ArithmeticLeft           = &Message{Code: 2362, Kind: TypeScriptError, Phase: PhaseCheck, Text: "the left-hand side of an arithmetic operation must be of type 'any', 'number', 'bigint' or an enum type"}
	ArithmeticRight          = &Message{Code: 2363, Kind: TypeScriptError, Phase: PhaseCheck, Text: "the right-hand side of an arithmetic operation must be of type 'any', 'number', 'bigint' or an enum type"}
	SymbolOperand            = &Message{Code: 2469, Kind: TypeScriptError, Phase: PhaseCheck, Text: "the '%s' operator cannot be applied to type 'symbol'"}
	OperatorNotApplied       = &Message{Code: 2365, Kind: TypeScriptError, Phase: PhaseCheck, Text: "operator '%s' cannot be applied to types '%s' and '%s'"}
	SetterParamInit          = &Message{Code: 1052, Kind: TypeScriptError, Phase: PhaseCheck, Text: "a 'set' accessor parameter cannot have an initializer"}
	BaseTypeArgCount         = &Message{Code: 2508, Kind: TypeScriptError, Phase: PhaseCheck, Text: "no base constructor has the specified number of type arguments"}
	TypeNotGeneric           = &Message{Code: 2315, Kind: TypeScriptError, Phase: PhaseCheck, Text: "type '%s' is not generic"}
	AbstractNew              = &Message{Code: 2511, Kind: TypeScriptError, Phase: PhaseCheck, Text: "cannot create an instance of an abstract class"}
	AbstractMissing          = &Message{Code: 2515, Kind: TypeScriptError, Phase: PhaseCheck, Text: "non-abstract class '%s' does not implement inherited abstract member '%s' from class '%s'"}
	AbstractMissingMany      = &Message{Code: 2654, Kind: TypeScriptError, Phase: PhaseCheck, Text: "non-abstract class '%s' is missing implementations for the following members of '%s': %s"}
	AbstractMissingMore      = &Message{Code: 2655, Kind: TypeScriptError, Phase: PhaseCheck, Text: "non-abstract class '%s' is missing implementations for the following members of '%s': %s and %d more"}
	AbstractExprMissing      = &Message{Code: 2653, Kind: TypeScriptError, Phase: PhaseCheck, Text: "non-abstract class expression does not implement inherited abstract member '%s' from class '%s'"}
	AbstractExprMissingMany  = &Message{Code: 2656, Kind: TypeScriptError, Phase: PhaseCheck, Text: "non-abstract class expression is missing implementations for the following members of '%s': %s"}
	AbstractExprMissingMore  = &Message{Code: 2650, Kind: TypeScriptError, Phase: PhaseCheck, Text: "non-abstract class expression is missing implementations for the following members of '%s': %s and %d more"}
	InstanceofLeft           = &Message{Code: 2358, Kind: TypeScriptError, Phase: PhaseCheck, Text: "the left-hand side of an 'instanceof' expression must be of type 'any', an object type or a type parameter"}
	InstanceofRight          = &Message{Code: 2359, Kind: TypeScriptError, Phase: PhaseCheck, Text: "the right-hand side of an 'instanceof' expression must be either of type 'any', a class, function, or other type assignable to the 'Function' interface type"}
	PossiblyNull             = &Message{Code: 18047, Kind: TypeScriptError, Phase: PhaseCheck, Text: "'%s' is possibly 'null'"}
	PossiblyUndefined        = &Message{Code: 18048, Kind: TypeScriptError, Phase: PhaseCheck, Text: "'%s' is possibly 'undefined'"}
	PossiblyNullish          = &Message{Code: 18049, Kind: TypeScriptError, Phase: PhaseCheck, Text: "'%s' is possibly 'null' or 'undefined'"}
	ObjectPossiblyNull       = &Message{Code: 2531, Kind: TypeScriptError, Phase: PhaseCheck, Text: "object is possibly 'null'"}
	ObjectPossiblyUndef      = &Message{Code: 2532, Kind: TypeScriptError, Phase: PhaseCheck, Text: "object is possibly 'undefined'"}
	ObjectPossiblyNullish    = &Message{Code: 2533, Kind: TypeScriptError, Phase: PhaseCheck, Text: "object is possibly 'null' or 'undefined'"}
	ValueCannotBeUsed        = &Message{Code: 18050, Kind: TypeScriptError, Phase: PhaseCheck, Text: "the value '%s' cannot be used here"}
	IsOfTypeUnknown          = &Message{Code: 18046, Kind: TypeScriptError, Phase: PhaseCheck, Text: "'%s' is of type 'unknown'"}
	ObjectOfTypeUnknown      = &Message{Code: 2571, Kind: TypeScriptError, Phase: PhaseCheck, Text: "object is of type 'unknown'"}
	InvokePossiblyNull       = &Message{Code: 2721, Kind: TypeScriptError, Phase: PhaseCheck, Text: "cannot invoke an object which is possibly 'null'"}
	InvokePossiblyUndef      = &Message{Code: 2722, Kind: TypeScriptError, Phase: PhaseCheck, Text: "cannot invoke an object which is possibly 'undefined'"}
	InvokePossiblyNullish    = &Message{Code: 2723, Kind: TypeScriptError, Phase: PhaseCheck, Text: "cannot invoke an object which is possibly 'null' or 'undefined'"}
	PrivateMember            = &Message{Code: 2341, Kind: TypeScriptError, Phase: PhaseCheck, Text: "property '%s' is private and only accessible within class '%s'"}
	ProtectedMember          = &Message{Code: 2445, Kind: TypeScriptError, Phase: PhaseCheck, Text: "property '%s' is protected and only accessible within class '%s' and its subclasses"}
	ProtectedReceiver        = &Message{Code: 2446, Kind: TypeScriptError, Phase: PhaseCheck, Text: "property '%s' is protected and only accessible through an instance of class '%s'. This is an instance of class '%s'"}
	PrivateConstructor       = &Message{Code: 2673, Kind: TypeScriptError, Phase: PhaseCheck, Text: "constructor of class '%s' is private and only accessible within the class declaration"}
	ProtectedConstructor     = &Message{Code: 2674, Kind: TypeScriptError, Phase: PhaseCheck, Text: "constructor of class '%s' is protected and only accessible within the class declaration"}
	ThisContext              = &Message{Code: 2684, Kind: TypeScriptError, Phase: PhaseCheck, Text: "the 'this' context of type '%s' is not assignable to method's 'this' of type '%s'"}
	SuperField               = &Message{Code: 2855, Kind: TypeScriptError, Phase: PhaseCheck, Text: "class field '%s' defined by the parent class is not accessible in the child class via super"}
	SuperTypeArgs            = &Message{Code: 2754, Kind: TypeScriptError, Phase: PhaseCheck, Text: "'super' may not use type arguments"}
	TypeArgCount             = &Message{Code: 2558, Kind: TypeScriptError, Phase: PhaseCheck, Text: "expected %s type arguments, but got %d"}
	NoOverloadMatches        = &Message{Code: 2769, Kind: TypeScriptError, Phase: PhaseCheck, Text: "no overload matches this call"}
	ParamNameNoType          = &Message{Code: 7051, Kind: TypeScriptError, Phase: PhaseCheck, Text: "parameter has a name but no type; did you mean '%s: %s'?"}
	InvalidIndexType         = &Message{Code: 2538, Kind: TypeScriptError, Phase: PhaseCheck, Text: "Type '%s' cannot be used as an index type."}
	GlobalThisImplicitAny    = &Message{Code: 7017, Kind: TypeScriptError, Phase: PhaseCheck, Text: "element implicitly has an 'any' type because type 'typeof globalThis' has no index signature"}
	IndexCallSuggestion      = &Message{Code: 7052, Kind: TypeScriptError, Phase: PhaseCheck, Text: "element implicitly has an 'any' type because type '%s' has no index signature. Did you mean to call '%s'?"}
	IndexNotNumber           = &Message{Code: 7015, Kind: TypeScriptError, Phase: PhaseCheck, Text: "element implicitly has an 'any' type because index expression is not of type 'number'"}
	IndexImplicitAny         = &Message{Code: 7053, Kind: TypeScriptError, Phase: PhaseCheck, Text: "element implicitly has an 'any' type because expression of type '%s' can't be used to index type '%s'"}
	VarImplicitAnySome       = &Message{Code: 7034, Kind: TypeScriptError, Phase: PhaseCheck, Text: "variable '%s' implicitly has type 'any' in some locations where its type cannot be determined"}
	VarImplicitAny           = &Message{Code: 7005, Kind: TypeScriptError, Phase: PhaseCheck, Text: "variable '%s' implicitly has an 'any' type"}
	VarImplicitAnyArraySome  = &Message{Code: 7034, Kind: TypeScriptError, Phase: PhaseCheck, Text: "variable '%s' implicitly has type 'any[]' in some locations where its type cannot be determined"}
	VarImplicitAnyArray      = &Message{Code: 7005, Kind: TypeScriptError, Phase: PhaseCheck, Text: "variable '%s' implicitly has an 'any[]' type"}
	ReturnImplicitAnyCycle   = &Message{Code: 7023, Kind: TypeScriptError, Phase: PhaseCheck, Text: "'%s' implicitly has return type 'any' because it does not have a return type annotation and is referenced directly or indirectly in one of its return expressions"}
	MemberImplicitAny        = &Message{Code: 7008, Kind: TypeScriptError, Phase: PhaseCheck, Text: "member '%s' implicitly has an 'any' type"}
	PropertyNotInitialized   = &Message{Code: 2564, Kind: TypeScriptError, Phase: PhaseCheck, Text: "Property '%s' has no initializer and is not definitely assigned in the constructor."}
	NotConstructable         = &Message{Code: 2351, Kind: TypeScriptError, Phase: PhaseCheck, Text: "This expression is not constructable."}
	NotConstructorType       = &Message{Code: 2507, Kind: TypeScriptError, Phase: PhaseCheck, Text: "type '%s' is not a constructor function type"}
	DeleteNotReference       = &Message{Code: 2703, Kind: TypeScriptError, Phase: PhaseCheck, Text: "the operand of a 'delete' operator must be a property reference"}
	DeleteReadonly           = &Message{Code: 2704, Kind: TypeScriptError, Phase: PhaseCheck, Text: "the operand of a 'delete' operator cannot be a read-only property"}
	DeleteNotOptional        = &Message{Code: 2790, Kind: TypeScriptError, Phase: PhaseCheck, Text: "the operand of a 'delete' operator must be optional"}
	NewImplicitAny           = &Message{Code: 7009, Kind: TypeScriptError, Phase: PhaseCheck, Text: "'new' expression, whose target lacks a construct signature, implicitly has an 'any' type"}
	ReturnImplicitAny        = &Message{Code: 7010, Kind: TypeScriptError, Phase: PhaseCheck, Text: "'%s', which lacks return-type annotation, implicitly has an 'any' return type."}
	CallSigReturnImplicitAny = &Message{Code: 7020, Kind: TypeScriptError, Phase: PhaseCheck, Text: "Call signature, which lacks return-type annotation, implicitly has an 'any' return type."}
	ParamImplicitAny         = &Message{Code: 7006, Kind: TypeScriptError, Phase: PhaseCheck, Text: "parameter '%s' implicitly has an 'any' type"}
	BindingImplicitAny       = &Message{Code: 7031, Kind: TypeScriptError, Phase: PhaseCheck, Text: "binding element '%s' implicitly has an 'any' type"}
	RestParamImplicitAny     = &Message{Code: 7019, Kind: TypeScriptError, Phase: PhaseCheck, Text: "rest parameter '%s' implicitly has an 'any[]' type"}
	YieldImplicitAny         = &Message{Code: 7057, Kind: TypeScriptError, Phase: PhaseCheck, Text: "'yield' expression implicitly results in an 'any' type because its containing generator lacks a return-type annotation"}
	OverrideNoBase           = &Message{Code: 4112, Kind: TypeScriptError, Phase: PhaseCheck, Text: "this member cannot have an 'override' modifier because its containing class '%s' does not extend another class"}
	OverrideNotInBase        = &Message{Code: 4113, Kind: TypeScriptError, Phase: PhaseCheck, Text: "this member cannot have an 'override' modifier because it is not declared in the base class '%s'"}
	IncorrectlyImplements    = &Message{Code: 2420, Kind: TypeScriptError, Phase: PhaseCheck, Text: "class '%s' incorrectly implements interface '%s'"}
	OverrideNotAssignable    = &Message{Code: 2416, Kind: TypeScriptError, Phase: PhaseCheck, Text: "property '%s' in type '%s' is not assignable to the same property in base type '%s'"}
	ValueUsedAsType          = &Message{Code: 2749, Kind: TypeScriptError, Phase: PhaseCheck, Text: "'%s' refers to a value, but is being used as a type here; did you mean 'typeof %s'?"}
	DynamicImportSpecifier   = &Message{Code: 7036, Kind: TypeScriptError, Phase: PhaseCheck, Text: "Dynamic import's specifier must be of type 'string', but here has type '%s'."}
	CannotFindName           = &Message{Code: 2304, Kind: TypeScriptError, Phase: PhaseCheck, Text: "cannot find name '%s'"}
	ArrowCapturesGlobalThis  = &Message{Code: 7041, Kind: TypeScriptError, Phase: PhaseCheck, Text: "The containing arrow function captures the global value of 'this'."}
	ThisInNamespace          = &Message{Code: 2331, Kind: TypeScriptError, Phase: PhaseCheck, Text: "'this' cannot be referenced in a module or namespace body"}
	ThisInvalidLocation      = &Message{Code: 2332, Kind: TypeScriptError, Phase: PhaseCheck, Text: "'this' cannot be referenced in current location"}
	ThisImplicitlyAny        = &Message{Code: 2683, Kind: TypeScriptError, Phase: PhaseCheck, Text: "'this' implicitly has type 'any' because it does not have a type annotation"}
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
	CatchAnnotation          = &Message{Code: 1196, Kind: TypeScriptError, Phase: PhaseCheck, Text: "Catch clause variable type annotation must be 'any' or 'unknown' if specified."}
	ReturnOutsideFunction    = &Message{Code: 1108, Kind: TypeScriptError, Phase: PhaseCheck, Text: "A 'return' statement can only be used within a function body."}
	TypeOnlyImportValue      = &Message{Code: 1361, Kind: TypeScriptError, Phase: PhaseCheck, Text: "'%s' cannot be used as a value because it was imported using 'import type'"}
	NoOverlap                = &Message{Code: 2367, Kind: TypeScriptError, Phase: PhaseCheck, Text: "this comparison appears to be unintentional because the types '%s' and '%s' have no overlap"}
	IndexSignatureKey        = syntax(1268, "only a string or number index signature `[k: string]: T` / `[i: number]: T` is supported (a `%s` key is not yet supported)")
)

// Parser and type conversion: valid TypeScript this compiler does not
// implement.
var (
	ComputedKeyBinding          = unsupported(90004, "a computed destructuring key must bind through `: name`")
	LiteralKeyBinding           = unsupported(90006, "a string or numeric destructuring key ('%s') must be bound with `: name`, got %s")
	ComputedClassMemberName     = unsupported(90007, "a computed class member name must be a constant string or number literal, a well-known symbol, or a const holding one or a symbol — a key computed at run time is not yet supported")
	NamespaceReexport           = unsupported(90009, "namespace re-exports ('export * as ns from') are not supported yet")
	NamespaceMemberBindings     = unsupported(90010, "a namespace const/let member must declare exactly one binding")
	ExportInNamespace           = syntax(1194, "export declarations are not permitted in a namespace")
	DynamicRequire              = unsupported(90012, "dynamic require(...) with a non-string-literal module path is not supported — use a string-literal path (e.g. require('path')); runtime/lazy module loading is a separate capability")
	UnsupportedDeclarationMerge = unsupported(90022, "merging the declarations of '%s' is not yet supported")
	ThisTypeOutsideClass        = &Message{Code: 2526, Kind: TypeScriptError, Phase: PhaseParse, Text: "a 'this' type is available only in a non-static member of a class or interface"}
	DecoratedMemberUnsupported  = unsupported(90025, "a decorator on %s is not supported yet")
	AutoAccessorNeedsStandard   = unsupported(90026, "an `accessor` auto-field decorator is a standard (TC39) decorator — compile with -decorators=standard")
	EmbedDirPath                = annotation(91020, "embedDir's path must be a string literal (it is read at compile time)")
	SpeciesComputed             = unsupported(90037, "%s's [Symbol.species] getter must return this, a class extending %s, %s itself, or undefined; another constructor is not supported yet")
	SetPrototypeStatic          = unsupported(90036, "Object.setPrototypeOf of an object with a fixed layout is not supported yet (give the binding an any type to change its prototype)")
	AssignRetypesField          = unsupported(90035, "Object.assign giving field '%s' a value of another type is not supported yet (this compiler stores an object's fields by their declared types)")
	NumberLocaleArgs            = unsupported(90034, "toLocaleString() locale/options arguments on a number are an Intl feature, not supported yet; the no-argument form gives the en-US default")
	ValueArrayTarget            = unsupported(90029, "@value applies to an array declaration (e.g. `/** @value */ const ps: Point[] = [...]`)")
	ValueArrayElement           = unsupported(90030, "@value requires a fixed-shape object element type (an interface/object shape) — class instances, tuples, nested arrays, and dynamic values keep the default layout")
	ValueArrayShared            = unsupported(90031, "a @value array read by other functions is not supported yet — declare it inside the function that uses it")
	ValueArrayInit              = unsupported(90032, "a @value array must be initialized with an array literal, with no spread or hole")
	ValueArrayForOfPattern      = unsupported(90033, "destructuring a @value array element in for...of is not supported — bind a name and read its fields")
	ValueArrayUse               = unsupported(90038, "a @value array supports index read/write, .length, for...of, and .push — this use of '%s' needs a regular array")
	CodegenUnsupported          = unsupported(90039, "%s is not supported yet")
	ReservedMember              = unsupported(90024, "a class cannot have a field named '%s', which this compiler reserves for its own use")
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
	PureViolation        = annotation(91011, "@pure function '%s' %s")
	MemAnnotation        = annotation(91012, "%s")
	MemoryFreeAuto       = annotation(91013, "Memory.free is not allowed under -mm=auto — the compiler inserts frees itself; annotate with /** @free */ or /** @owned */ to control placement")
	MemoryFreeArity      = annotation(91014, "Memory.free takes exactly 1 argument")
	LowerTypeShape       = annotation(91015, "a @lowerType class cannot extend another or be generic")
	ErasedNeedsTypeParam = annotation(91010, "@erased requires '%s' to declare a type parameter, e.g. 'function %s<T>(...)'")
)

// --no-any: the any/unknown escape hatch is banned.
var (
	HostBaseUnsupported   = &Message{Code: 92003, Kind: Unsupported, Phase: PhaseParse, Text: "extending the builtin '%s' is not supported yet"}
	ByteStreamUnsupported = &Message{Code: 92004, Kind: Unsupported, Phase: PhaseParse, Text: "a byte stream (an underlying source of type 'bytes') is not supported yet"}
	NoAnyType             = &Message{Code: 92001, Kind: Unsupported, Phase: PhaseCheck, Text: "the '%s' type is banned under --no-any; annotate a concrete type (or drop --no-any, or use -compat=js)"}
	NoAnyInferred         = &Message{Code: 92002, Kind: Unsupported, Phase: PhaseCheck, Text: "'%s' is inferred as 'any' under --no-any; annotate it with a concrete type (e.g. `JSON.parse(s) as Rec`)"}
)

// Strict code (TypeScript's alwaysStrict): JavaScript's strict-mode early
// errors, reported by the checker so -compat=js keeps sloppy code.
var (
	StrictInvalidUse   = &Message{Code: 1100, Kind: TypeScriptError, Phase: PhaseCheck, Text: "invalid use of '%s' in strict mode"}
	StrictDeleteIdent  = &Message{Code: 1102, Kind: TypeScriptError, Phase: PhaseCheck, Text: "'delete' cannot be called on an identifier in strict mode"}
	StrictReservedWord = &Message{Code: 1212, Kind: TypeScriptError, Phase: PhaseCheck, Text: "Identifier expected. '%s' is a reserved word in strict mode."}
)

// Unused declarations (TypeScript's noUnusedLocals and noUnusedParameters).
var (
	UnusedNeverRead       = &Message{Code: 6133, Kind: TypeScriptError, Phase: PhaseCheck, Text: "'%s' is declared but its value is never read"}
	UnusedProperty        = &Message{Code: 6138, Kind: TypeScriptError, Phase: PhaseCheck, Text: "property '%s' is declared but its value is never read"}
	UnusedAllImports      = &Message{Code: 6192, Kind: TypeScriptError, Phase: PhaseCheck, Text: "all imports in import declaration are unused"}
	UnusedNeverUsed       = &Message{Code: 6196, Kind: TypeScriptError, Phase: PhaseCheck, Text: "'%s' is declared but never used"}
	UnusedAllDestructured = &Message{Code: 6198, Kind: TypeScriptError, Phase: PhaseCheck, Text: "all destructured elements are unused"}
	UnusedAllVariables    = &Message{Code: 6199, Kind: TypeScriptError, Phase: PhaseCheck, Text: "all variables are unused"}
	UnusedAllTypeParams   = &Message{Code: 6205, Kind: TypeScriptError, Phase: PhaseCheck, Text: "all type parameters are unused"}
)
