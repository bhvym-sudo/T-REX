package store

import (
	"sync"

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
	return append([]model.Post(nil), s.posts...)
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
