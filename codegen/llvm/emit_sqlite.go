package llvm

import (
	"KlainMainLang/ast"
	"fmt"
	"strings"
)

// emit_sqlite.go — node:sqlite V1 (ADR-00540, implementing TDD-00151).
// Synchronous DatabaseSync/StatementSync over libsqlite3. The C surface is
// declared in runtime_sqlite.go; the object shapes are SQLiteDatabaseType /
// SQLiteStatementType (types.go). Result rows use the statically-typed shape
// supplied by an explicit call-site type argument (`stmt.all<Row>()` /
// `stmt.get<Row>()`), projected onto the query columns by name.

// SQLite result / open / storage-class constants (sqlite3.h).
const (
	sqliteOK            = 0
	sqliteRow           = 100
	sqliteDone          = 101
	sqliteOpenReadonly  = 0x1
	sqliteOpenReadWrite = 0x2
	sqliteOpenCreate    = 0x4
)

// emitNewDatabaseSync implements `new DatabaseSync(path, options?)`.
func (e *Emitter) emitNewDatabaseSync(ex *ast.NewDatabaseSyncExpression) (Value, error) {
	e.ensureSQLite3()
	e.ensureMalloc()
	e.ensureExceptionHelpers()

	pathVal, err := e.emitExpr(ex.Path)
	if err != nil {
		return Value{}, err
	}
	pathVal = e.coerce(pathVal, TypePtr)

	// Options — read statically from an object literal (V1 scope): a non-literal
	// options argument uses the defaults. readOnly=false, open=true,
	// enableForeignKeyConstraints=true, timeout unset.
	readOnly := sqliteStaticBoolOption(ex.Options, "readOnly")
	openIt := true
	if v, present := sqliteStaticBoolOptionPresent(ex.Options, "open"); present {
		openIt = v
	}
	fk := true
	if v, present := sqliteStaticBoolOptionPresent(ex.Options, "enableForeignKeyConstraints"); present {
		fk = v
	}
	timeout, hasTimeout := sqliteStaticNumberOption(ex.Options, "timeout")

	flags := sqliteOpenReadWrite | sqliteOpenCreate
	if readOnly {
		flags = sqliteOpenReadonly
	}

	dbTy := SQLiteDatabaseType()
	obj := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", obj, dbTy.StructSize()))
	e.storeSQLiteField(dbTy, obj, "__kml_path", "ptr", pathVal.Ref)
	e.storeSQLiteField(dbTy, obj, "__kml_flags", "i32", fmt.Sprintf("%d", flags))
	e.storeSQLiteField(dbTy, obj, "isOpen", "i1", "0")
	e.storeSQLiteField(dbTy, obj, "__kml_handle", "ptr", "null")
	if openIt {
		e.emitSQLiteOpenConnection(dbTy, obj, pathVal.Ref, flags, fk, timeout, hasTimeout)
	}
	return Value{Ref: obj, Ty: dbTy}, nil
}

// emitSQLiteOpenConnection opens (or reopens) the connection stored on obj and
// records the handle + isOpen=true. Foreign-key PRAGMA and busy-timeout are
// applied when requested (they matter on every open).
func (e *Emitter) emitSQLiteOpenConnection(dbTy Type, obj, pathRef string, flags int, fk bool, timeout int, hasTimeout bool) {
	dbSlot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", dbSlot))
	e.emitInstr(fmt.Sprintf("store ptr null, ptr %s, align 8", dbSlot))
	rc := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i32 @sqlite3_open_v2(ptr %s, ptr %s, i32 %d, ptr null)", rc, pathRef, dbSlot, flags))
	dbHandle := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", dbHandle, dbSlot))
	bad := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne i32 %s, %d", bad, rc, sqliteOK))
	e.emitSQLiteThrowOnCond(dbHandle, bad)
	if fk {
		pr := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i32 @sqlite3_exec(ptr %s, ptr %s, ptr null, ptr null, ptr null)", pr, dbHandle, e.internString("PRAGMA foreign_keys=ON;")))
	}
	if hasTimeout {
		tr := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i32 @sqlite3_busy_timeout(ptr %s, i32 %d)", tr, dbHandle, timeout))
	}
	e.storeSQLiteField(dbTy, obj, "__kml_handle", "ptr", dbHandle)
	e.storeSQLiteField(dbTy, obj, "isOpen", "i1", "1")
}

// emitSQLiteDatabaseMethod dispatches db.exec/prepare/close.
func (e *Emitter) emitSQLiteDatabaseMethod(objExpr ast.Expression, method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	objVal, err := e.emitExpr(objExpr)
	if err != nil {
		return Value{}, err
	}
	dbHandle := e.loadFieldValue(objVal, e.sqliteFieldIdx(objVal.Ty, "__kml_handle"), TypePtr).Ref

	switch method {
	case "exec":
		if len(args) != 1 {
			return Value{}, fmt.Errorf("%d:%d: DatabaseSync.exec takes exactly 1 argument (sql)", pos.Line, pos.Col)
		}
		sqlVal, err := e.emitExpr(args[0])
		if err != nil {
			return Value{}, err
		}
		sqlVal = e.coerce(sqlVal, TypePtr)
		rc := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i32 @sqlite3_exec(ptr %s, ptr %s, ptr null, ptr null, ptr null)", rc, dbHandle, sqlVal.Ref))
		bad := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp ne i32 %s, %d", bad, rc, sqliteOK))
		e.emitSQLiteThrowOnCond(dbHandle, bad)
		return Value{Ty: TypeVoid}, nil

	case "prepare":
		if len(args) != 1 {
			return Value{}, fmt.Errorf("%d:%d: DatabaseSync.prepare takes exactly 1 argument (sql)", pos.Line, pos.Col)
		}
		sqlVal, err := e.emitExpr(args[0])
		if err != nil {
			return Value{}, err
		}
		sqlVal = e.coerce(sqlVal, TypePtr)
		stmtSlot := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", stmtSlot))
		e.emitInstr(fmt.Sprintf("store ptr null, ptr %s, align 8", stmtSlot))
		rc := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i32 @sqlite3_prepare_v2(ptr %s, ptr %s, i32 -1, ptr %s, ptr null)", rc, dbHandle, sqlVal.Ref, stmtSlot))
		bad := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp ne i32 %s, %d", bad, rc, sqliteOK))
		e.emitSQLiteThrowOnCond(dbHandle, bad)
		stmtHandle := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", stmtHandle, stmtSlot))

		stmtTy := SQLiteStatementType()
		obj := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", obj, stmtTy.StructSize()))
		e.storeSQLiteField(stmtTy, obj, "__kml_handle", "ptr", stmtHandle)
		e.storeSQLiteField(stmtTy, obj, "__kml_db", "ptr", dbHandle)
		e.storeSQLiteField(stmtTy, obj, "sourceSQL", "ptr", sqlVal.Ref)
		return Value{Ref: obj, Ty: stmtTy}, nil

	case "close":
		rc := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i32 @sqlite3_close_v2(ptr %s)", rc, dbHandle))
		// Reflect the closed state in the object's isOpen field.
		e.storeSQLiteField(objVal.Ty, objVal.Ref, "isOpen", "i1", "0")
		e.storeSQLiteField(objVal.Ty, objVal.Ref, "__kml_handle", "ptr", "null")
		return Value{Ty: TypeVoid}, nil

	case "open":
		// Reopen a handle constructed with `open: false` (or after close()).
		pathRef := e.loadFieldValue(objVal, e.sqliteFieldIdx(objVal.Ty, "__kml_path"), TypePtr).Ref
		flags := e.loadFieldValue(objVal, e.sqliteFieldIdx(objVal.Ty, "__kml_flags"), TypeI32).Ref
		dbSlot := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", dbSlot))
		e.emitInstr(fmt.Sprintf("store ptr null, ptr %s, align 8", dbSlot))
		rc := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i32 @sqlite3_open_v2(ptr %s, ptr %s, i32 %s, ptr null)", rc, pathRef, dbSlot, flags))
		nh := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", nh, dbSlot))
		bad := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp ne i32 %s, %d", bad, rc, sqliteOK))
		e.emitSQLiteThrowOnCond(nh, bad)
		fkr := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i32 @sqlite3_exec(ptr %s, ptr %s, ptr null, ptr null, ptr null)", fkr, nh, e.internString("PRAGMA foreign_keys=ON;")))
		e.storeSQLiteField(objVal.Ty, objVal.Ref, "__kml_handle", "ptr", nh)
		e.storeSQLiteField(objVal.Ty, objVal.Ref, "isOpen", "i1", "1")
		return Value{Ty: TypeVoid}, nil

	case "location":
		// db.location([dbName]) → the file backing the named database, or null
		// for an in-memory/temporary database. Defaults to "main".
		dbName := e.internString("main")
		if len(args) == 1 {
			nv, err := e.emitExpr(args[0])
			if err != nil {
				return Value{}, err
			}
			dbName = e.coerce(nv, TypePtr).Ref
		}
		raw := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @sqlite3_db_filename(ptr %s, ptr %s)", raw, dbHandle, dbName))
		// db_filename returns "" (not null) for :memory:/temp — normalise "" → null.
		firstByte := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i8, ptr %s, align 1", firstByte, raw))
		isEmpty := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, 0", isEmpty, firstByte))
		res, err := e.emitStrBranch(isEmpty,
			func() (string, error) { return "null", nil },
			func() (string, error) {
				r := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_str_from_cstr(ptr %s)", r, raw))
				return r, nil
			})
		if err != nil {
			return Value{}, err
		}
		nt := TypePtr
		nt.Nullable = true
		return Value{Ref: res, Ty: nt}, nil

	case "function":
		return e.emitSQLiteFunction(dbHandle, args, pos)

	case "aggregate", "createSession", "applyChangeset", "applyChangesetSync",
		"enableLoadExtension", "loadExtension", "backup":
		return Value{}, fmt.Errorf("%d:%d: DatabaseSync.%s is not supported: %s", pos.Line, pos.Col, method, sqliteUnsupportedReason(method))
	}
	return Value{}, fmt.Errorf("%d:%d: DatabaseSync has no method '%s' (supported: exec/prepare/close/open/location/function)", pos.Line, pos.Col, method)
}

// emitSQLiteStatementMethod dispatches stmt.get/all/run.
func (e *Emitter) emitSQLiteStatementMethod(objExpr ast.Expression, method string, typeArgs []*ast.TypeAnnotation, args []ast.Expression, pos ast.Pos) (Value, error) {
	objVal, err := e.emitExpr(objExpr)
	if err != nil {
		return Value{}, err
	}
	stmtHandle := e.loadFieldValue(objVal, e.sqliteFieldIdx(objVal.Ty, "__kml_handle"), TypePtr).Ref
	dbHandle := e.loadFieldValue(objVal, e.sqliteFieldIdx(objVal.Ty, "__kml_db"), TypePtr).Ref

	// The query methods reset the prepared statement and rebind parameters;
	// the metadata/config methods (columns/setReadBigInts/…) take no SQL params.
	switch method {
	case "run", "get", "all", "iterate":
		rst := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i32 @sqlite3_reset(ptr %s)", rst, stmtHandle))
		if err := e.emitSQLiteBindParams(stmtHandle, args); err != nil {
			return Value{}, err
		}
	}

	switch method {
	case "run":
		rc := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i32 @sqlite3_step(ptr %s)", rc, stmtHandle))
		e.emitSQLiteThrowIfStepError(dbHandle, rc)
		ch := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @sqlite3_changes64(ptr %s)", ch, dbHandle))
		rid := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @sqlite3_last_insert_rowid(ptr %s)", rid, dbHandle))
		chD := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = sitofp i64 %s to double", chD, ch))
		ridD := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = sitofp i64 %s to double", ridD, rid))
		resTy := SQLiteRunResultType()
		obj := e.freshReg()
		e.ensureMalloc()
		e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", obj, resTy.StructSize()))
		e.storeSQLiteField(resTy, obj, "changes", "double", chD)
		e.storeSQLiteField(resTy, obj, "lastInsertRowid", "double", ridD)
		return Value{Ref: obj, Ty: resTy}, nil

	case "get":
		rowTy, err := e.sqliteRowType(typeArgs, "get", pos)
		if err != nil {
			return Value{}, err
		}
		rc := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i32 @sqlite3_step(ptr %s)", rc, stmtHandle))
		e.emitSQLiteThrowIfStepError(dbHandle, rc)
		isRow := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, %d", isRow, rc, sqliteRow))
		res := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", res))
		rowL := e.freshLabel("sqlite.get.row")
		noRowL := e.freshLabel("sqlite.get.norow")
		mergeL := e.freshLabel("sqlite.get.merge")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isRow, rowL, noRowL))
		e.emitLabel(rowL)
		obj, err := e.emitSQLiteBuildRow(stmtHandle, rowTy)
		if err != nil {
			return Value{}, err
		}
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", obj, res))
		e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))
		e.emitLabel(noRowL)
		e.emitInstr(fmt.Sprintf("store ptr null, ptr %s, align 8", res))
		e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))
		e.emitLabel(mergeL)
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", r, res))
		rowTy.Nullable = true
		return Value{Ref: r, Ty: rowTy}, nil

	case "all":
		rowTy, err := e.sqliteRowType(typeArgs, "all", pos)
		if err != nil {
			return Value{}, err
		}
		return e.emitSQLiteAllRows(stmtHandle, dbHandle, rowTy)

	case "iterate":
		// V1: materialised eagerly (same as all<T>()), so `for (const r of
		// stmt.iterate<T>())` works; a lazy .next() iterator is a later stage.
		rowTy, err := e.sqliteRowType(typeArgs, "iterate", pos)
		if err != nil {
			return Value{}, err
		}
		return e.emitSQLiteAllRows(stmtHandle, dbHandle, rowTy)

	case "columns":
		return e.emitSQLiteColumns(stmtHandle)

	case "setReadBigInts", "setAllowBareNamedParameters":
		// Accepted for API completeness. With statically-typed rows the field
		// type governs integer representation, and bare named parameters are
		// always accepted — so these are effectively no-ops (documented).
		return Value{Ty: TypeVoid}, nil
	}
	return Value{}, fmt.Errorf("%d:%d: StatementSync has no method '%s' (supported: get/all/run/iterate/columns/setReadBigInts/setAllowBareNamedParameters)", pos.Line, pos.Col, method)
}

// emitSQLiteAllRows steps the statement to completion, collecting each row as a
// typed object into a {ptr,i64} array. Shared by all() and iterate().
func (e *Emitter) emitSQLiteAllRows(stmtHandle, dbHandle string, rowTy Type) (Value, error) {
	{
		e.ensureRealloc()
		dataPtr := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", dataPtr))
		e.emitInstr(fmt.Sprintf("store ptr null, ptr %s, align 8", dataPtr))
		lenPtr := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", lenPtr))
		e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", lenPtr))

		loopL := e.freshLabel("sqlite.all.loop")
		bodyL := e.freshLabel("sqlite.all.body")
		doneChkL := e.freshLabel("sqlite.all.donechk")
		endL := e.freshLabel("sqlite.all.end")
		e.emitTerminator(fmt.Sprintf("br label %%%s", loopL))
		e.emitLabel(loopL)
		rc := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i32 @sqlite3_step(ptr %s)", rc, stmtHandle))
		isRow := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, %d", isRow, rc, sqliteRow))
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isRow, bodyL, doneChkL))

		e.emitLabel(bodyL)
		obj, err := e.emitSQLiteBuildRow(stmtHandle, rowTy)
		if err != nil {
			return Value{}, err
		}
		curPtr := e.freshReg()
		curLen := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", curPtr, dataPtr))
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", curLen, lenPtr))
		newLen := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", newLen, curLen))
		newBytes := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = mul i64 %s, 8", newBytes, newLen))
		newPtr := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @realloc(ptr %s, i64 %s)", newPtr, curPtr, newBytes))
		slot := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 %s", slot, newPtr, curLen))
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", obj, slot))
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", newPtr, dataPtr))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", newLen, lenPtr))
		e.emitTerminator(fmt.Sprintf("br label %%%s", loopL))

		e.emitLabel(doneChkL)
		isDone := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, %d", isDone, rc, sqliteDone))
		badStep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = xor i1 %s, true", badStep, isDone))
		e.emitSQLiteThrowOnCond(dbHandle, badStep)
		e.emitTerminator(fmt.Sprintf("br label %%%s", endL))

		e.emitLabel(endL)
		finalPtr := e.freshReg()
		finalLen := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", finalPtr, dataPtr))
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", finalLen, lenPtr))
		r0 := e.freshReg()
		r1 := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} undef, ptr %s, 0", r0, finalPtr))
		e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} %s, i64 %s, 1", r1, r0, finalLen))
		return Value{Ref: r1, Ty: ArrayOf(rowTy)}, nil
	}
}

// emitSQLiteColumns implements stmt.columns() → one metadata object per result
// column. Origin column/table/database need SQLITE_ENABLE_COLUMN_METADATA
// (absent from the system libsqlite3 on the target platforms), so those read
// back null — matching node:sqlite's own null for non-table columns; `name`
// (result name) and `type` (declared type) are always available.
func (e *Emitter) emitSQLiteColumns(stmtHandle string) (Value, error) {
	e.ensureMalloc()
	e.ensureRealloc()
	metaTy := SQLiteColumnMetaType()
	count := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i32 @sqlite3_column_count(ptr %s)", count, stmtHandle))
	dataPtr := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", dataPtr))
	e.emitInstr(fmt.Sprintf("store ptr null, ptr %s, align 8", dataPtr))
	iSlot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i32, align 4", iSlot))
	e.emitInstr(fmt.Sprintf("store i32 0, ptr %s, align 4", iSlot))

	loopL := e.freshLabel("sqlite.cols.loop")
	bodyL := e.freshLabel("sqlite.cols.body")
	endL := e.freshLabel("sqlite.cols.end")
	e.emitTerminator(fmt.Sprintf("br label %%%s", loopL))
	e.emitLabel(loopL)
	iv := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i32, ptr %s, align 4", iv, iSlot))
	cond := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp slt i32 %s, %s", cond, iv, count))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", cond, bodyL, endL))

	e.emitLabel(bodyL)
	nameRaw := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @sqlite3_column_name(ptr %s, i32 %s)", nameRaw, stmtHandle, iv))
	nameStr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_str_from_cstr(ptr %s)", nameStr, nameRaw))
	declRaw := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @sqlite3_column_decltype(ptr %s, i32 %s)", declRaw, stmtHandle, iv))
	declNull := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", declNull, declRaw))
	typeStr, err := e.emitStrBranch(declNull,
		func() (string, error) { return "null", nil },
		func() (string, error) {
			r := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_str_from_cstr(ptr %s)", r, declRaw))
			return r, nil
		})
	if err != nil {
		return Value{}, err
	}
	obj := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", obj, metaTy.StructSize()))
	e.storeSQLiteField(metaTy, obj, "column", "ptr", "null")
	e.storeSQLiteField(metaTy, obj, "database", "ptr", "null")
	e.storeSQLiteField(metaTy, obj, "table", "ptr", "null")
	e.storeSQLiteField(metaTy, obj, "type", "ptr", typeStr)
	e.storeSQLiteField(metaTy, obj, "name", "ptr", nameStr)
	// append
	curPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", curPtr, dataPtr))
	i64idx := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sext i32 %s to i64", i64idx, iv))
	newLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", newLen, i64idx))
	newBytes := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = mul i64 %s, 8", newBytes, newLen))
	newPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @realloc(ptr %s, i64 %s)", newPtr, curPtr, newBytes))
	slot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 %s", slot, newPtr, i64idx))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", obj, slot))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", newPtr, dataPtr))
	iv1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i32 %s, 1", iv1, iv))
	e.emitInstr(fmt.Sprintf("store i32 %s, ptr %s, align 4", iv1, iSlot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", loopL))

	e.emitLabel(endL)
	finalPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", finalPtr, dataPtr))
	n64 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sext i32 %s to i64", n64, count))
	r0 := e.freshReg()
	r1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} undef, ptr %s, 0", r0, finalPtr))
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} %s, i64 %s, 1", r1, r0, n64))
	return Value{Ref: r1, Ty: ArrayOf(metaTy)}, nil
}

// sqliteUnsupportedReason explains a deliberately-unimplemented DatabaseSync
// member — surfaced in the compile error so the gap is self-documenting.
func sqliteUnsupportedReason(method string) string {
	switch method {
	case "aggregate":
		return "user-defined aggregate functions are a later stage (scalar db.function() is supported)"
	case "createSession", "applyChangeset", "applyChangesetSync":
		return "the session/changeset extension is not compiled into the system libsqlite3 on the target platforms"
	case "enableLoadExtension", "loadExtension":
		return "runtime extension loading is disabled in the system libsqlite3 (and is a native-plugin security surface)"
	case "backup":
		return "the async backup() API awaits event-loop integration"
	}
	return "not implemented"
}

// sqliteUDFScalarIR maps a supported UDF parameter/return type to the LLVM
// scalar IR used at the closure-call boundary, or ("", false) if unsupported.
func sqliteUDFArgKind(t Type) string {
	switch {
	case t.IsBigInt:
		return "bigint"
	case t.IR == "double":
		return "double"
	case t.IsArray || t.IsTypedArray || t.IsObject || t.IsMap || t.IsSet:
		return ""
	case t.IR == "ptr":
		return "string"
	case t.IR == "i64" || t.IR == "i32" || t.IR == "i16" || t.IR == "i8":
		return "int"
	}
	return ""
}

// emitSQLiteFunction implements db.function(name[, options], fn) for scalar
// user-defined functions: a per-registration trampoline (generated below)
// converts each sqlite3_value to the closure's declared parameter type, invokes
// the closure (retrieved from sqlite3_user_data), and reports the result.
func (e *Emitter) emitSQLiteFunction(dbHandle string, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) < 2 {
		return Value{}, fmt.Errorf("%d:%d: DatabaseSync.function needs a name and a function", pos.Line, pos.Col)
	}
	nameVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	nameVal = e.coerce(nameVal, TypePtr)
	fnExpr := args[len(args)-1]
	cb, err := e.resolveCallbackWithHints(fnExpr, nil)
	if err != nil {
		return Value{}, err
	}
	if cb.kind != cbClosure || !cb.ty.IsFunc {
		return Value{}, fmt.Errorf("%d:%d: DatabaseSync.function's last argument must be an arrow function or closure", pos.Line, pos.Col)
	}
	params := cb.ty.FuncParams
	var ret Type = TypeVoid
	if cb.ty.FuncRetType != nil {
		ret = *cb.ty.FuncRetType
	}
	// Validate the signature is a supported scalar shape.
	for _, p := range params {
		if sqliteUDFArgKind(p) == "" {
			return Value{}, fmt.Errorf("%d:%d: DatabaseSync.function parameter type '%s' is unsupported (use number, integer, bigint, or string)", pos.Line, pos.Col, p.IR)
		}
	}
	if ret.IR != "void" && sqliteUDFArgKind(ret) == "" {
		return Value{}, fmt.Errorf("%d:%d: DatabaseSync.function return type '%s' is unsupported (use number, integer, bigint, string, or void)", pos.Line, pos.Col, ret.IR)
	}

	e.sqliteUDFCtr++
	trampName := fmt.Sprintf("@__kml_sqlite_udf_%d", e.sqliteUDFCtr)
	e.emitSQLiteUDFTrampoline(trampName, params, ret)

	// SQLITE_UTF8 = 1. Register with the closure header as user data.
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i32 @sqlite3_create_function_v2(ptr %s, ptr %s, i32 %d, i32 1, ptr %s, ptr %s, ptr null, ptr null, ptr null)",
		r, dbHandle, nameVal.Ref, len(params), cb.hdrPtr, trampName))
	bad := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne i32 %s, %d", bad, r, sqliteOK))
	e.emitSQLiteThrowOnCond(dbHandle, bad)
	return Value{Ty: TypeVoid}, nil
}

// emitSQLiteUDFTrampoline defines a C-callable
// `void(sqlite3_context*, int, sqlite3_value**)` that unpacks the closure from
// sqlite3_user_data, converts each argument to the closure's parameter type,
// calls it, and forwards the result. Follows the sort-comparator trampoline
// pattern (runtime_collections.go) but specialised per registration.
func (e *Emitter) emitSQLiteUDFTrampoline(name string, params []Type, ret Type) {
	restore := e.beginThunkEmit()
	// Retrieve the closure header from user data, then its {fnptr, env}.
	e.emitInstr("%clos = call ptr @sqlite3_user_data(ptr %ctx)")
	e.emitInstr("%fp_slot = getelementptr {ptr, ptr}, ptr %clos, i32 0, i32 0")
	fp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %%fp_slot, align 8", fp))
	e.emitInstr("%ep_slot = getelementptr {ptr, ptr}, ptr %clos, i32 0, i32 1")
	ep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %%ep_slot, align 8", ep))

	argTys := []string{"ptr"}
	argRefs := []string{"ptr " + ep}
	for i, p := range params {
		slot := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %%argv, i64 %d", slot, i))
		val := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", val, slot))
		switch sqliteUDFArgKind(p) {
		case "double":
			a := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call double @sqlite3_value_double(ptr %s)", a, val))
			argTys = append(argTys, "double")
			argRefs = append(argRefs, "double "+a)
		case "string":
			c := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call ptr @sqlite3_value_text(ptr %s)", c, val))
			s := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_str_from_cstr(ptr %s)", s, c))
			argTys = append(argTys, "ptr")
			argRefs = append(argRefs, "ptr "+s)
		case "bigint":
			e.ensureBigInt()
			a := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call i64 @sqlite3_value_int64(ptr %s)", a, val))
			bi := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_bigint_from_i64(i64 %s)", bi, a))
			argTys = append(argTys, "ptr")
			argRefs = append(argRefs, "ptr "+bi)
		default: // int
			a := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call i64 @sqlite3_value_int64(ptr %s)", a, val))
			ci := a
			if p.IR != "i64" {
				ci = e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = trunc i64 %s to %s", ci, a, p.IR))
			}
			argTys = append(argTys, p.IR)
			argRefs = append(argRefs, p.IR+" "+ci)
		}
	}
	fnTypePart := "(" + strings.Join(argTys, ", ") + ")"
	if ret.IR == "void" {
		e.emitInstr(fmt.Sprintf("call void %s %s(%s)", fnTypePart, fp, strings.Join(argRefs, ", ")))
		e.emitInstr("call void @sqlite3_result_null(ptr %ctx)")
	} else {
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call %s %s %s(%s)", r, ret.LLVMRetType(), fnTypePart, fp, strings.Join(argRefs, ", ")))
		switch sqliteUDFArgKind(ret) {
		case "double":
			e.emitInstr(fmt.Sprintf("call void @sqlite3_result_double(ptr %%ctx, double %s)", r))
		case "string":
			e.emitInstr(fmt.Sprintf("call void @sqlite3_result_text(ptr %%ctx, ptr %s, i32 -1, ptr inttoptr (i64 -1 to ptr))", r))
		case "bigint":
			e.ensureBigInt()
			iv := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_bigint_to_i64(ptr %s)", iv, r))
			e.emitInstr(fmt.Sprintf("call void @sqlite3_result_int64(ptr %%ctx, i64 %s)", iv))
		default: // int
			iv := r
			if ret.IR != "i64" {
				iv = e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = sext %s %s to i64", iv, ret.IR, r))
			}
			e.emitInstr(fmt.Sprintf("call void @sqlite3_result_int64(ptr %%ctx, i64 %s)", iv))
		}
	}
	e.emitInstr("ret void")
	body := e.allocas.String() + e.body.String()
	restore()
	e.functions.WriteString(fmt.Sprintf("\ndefine void %s(ptr %%ctx, i32 %%argc, ptr %%argv) {\nentry:\n%s}\n", name, body))
}

// sqliteRowType resolves the required call-site row-shape type argument.
func (e *Emitter) sqliteRowType(typeArgs []*ast.TypeAnnotation, method string, pos ast.Pos) (Type, error) {
	if len(typeArgs) != 1 {
		return Type{}, fmt.Errorf("%d:%d: StatementSync.%s needs an explicit row type argument (e.g. stmt.%s<{ id: number, name: string }>()); untyped rows await a later dynamic-object mode", pos.Line, pos.Col, method, method)
	}
	rowTy := e.resolveType(typeArgs[0])
	if !rowTy.IsObject || len(rowTy.Fields) == 0 {
		return Type{}, fmt.Errorf("%d:%d: StatementSync.%s row type must be an object type with named fields", pos.Line, pos.Col, method)
	}
	return rowTy, nil
}

// emitSQLiteBindParams binds call arguments. A single object-literal argument
// binds named parameters (`:name`/`@name`/`$name`, or the bare key —
// setAllowBareNamedParameters is on by default in Node); otherwise trailing
// arguments bind positionally to ?1, ?2, …
func (e *Emitter) emitSQLiteBindParams(stmtHandle string, args []ast.Expression) error {
	if len(args) == 1 {
		if lit, ok := args[0].(*ast.ObjectLiteral); ok {
			return e.emitSQLiteBindNamed(stmtHandle, lit)
		}
	}
	for i, arg := range args {
		if err := e.emitSQLiteBindOne(stmtHandle, fmt.Sprintf("%d", i+1), arg); err != nil {
			return err
		}
	}
	return nil
}

// emitSQLiteBindNamed binds each property of an object literal to the matching
// named parameter, resolving `:key`/`@key`/`$key`/`key` at runtime.
func (e *Emitter) emitSQLiteBindNamed(stmtHandle string, lit *ast.ObjectLiteral) error {
	for _, p := range lit.Properties {
		if p.KeyExpr != nil || p.Key == "" {
			return fmt.Errorf("node:sqlite: named-parameter object needs plain string keys (no computed keys or spreads)")
		}
		// First non-zero of the four candidate spellings.
		idx := "0"
		for _, cand := range []string{":" + p.Key, "@" + p.Key, "$" + p.Key, p.Key} {
			r := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call i32 @sqlite3_bind_parameter_index(ptr %s, ptr %s)", r, stmtHandle, e.internString(cand)))
			if idx == "0" {
				idx = r
				continue
			}
			cond := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = icmp ne i32 %s, 0", cond, idx))
			sel := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = select i1 %s, i32 %s, i32 %s", sel, cond, idx, r))
			idx = sel
		}
		// Unknown named parameter → throw, matching Node.
		missing := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, 0", missing, idx))
		missL := e.freshLabel("sqlite.param.miss")
		okL := e.freshLabel("sqlite.param.ok")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", missing, missL, okL))
		e.emitLabel(missL)
		e.emitInternalThrow(e.internString(fmt.Sprintf("node:sqlite: unknown named parameter '%s'", p.Key)))
		e.emitLabel(okL)
		if err := e.emitSQLiteBindOne(stmtHandle, idx, p.Value); err != nil {
			return err
		}
	}
	return nil
}

// emitSQLiteBindOne binds a single value expression at the given 1-based index
// (a literal or an i32 register).
func (e *Emitter) emitSQLiteBindOne(stmtHandle, idx string, arg ast.Expression) error {
	if _, ok := arg.(*ast.NullLiteral); ok {
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i32 @sqlite3_bind_null(ptr %s, i32 %s)", r, stmtHandle, idx))
		return nil
	}
	v, err := e.emitExpr(arg)
	if err != nil {
		return err
	}
	if v.Ty.IsArray || v.Ty.IsTypedArray {
		// Uint8Array → BLOB. Extract the {ptr,i64} data pointer and length.
		dptr := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 0", dptr, v.Ref))
		dlen := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 1", dlen, v.Ref))
		dlen32 := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = trunc i64 %s to i32", dlen32, dlen))
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i32 @sqlite3_bind_blob(ptr %s, i32 %s, ptr %s, i32 %s, ptr inttoptr (i64 -1 to ptr))", r, stmtHandle, idx, dptr, dlen32))
		return nil
	}
	if v.Ty.IsBigInt {
		e.ensureBigInt()
		i64r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_bigint_to_i64(ptr %s)", i64r, v.Ref))
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i32 @sqlite3_bind_int64(ptr %s, i32 %s, i64 %s)", r, stmtHandle, idx, i64r))
		return nil
	}
	switch v.Ty.IR {
	case "double":
		// Node binds an integral `number` as INTEGER, a fractional one as REAL.
		asI := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = fptosi double %s to i64", asI, v.Ref))
		back := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = sitofp i64 %s to double", back, asI))
		isInt := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = fcmp oeq double %s, %s", isInt, v.Ref, back))
		intL := e.freshLabel("sqlite.bind.int")
		dblL := e.freshLabel("sqlite.bind.dbl")
		contL := e.freshLabel("sqlite.bind.cont")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isInt, intL, dblL))
		e.emitLabel(intL)
		ri := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i32 @sqlite3_bind_int64(ptr %s, i32 %s, i64 %s)", ri, stmtHandle, idx, asI))
		e.emitTerminator(fmt.Sprintf("br label %%%s", contL))
		e.emitLabel(dblL)
		rd := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i32 @sqlite3_bind_double(ptr %s, i32 %s, double %s)", rd, stmtHandle, idx, v.Ref))
		e.emitTerminator(fmt.Sprintf("br label %%%s", contL))
		e.emitLabel(contL)
	case "ptr":
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i32 @sqlite3_bind_text(ptr %s, i32 %s, ptr %s, i32 -1, ptr inttoptr (i64 -1 to ptr))", r, stmtHandle, idx, v.Ref))
	case "i1":
		z := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = zext i1 %s to i64", z, v.Ref))
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i32 @sqlite3_bind_int64(ptr %s, i32 %s, i64 %s)", r, stmtHandle, idx, z))
	default:
		iv := e.coerce(v, TypeI64)
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i32 @sqlite3_bind_int64(ptr %s, i32 %s, i64 %s)", r, stmtHandle, idx, iv.Ref))
	}
	return nil
}

// emitSQLiteBuildRow projects the current result row onto rowTy's declared
// fields, matching each field to a column by name.
func (e *Emitter) emitSQLiteBuildRow(stmtHandle string, rowTy Type) (string, error) {
	e.ensureMalloc()
	obj := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", obj, rowTy.StructSize()))
	structIR := rowTy.StructIR()
	for _, f := range rowTy.Fields {
		colIdx := e.emitSQLiteColumnIndex(stmtHandle, e.internString(f.Name))
		// Missing column → throw (typed-row contract: declared fields must be
		// selected). Guard before extraction.
		neg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp slt i32 %s, 0", neg, colIdx))
		missL := e.freshLabel("sqlite.col.miss")
		okL := e.freshLabel("sqlite.col.ok")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", neg, missL, okL))
		e.emitLabel(missL)
		e.emitInternalThrow(e.internString(fmt.Sprintf("node:sqlite: column '%s' not present in result row", f.Name)))
		e.emitLabel(okL)

		valRef, err := e.emitSQLiteReadColumn(stmtHandle, colIdx, f.Ty)
		if err != nil {
			return "", err
		}
		idx, fieldTy, _ := rowTy.FieldIndex(f.Name)
		gep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, structIR, obj, idx))
		// Array/typed-array fields (BLOB → Uint8Array) are a {ptr,i64} aggregate
		// slot, not a bare ptr — store with the struct-field IR.
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", StructFieldIR(fieldTy), valRef, gep, fieldTy.Align()))
	}
	return obj, nil
}

// emitSQLiteReadColumn extracts column colIdx as fieldTy, applying the storage-
// class → JS value mapping. Returns a register of fieldTy.IR.
func (e *Emitter) emitSQLiteReadColumn(stmtHandle, colIdx string, fieldTy Type) (string, error) {
	// A nullable-scalar field (number|null, integer|null, boolean|null) reads a
	// SQL NULL as absent in its { i1, T } slot — otherwise NULL would silently
	// become 0. The payload is read unconditionally (0 for NULL, but the
	// presence bit hides it).
	if isNullableScalar(fieldTy) {
		isNull := e.emitSQLiteColIsNull(stmtHandle, colIdx)
		present := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = xor i1 %s, true", present, isNull))
		bare, err := e.emitSQLiteReadColumn(stmtHandle, colIdx, fieldTy.withoutNullable())
		if err != nil {
			return "", err
		}
		return e.makeNullableScalarAgg(fieldTy, present, bare), nil
	}
	// A nullable bigint field: SQL NULL → a null pointer (bigint is heap).
	if fieldTy.Nullable && fieldTy.IsBigInt {
		isNull := e.emitSQLiteColIsNull(stmtHandle, colIdx)
		return e.emitStrBranch(isNull,
			func() (string, error) { return "null", nil },
			func() (string, error) {
				return e.emitSQLiteReadColumn(stmtHandle, colIdx, fieldTy.withoutNullable())
			})
	}
	// A nullable BLOB field: SQL NULL → a null Uint8Array ({null, 0}).
	if fieldTy.Nullable && fieldTy.IsTypedArray {
		isNull := e.emitSQLiteColIsNull(stmtHandle, colIdx)
		nullAgg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} { ptr null, i64 0 }, ptr null, 0", nullAgg))
		full, err := e.emitSQLiteReadColumn(stmtHandle, colIdx, fieldTy.withoutNullable())
		if err != nil {
			return "", err
		}
		sel := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, {ptr, i64} %s, {ptr, i64} %s", sel, isNull, nullAgg, full))
		return sel, nil
	}
	switch {
	case fieldTy.IsTypedArray:
		// BLOB → Uint8Array. column_blob's pointer is invalidated by the next
		// step/reset, so copy the bytes into a fresh heap buffer.
		e.ensureMalloc()
		e.ensureMemcpy()
		raw := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @sqlite3_column_blob(ptr %s, i32 %s)", raw, stmtHandle, colIdx))
		nBytes := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i32 @sqlite3_column_bytes(ptr %s, i32 %s)", nBytes, stmtHandle, colIdx))
		n64 := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = sext i32 %s to i64", n64, nBytes))
		data := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %s)", data, n64))
		e.emitInstr(fmt.Sprintf("call ptr @memcpy(ptr %s, ptr %s, i64 %s)", data, raw, n64))
		r0 := e.freshReg()
		r1 := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} undef, ptr %s, 0", r0, data))
		e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} %s, i64 %s, 1", r1, r0, n64))
		return r1, nil
	case fieldTy.IsBigInt:
		// INTEGER → bigint. The compiler's BigInt is arbitrary-precision; seed it
		// from the i64 column value.
		e.ensureBigInt()
		cv := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @sqlite3_column_int64(ptr %s, i32 %s)", cv, stmtHandle, colIdx))
		bi := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_bigint_from_i64(i64 %s)", bi, cv))
		return bi, nil
	case fieldTy.IR == "double":
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call double @sqlite3_column_double(ptr %s, i32 %s)", r, stmtHandle, colIdx))
		return r, nil
	case fieldTy.IR == "i1":
		cv := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @sqlite3_column_int64(ptr %s, i32 %s)", cv, stmtHandle, colIdx))
		b := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", b, cv))
		return b, nil
	case fieldTy.IR == "ptr" && !fieldTy.IsObject && !fieldTy.IsMap && !fieldTy.IsSet && !fieldTy.IsTypedArray:
		// TEXT (or NULL) → string | null. column_text returns null for a SQL NULL.
		raw := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @sqlite3_column_text(ptr %s, i32 %s)", raw, stmtHandle, colIdx))
		isNull := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isNull, raw))
		return e.emitStrBranch(isNull,
			func() (string, error) { return "null", nil },
			func() (string, error) {
				r := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_str_from_cstr(ptr %s)", r, raw))
				return r, nil
			})
	case fieldTy.IR == "i64" || fieldTy.IR == "i32" || fieldTy.IR == "i16" || fieldTy.IR == "i8":
		cv := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @sqlite3_column_int64(ptr %s, i32 %s)", cv, stmtHandle, colIdx))
		if fieldTy.IR == "i64" {
			return cv, nil
		}
		t := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = trunc i64 %s to %s", t, cv, fieldTy.IR))
		return t, nil
	}
	return "", fmt.Errorf("node:sqlite: unsupported row field type '%s' (V1 supports number, integer, string, boolean; BLOB→Uint8Array is a later stage)", fieldTy.IR)
}

// emitSQLiteColIsNull returns an i1 register: whether column colIdx holds a SQL
// NULL (storage class SQLITE_NULL == 5).
func (e *Emitter) emitSQLiteColIsNull(stmtHandle, colIdx string) string {
	ct := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i32 @sqlite3_column_type(ptr %s, i32 %s)", ct, stmtHandle, colIdx))
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, 5", r, ct))
	return r
}

// emitSQLiteColumnIndex returns an i32 register: the index of the column whose
// name matches nameReg (a C string), or -1.
func (e *Emitter) emitSQLiteColumnIndex(stmtHandle, nameReg string) string {
	count := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i32 @sqlite3_column_count(ptr %s)", count, stmtHandle))
	iSlot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i32, align 4", iSlot))
	e.emitInstr(fmt.Sprintf("store i32 0, ptr %s, align 4", iSlot))
	resSlot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i32, align 4", resSlot))
	e.emitInstr(fmt.Sprintf("store i32 -1, ptr %s, align 4", resSlot))

	loopL := e.freshLabel("sqlite.colidx.loop")
	bodyL := e.freshLabel("sqlite.colidx.body")
	nextL := e.freshLabel("sqlite.colidx.next")
	foundL := e.freshLabel("sqlite.colidx.found")
	doneL := e.freshLabel("sqlite.colidx.done")
	e.emitTerminator(fmt.Sprintf("br label %%%s", loopL))
	e.emitLabel(loopL)
	iv := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i32, ptr %s, align 4", iv, iSlot))
	cond := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp slt i32 %s, %s", cond, iv, count))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", cond, bodyL, doneL))
	e.emitLabel(bodyL)
	cname := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @sqlite3_column_name(ptr %s, i32 %s)", cname, stmtHandle, iv))
	cmp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i32 @strcmp(ptr %s, ptr %s)", cmp, cname, nameReg))
	eqz := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, 0", eqz, cmp))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", eqz, foundL, nextL))
	e.emitLabel(foundL)
	e.emitInstr(fmt.Sprintf("store i32 %s, ptr %s, align 4", iv, resSlot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(nextL)
	iv1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i32 %s, 1", iv1, iv))
	e.emitInstr(fmt.Sprintf("store i32 %s, ptr %s, align 4", iv1, iSlot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", loopL))
	e.emitLabel(doneL)
	res := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i32, ptr %s, align 4", res, resSlot))
	return res
}

// emitSQLiteThrowIfStepError throws when a step result is neither SQLITE_ROW
// nor SQLITE_DONE.
func (e *Emitter) emitSQLiteThrowIfStepError(dbHandle, rc string) {
	isRow := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, %d", isRow, rc, sqliteRow))
	isDone := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, %d", isDone, rc, sqliteDone))
	ok := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", ok, isRow, isDone))
	bad := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = xor i1 %s, true", bad, ok))
	e.emitSQLiteThrowOnCond(dbHandle, bad)
}

// emitSQLiteThrowOnCond throws a KML Error carrying sqlite3_errmsg(dbHandle)
// when condReg is true, then continues straight-line code.
func (e *Emitter) emitSQLiteThrowOnCond(dbHandle, condReg string) {
	throwL := e.freshLabel("sqlite.err")
	contL := e.freshLabel("sqlite.cont")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", condReg, throwL, contL))
	e.emitLabel(throwL)
	raw := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @sqlite3_errmsg(ptr %s)", raw, dbHandle))
	msg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_str_from_cstr(ptr %s)", msg, raw))
	// Node attaches code='ERR_SQLITE_ERROR', a numeric errcode (the extended
	// result code), and errstr (its text) to the thrown Error.
	ec := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i32 @sqlite3_extended_errcode(ptr %s)", ec, dbHandle))
	ecD := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sitofp i32 %s to double", ecD, ec))
	esRaw := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @sqlite3_errstr(i32 %s)", esRaw, ec))
	esStr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_str_from_cstr(ptr %s)", esStr, esRaw))
	errReg := e.buildErrorObjWithCode(0, msg, e.internString("Error"), e.internString("ERR_SQLITE_ERROR"), ecD, esStr)
	e.emitInstr(fmt.Sprintf("call void @__kml_throw(ptr %s)", errReg))
	e.emitTerminator("unreachable")
	e.emitLabel(contL)
}

// storeSQLiteField stores rawIR (a register or literal of the given IR type)
// into obj's named field.
func (e *Emitter) storeSQLiteField(ty Type, obj, name, ir, rawIR string) {
	idx, fieldTy, _ := ty.FieldIndex(name)
	gep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, ty.StructIR(), obj, idx))
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", ir, rawIR, gep, fieldTy.Align()))
}

func (e *Emitter) sqliteFieldIdx(ty Type, name string) int {
	idx, _, _ := ty.FieldIndex(name)
	return idx
}

// --- static options readers (V1: object-literal options only) ---

func sqliteStaticBoolOption(opts ast.Expression, key string) bool {
	v, _ := sqliteStaticBoolOptionPresent(opts, key)
	return v
}

func sqliteStaticBoolOptionPresent(opts ast.Expression, key string) (val bool, present bool) {
	lit, ok := opts.(*ast.ObjectLiteral)
	if !ok {
		return false, false
	}
	for _, p := range lit.Properties {
		if p.KeyExpr == nil && p.Key == key {
			if b, ok := p.Value.(*ast.BooleanLiteral); ok {
				return b.Value, true
			}
		}
	}
	return false, false
}

func sqliteStaticNumberOption(opts ast.Expression, key string) (val int, present bool) {
	lit, ok := opts.(*ast.ObjectLiteral)
	if !ok {
		return 0, false
	}
	for _, p := range lit.Properties {
		if p.KeyExpr == nil && p.Key == key {
			if n, ok := p.Value.(*ast.NumberLiteral); ok {
				var iv int
				if _, err := fmt.Sscanf(n.Value, "%d", &iv); err == nil {
					return iv, true
				}
			}
		}
	}
	return 0, false
}
