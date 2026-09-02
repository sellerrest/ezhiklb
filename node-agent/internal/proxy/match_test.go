package proxy

import (
	"testing"

	"ezhiklb-node-agent/internal/domain"
)

func cond(field domain.MatchField, op domain.MatchOperator, value string) domain.MatchCondition {
	return domain.MatchCondition{Field: field, Operator: op, Value: value}
}

func TestGroupsMatchEmptyIsCatchAll(t *testing.T) {
	if !GroupsMatch(nil, "anything.example", "/any") {
		t.Fatal("empty groups should match everything")
	}
}

func TestGroupsMatchORAcrossGroupsANDWithinGroup(t *testing.T) {
	groups := []domain.MatchGroup{
		{Conditions: []domain.MatchCondition{cond(domain.MatchFieldSNI, domain.MatchOpEquals, "ex1.com"), cond(domain.MatchFieldPath, domain.MatchOpEquals, "/pach1")}},
		{Conditions: []domain.MatchCondition{cond(domain.MatchFieldSNI, domain.MatchOpEquals, "ex2.com")}},
	}
	cases := []struct {
		sni, path string
		want      bool
	}{
		{"ex1.com", "/pach1", true},    // first group fully matches
		{"ex1.com", "/pach2", false},   // first group's path condition fails, second group's sni doesn't match either
		{"ex2.com", "/whatever", true}, // second group matches regardless of path
		{"ex3.com", "/pach1", false},
	}
	for _, c := range cases {
		if got := GroupsMatch(groups, c.sni, c.path); got != c.want {
			t.Errorf("GroupsMatch(sni=%q, path=%q) = %v, want %v", c.sni, c.path, got, c.want)
		}
	}
}

func TestGroupWithNoConditionsNeverMatches(t *testing.T) {
	groups := []domain.MatchGroup{{Conditions: nil}}
	if GroupsMatch(groups, "anything", "/any") {
		t.Fatal("a group with zero conditions must never match (it is not the same as an empty groups list)")
	}
}

func TestConditionOperators(t *testing.T) {
	cases := []struct {
		op    domain.MatchOperator
		value string
		sni   string
		want  bool
	}{
		{domain.MatchOpEquals, "example.com", "example.com", true},
		{domain.MatchOpEquals, "example.com", "other.com", false},
		{domain.MatchOpNotEquals, "example.com", "other.com", true},
		{domain.MatchOpNotEquals, "example.com", "example.com", false},
		{domain.MatchOpContains, "ample", "example.com", true},
		{domain.MatchOpContains, "zzz", "example.com", false},
		{domain.MatchOpNotContains, "zzz", "example.com", true},
		{domain.MatchOpStartsWith, "ex", "example.com", true},
		{domain.MatchOpStartsWith, "zz", "example.com", false},
		{domain.MatchOpNotStartsWith, "zz", "example.com", true},
	}
	for _, c := range cases {
		groups := []domain.MatchGroup{{Conditions: []domain.MatchCondition{cond(domain.MatchFieldSNI, c.op, c.value)}}}
		if got := GroupsMatch(groups, c.sni, ""); got != c.want {
			t.Errorf("%s(%q) against sni=%q = %v, want %v", c.op, c.value, c.sni, got, c.want)
		}
	}
}

func TestConditionFieldSelectsPathForHTTP(t *testing.T) {
	groups := []domain.MatchGroup{{Conditions: []domain.MatchCondition{cond(domain.MatchFieldPath, domain.MatchOpStartsWith, "/api")}}}
	if !GroupsMatch(groups, "irrelevant.example", "/api/users") {
		t.Fatal("path condition should match against the path, not the sni")
	}
	if GroupsMatch(groups, "irrelevant.example", "/other") {
		t.Fatal("path condition should not match an unrelated path")
	}
}
