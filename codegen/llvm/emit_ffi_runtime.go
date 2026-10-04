package llvm

import (
	_ "embed"
	"fmt"
	"runtime"
)

// emit_ffi_runtime.go — the native side of node:ffi (lib/node/ffi.ts): the
// library registry (ffisrc/ffi_registry.c), whose `symbols` and `functions`
// tables are ordered exactly as Node's std::unordered_map (the real STL
// container on POSIX, an MSVC port on Windows), and the libffi calls,
// callbacks and memory helpers over it (ffisrc/ffi_native.c).

//go:embed ffisrc/ffi_registry.c
var ffiRegistrySource string

//go:embed ffisrc/ffi_umap_stl.cc
var ffiUmapSTLSource string

//go:embed ffisrc/ffi_umap_msvc.c
var ffiUmapMSVCSource string

//go:embed ffisrc/ffi_native.c
var ffiNativeSource string

//go:embed ffisrc/umap_order.c
var umapOrderSource string

// FFIRegistrySources returns the C/C++ members behind the registry and
// umapOrder for the current target: the registry, the order helper, each
// when used, and the order container.
func (e *Emitter) FFIRegistrySources() []CSource {
	var out []CSource
	if e.usedFFIRegistry {
		out = append(out, CSource{"ffireg", ffiRegistrySource, nil, nil, ""})
	}
	if e.usedFFINatives {
		out = append(out, CSource{"ffinative", ffiNativeSource, nil, nil, ""})
	}
	if e.usedUmapOrder {
		out = append(out, CSource{"umaporder", umapOrderSource, nil, nil, ""})
	}
	if e.opts.Target.OS() == "windows" {
		return append(out, CSource{"ffiumap", ffiUmapMSVCSource, nil, nil, ""})
	}
	cxx := "-lstdc++"
	if e.opts.Target.OS() == "darwin" || runtime.GOOS == "darwin" && e.opts.Target.OS() == "" {
		cxx = "-lc++"
	}
	return append(out, CSource{"ffiumap", ffiUmapSTLSource, nil, []string{cxx}, "cc"})
}

// ensureFFINatives compiles ffi_native.c (lib/node/ffi.ts's natives over the
// registry and libffi) and links libffi: the `@link ffi` of lib/native.d.ts.
func (e *Emitter) ensureFFINatives() {
	if e.usedFFINatives {
		return
	}
	e.usedFFINatives = true
	e.usedFFIRegistry = true
	e.ensureFFIDl()
	e.ensureBigInt()
	e.ensureStrHeaderRuntime()
	e.requireLink("ffi")
}

// Registered from init: the view boxes through emitBoxValue, which the
// runtimeSymbols table itself is reachable from.
func init() { runtimeSymbols["__kml_native_ffi_view"] = (*Emitter).ensureNativeFFIView }

// ensureNativeFFIView defines @__kml_native_ffi_view(addr, len, arrayBuffer):
// len bytes of foreign memory at addr as a Buffer, or an ArrayBuffer, that
// shares them (Node's toBuffer/toArrayBuffer without a copy), boxed.
func (e *Emitter) ensureNativeFFIView() {
	if e.fnDecls["__kml_native_ffi_view"] {
		return
	}
	e.fnDecls["__kml_native_ffi_view"] = true
	e.ensureMalloc()
	restore := e.beginDetachedFunc()
	addr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = fptoui double %%addr to i64", addr))
	p := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", p, addr))
	n := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = fptosi double %%len to i64", n))
	abL, bufL := e.freshLabel("ffiview.ab"), e.freshLabel("ffiview.buf")
	e.emitTerminator(fmt.Sprintf("br i1 %%ab, label %%%s, label %%%s", abL, bufL))
	e.emitLabel(bufL)
	bv, err := e.emitBoxValue(e.bufferAggregate(p, n))
	if err != nil {
		panic(err)
	}
	e.emitTerminator(fmt.Sprintf("ret i64 %s", bv.Ref))
	e.emitLabel(abL)
	hdr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 16)", hdr))
	lg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { i64, ptr }, ptr %s, i32 0, i32 0", lg, hdr))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", n, lg))
	dg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { i64, ptr }, ptr %s, i32 0, i32 1", dg, hdr))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", p, dg))
	av, err := e.emitBoxValue(Value{Ref: hdr, Ty: ArrayBufferType()})
	if err != nil {
		panic(err)
	}
	e.emitTerminator(fmt.Sprintf("ret i64 %s", av.Ref))
	body := e.allocas.String() + e.body.String()
	restore()
	e.functions.WriteString(fmt.Sprintf("\ndefine i64 @__kml_native_ffi_view(double %%addr, double %%len, i1 %%ab) {\nentry:\n%s}\n", body))
}

// UsesFFIRegistry reports whether the registry or the order container must
// be linked.
func (e *Emitter) UsesFFIRegistry() bool { return e.usedFFIRegistry || e.usedUmapOrder }

// ensureUmapOrder links __kml_native_umap_order (umap_order.c) and its
// container: the `@link umap` of lib/native.d.ts.
func (e *Emitter) ensureUmapOrder() { e.usedUmapOrder = true }
