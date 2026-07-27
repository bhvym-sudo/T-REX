package store

import (
	"sort"
	"strings"
	"sync"
	"time"

	"trex/backend/internal/model"
)

type Store struct {
	mu       sync.RWMutex
	posts    []model.Post
	postIDs  map[string]struct{}
	accounts map[string]model.AccountRecord
	jobs     map[string]model.JobSnapshot
}

func New() *Store {
	return &Store{
		postIDs:  map[string]struct{}{},
		accounts: map[string]model.AccountRecord{},
		jobs:     map[string]model.JobSnapshot{},
	}
}

func (s *Store) ResetPosts() {
	s.mu.Lock()
	s.posts = nil
	s.postIDs = map[string]struct{}{}
	s.mu.Unlock()
}

func (s *Store) AddPost(post model.Post) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if post.ID != "" {
		if _, exists := s.postIDs[post.ID]; exists {
			return false
		}
		s.postIDs[post.ID] = struct{}{}
	}
	s.posts = append(s.posts, post)
	return true
}

func (s *Store) Posts() []model.Post {
	s.mu.RLock()
	defer s.mu.RUnlock()
	posts := append([]model.Post(nil), s.posts...)
	sort.SliceStable(posts, func(i, j int) bool {
		return postTime(posts[i]).After(postTime(posts[j]))
	})
	return posts
}

func postTime(post model.Post) time.Time {
	created := strings.TrimSpace(post.CreatedAt)
	if created == "" {
		return time.Time{}
	}
	if parsed, err := time.Parse("Mon Jan 02 15:04:05 -0700 2006", created); err == nil {
		return parsed
	}
	if parsed, err := time.Parse(time.RFC3339, created); err == nil {
		return parsed
	}
	return time.Time{}
}

func (s *Store) SetAccount(key string, value model.AccountRecord) {
	s.mu.Lock()
	s.accounts[key] = value
	s.mu.Unlock()
}

func (s *Store) Account(key string) (model.AccountRecord, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	value, ok := s.accounts[key]
	return value, ok
}

func (s *Store) SetJob(job model.JobSnapshot) {
	s.mu.Lock()
	s.jobs[job.ID] = job
	s.mu.Unlock()
}

func (s *Store) Job(id string) (model.JobSnapshot, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	job, ok := s.jobs[id]
	return job, ok
}
