package output

import (
	"reflect"
	"testing"
)

// Inner is exported on purpose: reflect cannot read through an embedded field
// of unexported type without panicking, so that shape is out of scope. Every
// record type in this repo embeds exported structs.
type Inner struct {
	DocID string `json:"docId"`
	Seq   int    `json:"seqNumber"`
}

type outer struct {
	Inner
	Label  string `json:"label"`
	Hidden string `json:"-"`
	NoTag  string
	priv   string
}

func TestFieldsOf(t *testing.T) {
	tests := []struct {
		name string
		typ  reflect.Type
		want []string
	}{
		{
			name: "embedded structs are flattened in place",
			typ:  reflect.TypeFor[outer](),
			want: []string{"docId", "seqNumber", "label", "NoTag"},
		},
		{
			name: "plain struct keeps tag order",
			typ:  reflect.TypeFor[Inner](),
			want: []string{"docId", "seqNumber"},
		},
		{
			name: "non-struct has no columns",
			typ:  reflect.TypeFor[string](),
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := fieldsOf(tt.typ)
			if len(got) != len(tt.want) {
				t.Fatalf("fieldsOf = %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("column %d = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestValuesOfMatchesFieldsOf(t *testing.T) {
	rec := outer{
		Inner:  Inner{DocID: "S100AAAA", Seq: 3},
		Label:  "売上高",
		Hidden: "must not appear",
		NoTag:  "kept",
		priv:   "unexported",
	}

	names := fieldsOf(reflect.TypeFor[outer]())
	values := valuesOf(rec)
	if len(values) != len(names) {
		t.Fatalf("got %d values for %d columns: %v vs %v", len(values), len(names), values, names)
	}

	want := []string{"S100AAAA", "3", "売上高", "kept"}
	for i := range want {
		if values[i] != want[i] {
			t.Errorf("value %d (%s) = %q, want %q", i, names[i], values[i], want[i])
		}
	}
}
