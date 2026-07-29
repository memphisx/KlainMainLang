package llvm

import ()

// ensureAnyEq declares __kml_any_eq: compares two boxed any/unknown values
// { i8 tag, i64 payload } for equality (backs === / !==). Equal-tag pairs
// compare directly per tag's meaning (string payloads are ptrtoint'd string
// pointers, so string/string compares via strcmp, not pointer identity;
// object/object compares by pointer, matching JS reference equality); an
// int/float tag mismatch (either order) is still a real numeric comparison,
// converting the int side to double first; any other tag mismatch is false.
// Tags: 0=int, 1=float, 2=string, 3=boolean, 4=null, 5=undefined, 6=object.
func (e *Emitter) ensureAnyEq() {
	if e.usedAnyEq {
		return
	}
	e.usedAnyEq = true
	e.ensureStrcmp()
	e.emitGlobal(`
define i1 @__kml_any_eq({ i8, i64 } %a, { i8, i64 } %b) {
entry:
  %tagA = extractvalue { i8, i64 } %a, 0
  %payA = extractvalue { i8, i64 } %a, 1
  %tagB = extractvalue { i8, i64 } %b, 0
  %payB = extractvalue { i8, i64 } %b, 1
  %same_tag = icmp eq i8 %tagA, %tagB
  br i1 %same_tag, label %same, label %cross_check
cross_check:
  %a_is_int = icmp eq i8 %tagA, 0
  %a_is_float = icmp eq i8 %tagA, 1
  %b_is_int = icmp eq i8 %tagB, 0
  %b_is_float = icmp eq i8 %tagB, 1
  %int_float = and i1 %a_is_int, %b_is_float
  %float_int = and i1 %a_is_float, %b_is_int
  %is_cross_numeric = or i1 %int_float, %float_int
  br i1 %is_cross_numeric, label %cross_numeric, label %not_equal
cross_numeric:
  %a_from_int = sitofp i64 %payA to double
  %a_from_float = bitcast i64 %payA to double
  %a_double = select i1 %a_is_int, double %a_from_int, double %a_from_float
  %b_from_int = sitofp i64 %payB to double
  %b_from_float = bitcast i64 %payB to double
  %b_double = select i1 %b_is_int, double %b_from_int, double %b_from_float
  %cross_eq = fcmp oeq double %a_double, %b_double
  ret i1 %cross_eq
same:
  %is_int = icmp eq i8 %tagA, 0
  br i1 %is_int, label %cmp_int, label %check_float
cmp_int:
  %int_eq = icmp eq i64 %payA, %payB
  ret i1 %int_eq
check_float:
  %is_float = icmp eq i8 %tagA, 1
  br i1 %is_float, label %cmp_float, label %check_string
cmp_float:
  %fa = bitcast i64 %payA to double
  %fb = bitcast i64 %payB to double
  %float_eq = fcmp oeq double %fa, %fb
  ret i1 %float_eq
check_string:
  %is_string = icmp eq i8 %tagA, 2
  br i1 %is_string, label %cmp_string, label %check_bool
cmp_string:
  %sa = inttoptr i64 %payA to ptr
  %sb = inttoptr i64 %payB to ptr
  %scmp = call i32 @strcmp(ptr %sa, ptr %sb)
  %string_eq = icmp eq i32 %scmp, 0
  ret i1 %string_eq
check_bool:
  %is_bool = icmp eq i8 %tagA, 3
  br i1 %is_bool, label %cmp_bool, label %check_null_undef
cmp_bool:
  %bool_eq = icmp eq i64 %payA, %payB
  ret i1 %bool_eq
check_null_undef:
  %is_null = icmp eq i8 %tagA, 4
  %is_undef = icmp eq i8 %tagA, 5
  %is_null_or_undef = or i1 %is_null, %is_undef
  br i1 %is_null_or_undef, label %ret_true, label %check_object
check_object:
  %is_object = icmp eq i8 %tagA, 6
  br i1 %is_object, label %cmp_object, label %not_equal
cmp_object:
  %oa = inttoptr i64 %payA to ptr
  %ob = inttoptr i64 %payB to ptr
  %obj_eq = icmp eq ptr %oa, %ob
  ret i1 %obj_eq
ret_true:
  ret i1 true
not_equal:
  ret i1 false
}`)
}
