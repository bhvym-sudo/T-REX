package xapi

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"trex/backend/internal/model"
)

func ExtractPosts(payload map[string]any, query string) []model.Post {
	results := []model.Post{}
	walk(payload, func(value map[string]any) {
		tweetResult := asMap(value["tweet_results"])
		if len(tweetResult) == 0 {
			return
		}
		result := asMap(tweetResult["result"])
		if len(result) == 0 {
			return
		}
		if nested := asMap(result["tweet"]); len(nested) > 0 {
			result = nested
		}
		if visibility := asMap(result["tweet"]); len(visibility) > 0 {
			result = visibility
		}
		post := normalizeTweet(result, query)
		if post.ID != "" {
			results = append(results, post)
		}
	})
	if len(results) == 0 {
		walk(payload, func(value map[string]any) {
			if _, ok := value["rest_id"]; !ok {
				return
			}
			if _, ok := value["legacy"]; !ok {
				return
			}
			post := normalizeTweet(value, query)
			if post.ID != "" && post.Text != "" {
				results = append(results, post)
			}
		})
	}
	unique := map[string]model.Post{}
	for _, post := range results {
		unique[post.ID] = post
	}
	output := make([]model.Post, 0, len(unique))
	for _, post := range unique {
		output = append(output, post)
	}
	return output
}

func normalizeTweet(result map[string]any, query string) model.Post {
	legacy := asMap(result["legacy"])
	core := asMap(result["core"])
	userResult := asMap(pathValue(core, "user_results", "result"))
	userLegacy := asMap(userResult["legacy"])
	userCore := asMap(userResult["core"])
	id := firstString(result["rest_id"], legacy["id_str"])
	text := firstString(pathValue(result, "note_tweet", "note_tweet_results", "result", "text"), legacy["full_text"], legacy["text"])
	handle := firstString(userCore["screen_name"], userLegacy["screen_name"])
	author := map[string]any{
		"id":                firstValue(userResult["rest_id"], userLegacy["id_str"]),
		"name":              firstValue(userCore["name"], userLegacy["name"]),
		"screen_name":       handle,
		"verified":          firstValue(pathValue(userResult, "verification", "verified"), userLegacy["verified"]),
		"is_blue_verified":  userResult["is_blue_verified"],
		"followers_count":   userLegacy["followers_count"],
		"following_count":   userLegacy["friends_count"],
		"description":       userLegacy["description"],
		"location":          userLegacy["location"],
		"profile_image_url": firstValue(pathValue(userResult, "avatar", "image_url"), userLegacy["profile_image_url_https"]),
	}
	metrics := map[string]any{
		"reply_count":    legacy["reply_count"],
		"retweet_count":  legacy["retweet_count"],
		"quote_count":    legacy["quote_count"],
		"like_count":     legacy["favorite_count"],
		"bookmark_count": legacy["bookmark_count"],
		"view_count":     pathValue(result, "views", "count"),
	}
	url := ""
	if id != "" && handle != "" {
		url = fmt.Sprintf("https://x.com/%s/status/%s", handle, id)
	}
	raw := cloneMap(result)
	return model.Post{
		ID: id, Conversation: firstString(legacy["conversation_id_str"]), Text: text,
		CreatedAt: firstString(legacy["created_at"]), URL: url, Query: query,
		Author: author, Metrics: metrics, Entities: asMap(legacy["entities"]),
		Media: asSlice(pathValue(legacy, "extended_entities", "media")), Raw: raw,
	}
}

func findCursor(payload map[string]any, wanted string) string {
	result := ""
	walk(payload, func(value map[string]any) {
		if result != "" {
			return
		}
		cursorType := strings.ToLower(firstString(value["cursorType"]))
		entryID := strings.ToLower(firstString(value["entryId"], value["entry_id"]))
		if wanted != "" && !strings.EqualFold(cursorType, wanted) && !strings.Contains(entryID, strings.ToLower(wanted)) {
			return
		}
		if cursor := firstString(value["value"], value["cursor"]); cursor != "" {
			result = cursor
		}
	})
	return result
}

func walk(value any, visit func(map[string]any)) {
	switch typed := value.(type) {
	case map[string]any:
		visit(typed)
		for _, child := range typed {
			walk(child, visit)
		}
	case []any:
		for _, child := range typed {
			walk(child, visit)
		}
	}
}

func mapPath(value map[string]any, keys ...string) map[string]any {
	current := any(value)
	for _, key := range keys {
		current = pathValue(current, key)
	}
	return asMap(current)
}

func pathValue(value any, keys ...any) any {
	current := value
	for _, key := range keys {
		switch typed := key.(type) {
		case string:
			mapped, ok := current.(map[string]any)
			if !ok {
				return nil
			}
			current = mapped[typed]
		case int:
			items, ok := current.([]any)
			if !ok || typed < 0 || typed >= len(items) {
				return nil
			}
			current = items[typed]
		}
	}
	return current
}

func findMapByKeys(value any, keys ...string) map[string]any {
	var found map[string]any
	walk(value, func(item map[string]any) {
		if found != nil {
			return
		}
		for _, key := range keys {
			if _, ok := item[key]; !ok {
				return
			}
		}
		found = item
	})
	return found
}

func asMap(value any) map[string]any {
	if result, ok := value.(map[string]any); ok {
		return result
	}
	return map[string]any{}
}

func asSlice(value any) []any {
	if result, ok := value.([]any); ok {
		return result
	}
	return nil
}

func firstValue(values ...any) any {
	for _, value := range values {
		if hasValue(value) {
			return value
		}
	}
	return nil
}

func firstString(values ...any) string {
	for _, value := range values {
		if value == nil {
			continue
		}
		text := fmt.Sprint(value)
		if text != "" && text != "<nil>" {
			return text
		}
	}
	return ""
}

func hasValue(value any) bool {
	if value == nil {
		return false
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed) != ""
	case []any:
		return len(typed) > 0
	case map[string]any:
		return len(typed) > 0
	}
	return !reflect.ValueOf(value).IsZero()
}

func humanize(value string) string {
	value = strings.ReplaceAll(value, "_", " ")
	parts := strings.Fields(value)
	for index := range parts {
		parts[index] = strings.ToUpper(parts[index][:1]) + parts[index][1:]
	}
	return strings.Join(parts, " ")
}

func cloneMap(value map[string]any) map[string]any {
	data, _ := json.Marshal(value)
	result := map[string]any{}
	_ = json.Unmarshal(data, &result)
	return result
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func intValue(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case float64:
		return int(typed)
	case string:
		result, _ := strconv.Atoi(typed)
		return result
	}
	return 0
}
