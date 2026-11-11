package llvm

import (
	"regexp"
	"strings"
)

// toManagedSpace (idempotent) moves the module's references into the managed address
// space where code and stack slots cannot live: every stack slot's address,
// every function address used as a value, and every call through a
// register, whose callee is such a value. It runs on every unit of every
// compile, so each pass is one linear scan of the text.
func toManagedSpace(ir string) string {
	return spaceIndirectCalls(spaceFnOperands(spaceLines(ir)))
}

// isSpaceNameByte reports a byte of an unquoted IR name.
func isSpaceNameByte(c byte) bool {
	return c == '-' || c == '.' || c == '$' || c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

// irNameEnd is the end of the IR name starting at i.
func irNameEnd(s string, i int) int {
	for i < len(s) && isSpaceNameByte(s[i]) {
		i++
	}
	return i
}

// spaceLines rewrites the two line shapes: a global's definition gains the
// managed space, and a stack slot's definition becomes the slot and its
// cast into the space.
func spaceLines(ir string) string {
	var b strings.Builder
	b.Grow(len(ir) + len(ir)/16)
	for len(ir) > 0 {
		end := strings.IndexByte(ir, '\n')
		line, rest := ir, ""
		if end >= 0 {
			line, rest = ir[:end], ir[end:]
		}
		switch {
		case line != "" && line[0] == '@':
			writeGlobalDef(&b, line)
		default:
			writeAllocaOrLine(&b, line)
		}
		if rest != "" {
			b.WriteByte('\n')
			rest = rest[1:]
		}
		ir = rest
	}
	return b.String()
}

// writeGlobalDef writes a line starting with `@`, a global's definition
// taking the managed space before its global or constant keyword.
func writeGlobalDef(b *strings.Builder, line string) {
	i := 1
	for i < len(line) && (isSpaceNameByte(line[i]) || line[i] == '"') {
		i++
	}
	if i == 1 || !strings.HasPrefix(line[i:], " =") {
		b.WriteString(line)
		return
	}
	after := i + 2
	k := -1
	for j := after; j < len(line); j++ {
		if line[j] == '=' {
			break
		}
		if line[j] == ' ' && (strings.HasPrefix(line[j:], " global ") || strings.HasPrefix(line[j:], " constant ")) {
			k = j
			break
		}
	}
	if k < 0 || strings.HasPrefix(line, "@llvm.") || strings.Contains(line[:k], "addrspace(") {
		b.WriteString(line)
		return
	}
	b.WriteString(line[:k])
	b.WriteString(" addrspace(1)")
	b.WriteString(line[k:])
}

// writeAllocaOrLine writes a line, a stack slot's definition as the slot
// and its cast.
func writeAllocaOrLine(b *strings.Builder, line string) {
	i := 0
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	if i < len(line) && line[i] == '%' {
		e := irNameEnd(line, i+1)
		const kw = " = alloca "
		if e > i+1 && strings.HasPrefix(line[e:], kw) && !strings.HasSuffix(line[i:e], ".slot") {
			indent, reg, rest := line[:i], line[i:e], line[e+len(kw):]
			b.WriteString(indent + reg + ".slot = alloca " + rest + "\n" + indent + reg + " = " + irAddrCast + " ptr " + reg + ".slot to " + irPtr)
			return
		}
	}
	b.WriteString(line)
}

// irFnNames is the functions the module defines or declares.
func irFnNames(ir string) map[string]bool {
	fns := map[string]bool{}
	for len(ir) > 0 {
		end := strings.IndexByte(ir, '\n')
		line := ir
		if end >= 0 {
			line, ir = ir[:end], ir[end+1:]
		} else {
			ir = ""
		}
		if len(line) < 8 || !(strings.HasPrefix(line, "define") && !isWordByte(line[6]) || strings.HasPrefix(line, "declare") && !isWordByte(line[7])) {
			continue
		}
		if at := strings.IndexByte(line, '@'); at >= 0 {
			e := irNameEnd(line, at+1)
			if e > at+1 && e < len(line) && line[e] == '(' {
				fns[line[at+1:e]] = true
			}
		}
	}
	return fns
}

// isWordByte reports a word character, as a regexp's \\b sees one.
func isWordByte(c byte) bool {
	return c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

// spaceFnOperands casts a function named as a managed-pointer operand.
func spaceFnOperands(ir string) string {
	fns := irFnNames(ir)
	var b strings.Builder
	b.Grow(len(ir) + len(ir)/16)
	last, pos := 0, 0
	for {
		k := strings.Index(ir[pos:], irPtr)
		if k < 0 {
			break
		}
		start := pos + k
		i := start + len(irPtr)
		// Any lowercase attribute words, then the operand.
		for i+1 < len(ir) && ir[i] == ' ' && (ir[i+1] >= 'a' && ir[i+1] <= 'z' || ir[i+1] == '_') {
			j := i + 1
			for j < len(ir) && (ir[j] >= 'a' && ir[j] <= 'z' || ir[j] == '_') {
				j++
			}
			i = j
		}
		pos = start + len(irPtr)
		if i+1 >= len(ir) || ir[i] != ' ' || ir[i+1] != '@' {
			continue
		}
		e := irNameEnd(ir, i+2)
		if e == i+2 || e >= len(ir) || ir[e] == '(' {
			continue
		}
		pos = e + 1
		if !fns[ir[i+2:e]] {
			continue
		}
		b.WriteString(ir[last:start])
		b.WriteString(irPtr)
		b.WriteString(ir[start+len(irPtr) : i])
		b.WriteString(" addrspacecast (ptr @")
		b.WriteString(ir[i+2 : e])
		b.WriteString(" to " + irPtr + ")")
		last = e
	}
	b.WriteString(ir[last:])
	return b.String()
}

// spaceIndirectCalls gives a call through a register the managed space.
func spaceIndirectCalls(ir string) string {
	var b strings.Builder
	b.Grow(len(ir) + len(ir)/32)
	last, pos := 0, 0
	for {
		k := strings.Index(ir[pos:], "call ")
		if k < 0 {
			break
		}
		start := pos + k
		pos = start + 1
		if start > 0 && isWordByte(ir[start-1]) {
			continue
		}
		end, ok := indirectCallEnd(ir, start+len("call "))
		if !ok {
			continue
		}
		pos = end
		if strings.HasPrefix(ir[start:], "call addrspace(") {
			continue
		}
		b.WriteString(ir[last:start])
		b.WriteString("call addrspace(1) ")
		last = start + len("call ")
	}
	b.WriteString(ir[last:])
	return b.String()
}

// indirectCallEnd scans a call's return type from i for a register callee,
// and is the offset past the callee's `(`.
func indirectCallEnd(s string, i int) (int, bool) {
	for i < len(s) {
		switch s[i] {
		case '@', '\n':
			return 0, false
		case '(':
			j := i + 1
			for j < len(s) && s[j] != ')' && s[j] != '\n' {
				j++
			}
			if j >= len(s) || s[j] != ')' {
				return 0, false
			}
			i = j + 1
		case '%':
			e := irNameEnd(s, i+1)
			if e == i+1 || e >= len(s) || s[e] != '(' {
				return 0, false
			}
			return e + 1, true
		default:
			i++
		}
	}
	return 0, false
}

// fnValue is function fn's address (`@name`) as a value stored or passed
// where a reference is spelled irPtr: functions stay in the program
// address space, so outside it the address is cast in.
func fnValue(fn string) string {
	if !managedSpace {
		return fn
	}
	return "addrspacecast (ptr " + fn + " to " + irPtr + ")"
}

// globalAddr is managed global g's address where LLVM's own metadata
// lists (llvm.used) take a program-address-space pointer.
func globalAddr(g string) string {
	if !managedSpace {
		return g
	}
	return "addrspacecast (" + irPtr + " " + g + " to ptr)"
}

// unitText is a split unit's IR as compiled.
func unitText(ir string) string {
	if !managedSpace {
		return ir
	}
	return toManagedSpace(ir)
}

// ShapeIR is ir with the managed address space spelled away, for checks of
// an IR shape that hold in either spelling.
func ShapeIR(ir string) string {
	return fnCast.ReplaceAllString(strings.ReplaceAll(ir, " addrspace(1)", ""), "$1")
}

// fnCast matches a function address cast into the managed space, once the
// space is spelled away.
var fnCast = regexp.MustCompile(`addrspacecast \(ptr (@[-\w.$]+) to ptr\)`)
