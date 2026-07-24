package xapi

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"trex/backend/internal/model"
)

func (c *Client) TweetDetail(ctx context.Context, input string) (model.TweetDetailRecord, error) {
	match := tweetIDPattern.FindStringSubmatch(strings.TrimSpace(input))
	if len(match) < 2 {
		return model.TweetDetailRecord{}, fmt.Errorf("enter a valid X tweet URL or tweet ID")
	}
	tweetID := match[1]
	payload, _, err := c.Do(ctx, "TweetDetail", map[string]any{
		"focalTweetId":                           tweetID,
		"referrer":                               "home",
		"with_rux_injections":                    false,
		"rankingMode":                            "Relevance",
		"includePromotedContent":                 true,
		"withCommunity":                          true,
		"withQuickPromoteEligibilityTweetFields": true,
		"withBirdwatchNotes":                     true,
		"withVoice":                              true,
	}, tweetFeatures, tweetDetailFieldToggles, "https://x.com/i/status/"+tweetID)
	if err != nil {
		return model.TweetDetailRecord{}, err
	}
	result := findTweetResult(payload, tweetID)
	if len(result) == 0 {
		return model.TweetDetailRecord{}, fmt.Errorf("tweet %s was not present in the TweetDetail response", tweetID)
	}
	post := normalizeTweet(result, "")
	return model.TweetDetailRecord{Tweet: post, Sections: buildTweetDetailSections(result, post)}, nil
}

func findTweetResult(payload map[string]any, tweetID string) map[string]any {
	var found map[string]any
	walk(payload, func(value map[string]any) {
		if found != nil {
			return
		}
		result := unwrapTweetResult(asMap(pathValue(value, "tweet_results", "result")))
		if len(result) > 0 && firstString(result["rest_id"], pathValue(result, "legacy", "id_str")) == tweetID {
			found = result
		}
	})
	return found
}

func unwrapTweetResult(result map[string]any) map[string]any {
	for index := 0; index < 3; index++ {
		nested := asMap(result["tweet"])
		if len(nested) == 0 {
			break
		}
		result = nested
	}
	return result
}

func buildTweetDetailSections(result map[string]any, post model.Post) []model.Section {
	legacy := asMap(result["legacy"])
	sections := []model.Section{
		detailSection("Identity & Content",
			detailField("Tweet ID", post.ID),
			detailField("Conversation ID", post.Conversation),
			detailField("Tweet URL", post.URL),
			detailField("Created At", post.CreatedAt),
			detailField("Text", post.Text),
			detailField("Language", legacy["lang"]),
			detailField("Source", legacy["source"]),
			detailField("Display Text Range", legacy["display_text_range"]),
			detailField("In Reply To Tweet ID", legacy["in_reply_to_status_id_str"]),
			detailField("In Reply To User ID", legacy["in_reply_to_user_id_str"]),
			detailField("In Reply To Username", legacy["in_reply_to_screen_name"]),
			detailField("Quoted Tweet ID", legacy["quoted_status_id_str"]),
		),
		detailSection("Engagement",
			detailField("Replies", post.Metrics["reply_count"]),
			detailField("Reposts", post.Metrics["retweet_count"]),
			detailField("Quotes", post.Metrics["quote_count"]),
			detailField("Likes", post.Metrics["like_count"]),
			detailField("Bookmarks", post.Metrics["bookmark_count"]),
			detailField("Views", post.Metrics["view_count"]),
			detailField("Bookmarked By Session", legacy["bookmarked"]),
			detailField("Favorited By Session", legacy["favorited"]),
			detailField("Reposted By Session", legacy["retweeted"]),
		),
	}
	if rows := flattenDetailRows(pathValue(result, "core", "user_results", "result"), ""); len(rows) > 0 {
		sections = append(sections, model.Section{Title: "Author", Rows: rows})
	}
	if rows := flattenDetailRows(legacy["entities"], ""); len(rows) > 0 {
		sections = append(sections, model.Section{Title: "Entities", Rows: rows})
	}
	media := pathValue(legacy, "extended_entities", "media")
	if media == nil {
		media = pathValue(legacy, "entities", "media")
	}
	if rows := flattenDetailRows(media, ""); len(rows) > 0 {
		sections = append(sections, model.Section{Title: "Media", Rows: rows})
	}

	legacyAdditional := cloneMap(legacy)
	for _, key := range []string{
		"id_str", "full_text", "text", "created_at", "conversation_id_str", "lang", "source",
		"display_text_range", "in_reply_to_status_id_str", "in_reply_to_user_id_str",
		"in_reply_to_screen_name", "quoted_status_id_str", "reply_count", "retweet_count",
		"quote_count", "favorite_count", "bookmark_count", "bookmarked", "favorited",
		"retweeted", "entities", "extended_entities",
	} {
		delete(legacyAdditional, key)
	}
	if rows := flattenDetailRows(legacyAdditional, ""); len(rows) > 0 {
		sections = append(sections, model.Section{Title: "Additional Tweet Fields", Rows: rows})
	}

	platform := cloneMap(result)
	delete(platform, "legacy")
	delete(platform, "core")
	delete(platform, "rest_id")
	if rows := flattenDetailRows(platform, ""); len(rows) > 0 {
		sections = append(sections, model.Section{Title: "Platform Metadata", Rows: rows})
	}
	return compactDetailSections(sections)
}

func detailField(label string, value any) model.Field {
	return model.Field{Label: label, Value: value}
}

func detailSection(title string, rows ...model.Field) model.Section {
	filtered := make([]model.Field, 0, len(rows))
	for _, row := range rows {
		if row.Value != nil && fmt.Sprint(row.Value) != "" {
			filtered = append(filtered, row)
		}
	}
	return model.Section{Title: title, Rows: filtered}
}

func compactDetailSections(sections []model.Section) []model.Section {
	output := make([]model.Section, 0, len(sections))
	for _, item := range sections {
		if len(item.Rows) > 0 {
			output = append(output, item)
		}
	}
	return output
}

func flattenDetailRows(value any, prefix string) []model.Field {
	rows := []model.Field{}
	var flatten func(any, string)
	flatten = func(current any, path string) {
		switch typed := current.(type) {
		case map[string]any:
			keys := make([]string, 0, len(typed))
			for key := range typed {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				next := key
				if path != "" {
					next = path + " / " + key
				}
				flatten(typed[key], next)
			}
		case []any:
			for index, item := range typed {
				flatten(item, fmt.Sprintf("%s / %d", path, index+1))
			}
		default:
			if current != nil {
				rows = append(rows, model.Field{Label: humanizeDetailPath(path), Value: current})
			}
		}
	}
	flatten(value, prefix)
	return rows
}

func humanizeDetailPath(path string) string {
	parts := strings.Split(strings.Trim(path, " /"), " / ")
	for index, part := range parts {
		parts[index] = humanize(part)
	}
	return strings.Join(parts, " / ")
}
