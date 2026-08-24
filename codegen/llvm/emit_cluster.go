// emit_cluster.go — codegen for Node's `cluster` module (TDD-00105): the
// cluster.fork() callable and the cluster.isWorker accessor. cluster.isPrimary
// and cluster.workerId live in emit_http.go (they predate this file, reading
// the shared @__kml_cluster_worker_id global). Backed by runtime_cluster.go.
package llvm

import (
	"fmt"

	"KlainMainLang/ast"
)

// emitClusterModuleCall dispatches cluster.fork().
func (e *Emitter) emitClusterModuleCall(method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	switch method {
	case "fork":
		if len(args) != 0 {
			return Value{}, fmt.Errorf("%d:%d: cluster.fork takes no arguments", pos.Line, pos.Col)
		}
		e.ensureClusterRuntime()
		w := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_cluster_fork()", w))
		return Value{Ref: w, Ty: ClusterWorkerType()}, nil
	}
	return Value{}, fmt.Errorf("%d:%d: cluster.%s is not supported", pos.Line, pos.Col, method)
}

// emitClusterIsWorker implements cluster.isWorker (= worker id != 0).
func (e *Emitter) emitClusterIsWorker() (Value, error) {
	e.ensureHTTPClusterFork()
	id := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr @__kml_cluster_worker_id, align 8", id))
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", r, id))
	return Value{Ref: r, Ty: TypeBool}, nil
}

// emitClusterWorkerMember reads a Worker handle's `.id`.
func (e *Emitter) emitClusterWorkerMember(objExpr ast.Expression, prop string, pos ast.Pos) (Value, error) {
	objVal, err := e.emitExpr(objExpr)
	if err != nil {
		return Value{}, err
	}
	switch prop {
	case "id":
		slot := e.freshReg()
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 0", slot, clusterWorkerIR, objVal.Ref))
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", r, slot))
		return Value{Ref: r, Ty: TypeI64}, nil
	}
	return Value{}, fmt.Errorf("%d:%d: a cluster Worker has no property '%s' (V1 exposes only .id)", pos.Line, pos.Col, prop)
}
