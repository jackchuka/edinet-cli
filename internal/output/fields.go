package output

import (
	"fmt"
	"reflect"
	"strings"
)

// fieldsOf returns the CSV column names for a record type.
//
// CSV is a flattening of the JSON rather than of the display table, so the two
// machine-readable formats can never disagree about a field. Deriving both from
// the same struct is what makes that guarantee structural rather than a
// convention someone has to remember.
func fieldsOf(t reflect.Type) []string {
	var names []string
	walkFields(t, func(name string, _ []int) {
		names = append(names, name)
	})
	return names
}

// valuesOf returns one CSV row for rec, ordered to match fieldsOf.
//
// Each field is stringified with fmt.Sprint, which only gives a sensible,
// spreadsheet-usable answer for strings, numbers, bools, and the embedded
// structs walkFields flattens; a slice, map, or non-embedded struct field
// would print Go's %v syntax into the cell instead of a value.
func valuesOf(rec any) []string {
	v := reflect.ValueOf(rec)
	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return nil
		}
		v = v.Elem()
	}

	var out []string
	walkFields(v.Type(), func(_ string, index []int) {
		out = append(out, fmt.Sprint(v.FieldByIndex(index).Interface()))
	})
	return out
}

// walkFields visits every column-worthy field, expanding embedded structs in
// place the way encoding/json does.
//
// An embedded field of unexported type is skipped rather than promoted, which
// is where this diverges from encoding/json: reflect propagates the read-only
// flag through such a field, so calling Interface() on anything reached through
// it panics. Every record type here embeds exported structs.
func walkFields(t reflect.Type, fn func(name string, index []int)) {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return
	}

	for i := range t.NumField() {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		if f.Anonymous && f.Type.Kind() == reflect.Struct {
			walkFields(f.Type, func(name string, index []int) {
				fn(name, append([]int{i}, index...))
			})
			continue
		}
		name, ok := jsonName(f)
		if !ok {
			continue
		}
		fn(name, []int{i})
	}
}

// jsonName returns a field's column name, or false when `json:"-"` excludes it.
func jsonName(f reflect.StructField) (string, bool) {
	tag, ok := f.Tag.Lookup("json")
	if !ok {
		return f.Name, true
	}
	name, _, _ := strings.Cut(tag, ",")
	switch name {
	case "-":
		return "", false
	case "":
		return f.Name, true
	}
	return name, true
}
