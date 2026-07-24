package xapi

import "testing"

func TestFindTweetResultSelectsFocalTweet(t *testing.T) {
	payload := map[string]any{
		"data": map[string]any{
			"entries": []any{
				map[string]any{"tweet_results": map[string]any{"result": map[string]any{
					"rest_id": "999",
					"legacy":  map[string]any{"full_text": "reply"},
				}}},
				map[string]any{"tweet_results": map[string]any{"result": map[string]any{
					"rest_id": "123",
					"legacy": map[string]any{
						"full_text":           "focal tweet",
						"created_at":          "Thu Jul 23 14:10:26 +0000 2026",
						"conversation_id_str": "123",
						"reply_count":         float64(12),
					},
				}}},
			},
		},
	}
	result := findTweetResult(payload, "123")
	if result["rest_id"] != "123" {
		t.Fatalf("expected focal tweet 123, got %v", result["rest_id"])
	}
	post := normalizeTweet(result, "")
	sections := buildTweetDetailSections(result, post)
	if post.Text != "focal tweet" {
		t.Fatalf("expected focal tweet text, got %q", post.Text)
	}
	if len(sections) < 2 {
		t.Fatalf("expected structured sections, got %d", len(sections))
	}
}
