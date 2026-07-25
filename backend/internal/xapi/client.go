package xapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"trex/backend/internal/logging"
	"trex/backend/internal/session"
)

type Client struct {
	queryIDs map[string]string
	session  *session.Manager
	http     *http.Client
	logger   *logging.Logger
}

func New(queryPath string, manager *session.Manager, logger *logging.Logger) (*Client, error) {
	data, err := os.ReadFile(queryPath)
	if err != nil {
		return nil, fmt.Errorf("read query IDs: %w", err)
	}
	ids := map[string]string{}
	if err := json.Unmarshal(data, &ids); err != nil {
		return nil, fmt.Errorf("decode query IDs: %w", err)
	}
	client := &Client{
		queryIDs: ids, session: manager, logger: logger,
		http: &http.Client{
			Timeout: 60 * time.Second,
			Transport: &http.Transport{
				Proxy:                 http.ProxyFromEnvironment,
				TLSHandshakeTimeout:   30 * time.Second,
				ResponseHeaderTimeout: 45 * time.Second,
				IdleConnTimeout:       60 * time.Second,
			},
		},
	}
	return client, nil
}

func (c *Client) QueryID(operation string) string {
	return c.queryIDs[operation]
}

func (c *Client) Do(
	ctx context.Context,
	operation string,
	variables map[string]any,
	features map[string]any,
	fieldToggles map[string]any,
	referer string,
) (map[string]any, http.Header, error) {
	return c.DoWithHeaders(ctx, operation, variables, features, fieldToggles, referer, nil)
}

func (c *Client) DoWithHeaders(
	ctx context.Context,
	operation string,
	variables map[string]any,
	features map[string]any,
	fieldToggles map[string]any,
	referer string,
	additionalHeaders map[string]string,
) (map[string]any, http.Header, error) {
	queryID := c.QueryID(operation)
	if queryID == "" {
		return nil, nil, fmt.Errorf("query ID is not configured for %s", operation)
	}
	runtime, err := c.session.Runtime()
	if err != nil {
		return nil, nil, err
	}
	endpoint, err := buildURL(queryID, operation, variables, features, fieldToggles)
	if err != nil {
		return nil, nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, nil, err
	}
	request.Header.Set("accept", "*/*")
	request.Header.Set("accept-language", "en-US,en;q=0.9")
	request.Header.Set("authorization", "Bearer "+runtime.Bearer)
	request.Header.Set("cache-control", "no-cache")
	request.Header.Set("content-type", "application/json")
	request.Header.Set("pragma", "no-cache")
	request.Header.Set("referer", referer)
	request.Header.Set("user-agent", fallback(runtime.UserAgent, "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/126 Safari/537.36"))
	request.Header.Set("x-csrf-token", cookieValue(runtime.Cookies, "ct0"))
	request.Header.Set("x-twitter-active-user", "yes")
	request.Header.Set("x-twitter-auth-type", "OAuth2Session")
	request.Header.Set("x-twitter-client-language", "en")
	request.Header.Set("cookie", cookieHeader(runtime.Cookies))
	for key, value := range additionalHeaders {
		if value != "" {
			request.Header.Set(key, value)
		}
	}

	response, err := c.http.Do(request)
	if err != nil {
		return nil, nil, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, response.Header, err
	}
	if response.StatusCode == http.StatusTooManyRequests {
		return nil, response.Header, errors.New("X rate limit reached; wait until the rate-limit reset before retrying")
	}
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return nil, response.Header, errors.New("X session authorization failed; refresh the session from the startup screen")
	}
	if response.StatusCode == http.StatusNotFound {
		return nil, response.Header, fmt.Errorf("%s GraphQL endpoint returned 404; its query ID may have changed", operation)
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return nil, response.Header, fmt.Errorf("%s returned HTTP %d: %s", operation, response.StatusCode, truncate(string(body), 240))
	}
	result := map[string]any{}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, response.Header, fmt.Errorf("decode %s response: %w", operation, err)
	}
	if errs, ok := result["errors"].([]any); ok && len(errs) > 0 {
		return result, response.Header, fmt.Errorf("%s API error: %v", operation, errs[0])
	}
	return result, response.Header, nil
}

func buildURL(queryID, operation string, variables, features, fieldToggles map[string]any) (string, error) {
	values := url.Values{}
	encoded, err := json.Marshal(variables)
	if err != nil {
		return "", err
	}
	values.Set("variables", string(encoded))
	if features != nil {
		encoded, _ = json.Marshal(features)
		values.Set("features", string(encoded))
	}
	if fieldToggles != nil {
		encoded, _ = json.Marshal(fieldToggles)
		values.Set("fieldToggles", string(encoded))
	}
	return fmt.Sprintf("https://x.com/i/api/graphql/%s/%s?%s", queryID, operation, values.Encode()), nil
}

func cookieHeader(cookies []session.Cookie) string {
	values := make([]string, 0, len(cookies))
	for _, cookie := range cookies {
		if strings.Contains(cookie.Domain, "x.com") || strings.Contains(cookie.Domain, "twitter.com") {
			values = append(values, cookie.Name+"="+cookie.Value)
		}
	}
	return strings.Join(values, "; ")
}

func cookieValue(cookies []session.Cookie, name string) string {
	for _, cookie := range cookies {
		if cookie.Name == name {
			return cookie.Value
		}
	}
	return ""
}

func fallback(value, defaultValue string) string {
	if strings.TrimSpace(value) == "" {
		return defaultValue
	}
	return value
}

func truncate(value string, length int) string {
	if len(value) <= length {
		return value
	}
	return value[:length] + "…"
}

func cloneAnyMap(value map[string]any) map[string]any {
	result := map[string]any{}
	for key, item := range value {
		result[key] = item
	}
	return result
}
