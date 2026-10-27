package csvlist

import (
	"slices"
	"testing"
)

func TestUnique(t *testing.T) {
	cases := map[string][]string{
		"a,b,c":        {"a", "b", "c"},
		" a , b ,, a ": {"a", "b"},
		"b,a,b,c,a":    {"b", "a", "c"},
		"":             nil,
		" , ,":         nil,
		"x+y,z":        {"x+y", "z"},
	}
	for in, want := range cases {
		if got := Unique(in); !slices.Equal(got, want) {
			t.Errorf("Unique(%q) = %v, want %v", in, got, want)
		}
	}
}
