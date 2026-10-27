package strategies

import (
	"slices"
	"testing"
)

func TestMemberGreenlitViaStack(t *testing.T) {
	allow := []string{"sig-voo-buy-tecl+mara_tree+pdd_tree"}
	if !slices.Contains(allow, "sig-voo-buy-tecl+mara_tree+pdd_tree") {
		t.Fatal("full stack id should be on the allowlist")
	}
	if slices.Contains(allow, "mara_tree") {
		t.Fatal("overlay must not be independently allowlisted")
	}
	if !MemberGreenlit(allow, "mara_tree") {
		t.Fatal("mara_tree should be greenlit as a stack member")
	}
	if !MemberGreenlit(allow, "sig-voo-buy-tecl") {
		t.Fatal("primary should be greenlit as a stack member")
	}
	if MemberGreenlit(allow, "gld-decline") {
		t.Fatal("unrelated strategy must not be greenlit")
	}
}
