package ast

import "reflect"

// Clone deep-copies the tree under n: every node, slice and map it reaches
// is new, positions included. A node reached twice (shared, or through a
// cycle) is copied once and shared the same way in the copy.
func Clone[T any](n T) T {
	seen := map[uintptr]reflect.Value{}
	v := reflect.ValueOf(&n).Elem()
	out := reflect.New(v.Type()).Elem()
	cloneInto(out, v, seen)
	return out.Interface().(T)
}

func cloneInto(dst, src reflect.Value, seen map[uintptr]reflect.Value) {
	switch src.Kind() {
	case reflect.Pointer:
		if src.IsNil() {
			return
		}
		if c, ok := seen[src.Pointer()]; ok {
			dst.Set(c)
			return
		}
		c := reflect.New(src.Type().Elem())
		seen[src.Pointer()] = c
		// The whole struct first (unexported fields such as a position
		// included), then each field a copy can reach is replaced.
		c.Elem().Set(src.Elem())
		cloneFields(c.Elem(), src.Elem(), seen)
		dst.Set(c)
	case reflect.Interface:
		if src.IsNil() {
			return
		}
		e := reflect.New(src.Elem().Type()).Elem()
		cloneInto(e, src.Elem(), seen)
		dst.Set(e)
	case reflect.Slice:
		if src.IsNil() {
			return
		}
		s := reflect.MakeSlice(src.Type(), src.Len(), src.Len())
		for i := 0; i < src.Len(); i++ {
			cloneInto(s.Index(i), src.Index(i), seen)
		}
		dst.Set(s)
	case reflect.Map:
		if src.IsNil() {
			return
		}
		m := reflect.MakeMapWithSize(src.Type(), src.Len())
		for it := src.MapRange(); it.Next(); {
			k := reflect.New(it.Key().Type()).Elem()
			cloneInto(k, it.Key(), seen)
			val := reflect.New(it.Value().Type()).Elem()
			cloneInto(val, it.Value(), seen)
			m.SetMapIndex(k, val)
		}
		dst.Set(m)
	case reflect.Struct:
		dst.Set(src)
		cloneFields(dst, src, seen)
	default:
		dst.Set(src)
	}
}

// cloneFields replaces dst's exported fields that hold references with
// copies of src's.
func cloneFields(dst, src reflect.Value, seen map[uintptr]reflect.Value) {
	for i := 0; i < src.NumField(); i++ {
		if !dst.Field(i).CanSet() {
			continue
		}
		switch src.Field(i).Kind() {
		case reflect.Pointer, reflect.Interface, reflect.Slice, reflect.Map, reflect.Struct:
			f := reflect.New(src.Field(i).Type()).Elem()
			cloneInto(f, src.Field(i), seen)
			dst.Field(i).Set(f)
		}
	}
}
