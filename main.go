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
	static := flag.Bool("static", false, "statically link the output binary — for minimal/scratch Docker images. Linux only: run klainmain itself on Linux to use this (macOS's linker has no static-libc support at all, by design)")
	mm := flag.String("mm", "manual", "memory management mode: manual (default, Memory.free(x) only) or gc (Boehm GC — see docs/tdd/TDD-00001.md)")
	globals := flag.String("globals", "strict", "ambient built-in global names (Math/JSON/console/process/fetch/...): strict (default, a colliding declaration is a compile error) or permissive (real JS/browser shadowing — see docs/tdd/TDD-00050.md). Constructor-style built-ins (Map/Date/RegExp/...) stay reserved either way")
	flag.Parse()

	if flag.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "usage: klainmain [flags] <file.ts>")
		os.Exit(1)
	}

	if *static && runtime.GOOS != "linux" {
		fatal("--static is only supported when compiling on Linux (this run is on %s). This is not a missing-package issue: static linking needs a static libc to link against, and macOS's linker ships none at all — Apple deliberately never provides a static libSystem/crt0.o, with no workaround. To produce a statically-linked binary for a scratch/distroless Docker image, run klainmain itself on Linux — e.g. a Linux build stage in a multi-stage Dockerfile (build the compiler and your program there, then copy just the resulting static binary into a scratch final stage).", runtime.GOOS)
	}

	switch *mm {
	case "manual", "gc":
		// ok
	case "auto":
		fatal("-mm=auto is not implemented yet (see docs/tdd/TDD-00001.md) — use -mm=manual or -mm=gc")
	default:
		fatal("unrecognized -mm value %q — must be one of: manual, gc (auto is scoped but not implemented yet, see docs/tdd/TDD-00001.md)", *mm)
	}

	switch *globals {
	case "strict", "permissive":
		// ok
	default:
		fatal("unrecognized -globals value %q — must be one of: strict, permissive (see docs/tdd/TDD-00050.md)", *globals)
	}

	inFile := flag.Arg(0)
	prog, err := resolver.ResolveProgramWithOptions(inFile, *globals == "permissive")
	if err != nil {
		fatal("parse error: %v", err)
	}

	em := llvm.NewEmitter()
	em.SetMemMode(*mm)
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

	clangArgs := []string{"-O2", llFile}
	if *mm == "gc" {
		gcShimPath := strings.TrimSuffix(inFile, filepath.Ext(inFile)) + ".gcshim.c"
		if err := os.WriteFile(gcShimPath, []byte(llvm.GCShimSource), 0644); err != nil {
			fatal("cannot write GC shim: %v", err)
		}
		clangArgs = append(clangArgs, gcShimPath)
	}
	clangArgs = append(clangArgs, "-o", outBin)
	if *static {
		clangArgs = append(clangArgs, "-static")
	}
	if *mm == "gc" {
		cflags, libs, err := llvm.LocateGC()
		if err != nil {
			fatal("gc mode: %v", err)
		}
		clangArgs = append(clangArgs, cflags...)
		clangArgs = append(clangArgs, libs...)
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
	fmt.Fprintf(os.Stderr, "klainmain: "+format+"\n", args...)
	os.Exit(1)
}
