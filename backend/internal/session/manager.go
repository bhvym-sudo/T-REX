package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	neturl "net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"trex/backend/internal/logging"
	"trex/backend/internal/model"
)

type Cookie struct {
	Name     string  `json:"name"`
	Value    string  `json:"value"`
	Domain   string  `json:"domain"`
	Path     string  `json:"path"`
	Expires  float64 `json:"expires,omitempty"`
	HTTPOnly bool    `json:"httpOnly,omitempty"`
	Secure   bool    `json:"secure,omitempty"`
	SameSite string  `json:"sameSite,omitempty"`
}

type Runtime struct {
	Bearer     string                      `json:"bearer"`
	Cookies    []Cookie                    `json:"cookies"`
	UserAgent  string                      `json:"userAgent"`
	CapturedAt time.Time                   `json:"capturedAt"`
	ScreenName string                      `json:"screenName,omitempty"`
	Operations map[string]GraphQLOperation `json:"operations,omitempty"`
}

type GraphQLOperation struct {
	QueryID      string            `json:"queryId"`
	Operation    string            `json:"operation"`
	Variables    map[string]any    `json:"variables"`
	Features     map[string]any    `json:"features"`
	FieldToggles map[string]any    `json:"fieldToggles"`
	Headers      map[string]string `json:"headers"`
	RequestURL   string            `json:"requestUrl"`
}

type Manager struct {
	mu          sync.RWMutex
	runtime     Runtime
	runtimePath string
	profileDir  string
	logger      *logging.Logger
	capturing   atomic.Bool
}

func New(runtimePath, profileDir string, logger *logging.Logger) *Manager {
	manager := &Manager{runtimePath: runtimePath, profileDir: profileDir, logger: logger}
	_ = manager.load()
	return manager
}

func (m *Manager) Status() model.SessionStatus {
	profileReady := m.profileHasSessionData()
	m.mu.RLock()
	defer m.mu.RUnlock()
	hasAuth := profileReady && m.cookieLocked("auth_token") != ""
	hasCSRF := profileReady && m.cookieLocked("ct0") != ""
	hasBearer := profileReady && m.runtime.Bearer != ""
	ready := hasAuth && hasCSRF && hasBearer
	message := "X session is ready."
	if !ready {
		if !profileReady {
			message = "No local X browser session was found. Opening Edge for login."
		} else {
			message = "X session metadata is missing. Open Edge and sign in."
		}
	}
	return model.SessionStatus{
		Ready: ready, ProfileDir: m.profileDir, ScreenName: m.runtime.ScreenName,
		HasCookies: hasAuth && hasCSRF, HasBearer: hasBearer,
		LastUpdated: m.runtime.CapturedAt.Format(time.RFC3339), Message: message,
	}
}

func (m *Manager) Runtime() (Runtime, error) {
	if !m.profileHasSessionData() {
		return Runtime{}, errors.New("local X browser session folder is missing or empty")
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.cookieLocked("auth_token") == "" || m.cookieLocked("ct0") == "" {
		return Runtime{}, errors.New("X session cookies are missing")
	}
	if m.runtime.Bearer == "" {
		return Runtime{}, errors.New("X web bearer token has not been captured")
	}
	copy := m.runtime
	copy.Cookies = append([]Cookie(nil), m.runtime.Cookies...)
	return copy, nil
}

func (m *Manager) Operation(name string) (GraphQLOperation, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.runtime.Operations == nil {
		return GraphQLOperation{}, false
	}
	value, ok := m.runtime.Operations[name]
	return value, ok
}

func (m *Manager) DiscoverGraphQLOperation(ctx context.Context, operation, navigationURL string) (GraphQLOperation, error) {
	if !m.capturing.CompareAndSwap(false, true) {
		return GraphQLOperation{}, errors.New("browser session operation is already running")
	}
	defer m.capturing.Store(false)
	if runtime.GOOS != "windows" {
		return GraphQLOperation{}, errors.New("GraphQL operation discovery currently requires Windows")
	}
	edge, err := findEdge()
	if err != nil {
		return GraphQLOperation{}, err
	}
	port, err := availablePort()
	if err != nil {
		return GraphQLOperation{}, err
	}
	args := []string{
		fmt.Sprintf("--remote-debugging-port=%d", port),
		"--remote-debugging-address=127.0.0.1",
		"--user-data-dir=" + m.profileDir,
		"--headless=new",
		"--disable-gpu",
		"--disable-extensions",
		"--disable-notifications",
		"--disable-component-update",
		"--no-first-run",
		"--no-default-browser-check",
		"about:blank",
	}
	cmd := exec.CommandContext(ctx, edge, args...)
	if err := cmd.Start(); err != nil {
		return GraphQLOperation{}, fmt.Errorf("open Edge for GraphQL metadata refresh: %w", err)
	}
	defer func() {
		if cmd.Process != nil && cmd.ProcessState == nil {
			terminateProcess(cmd)
		}
	}()
	m.logger.Info("Refreshing X GraphQL metadata for " + operation)
	client := &http.Client{Timeout: 2 * time.Second}
	var target debuggerTarget
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return GraphQLOperation{}, ctx.Err()
		}
		target, _ = findPageTarget(client, port)
		if target.WebSocketDebuggerURL != "" {
			break
		}
		time.Sleep(400 * time.Millisecond)
	}
	if target.WebSocketDebuggerURL == "" {
		return GraphQLOperation{}, errors.New("could not connect to Edge while refreshing GraphQL metadata")
	}
	conn, _, err := websocket.DefaultDialer.Dial(target.WebSocketDebuggerURL, nil)
	if err != nil {
		return GraphQLOperation{}, fmt.Errorf("connect to Edge metadata page: %w", err)
	}
	defer conn.Close()
	_ = conn.WriteJSON(map[string]any{"id": 1, "method": "Network.enable"})
	_ = conn.WriteJSON(map[string]any{"id": 2, "method": "Network.setCacheDisabled", "params": map[string]any{"cacheDisabled": true}})
	_ = conn.WriteJSON(map[string]any{"id": 3, "method": "Page.navigate", "params": map[string]any{"url": navigationURL}})
	_ = conn.SetReadDeadline(deadline)
	requestHeaders := map[string]map[string]string{}
	for {
		var message map[string]any
		if err := conn.ReadJSON(&message); err != nil {
			return GraphQLOperation{}, fmt.Errorf("could not discover a successful current %s request: %w", operation, err)
		}
		if message["method"] == "Network.requestWillBeSent" {
			params, _ := message["params"].(map[string]any)
			requestID := fmt.Sprint(params["requestId"])
			request, _ := params["request"].(map[string]any)
			headers, _ := request["headers"].(map[string]any)
			requestHeaders[requestID] = mergeHeaderMaps(requestHeaders[requestID], headers)
			continue
		}
		if message["method"] == "Network.requestWillBeSentExtraInfo" {
			params, _ := message["params"].(map[string]any)
			requestID := fmt.Sprint(params["requestId"])
			headers, _ := params["headers"].(map[string]any)
			requestHeaders[requestID] = mergeHeaderMaps(requestHeaders[requestID], headers)
			continue
		}
		if message["method"] != "Network.responseReceived" {
			continue
		}
		params, _ := message["params"].(map[string]any)
		requestID := fmt.Sprint(params["requestId"])
		response, _ := params["response"].(map[string]any)
		requestURL, _ := response["url"].(string)
		status, _ := number(response["status"])
		metadata := graphQLOperationFromURL(requestURL, operation)
		if metadata.QueryID == "" || status < 200 || status >= 300 {
			continue
		}
		metadata.Headers = selectedRequestHeaders(requestHeaders[requestID])
		_ = conn.WriteJSON(map[string]any{"id": 4, "method": "Browser.close"})
		time.Sleep(450 * time.Millisecond)
		m.mu.Lock()
		if m.runtime.Operations == nil {
			m.runtime.Operations = map[string]GraphQLOperation{}
		}
		m.runtime.Operations[operation] = metadata
		m.mu.Unlock()
		if err := m.save(); err != nil {
			return GraphQLOperation{}, err
		}
		m.logger.Info(fmt.Sprintf("Discovered successful current %s query ID: %s", operation, metadata.QueryID))
		return metadata, nil
	}
}

func (m *Manager) LaunchAndCapture(ctx context.Context, onStatus func(string, int)) error {
	if !m.capturing.CompareAndSwap(false, true) {
		return errors.New("session capture is already running")
	}
	defer m.capturing.Store(false)
	if runtime.GOOS != "windows" {
		return errors.New("automatic Edge session bootstrap currently requires Windows")
	}
	edge, err := findEdge()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(m.profileDir, 0o755); err != nil {
		return err
	}
	port := 9223
	args := []string{
		fmt.Sprintf("--remote-debugging-port=%d", port),
		"--remote-debugging-address=127.0.0.1",
		"--user-data-dir=" + m.profileDir,
		"--new-window",
		"--window-size=1200,800",
		"--window-position=100,100",
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-extensions",
		"--disable-notifications",
		"--disable-popup-blocking",
		"--disable-component-update",
		"https://x.com/home",
	}
	if onStatus != nil {
		onStatus("Opening Microsoft Edge. Sign in to X if required.", 15)
	}
	cmd := exec.CommandContext(ctx, edge, args...)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("open Edge: %w", err)
	}
	m.logger.Info("Microsoft Edge opened for X session bootstrap")
	defer func() {
		if cmd.Process != nil && cmd.ProcessState == nil {
			terminateProcess(cmd)
		}
	}()
	client := &http.Client{Timeout: 2 * time.Second}
	var target debuggerTarget
	deadline := time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		target, _ = findPageTarget(client, port)
		if target.WebSocketDebuggerURL != "" {
			break
		}
		time.Sleep(700 * time.Millisecond)
	}
	if target.WebSocketDebuggerURL == "" {
		return errors.New("could not connect to Edge debugging session")
	}
	if onStatus != nil {
		onStatus("Watching the signed-in X session for authorization metadata.", 35)
	}
	conn, _, err := websocket.DefaultDialer.Dial(target.WebSocketDebuggerURL, nil)
	if err != nil {
		return fmt.Errorf("connect to Edge page: %w", err)
	}
	defer conn.Close()
	var nextID atomic.Int64
	var writeMu sync.Mutex
	send := func(method string) int {
		id := int(nextID.Add(1))
		writeMu.Lock()
		_ = conn.WriteJSON(map[string]any{"id": id, "method": method})
		writeMu.Unlock()
		return id
	}
	send("Network.enable")
	send("Network.getAllCookies")
	userAgentID := int(nextID.Add(1))
	writeMu.Lock()
	_ = conn.WriteJSON(map[string]any{"id": userAgentID, "method": "Runtime.evaluate", "params": map[string]any{"expression": "navigator.userAgent"}})
	writeMu.Unlock()

	current := Runtime{}
	pollDone := make(chan struct{})
	var pollStopOnce sync.Once
	var pollWG sync.WaitGroup
	stopPolling := func() {
		pollStopOnce.Do(func() {
			close(pollDone)
			pollWG.Wait()
		})
	}
	defer stopPolling()
	pollWG.Add(1)
	go func() {
		defer pollWG.Done()
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-pollDone:
				return
			case <-ticker.C:
				send("Network.getAllCookies")
			}
		}
	}()
	for time.Now().Before(deadline) {
		var message map[string]any
		if err := conn.ReadJSON(&message); err != nil {
			if sessionRuntimeReady(current) {
				stopPolling()
				return m.finishCapture(current, nil, cmd, onStatus)
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("Edge debugging connection closed before login was captured: %w", err)
		}
		result, _ := message["result"].(map[string]any)
		if raw, ok := result["cookies"].([]any); ok {
			current.Cookies = decodeCookies(raw)
		}
		if id, ok := number(message["id"]); ok && int(id) == userAgentID {
			value, _ := result["result"].(map[string]any)
			current.UserAgent, _ = value["value"].(string)
		}
		if message["method"] == "Network.requestWillBeSent" {
			params, _ := message["params"].(map[string]any)
			request, _ := params["request"].(map[string]any)
			url, _ := request["url"].(string)
			headers, _ := request["headers"].(map[string]any)
			if strings.Contains(url, "/i/api/") || strings.Contains(url, "/i/api/graphql/") {
				for key, value := range headers {
					if strings.EqualFold(key, "authorization") {
						auth := fmt.Sprint(value)
						current.Bearer = strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
					}
				}
			}
		}
		if message["method"] == "Network.requestWillBeSentExtraInfo" {
			params, _ := message["params"].(map[string]any)
			headers, _ := params["headers"].(map[string]any)
			for key, value := range headers {
				if strings.EqualFold(key, "authorization") {
					auth := fmt.Sprint(value)
					current.Bearer = strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
				}
			}
		}
		if sessionRuntimeReady(current) {
			stopPolling()
			return m.finishCapture(current, conn, cmd, onStatus)
		}
	}
	return errors.New("timed out waiting for an authenticated X session")
}

func sessionRuntimeReady(current Runtime) bool {
	return cookieValue(current.Cookies, "auth_token") != "" &&
		cookieValue(current.Cookies, "ct0") != "" &&
		current.Bearer != ""
}

func (m *Manager) finishCapture(current Runtime, conn *websocket.Conn, cmd *exec.Cmd, onStatus func(string, int)) error {
	current.CapturedAt = time.Now()
	m.mu.Lock()
	m.runtime = current
	m.mu.Unlock()
	if err := m.save(); err != nil {
		return err
	}
	if err := m.saveProfileMarker(current.CapturedAt); err != nil {
		return err
	}
	if onStatus != nil {
		onStatus("X session captured successfully. Closing Microsoft Edge.", 96)
	}
	m.logger.Info("X cookies and web authorization metadata captured")
	if conn != nil {
		_ = conn.WriteJSON(map[string]any{"id": 999999, "method": "Browser.close"})
	}
	time.Sleep(900 * time.Millisecond)
	if cmd != nil && cmd.Process != nil && cmd.ProcessState == nil {
		terminateProcess(cmd)
	}
	if onStatus != nil {
		onStatus("X session is ready.", 100)
	}
	return nil
}

func terminateProcess(command *exec.Cmd) {
	if command == nil || command.Process == nil || command.ProcessState != nil {
		return
	}
	if runtime.GOOS == "windows" {
		_ = exec.Command(
			"taskkill",
			"/PID", strconv.Itoa(command.Process.Pid),
			"/T",
			"/F",
		).Run()
		return
	}
	_ = command.Process.Kill()
}

func (m *Manager) load() error {
	data, err := os.ReadFile(m.runtimePath)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, &m.runtime)
}

func (m *Manager) save() error {
	m.mu.RLock()
	data, err := json.MarshalIndent(m.runtime, "", "  ")
	m.mu.RUnlock()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(m.runtimePath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(m.runtimePath, data, 0o600)
}

func (m *Manager) profileHasSessionData() bool {
	if info, err := os.Stat(m.profileDir); err != nil || !info.IsDir() {
		return false
	}
	if info, err := os.Stat(m.profileMarkerPath()); err != nil || info.IsDir() || info.Size() == 0 {
		return false
	}
	for _, relative := range []string{
		filepath.Join("Default", "Network", "Cookies"),
		filepath.Join("Default", "Cookies"),
	} {
		path := filepath.Join(m.profileDir, relative)
		if info, err := os.Stat(path); err == nil && !info.IsDir() && info.Size() > 0 {
			return true
		}
	}
	return false
}

func (m *Manager) saveProfileMarker(capturedAt time.Time) error {
	if err := os.MkdirAll(m.profileDir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(map[string]string{
		"capturedAt": capturedAt.Format(time.RFC3339),
	}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(m.profileMarkerPath(), data, 0o600)
}

func (m *Manager) profileMarkerPath() string {
	return filepath.Join(m.profileDir, ".trex-session.json")
}

func (m *Manager) cookieLocked(name string) string {
	return cookieValue(m.runtime.Cookies, name)
}

func cookieValue(cookies []Cookie, name string) string {
	for _, cookie := range cookies {
		if cookie.Name == name {
			return cookie.Value
		}
	}
	return ""
}

func findEdge() (string, error) {
	for _, path := range []string{
		`C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`,
		`C:\Program Files\Microsoft\Edge\Application\msedge.exe`,
	} {
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
	}
	return "", errors.New("Microsoft Edge was not found")
}

func availablePort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port, nil
}

func queryIDFromGraphQLURL(rawURL, operation string) string {
	return graphQLOperationFromURL(rawURL, operation).QueryID
}

func graphQLOperationFromURL(rawURL, operation string) GraphQLOperation {
	parsed, err := neturl.Parse(rawURL)
	if err != nil {
		return GraphQLOperation{}
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	for index := 0; index+4 < len(parts); index++ {
		if parts[index] == "i" && parts[index+1] == "api" && parts[index+2] == "graphql" && parts[index+4] == operation {
			result := GraphQLOperation{
				QueryID: parts[index+3], Operation: operation, RequestURL: rawURL,
				Variables: map[string]any{}, Features: map[string]any{}, FieldToggles: map[string]any{},
				Headers: map[string]string{},
			}
			decodeQueryJSON(parsed.Query().Get("variables"), &result.Variables)
			decodeQueryJSON(parsed.Query().Get("features"), &result.Features)
			decodeQueryJSON(parsed.Query().Get("fieldToggles"), &result.FieldToggles)
			return result
		}
	}
	return GraphQLOperation{}
}

func mergeHeaderMaps(existing map[string]string, incoming map[string]any) map[string]string {
	if existing == nil {
		existing = map[string]string{}
	}
	for key, value := range incoming {
		existing[strings.ToLower(key)] = fmt.Sprint(value)
	}
	return existing
}

func selectedRequestHeaders(headers map[string]string) map[string]string {
	result := map[string]string{}
	for _, key := range []string{
		"x-client-transaction-id",
		"sec-ch-ua",
		"sec-ch-ua-mobile",
		"sec-ch-ua-platform",
		"sec-gpc",
		"x-twitter-client-language",
	} {
		if value := headers[key]; value != "" {
			result[key] = value
		}
	}
	return result
}

func decodeQueryJSON(value string, target *map[string]any) {
	if strings.TrimSpace(value) == "" {
		return
	}
	_ = json.Unmarshal([]byte(value), target)
}

type debuggerTarget struct {
	Type                 string `json:"type"`
	URL                  string `json:"url"`
	WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
}

func findPageTarget(client *http.Client, port int) (debuggerTarget, error) {
	response, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/json/list", port))
	if err != nil {
		return debuggerTarget{}, err
	}
	defer response.Body.Close()
	data, _ := io.ReadAll(response.Body)
	var targets []debuggerTarget
	if err := json.Unmarshal(data, &targets); err != nil {
		return debuggerTarget{}, err
	}
	for _, target := range targets {
		if target.Type == "page" && strings.Contains(target.URL, "x.com") {
			return target, nil
		}
	}
	for _, target := range targets {
		if target.Type == "page" {
			return target, nil
		}
	}
	return debuggerTarget{}, errors.New("no Edge page target")
}

func decodeCookies(raw []any) []Cookie {
	cookies := make([]Cookie, 0, len(raw))
	for _, item := range raw {
		value, _ := item.(map[string]any)
		domain := fmt.Sprint(value["domain"])
		if !strings.Contains(domain, "x.com") && !strings.Contains(domain, "twitter.com") {
			continue
		}
		expires, _ := number(value["expires"])
		cookies = append(cookies, Cookie{
			Name: fmt.Sprint(value["name"]), Value: fmt.Sprint(value["value"]),
			Domain: domain, Path: fmt.Sprint(value["path"]), Expires: expires,
			HTTPOnly: boolValue(value["httpOnly"]), Secure: boolValue(value["secure"]),
			SameSite: fmt.Sprint(value["sameSite"]),
		})
	}
	return cookies
}

func number(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case int:
		return float64(typed), true
	}
	return 0, false
}

func boolValue(value any) bool {
	result, _ := value.(bool)
	return result
}
