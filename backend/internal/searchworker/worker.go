package searchworker

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"trex/backend/internal/model"
	"trex/backend/internal/xapi"
)

type Progress func(message string, progress int, post *model.Post)

type Collector struct {
	root       string
	profileDir string
	scriptPath string
}

type workerRequest struct {
	Query      string `json:"query"`
	ResultMode string `json:"resultMode"`
	MaxPosts   int    `json:"maxPosts"`
	MaxScrolls int    `json:"maxScrolls"`
}

type workerEvent struct {
	Type          string          `json:"type"`
	Message       string          `json:"message"`
	Progress      int             `json:"progress"`
	Total         int             `json:"total"`
	ResponseCount int             `json:"responseCount"`
	Reason        string          `json:"reason"`
	Payload       json.RawMessage `json:"payload"`
}

func New(root, profileDir string) *Collector {
	scriptPath := strings.TrimSpace(os.Getenv("TREX_PYTHON_WORKER"))
	if scriptPath == "" {
		scriptPath = filepath.Join(root, "python_worker", "search_timeline.py")
	}
	return &Collector{
		root: root, profileDir: profileDir, scriptPath: scriptPath,
	}
}

func (c *Collector) Collect(
	ctx context.Context,
	query, resultMode string,
	maxPosts int,
	progress Progress,
) ([]model.Post, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("search query is empty")
	}
	resultMode = strings.ToLower(strings.TrimSpace(resultMode))
	if resultMode == "" {
		resultMode = "latest"
	}
	if resultMode != "latest" && resultMode != "top" {
		return nil, fmt.Errorf("result mode must be latest or top")
	}
	if maxPosts <= 0 {
		maxPosts = 5000
	}
	if _, err := os.Stat(c.scriptPath); err != nil {
		return nil, fmt.Errorf("Python SearchTimeline worker is missing at %s", c.scriptPath)
	}

	command, arguments, err := c.command()
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, command, arguments...)
	configureProcess(cmd)
	cmd.Dir = c.root
	cmd.Env = append(os.Environ(), "PYTHONUNBUFFERED=1")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start Python SearchTimeline worker: %w", err)
	}

	request := workerRequest{
		Query: query, ResultMode: resultMode, MaxPosts: maxPosts, MaxScrolls: 600,
	}
	if err := json.NewEncoder(stdin).Encode(request); err != nil {
		_ = stdin.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, err
	}
	_ = stdin.Close()

	seen := map[string]bool{}
	posts := make([]model.Post, 0, 512)
	workerError := ""
	doneReason := ""
	lastProgress := 0
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 256*1024), 64*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		event := workerEvent{}
		if err := json.Unmarshal(line, &event); err != nil {
			continue
		}
		switch event.Type {
		case "status":
			if event.Progress < lastProgress {
				event.Progress = lastProgress
			} else {
				lastProgress = event.Progress
			}
			if progress != nil {
				progress(event.Message, event.Progress, nil)
			}
		case "payload":
			payload := map[string]any{}
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				continue
			}
			for _, post := range xapi.ExtractPosts(payload, query) {
				if post.ID == "" || seen[post.ID] || len(posts) >= maxPosts {
					continue
				}
				seen[post.ID] = true
				posts = append(posts, post)
				if progress != nil {
					copy := post
					postProgress := min(94, 10+len(posts)/10)
					if postProgress < lastProgress {
						postProgress = lastProgress
					} else {
						lastProgress = postProgress
					}
					progress(fmt.Sprintf("Extracted %d unique post(s)", len(posts)), postProgress, &copy)
				}
			}
		case "done":
			doneReason = event.Reason
			if progress != nil {
				progress(
					fmt.Sprintf("Finalizing %d unique post(s) · %s", len(posts), event.Reason),
					98,
					nil,
				)
			}
		case "error":
			workerError = event.Message
		}
	}
	scanErr := scanner.Err()
	waitErr := cmd.Wait()
	if ctx.Err() != nil {
		return posts, ctx.Err()
	}
	if scanErr != nil {
		return posts, fmt.Errorf("read Python SearchTimeline worker output: %w", scanErr)
	}
	if workerError != "" {
		return posts, fmt.Errorf("%s", workerError)
	}
	if waitErr != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = waitErr.Error()
		}
		return posts, fmt.Errorf("Python SearchTimeline worker failed: %s", detail)
	}

	sort.SliceStable(posts, func(i, j int) bool {
		return posts[i].CreatedAt > posts[j].CreatedAt
	})
	if progress != nil {
		message := fmt.Sprintf("Scan complete · %d unique post(s)", len(posts))
		if doneReason != "" {
			message += " · " + doneReason
		}
		progress(message, 100, nil)
	}
	return posts, nil
}

func (c *Collector) command() (string, []string, error) {
	if executable := strings.TrimSpace(os.Getenv("TREX_SEARCH_WORKER_EXE")); executable != "" {
		return executable, []string{"--profile", c.profileDir}, nil
	}
	if executable := filepath.Join(filepath.Dir(c.scriptPath), "search_timeline.exe"); fileExists(executable) {
		return executable, []string{"--profile", c.profileDir}, nil
	}
	if configured := strings.TrimSpace(os.Getenv("TREX_PYTHON")); configured != "" {
		return configured, []string{"-u", c.scriptPath, "--profile", c.profileDir}, nil
	}
	if python, err := exec.LookPath("python"); err == nil {
		return python, []string{"-u", c.scriptPath, "--profile", c.profileDir}, nil
	}
	if launcher, err := exec.LookPath("py"); err == nil {
		return launcher, []string{"-3", "-u", c.scriptPath, "--profile", c.profileDir}, nil
	}
	return "", nil, fmt.Errorf(
		"Python was not found. Install Python and run: pip install -r python_worker/requirements.txt",
	)
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
