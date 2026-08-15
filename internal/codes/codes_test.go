package codes

import (
	"strings"
	"testing"
)

func TestResolveDocType(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"yuho", "120"},
		{"YUHO", "120"},
		{" yuho ", "120"},
		{"120", "120"},
		{"hanki", "160"},
		{"rinji", "180"},
		{"taryo", "350"},
		{"235", "235"},
	}
	for _, tt := range tests {
		got, err := ResolveDocType(tt.in)
		if err != nil {
			t.Errorf("ResolveDocType(%q): %v", tt.in, err)
			continue
		}
		if got != tt.want {
			t.Errorf("ResolveDocType(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}

	for _, in := range []string{"", "999", "nonsense"} {
		if _, err := ResolveDocType(in); err == nil {
			t.Errorf("ResolveDocType(%q) should have failed", in)
		}
	}
}

// An unknown code must pass through rather than render as blank, so a filing
// using a type added after this build still displays something.
func TestDocTypeNameFallsBackToCode(t *testing.T) {
	if got := DocTypeName("120"); got != "有価証券報告書" {
		t.Errorf("DocTypeName(120) = %q", got)
	}
	if got := DocTypeName("999"); got != "999" {
		t.Errorf("DocTypeName(999) = %q, want the code itself", got)
	}
}

// Every alias must point at a code that exists, or --type would accept a value
// that silently matches nothing.
func TestAliasesPointAtRealCodes(t *testing.T) {
	for _, pair := range Aliases() {
		alias, code := pair[0], pair[1]
		if _, ok := DocTypes[code]; !ok {
			t.Errorf("alias %q maps to %q, which is not a known document type", alias, code)
		}
		if strings.ToLower(alias) != alias {
			t.Errorf("alias %q should be lowercase; lookup lowercases the input", alias)
		}
	}
}

func TestSortedTablesAreOrdered(t *testing.T) {
	for _, table := range [][][2]string{SortedDocTypes(), SortedOrdinances(), Aliases()} {
		for i := 1; i < len(table); i++ {
			if table[i-1][0] >= table[i][0] {
				t.Errorf("table is not sorted: %q before %q", table[i-1][0], table[i][0])
			}
		}
	}
}
