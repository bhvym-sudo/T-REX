package model

import "time"

type SessionStatus struct {
	Ready       bool   `json:"ready"`
	ProfileDir  string `json:"profileDir"`
	ScreenName  string `json:"screenName,omitempty"`
	HasCookies  bool   `json:"hasCookies"`
	HasBearer   bool   `json:"hasBearer"`
	LastUpdated string `json:"lastUpdated,omitempty"`
	Message     string `json:"message"`
}

type ScanRequest struct {
	Mode           string   `json:"mode"`
	ResultMode     string   `json:"resultMode"`
	MatchMode      string   `json:"matchMode"`
	Terms          []string `json:"terms"`
	AccountFilters []string `json:"accountFilters"`
	CustomQuery    string   `json:"customQuery"`
	FromDate       string   `json:"fromDate"`
	ToDate         string   `json:"toDate"`
	MaxPosts       int      `json:"maxPosts"`
}

type Post struct {
	ID           string         `json:"id"`
	Conversation string         `json:"conversationId,omitempty"`
	Text         string         `json:"text"`
	CreatedAt    string         `json:"createdAt"`
	URL          string         `json:"url"`
	Query        string         `json:"query,omitempty"`
	Author       map[string]any `json:"author"`
	Metrics      map[string]any `json:"metrics"`
	Entities     map[string]any `json:"entities,omitempty"`
	Media        []any          `json:"media,omitempty"`
	Raw          map[string]any `json:"raw,omitempty"`
}

type AccountRecord struct {
	ScreenName string         `json:"screenName"`
	Name       string         `json:"name"`
	AvatarURL  string         `json:"avatarUrl"`
	Fields     map[string]any `json:"fields"`
	Sections   []Section      `json:"sections"`
	RawProfile map[string]any `json:"rawProfile"`
	RawAbout   map[string]any `json:"rawAbout"`
}

type Section struct {
	Title string  `json:"title"`
	Rows  []Field `json:"rows"`
}

type Field struct {
	Label string `json:"label"`
	Value any    `json:"value"`
}

type Event struct {
	Type      string    `json:"type"`
	Level     string    `json:"level,omitempty"`
	Message   string    `json:"message,omitempty"`
	Progress  int       `json:"progress,omitempty"`
	JobID     string    `json:"jobId,omitempty"`
	Data      any       `json:"data,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

type JobSnapshot struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Status   string `json:"status"`
	Progress int    `json:"progress"`
	Message  string `json:"message"`
	Count    int    `json:"count"`
	Error    string `json:"error,omitempty"`
}
