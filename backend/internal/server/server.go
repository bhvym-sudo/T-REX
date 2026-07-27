package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"trex/backend/internal/app"
	"trex/backend/internal/events"
	"trex/backend/internal/exporter"
	"trex/backend/internal/logging"
	"trex/backend/internal/model"
	"trex/backend/internal/searchworker"
	"trex/backend/internal/session"
	"trex/backend/internal/store"
	"trex/backend/internal/xapi"
)

type Server struct {
	paths    app.Paths
	logger   *logging.Logger
	hub      *events.Hub
	store    *store.Store
	session  *session.Manager
	x        *xapi.Client
	search   *searchworker.Collector
	http     *http.Server
	sequence atomic.Uint64
	mu       sync.Mutex
	cancels  map[string]context.CancelFunc
	replies  map[string]replyResult
}

type replyResult struct {
	Tweet   model.Post   `json:"tweet"`
	Replies []model.Post `json:"replies"`
}

func New(paths app.Paths) (*Server, error) {
	if err := paths.Ensure(); err != nil {
		return nil, err
	}
	logger, err := logging.New(paths.Logs)
	if err != nil {
		return nil, err
	}
	hub := events.New()
	logger.SetSink(func(level, message string) {
		hub.Publish(model.Event{Type: "log", Level: level, Message: message})
	})
	manager := session.New(paths.Runtime, paths.SessionDir, logger)
	client, err := xapi.New(paths.QueryIDs, manager, logger)
	if err != nil {
		logger.Close()
		return nil, err
	}
	server := &Server{
		paths: paths, logger: logger, hub: hub, store: store.New(),
		session: manager, x: client, search: searchworker.New(paths.Root, paths.SessionDir),
		cancels: map[string]context.CancelFunc{},
		replies: map[string]replyResult{},
	}
	mux := http.NewServeMux()
	server.routes(mux)
	server.http = &http.Server{
		Addr: "127.0.0.1:8787", Handler: withMiddleware(mux),
		ReadHeaderTimeout: 10 * time.Second,
	}
	return server, nil
}

func (s *Server) ListenAndServe() error {
	s.logger.Info("Go backend listening on " + s.http.Addr)
	return s.http.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	for _, cancel := range s.cancels {
		cancel()
	}
	s.mu.Unlock()
	s.logger.Close()
	return s.http.Shutdown(ctx)
}

func (s *Server) routes(mux *http.ServeMux) {
	mux.Handle("/ws", s.hub)
	mux.HandleFunc("GET /api/health", s.health)
	mux.HandleFunc("POST /api/shutdown", s.shutdown)
	mux.HandleFunc("GET /api/session/status", s.sessionStatus)
	mux.HandleFunc("POST /api/session/bootstrap", s.bootstrapSession)
	mux.HandleFunc("GET /api/logs", s.logs)
	mux.HandleFunc("POST /api/scan", s.startScan)
	mux.HandleFunc("GET /api/posts", s.posts)
	mux.HandleFunc("POST /api/account", s.account)
	mux.HandleFunc("POST /api/tweet/detail", s.tweetDetail)
	mux.HandleFunc("POST /api/replies", s.startReplies)
	mux.HandleFunc("GET /api/replies/{jobID}", s.replyResult)
	mux.HandleFunc("GET /api/jobs/{jobID}", s.job)
	mux.HandleFunc("DELETE /api/jobs/{jobID}", s.cancelJob)
	mux.HandleFunc("POST /api/export/posts", s.exportPosts)
	mux.HandleFunc("POST /api/export/account", s.exportAccount)
	mux.HandleFunc("POST /api/export/authors", s.exportAuthors)
	mux.HandleFunc("POST /api/export/replies", s.exportReplies)
	mux.HandleFunc("POST /api/tracker/start", s.startTracker)
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "service": "trex-backend", "version": "0.1.0"})
}

func (s *Server) shutdown(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"shuttingDown": true})
	go func() {
		time.Sleep(150 * time.Millisecond)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.Shutdown(ctx)
	}()
}

func (s *Server) sessionStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.session.Status())
}

func (s *Server) bootstrapSession(w http.ResponseWriter, r *http.Request) {
	jobID := s.newJob("session")
	ctx, cancel := context.WithCancel(context.Background())
	s.rememberCancel(jobID, cancel)
	go func() {
		defer s.forgetCancel(jobID)
		err := s.session.LaunchAndCapture(ctx, func(message string, progress int) {
			s.updateJob(jobID, "running", progress, message, 0, "")
		})
		if err != nil {
			s.updateJob(jobID, "failed", 100, "Session bootstrap failed", 0, err.Error())
			s.logger.Error("Session bootstrap failed: " + err.Error())
			return
		}
		s.updateJob(jobID, "completed", 100, "X session is ready", 0, "")
	}()
	writeJSON(w, http.StatusAccepted, map[string]string{"jobId": jobID})
}

func (s *Server) logs(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"entries": s.logger.Entries()})
}

func (s *Server) startScan(w http.ResponseWriter, r *http.Request) {
	var request model.ScanRequest
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	request.MaxPosts = normalizeScanLimit(request.MaxPosts)
	request.ScrollDelay = normalizeScrollDelay(request.ScrollDelay)
	if _, err := xapi.BuildQuery(request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if !s.session.Status().Ready {
		writeError(w, http.StatusUnauthorized, fmt.Errorf("create or refresh the X session first"))
		return
	}
	jobID := s.newJob("scan")
	ctx, cancel := context.WithCancel(context.Background())
	s.rememberCancel(jobID, cancel)
	s.store.ResetPosts()
	s.logger.Info(fmt.Sprintf("Scan post limit set to %d", request.MaxPosts))
	s.logger.Info(fmt.Sprintf("Scan scroll delay set to %d second(s)", request.ScrollDelay))
	s.logger.Info(fmt.Sprintf("Scan started · mode=%s · result=%s · dates=%s to %s", request.Mode, request.ResultMode, request.FromDate, request.ToDate))
	go func() {
		defer s.forgetCancel(jobID)
		query, queryErr := xapi.BuildQuery(request)
		if queryErr != nil {
			s.updateJob(jobID, "failed", 100, "Scan failed", 0, queryErr.Error())
			s.logger.Error("Scan failed: " + queryErr.Error())
			return
		}
		posts, err := s.search.Collect(ctx, query, request.ResultMode, request.MaxPosts, request.ScrollDelay, func(message string, progress int, post *model.Post) {
			if post == nil && strings.TrimSpace(message) != "" {
				s.logger.Info("Scan progress · " + message)
			}
			count := len(s.store.Posts())
			if post != nil && s.store.AddPost(*post) {
				count++
				s.hub.Publish(model.Event{Type: "post", JobID: jobID, Data: post})
			}
			s.updateJob(jobID, "running", progress, message, count, "")
		})
		if err != nil {
			s.updateJob(jobID, "failed", 100, "Scan failed", len(s.store.Posts()), err.Error())
			s.logger.Error("Scan failed: " + err.Error())
			return
		}
		for _, post := range posts {
			s.store.AddPost(post)
		}
		s.updateJob(jobID, "completed", 100, fmt.Sprintf("Extracted %d unique posts", len(posts)), len(posts), "")
		s.logger.Info(fmt.Sprintf("Scan completed · %d unique posts", len(posts)))
	}()
	writeJSON(w, http.StatusAccepted, map[string]string{"jobId": jobID})
}

func normalizeScanLimit(limit int) int {
	if limit <= 0 {
		return 500
	}
	if limit < 20 {
		return 20
	}
	if limit > 5000 {
		return 5000
	}
	return (limit / 20) * 20
}

func normalizeScrollDelay(delay int) int {
	if delay <= 0 {
		return 3
	}
	if delay > 15 {
		return 15
	}
	return delay
}

func (s *Server) posts(w http.ResponseWriter, r *http.Request) {
	posts := s.store.Posts()
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit > 0 && len(posts) > limit {
		posts = posts[:limit]
	}
	writeJSON(w, http.StatusOK, map[string]any{"total": len(s.store.Posts()), "posts": posts})
}

func (s *Server) account(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ScreenName string `json:"screenName"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	key := strings.ToLower(strings.TrimSpace(input.ScreenName))
	if cached, ok := s.store.Account(key); ok {
		writeJSON(w, http.StatusOK, cached)
		return
	}
	s.logger.Info("Account lookup started for @" + input.ScreenName)
	account, err := s.x.Account(r.Context(), input.ScreenName)
	if err != nil {
		s.logger.Error("Account lookup failed for @" + input.ScreenName + ": " + err.Error())
		writeError(w, http.StatusBadGateway, err)
		return
	}
	s.store.SetAccount(key, account)
	s.logger.Info("Account lookup completed for @" + account.ScreenName)
	writeJSON(w, http.StatusOK, account)
}

func (s *Server) tweetDetail(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Tweet string `json:"tweet"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	s.logger.Info("Tweet detail lookup started for " + input.Tweet)
	record, err := s.x.TweetDetail(r.Context(), input.Tweet)
	if err != nil {
		s.logger.Error("Tweet detail lookup failed: " + err.Error())
		writeError(w, http.StatusBadGateway, err)
		return
	}
	s.logger.Info("Tweet detail lookup completed for tweet " + record.Tweet.ID)
	writeJSON(w, http.StatusOK, record)
}

func (s *Server) startReplies(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Tweet      string `json:"tweet"`
		MaxReplies int    `json:"maxReplies"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	jobID := s.newJob("replies")
	ctx, cancel := context.WithCancel(context.Background())
	s.rememberCancel(jobID, cancel)
	s.logger.Info("Reply collection started for " + input.Tweet)
	go func() {
		defer s.forgetCancel(jobID)
		tweet, replies, err := s.x.Replies(ctx, input.Tweet, input.MaxReplies, func(message string, progress int, _ *model.Post) {
			s.updateJob(jobID, "running", progress, message, 0, "")
		})
		if err != nil {
			s.updateJob(jobID, "failed", 100, "Reply collection failed", len(replies), err.Error())
			s.logger.Error("Reply collection failed: " + err.Error())
			return
		}
		s.mu.Lock()
		s.replies[jobID] = replyResult{Tweet: tweet, Replies: replies}
		s.mu.Unlock()
		s.updateJob(jobID, "completed", 100, fmt.Sprintf("Collected %d replies", len(replies)), len(replies), "")
		s.logger.Info(fmt.Sprintf("Reply collection completed · %d replies", len(replies)))
	}()
	writeJSON(w, http.StatusAccepted, map[string]string{"jobId": jobID})
}

func (s *Server) replyResult(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	result, ok := s.replies[r.PathValue("jobID")]
	s.mu.Unlock()
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Errorf("reply result is not ready"))
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) job(w http.ResponseWriter, r *http.Request) {
	job, ok := s.store.Job(r.PathValue("jobID"))
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Errorf("job not found"))
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (s *Server) cancelJob(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("jobID")
	s.mu.Lock()
	cancel := s.cancels[id]
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	s.updateJob(id, "cancelled", 100, "Cancelled", 0, "")
	writeJSON(w, http.StatusOK, map[string]bool{"cancelled": true})
}

func (s *Server) exportPosts(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Path string `json:"path"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if input.Path == "" {
		input.Path = filepath.Join(s.paths.Exports, "trex_posts.xlsx")
	}
	posts := s.store.Posts()
	s.hub.Publish(model.Event{Type: "export", Message: "Writing tweet data workbook", Progress: 35})
	if err := exporter.PostsToExcel(posts, input.Path); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	s.logger.Info(fmt.Sprintf("Tweet data exported · %d posts · %s", len(posts), input.Path))
	s.hub.Publish(model.Event{Type: "export", Message: "Tweet data export complete", Progress: 100})
	writeJSON(w, http.StatusOK, map[string]any{"path": input.Path, "count": len(posts)})
}

func (s *Server) exportAccount(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Path       string `json:"path"`
		ScreenName string `json:"screenName"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	account, ok := s.store.Account(strings.ToLower(input.ScreenName))
	if !ok {
		var err error
		account, err = s.x.Account(r.Context(), input.ScreenName)
		if err != nil {
			writeError(w, http.StatusBadGateway, err)
			return
		}
	}
	if err := exporter.AccountToExcel(account, input.Path); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	s.logger.Info("Account data exported for @" + account.ScreenName)
	writeJSON(w, http.StatusOK, map[string]any{"path": input.Path})
}

func (s *Server) exportReplies(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Path  string `json:"path"`
		JobID string `json:"jobId"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	s.mu.Lock()
	result, ok := s.replies[input.JobID]
	s.mu.Unlock()
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Errorf("reply analysis result is not ready"))
		return
	}
	if err := exporter.RepliesToExcel(result.Tweet, result.Replies, input.Path); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	s.logger.Info(fmt.Sprintf("Reply data exported · %d replies", len(result.Replies)))
	writeJSON(w, http.StatusOK, map[string]any{"path": input.Path})
}

func (s *Server) startTracker(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Request         model.ScanRequest `json:"request"`
		IntervalSeconds int               `json:"intervalSeconds"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if input.IntervalSeconds < 15 {
		input.IntervalSeconds = 30
	}
	input.Request.Mode = "custom"
	input.Request.ResultMode = "latest"
	input.Request.MaxPosts = 20
	input.Request.ScrollDelay = normalizeScrollDelay(input.Request.ScrollDelay)
	if _, err := xapi.BuildQuery(input.Request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	jobID := s.newJob("tracker")
	ctx, cancel := context.WithCancel(context.Background())
	s.rememberCancel(jobID, cancel)
	go func() {
		defer s.forgetCancel(jobID)
		ticker := time.NewTicker(time.Duration(input.IntervalSeconds) * time.Second)
		defer ticker.Stop()
		cycle := 0
		seen := map[string]bool{}
		query, queryErr := xapi.BuildQuery(input.Request)
		if queryErr != nil {
			s.updateJob(jobID, "failed", 100, "Tracker failed", 0, queryErr.Error())
			return
		}
		s.logger.Info(fmt.Sprintf("Tracker started · interval=%ds · query=%s", input.IntervalSeconds, query))
		for {
			cycle++
			posts, err := s.search.Collect(ctx, query, input.Request.ResultMode, input.Request.MaxPosts, input.Request.ScrollDelay, nil)
			if err != nil {
				s.updateJob(jobID, "failed", 100, "Tracker failed", len(seen), err.Error())
				s.logger.Error("Tracker failed: " + err.Error())
				return
			}
			added := 0
			for _, post := range posts {
				if post.ID == "" || seen[post.ID] {
					continue
				}
				seen[post.ID] = true
				added++
				copy := post
				s.hub.Publish(model.Event{Type: "post", JobID: jobID, Data: &copy})
			}
			s.updateJob(jobID, "running", 0, fmt.Sprintf("Tracker cycle %d complete - %d new post(s) - next check in %ds", cycle, added, input.IntervalSeconds), len(seen), "")
			select {
			case <-ctx.Done():
				s.updateJob(jobID, "cancelled", 100, "Tracker stopped", len(seen), "")
				s.logger.Info("Tracker stopped")
				return
			case <-ticker.C:
			}
		}
	}()
	writeJSON(w, http.StatusAccepted, map[string]string{"jobId": jobID})
}

func (s *Server) exportAuthors(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Path string `json:"path"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	posts := s.store.Posts()
	if len(posts) == 0 {
		writeError(w, http.StatusBadRequest, fmt.Errorf("there are no posts to export"))
		return
	}
	jobID := s.newJob("authors-export")
	ctx, cancel := context.WithCancel(context.Background())
	s.rememberCancel(jobID, cancel)
	go func() {
		defer s.forgetCancel(jobID)
		handles := []string{}
		seen := map[string]bool{}
		for _, post := range posts {
			handle := strings.ToLower(fmt.Sprint(post.Author["screen_name"]))
			if handle != "" && !seen[handle] {
				seen[handle] = true
				handles = append(handles, handle)
			}
		}
		accounts := map[string]model.AccountRecord{}
		for index, handle := range handles {
			if ctx.Err() != nil {
				return
			}
			progress := int(float64(index) / float64(maxInt(len(handles), 1)) * 85)
			s.updateJob(jobID, "running", progress, fmt.Sprintf("Collecting author %d of %d · @%s", index+1, len(handles), handle), index, "")
			account, ok := s.store.Account(handle)
			if !ok {
				var err error
				account, err = s.x.Account(ctx, handle)
				if err != nil {
					s.logger.Error("Author export skipped @" + handle + ": " + err.Error())
					continue
				}
				s.store.SetAccount(handle, account)
			}
			accounts[handle] = account
			time.Sleep(250 * time.Millisecond)
		}
		s.updateJob(jobID, "running", 92, "Writing authors workbook", len(accounts), "")
		if err := exporter.AuthorsToExcel(posts, accounts, input.Path); err != nil {
			s.updateJob(jobID, "failed", 100, "Authors export failed", len(accounts), err.Error())
			return
		}
		s.updateJob(jobID, "completed", 100, fmt.Sprintf("Exported %d author profile(s)", len(accounts)), len(accounts), "")
		s.logger.Info(fmt.Sprintf("Authors data exported · %d profiles · %s", len(accounts), input.Path))
	}()
	writeJSON(w, http.StatusAccepted, map[string]string{"jobId": jobID})
}

func (s *Server) newJob(kind string) string {
	id := fmt.Sprintf("%s-%d-%d", kind, time.Now().Unix(), s.sequence.Add(1))
	s.store.SetJob(model.JobSnapshot{ID: id, Kind: kind, Status: "queued", Message: "Queued"})
	return id
}

func (s *Server) updateJob(id, status string, progress int, message string, count int, errorText string) {
	job, _ := s.store.Job(id)
	job.ID = id
	job.Status = status
	job.Progress = progress
	job.Message = message
	job.Count = count
	job.Error = errorText
	s.store.SetJob(job)
	s.hub.Publish(model.Event{Type: "job", JobID: id, Progress: progress, Message: message, Data: job})
}

func (s *Server) rememberCancel(id string, cancel context.CancelFunc) {
	s.mu.Lock()
	s.cancels[id] = cancel
	s.mu.Unlock()
}

func (s *Server) forgetCancel(id string) {
	s.mu.Lock()
	delete(s.cancels, id)
	s.mu.Unlock()
}

func withMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,DELETE,OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func decodeJSON(r *http.Request, output any) error {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	return decoder.Decode(output)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func IsAddressAvailable(address string) bool {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return false
	}
	_ = listener.Close()
	return true
}

func EnsureConfig(paths app.Paths) error {
	if _, err := os.Stat(paths.QueryIDs); err == nil {
		return nil
	}
	return os.WriteFile(paths.QueryIDs, []byte(`{
  "TweetDetail": "Lq1caG5YPcdhpTdS2ZRx7Q",
  "TweetResultByRestId": "4hhGRbehkcUVTKf8n0f0xw",
  "UserByScreenName": "2qvSHpkWTMS9i0zJAwDNiA",
  "AboutAccountQuery": "TzOG2twZEfhr9KmClvVVqA"
}`), 0o644)
}
