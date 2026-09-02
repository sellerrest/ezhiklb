package proxy

import (
	"strings"

	"ezhiklb-node-agent/internal/domain"
)

// GroupsMatch evaluates a Binding's rule set against a connection's SNI and
// (for HTTP-mode inbounds) URI path: groups are OR'd, a group's conditions
// are AND'd. An empty group list means "match everything" (a catch-all
// binding).
func GroupsMatch(groups []domain.MatchGroup, sni, path string) bool {
	if len(groups) == 0 {
		return true
	}
	for _, group := range groups {
		if groupMatches(group, sni, path) {
			return true
		}
	}
	return false
}

func groupMatches(group domain.MatchGroup, sni, path string) bool {
	if len(group.Conditions) == 0 {
		return false
	}
	for _, condition := range group.Conditions {
		if !conditionMatches(condition, sni, path) {
			return false
		}
	}
	return true
}

func conditionMatches(condition domain.MatchCondition, sni, path string) bool {
	subject := sni
	if condition.Field == domain.MatchFieldPath {
		subject = path
	}
	switch condition.Operator {
	case domain.MatchOpEquals:
		return subject == condition.Value
	case domain.MatchOpNotEquals:
		return subject != condition.Value
	case domain.MatchOpContains:
		return strings.Contains(subject, condition.Value)
	case domain.MatchOpNotContains:
		return !strings.Contains(subject, condition.Value)
	case domain.MatchOpStartsWith:
		return strings.HasPrefix(subject, condition.Value)
	case domain.MatchOpNotStartsWith:
		return !strings.HasPrefix(subject, condition.Value)
	default:
		return false
	}
}
