package session

import "testing"

func TestQueryIDFromGraphQLURL(t *testing.T) {
	value := queryIDFromGraphQLURL(
		"https://x.com/i/api/graphql/current-id/SearchTimeline?variables=%7B%7D",
		"SearchTimeline",
	)
	if value != "current-id" {
		t.Fatalf("expected current-id, got %q", value)
	}
	if value := queryIDFromGraphQLURL("https://x.com/home", "SearchTimeline"); value != "" {
		t.Fatalf("expected empty result, got %q", value)
	}
}
