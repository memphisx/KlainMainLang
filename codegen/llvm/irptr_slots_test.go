package llvm

import (
	"regexp"
	"strings"
	"testing"
)

var (
	refAllocaLine   = regexp.MustCompile(`(?m)^([ \t]*)(%[-\w.$]+) = alloca (.*)$`)
	refGlobalDef    = regexp.MustCompile(`(?m)^(@[-\w.$"]+ =[^\n=]*?) (global|constant) `)
	refFnDecl       = regexp.MustCompile(`(?m)^(?:define|declare)\b[^@\n]*@([-\w.$]+)\(`)
	refFnOperand    = regexp.MustCompile(regexp.QuoteMeta(irPtr) + `((?: [a-z_]+)*) @([-\w.$]+)([^-\w.$(])`)
	refIndirectCall = regexp.MustCompile(`\bcall ((?:[^@%(\n]|\([^)\n]*\))*?)(%[-\w.$]+)\(`)
)

// refToManagedSpace is the rewrite stated as regular expressions, the
// specification the scanners are held to (far too slow to run on every unit).
func refToManagedSpace(ir string) string {
	ir = refGlobalDef.ReplaceAllStringFunc(ir, func(s string) string {
		if strings.HasPrefix(s, "@llvm.") || strings.Contains(s, "addrspace(") {
			return s
		}
		m := refGlobalDef.FindStringSubmatch(s)
		return m[1] + " addrspace(1) " + m[2] + " "
	})
	ir = refAllocaLine.ReplaceAllStringFunc(ir, func(s string) string {
		m := refAllocaLine.FindStringSubmatch(s)
		if strings.HasSuffix(m[2], ".slot") {
			return s
		}
		return m[1] + m[2] + ".slot = alloca " + m[3] + "\n" + m[1] + m[2] + " = " + irAddrCast + " ptr " + m[2] + ".slot to " + irPtr
	})
	fns := map[string]bool{}
	for _, m := range refFnDecl.FindAllStringSubmatch(ir, -1) {
		fns[m[1]] = true
	}
	ir = refFnOperand.ReplaceAllStringFunc(ir, func(s string) string {
		m := refFnOperand.FindStringSubmatch(s)
		if !fns[m[2]] {
			return s
		}
		return irPtr + m[1] + " addrspacecast (ptr @" + m[2] + " to " + irPtr + ")" + m[3]
	})
	return refIndirectCall.ReplaceAllStringFunc(ir, func(s string) string {
		if strings.HasPrefix(s, "call addrspace(") {
			return s
		}
		return "call addrspace(1) " + s[len("call "):]
	})
}

func TestToManagedSpaceMatchesSpecification(t *testing.T) {
	p := irPtr
	ir := strings.Join([]string{
		`@g = global i64 0`, `@h = private constant [2 x i8] c"a "`, `@llvm.used = appending global [1 x ptr] [ptr @f]`,
		`@q = addrspace(1) global i64 0`, `@"w.x" = internal global i64 1`, `@e = external global i64`,
		`define void @f(` + p + ` %a) {`, `declare i32 @c(` + p + `)`, `  %s = alloca i64, align 8`, `  %s.slot = alloca i64`,
		"\t%t = alloca { i64, i64 }", `  store ` + p + ` @f, ` + p + ` %s`, `  store ` + p + ` noundef nonnull @f, ` + p + ` @g, align 8`,
		`  call void ` + `%r(i64 1)`, `  %x = call i32 (` + p + `, ...) %r(i64 1)`, `  notail call void @f(` + p + ` @c)`,
		`  call addrspace(1) void %r()`, `  recall %z(`, `  %y = tail call fastcc ` + p + ` %r2(` + p + ` @f)`, `  call void (`, `  call %`, `}`, `  ` + p + ` @f(`, p + ` @f`,
	}, "\n")
	if got, want := toManagedSpace(ir), refToManagedSpace(ir); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	if once := toManagedSpace(ir); toManagedSpace(once) != once {
		t.Fatalf("not idempotent:\n%s", once)
	}
}
