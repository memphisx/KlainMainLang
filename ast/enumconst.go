package ast

import (
	"math"
	"strconv"
)

// EnumConst is a constant enum member's value: a number or a string.
type EnumConst struct {
	Num   float64
	Str   string
	IsStr bool
}

// FoldEnumConst evaluates a constant enum expression (TypeScript's
// isConstantMemberAccess rules): literals, earlier members (bare or through
// the enum's name), parentheses, and the unary and binary arithmetic,
// bitwise and string-concatenation operators.
func FoldEnumConst(x Expression, enum string, prior map[string]EnumConst) (EnumConst, bool) {
	switch v := x.(type) {
	case *NumberLiteral:
		if v.IsBigInt {
			return EnumConst{}, false
		}
		if s := v.Value; len(s) >= 2 && s[0] == '0' && (s[1]|32 == 'x' || s[1]|32 == 'b' || s[1]|32 == 'o') {
			n, err := strconv.ParseInt(s, 0, 64)
			return EnumConst{Num: float64(n)}, err == nil
		}
		f, err := strconv.ParseFloat(v.Value, 64)
		if ne, isNum := err.(*strconv.NumError); isNum && ne.Err == strconv.ErrRange {
			err = nil // past the double range: Infinity (or 0), as JS reads it
		}
		return EnumConst{Num: f}, err == nil
	case *StringLiteral:
		return EnumConst{Str: v.Value, IsStr: true}, true
	case *TemplateLiteral:
		if len(v.Exprs) == 0 && len(v.Quasis) == 1 {
			return EnumConst{Str: v.Quasis[0], IsStr: true}, true
		}
	case *Identifier:
		c, ok := prior[v.Name]
		return c, ok
	case *MemberExpression:
		if id, ok := v.Object.(*Identifier); ok && id.Name == enum {
			c, ok := prior[v.Property]
			return c, ok
		}
	case *IndexExpression:
		if id, ok := v.Object.(*Identifier); ok && id.Name == enum {
			if sl, ok := v.Index.(*StringLiteral); ok {
				c, ok := prior[sl.Value]
				return c, ok
			}
		}
	case *UnaryExpression:
		a, ok := FoldEnumConst(v.Arg, enum, prior)
		if !ok || a.IsStr {
			return EnumConst{}, false
		}
		switch v.Op {
		case "+":
			return a, true
		case "-":
			return EnumConst{Num: -a.Num}, true
		case "~":
			return EnumConst{Num: float64(^ToInt32(a.Num))}, true
		}
	case *BinaryExpression:
		a, ok1 := FoldEnumConst(v.Left, enum, prior)
		b, ok2 := FoldEnumConst(v.Right, enum, prior)
		if !ok1 || !ok2 {
			return EnumConst{}, false
		}
		if v.Op == "+" && (a.IsStr || b.IsStr) {
			return EnumConst{Str: EnumConstString(a) + EnumConstString(b), IsStr: true}, true
		}
		if a.IsStr || b.IsStr {
			return EnumConst{}, false
		}
		x, y := a.Num, b.Num
		switch v.Op {
		case "+":
			return EnumConst{Num: x + y}, true
		case "-":
			return EnumConst{Num: x - y}, true
		case "*":
			return EnumConst{Num: x * y}, true
		case "/":
			return EnumConst{Num: x / y}, true
		case "%":
			return EnumConst{Num: math.Mod(x, y)}, true
		case "**":
			return EnumConst{Num: math.Pow(x, y)}, true
		case "<<":
			return EnumConst{Num: float64(ToInt32(x) << (ToUint32(y) & 31))}, true
		case ">>":
			return EnumConst{Num: float64(ToInt32(x) >> (ToUint32(y) & 31))}, true
		case ">>>":
			return EnumConst{Num: float64(ToUint32(x) >> (ToUint32(y) & 31))}, true
		case "&":
			return EnumConst{Num: float64(ToInt32(x) & ToInt32(y))}, true
		case "|":
			return EnumConst{Num: float64(ToInt32(x) | ToInt32(y))}, true
		case "^":
			return EnumConst{Num: float64(ToInt32(x) ^ ToInt32(y))}, true
		}
	}
	return EnumConst{}, false
}

// enumConstString is a member's value as `+` concatenates it.
func EnumConstString(c EnumConst) string {
	if c.IsStr {
		return c.Str
	}
	return JSNumberString(c.Num)
}

// ToInt32 and ToUint32 are ToInt32 and ToUint32.
func ToInt32(f float64) int32 { return int32(ToUint32(f)) }

func ToUint32(f float64) uint32 {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0
	}
	return uint32(int64(math.Mod(math.Trunc(f), 4294967296)))
}
