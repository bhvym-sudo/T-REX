package xapi

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"trex/backend/internal/model"
)

var tweetIDPattern = regexp.MustCompile(`(?:status/)?(\d{6,})`)

type ReplyProgress func(message string, progress int, reply *model.Post)

func (c *Client) Replies(ctx context.Context, input string, maxReplies int, progress ReplyProgress) (model.Post, []model.Post, error) {
	match := tweetIDPattern.FindStringSubmatch(strings.TrimSpace(input))
	if len(match) < 2 {
		return model.Post{}, nil, fmt.Errorf("enter a valid X tweet URL or tweet ID")
	}
	tweetID := match[1]
	if maxReplies <= 0 {
		maxReplies = 5000
	}
	mainTweet, _ := c.tweetByID(ctx, tweetID)
	replies := []model.Post{}
	seen := map[string]bool{tweetID: true}
	seenCursors := map[string]bool{}
	cursor := ""
	emptyPages := 0
	for page := 1; len(replies) < maxReplies; page++ {
		if ctx.Err() != nil {
			return mainTweet, replies, ctx.Err()
		}
		variables := map[string]any{
			"focalTweetId":                           tweetID,
			"referrer":                               "tweet",
			"with_rux_injections":                    false,
			"rankingMode":                            "Relevance",
			"includePromotedContent":                 false,
			"withCommunity":                          true,
			"withQuickPromoteEligibilityTweetFields": true,
			"withBirdwatchNotes":                     true,
			"withVoice":                              true,
		}
		if cursor != "" {
			variables["cursor"] = cursor
		}
		if progress != nil {
			progress(fmt.Sprintf("Fetching reply page %d · %d collected", page, len(replies)), min(94, 4+page), nil)
		}
		payload, _, err := c.Do(ctx, "TweetDetail", variables, tweetFeatures, searchFieldToggles, "https://x.com/i/status/"+tweetID)
		if err != nil {
			return mainTweet, replies, err
		}
		pagePosts := ExtractPosts(payload, "")
		added := 0
		for _, post := range pagePosts {
			if post.ID == "" || seen[post.ID] {
				continue
			}
			if mainTweet.ID == "" && post.ID == tweetID {
				mainTweet = post
				seen[post.ID] = true
				continue
			}
			if post.Conversation != "" && post.Conversation != tweetID {
				continue
			}
			seen[post.ID] = true
			replies = append(replies, post)
			added++
			if progress != nil {
				copy := post
				progress(fmt.Sprintf("Collected %d replies", len(replies)), min(94, 4+page), &copy)
			}
			if len(replies) >= maxReplies {
				break
			}
		}
		next := findCursor(payload, "Bottom")
		if next == "" || seenCursors[next] {
			break
		}
		seenCursors[next] = true
		cursor = next
		if added == 0 {
			emptyPages++
		} else {
			emptyPages = 0
		}
		if emptyPages >= 8 {
			break
		}
		time.Sleep(450 * time.Millisecond)
	}
	if progress != nil {
		progress(fmt.Sprintf("Reply collection complete · %d replies", len(replies)), 100, nil)
	}
	return mainTweet, replies, nil
}

func (c *Client) tweetByID(ctx context.Context, tweetID string) (model.Post, error) {
	payload, _, err := c.Do(ctx, "TweetResultByRestId", map[string]any{
		"tweetId":                tweetID,
		"withCommunity":          true,
		"includePromotedContent": false,
		"withVoice":              true,
	}, tweetFeatures, searchFieldToggles, "https://x.com/i/status/"+tweetID)
	if err != nil {
		return model.Post{}, err
	}
	posts := ExtractPosts(payload, "")
	for _, post := range posts {
		if post.ID == tweetID {
			return post, nil
		}
	}
	if len(posts) > 0 {
		return posts[0], nil
	}
	return model.Post{}, fmt.Errorf("tweet was not found")
}
