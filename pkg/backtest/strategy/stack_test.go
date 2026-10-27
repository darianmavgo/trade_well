package strategy

import (
	"reflect"
	"testing"
)

func TestStackRoundTrip(t *testing.T) {
	id := StackID("streak-voo-buy-tecl", "mara_tree", "pdd_tree")
	if id != "streak-voo-buy-tecl+mara_tree+pdd_tree" || !IsStack(id) {
		t.Fatalf("StackID = %q", id)
	}
	if got := ParseStack(id); !reflect.DeepEqual(got, []string{"streak-voo-buy-tecl", "mara_tree", "pdd_tree"}) {
		t.Fatalf("ParseStack = %v", got)
	}
	if got := ParseStack(" a + +b+a "); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("blank/dupe handling = %v", got)
	}
	if IsStack("mara_tree") || len(ParseStack("mara_tree")) != 1 {
		t.Fatal("plain id is not a stack")
	}
}
