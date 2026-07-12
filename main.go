package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"KlainMainLang/codegen/llvm"
	"KlainMainLang/resolver"
)

func main() {
	emitLLVM := flag.Bool("emit-llvm", false, "emit LLVM IR and stop")
	output := flag.String("o", "", "output binary name (default: input name without extension)")
	static := flag.Bool("static", false, "statically link the output binary — for minimal/scratch Docker images. Linux only: run KlainMainLang itself on Linux to use this (macOS's linker has no static-libc support at all, by design)")
	flag.Parse()

	if flag.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "usage: KlainMainLang [flags] <file.ts>")
		os.Exit(1)
	}

	if *static && runtime.GOOS != "linux" {
		fatal("--static is only supported when compiling on Linux (this run is on %s). This is not a missing-package issue: static linking needs a static libc to link against, and macOS's linker ships none at all — Apple deliberately never provides a static libSystem/crt0.o, with no workaround. To produce a statically-linked binary for a scratch/distroless Docker image, run KlainMainLang itself on Linux — e.g. a Linux build stage in a multi-stage Dockerfile (build the compiler and your program there, then copy just the resulting static binary into a scratch final stage).", runtime.GOOS)
	}

	inFile := flag.Arg(0)
	prog, err := resolver.ResolveProgram(inFile)
	if err != nil {
		fatal("parse error: %v", err)
	}

	em := llvm.NewEmitter()
	ir, err := em.EmitProgram(prog)
	if err != nil {
		fatal("codegen error: %v", err)
	}

	if *emitLLVM {
		fmt.Print(ir)
		return
	}

	// Write IR to a temp file, then compile with clang.
	llFile := strings.TrimSuffix(inFile, filepath.Ext(inFile)) + ".ll"
	if err := os.WriteFile(llFile, []byte(ir), 0644); err != nil {
		fatal("cannot write IR: %v", err)
	}

	outBin := *output
	if outBin == "" {
		outBin = strings.TrimSuffix(inFile, filepath.Ext(inFile))
	}

	clangArgs := []string{"-O2", llFile, "-o", outBin}
	if *static {
		clangArgs = append(clangArgs, "-static")
	}
	for _, lib := range em.LinkLibs() {
		clangArgs = append(clangArgs, "-l"+lib)
	}
	cmd := exec.Command("clang", clangArgs...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fatal("clang: %v", err)
	}

	fmt.Fprintf(os.Stderr, "compiled: %s\n", outBin)
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "KlainMainLang: "+format+"\n", args...)
	os.Exit(1)
}
