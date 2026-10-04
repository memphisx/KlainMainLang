// libsplit splits a program's IR into the program's unit and one unit per
// builtin library module (TDD-00238 Stage 4), writing <out>/<name>.ll.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"KlainMainLang/codegen/llvm"
)

func main() {
	out := flag.String("o", ".", "output directory")
	flag.Parse()
	src, err := os.ReadFile(flag.Arg(0))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	u, err := llvm.SplitLibUnits(string(src))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.WriteFile(filepath.Join(*out, "program.ll"), []byte(u.Program), 0o644)
	for k, t := range u.Modules {
		os.WriteFile(filepath.Join(*out, "lib_"+strings.ReplaceAll(k, "/", "_")+".ll"), []byte(t), 0o644)
	}
	fmt.Println(len(u.Modules), "modules")
}
