package xapi

import (
	"strings"
	"testing"

	"trex/backend/internal/model"
)

func TestBuildKeywordQuery(t *testing.T) {
	query, err := BuildQuery(model.ScanRequest{
		Mode: "keywords", MatchMode: "OR",
		Terms:    []string{"Narendra Modi", "Melbourne"},
		FromDate: "2026-07-04", ToDate: "2026-07-11",
	})
	if err != nil {
		t.Fatal(err)
	}
	expected := `("Narendra Modi" OR Melbourne) since:2026-07-04 until:2026-07-11`
	if query != expected {
		t.Fatalf("expected %q, got %q", expected, query)
	}
}

func TestBuildAccountQueryWithFilters(t *testing.T) {
	query, err := BuildQuery(model.ScanRequest{
		Mode: "accounts", Terms: []string{"@narendramodi"},
		AccountFilters: []string{"Australia", "Marvel Stadium"},
		FromDate:       "2026-07-04", ToDate: "2026-07-11",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"from:narendramodi", "Australia", `"Marvel Stadium"`, "since:2026-07-04"} {
		if !strings.Contains(query, value) {
			t.Fatalf("query %q does not contain %q", query, value)
		}
	}
}

func TestValidateCustomQuery(t *testing.T) {
	if err := ValidateQuery(`(Modi OR "Narendra Modi") lang:en`); err != nil {
		t.Fatal(err)
	}
	if err := ValidateQuery(`(Modi OR "Narendra Modi"`); err == nil {
		t.Fatal("expected unbalanced query error")
	}
}

func TestExtractPostsAndBottomCursor(t *testing.T) {
	payload := map[string]any{
		"data": map[string]any{
			"search_by_raw_query": map[string]any{
				"search_timeline": map[string]any{
					"timeline": map[string]any{
						"instructions": []any{
							map[string]any{
								"entries": []any{
									map[string]any{
										"entryId": "tweet-123",
										"content": map[string]any{
											"itemContent": map[string]any{
												"tweet_results": map[string]any{
													"result": map[string]any{
														"rest_id": "123",
														"legacy": map[string]any{
															"full_text":           "Test post",
															"created_at":          "Thu Jul 23 12:00:00 +0000 2026",
															"conversation_id_str": "123",
														},
														"core": map[string]any{
															"user_results": map[string]any{
																"result": map[string]any{
																	"rest_id": "u1",
																	"legacy": map[string]any{
																		"screen_name": "tester",
																		"name":        "Tester",
																	},
																},
															},
														},
													},
												},
											},
										},
									},
									map[string]any{
										"entryId": "cursor-bottom",
										"content": map[string]any{
											"cursorType": "Bottom",
											"value":      "next-cursor",
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
	posts := ExtractPosts(payload, "test")
	if len(posts) != 1 || posts[0].ID != "123" || posts[0].Author["screen_name"] != "tester" {
		t.Fatalf("unexpected posts: %#v", posts)
	}
	if cursor := findCursor(payload, "Bottom"); cursor != "next-cursor" {
		t.Fatalf("unexpected cursor: %q", cursor)
	}
}
