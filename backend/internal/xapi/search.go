package xapi

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"trex/backend/internal/model"
)

type SearchProgress func(message string, progress int, post *model.Post)

func (c *Client) Search(ctx context.Context, request model.ScanRequest, progress SearchProgress) ([]model.Post, error) {
	query, err := BuildQuery(request)
	if err != nil {
		return nil, err
	}
	product := "Latest"
	if strings.EqualFold(request.ResultMode, "top") {
		product = "Top"
	}
	maxPosts := request.MaxPosts
	if maxPosts <= 0 {
		maxPosts = 5000
	}
	seen := map[string]bool{}
	seenCursors := map[string]bool{}
	posts := make([]model.Post, 0, 256)
	cursor := ""
	page := 0
	emptyPages := 0
	refreshedQueryID := false
	templateVariables, activeFeatures, activeFieldToggles, activeHeaders := c.SearchTemplate()
	referer := "https://x.com/search?q=" + url.QueryEscape(query)
	if product == "Latest" {
		referer += "&f=live"
	}
	for len(posts) < maxPosts {
		if ctx.Err() != nil {
			return posts, ctx.Err()
		}
		page++
		variables := buildSearchVariables(templateVariables, query, product, cursor)
		if progress != nil {
			progress(fmt.Sprintf("Fetching %s search page %d · %d post(s)", strings.ToLower(product), page, len(posts)), min(94, 5+page), nil)
		}
		payload, headers, err := c.doSearchRequestWithRetry(ctx, variables, activeFeatures, activeFieldToggles, referer, activeHeaders)
		if err != nil && !refreshedQueryID && strings.Contains(err.Error(), "404") {
			if progress != nil {
				progress("SearchTimeline metadata changed. Refreshing the current operation ID from X…", 3, nil)
			}
			if _, refreshErr := c.RefreshQueryID(ctx, "SearchTimeline", referer); refreshErr != nil {
				return posts, fmt.Errorf("%v; automatic query ID refresh failed: %w", err, refreshErr)
			}
			refreshedQueryID = true
			templateVariables, activeFeatures, activeFieldToggles, activeHeaders = c.SearchTemplate()
			variables = buildSearchVariables(templateVariables, query, product, cursor)
			time.Sleep(1200 * time.Millisecond)
			payload, headers, err = c.doSearchRequestWithRetry(ctx, variables, activeFeatures, activeFieldToggles, referer, activeHeaders)
		}
		if err != nil {
			return posts, err
		}
		pagePosts := ExtractPosts(payload, query)
		added := 0
		for _, post := range pagePosts {
			if post.ID == "" || seen[post.ID] {
				continue
			}
			seen[post.ID] = true
			posts = append(posts, post)
			added++
			if progress != nil {
				copy := post
				progress(fmt.Sprintf("Extracted %d post(s)", len(posts)), min(94, 5+page), &copy)
			}
			if len(posts) >= maxPosts {
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
		if emptyPages >= 5 {
			break
		}
		if remaining := headers.Get("x-rate-limit-remaining"); remaining == "0" {
			break
		}
		time.Sleep(450 * time.Millisecond)
	}
	sort.SliceStable(posts, func(i, j int) bool { return posts[i].CreatedAt > posts[j].CreatedAt })
	if progress != nil {
		progress(fmt.Sprintf("Scan complete · %d unique post(s)", len(posts)), 100, nil)
	}
	return posts, nil
}

func (c *Client) doSearchRequestWithRetry(
	ctx context.Context,
	variables, features, fieldToggles map[string]any,
	referer string,
	headers map[string]string,
) (map[string]any, http.Header, error) {
	var payload map[string]any
	var responseHeaders http.Header
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		payload, responseHeaders, err = c.DoWithHeaders(ctx, "SearchTimeline", variables, features, fieldToggles, referer, headers)
		if err == nil {
			return payload, responseHeaders, nil
		}
		message := strings.ToLower(err.Error())
		if strings.Contains(message, "404") ||
			strings.Contains(message, "rate limit") ||
			strings.Contains(message, "authorization failed") {
			return payload, responseHeaders, err
		}
		select {
		case <-ctx.Done():
			return payload, responseHeaders, ctx.Err()
		case <-time.After(time.Duration(attempt+1) * time.Second):
		}
	}
	return payload, responseHeaders, err
}

func buildSearchVariables(template map[string]any, query, product, cursor string) map[string]any {
	variables := cloneAnyMap(template)
	variables["rawQuery"] = query
	variables["count"] = 20
	variables["querySource"] = "typed_query"
	variables["product"] = product
	if cursor != "" {
		variables["cursor"] = cursor
	} else {
		delete(variables, "cursor")
	}
	return variables
}

func BuildQuery(request model.ScanRequest) (string, error) {
	if request.Mode == "custom" {
		query := strings.TrimSpace(request.CustomQuery)
		if query == "" {
			return "", fmt.Errorf("custom query is required")
		}
		if err := ValidateQuery(query); err != nil {
			return "", err
		}
		return query, nil
	}
	terms := cleanTerms(request.Terms)
	if len(terms) == 0 {
		return "", fmt.Errorf("enter at least one keyword or account")
	}
	joiner := " OR "
	if strings.EqualFold(request.MatchMode, "AND") {
		joiner = " "
	}
	if request.Mode == "accounts" {
		accounts := make([]string, 0, len(terms))
		for _, term := range terms {
			accounts = append(accounts, "from:"+strings.TrimLeft(term, "@"))
		}
		query := "(" + strings.Join(accounts, " OR ") + ")"
		filters := cleanTerms(request.AccountFilters)
		if len(filters) > 0 {
			query += " (" + strings.Join(filters, joiner) + ")"
		}
		return addDates(query, request.FromDate, request.ToDate), nil
	}
	return addDates("("+strings.Join(terms, joiner)+")", request.FromDate, request.ToDate), nil
}

func ValidateQuery(query string) error {
	depth := 0
	quoted := false
	for _, char := range query {
		switch char {
		case '"':
			quoted = !quoted
		case '(':
			if !quoted {
				depth++
			}
		case ')':
			if !quoted {
				depth--
				if depth < 0 {
					return fmt.Errorf("custom query has a closing bracket without an opening bracket")
				}
			}
		case '\n', '\r':
			return fmt.Errorf("custom query must be on one line")
		}
	}
	if quoted {
		return fmt.Errorf("custom query has an unclosed quote")
	}
	if depth != 0 {
		return fmt.Errorf("custom query has unbalanced brackets")
	}
	return nil
}

func cleanTerms(values []string) []string {
	result := []string{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if strings.Contains(value, " ") && !strings.HasPrefix(value, "\"") {
			value = "\"" + strings.Trim(value, "\"") + "\""
		}
		result = append(result, value)
	}
	return result
}

func addDates(query, from, to string) string {
	if from != "" {
		query += " since:" + from
	}
	if to != "" {
		query += " until:" + to
	}
	return query
}
