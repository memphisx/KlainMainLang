package llvm

// ensureSQLite3 declares the libsqlite3 C API surface node:sqlite's V1
// DatabaseSync/StatementSync uses (ADR-00540), and requires linking -lsqlite3.
// libsqlite3 ships on macOS (on clang's default search path) and on Linux via
// the distro's sqlite3 dev package — the same "a program only needs the
// library installed if it actually imports the module" posture fetch/libcurl
// established, so no LocateSQLite probe is needed: a bare -lsqlite3 resolves on
// both dev machines.
//
// Signatures verified against sqlite3.h. Result-code constants used by the
// emitter: SQLITE_OK=0, SQLITE_ROW=100, SQLITE_DONE=101. open flags:
// SQLITE_OPEN_READONLY=0x1, READWRITE=0x2, CREATE=0x4. column storage classes:
// SQLITE_INTEGER=1, FLOAT=2, TEXT=3, BLOB=4, NULL=5.
func (e *Emitter) ensureSQLite3() {
	if e.usedSQLite3 {
		return
	}
	e.usedSQLite3 = true
	e.requireLink("sqlite3")
	e.ensureStrHeaderRuntime() // __kml_str_from_cstr for TEXT columns / errmsg
	// strcmp for column-name → declared-field matching.
	e.emitGlobal("declare i32 @strcmp(ptr noundef, ptr noundef)")
	e.emitGlobal("declare i32 @sqlite3_open_v2(ptr noundef, ptr noundef, i32 noundef, ptr noundef)")
	e.emitGlobal("declare i32 @sqlite3_close_v2(ptr noundef)")
	e.emitGlobal("declare i32 @sqlite3_exec(ptr noundef, ptr noundef, ptr noundef, ptr noundef, ptr noundef)")
	e.emitGlobal("declare i32 @sqlite3_prepare_v2(ptr noundef, ptr noundef, i32 noundef, ptr noundef, ptr noundef)")
	e.emitGlobal("declare i32 @sqlite3_step(ptr noundef)")
	e.emitGlobal("declare i32 @sqlite3_reset(ptr noundef)")
	e.emitGlobal("declare i32 @sqlite3_finalize(ptr noundef)")
	e.emitGlobal("declare i32 @sqlite3_bind_int64(ptr noundef, i32 noundef, i64 noundef)")
	e.emitGlobal("declare i32 @sqlite3_bind_double(ptr noundef, i32 noundef, double noundef)")
	e.emitGlobal("declare i32 @sqlite3_bind_text(ptr noundef, i32 noundef, ptr noundef, i32 noundef, ptr noundef)")
	e.emitGlobal("declare i32 @sqlite3_bind_null(ptr noundef, i32 noundef)")
	e.emitGlobal("declare i32 @sqlite3_bind_parameter_index(ptr noundef, ptr noundef)")
	e.emitGlobal("declare i32 @sqlite3_column_count(ptr noundef)")
	e.emitGlobal("declare ptr @sqlite3_column_name(ptr noundef, i32 noundef)")
	e.emitGlobal("declare i32 @sqlite3_column_type(ptr noundef, i32 noundef)")
	e.emitGlobal("declare i64 @sqlite3_column_int64(ptr noundef, i32 noundef)")
	e.emitGlobal("declare double @sqlite3_column_double(ptr noundef, i32 noundef)")
	e.emitGlobal("declare ptr @sqlite3_column_text(ptr noundef, i32 noundef)")
	e.emitGlobal("declare i64 @sqlite3_changes64(ptr noundef)")
	e.emitGlobal("declare i64 @sqlite3_last_insert_rowid(ptr noundef)")
	e.emitGlobal("declare ptr @sqlite3_errmsg(ptr noundef)")
	e.emitGlobal("declare i32 @sqlite3_busy_timeout(ptr noundef, i32 noundef)")
	// BLOB read/bind (Stage A).
	e.emitGlobal("declare ptr @sqlite3_column_blob(ptr noundef, i32 noundef)")
	e.emitGlobal("declare i32 @sqlite3_column_bytes(ptr noundef, i32 noundef)")
	e.emitGlobal("declare i32 @sqlite3_bind_blob(ptr noundef, i32 noundef, ptr noundef, i32 noundef, ptr noundef)")
	// expandedSQL / error codes / transaction state / filename (Stage A).
	e.emitGlobal("declare ptr @sqlite3_expanded_sql(ptr noundef)")
	e.emitGlobal("declare void @sqlite3_free(ptr noundef)")
	e.emitGlobal("declare i32 @sqlite3_extended_errcode(ptr noundef)")
	e.emitGlobal("declare ptr @sqlite3_errstr(i32 noundef)")
	e.emitGlobal("declare i32 @sqlite3_get_autocommit(ptr noundef)")
	e.emitGlobal("declare ptr @sqlite3_db_filename(ptr noundef, ptr noundef)")
	// column metadata (Stage B).
	e.emitGlobal("declare ptr @sqlite3_column_decltype(ptr noundef, i32 noundef)")
	// user-defined scalar functions (Stage C).
	e.emitGlobal("declare i32 @sqlite3_create_function_v2(ptr noundef, ptr noundef, i32 noundef, i32 noundef, ptr noundef, ptr noundef, ptr noundef, ptr noundef, ptr noundef)")
	e.emitGlobal("declare ptr @sqlite3_user_data(ptr noundef)")
	e.emitGlobal("declare double @sqlite3_value_double(ptr noundef)")
	e.emitGlobal("declare i64 @sqlite3_value_int64(ptr noundef)")
	e.emitGlobal("declare ptr @sqlite3_value_text(ptr noundef)")
	e.emitGlobal("declare i32 @sqlite3_value_type(ptr noundef)")
	e.emitGlobal("declare void @sqlite3_result_double(ptr noundef, double noundef)")
	e.emitGlobal("declare void @sqlite3_result_int64(ptr noundef, i64 noundef)")
	e.emitGlobal("declare void @sqlite3_result_text(ptr noundef, ptr noundef, i32 noundef, ptr noundef)")
	e.emitGlobal("declare void @sqlite3_result_null(ptr noundef)")
}
