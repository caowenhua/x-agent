package daemon

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/caowenhua/x-agent/xxx-code/internal/config"
	"github.com/caowenhua/x-agent/xxx-code/internal/diag"
	"github.com/caowenhua/x-agent/xxx-code/internal/engine"
	"github.com/caowenhua/x-agent/xxx-code/internal/persist"
	"github.com/caowenhua/x-agent/xxx-code/internal/sse"
	"github.com/caowenhua/x-agent/xxx-code/internal/tools"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

type daemonTestProvider struct{}

type blockingProvider struct {
	started chan struct{}
}

type toolAuditProvider struct{}

type daemonStreamingProvider struct{}

type daemonOrchestrationProvider struct{}

func (p *daemonTestProvider) CreateMessage(ctx context.Context, request engine.CompletionRequest) (engine.CompletionResponse, error) {
	_ = ctx
	text := ""
	for i := len(request.Messages) - 1; i >= 0; i-- {
		if request.Messages[i].Role == engine.RoleUser {
			text = request.Messages[i].Text()
			break
		}
	}
	return engine.CompletionResponse{
		Message: engine.NewTextMessage(engine.RoleAssistant, "reply:"+text),
	}, nil
}

func (p *blockingProvider) CreateMessage(ctx context.Context, request engine.CompletionRequest) (engine.CompletionResponse, error) {
	_ = request
	select {
	case <-p.started:
	default:
		close(p.started)
	}
	<-ctx.Done()
	return engine.CompletionResponse{}, ctx.Err()
}

func (p *toolAuditProvider) CreateMessage(ctx context.Context, request engine.CompletionRequest) (engine.CompletionResponse, error) {
	_ = ctx
	if toolResult, ok := latestDaemonToolResult(request.Messages); ok {
		return engine.CompletionResponse{
			Message: engine.NewTextMessage(engine.RoleAssistant, "reply:"+toolResult),
		}, nil
	}
	if latestDaemonUserText(request.Messages) == "policy block" {
		input, _ := json.Marshal(map[string]any{
			"command": "pwd",
		})
		return engine.CompletionResponse{
			Message: engine.Message{
				Role: engine.RoleAssistant,
				Content: []engine.Block{
					{Type: engine.BlockText, Text: "attempting bash"},
					{Type: engine.BlockToolUse, ID: "toolu_bash", Name: "bash", Input: input},
				},
			},
		}, nil
	}
	return engine.CompletionResponse{
		Message: engine.NewTextMessage(engine.RoleAssistant, "reply:"+latestDaemonUserText(request.Messages)),
	}, nil
}

func (p *daemonStreamingProvider) CreateMessage(ctx context.Context, request engine.CompletionRequest) (engine.CompletionResponse, error) {
	_ = ctx
	prompt := latestDaemonUserText(request.Messages)
	return engine.CompletionResponse{
		Message: engine.NewTextMessage(engine.RoleAssistant, "reply:"+prompt),
	}, nil
}

func (p *daemonStreamingProvider) CreateMessageStream(ctx context.Context, request engine.CompletionRequest, handle func(engine.StreamEvent)) (engine.CompletionResponse, error) {
	_ = ctx
	prompt := latestDaemonUserText(request.Messages)
	for _, chunk := range []string{"reply:", prompt} {
		handle(engine.StreamEvent{
			Kind: engine.StreamEventTextDelta,
			Text: chunk,
		})
	}
	return engine.CompletionResponse{
		Message: engine.NewTextMessage(engine.RoleAssistant, "reply:"+prompt),
	}, nil
}

func (p *daemonOrchestrationProvider) CreateMessage(ctx context.Context, request engine.CompletionRequest) (engine.CompletionResponse, error) {
	if toolResult, ok := latestDaemonToolResult(request.Messages); ok {
		return engine.CompletionResponse{
			Message: engine.NewTextMessage(engine.RoleAssistant, "tool-result:"+toolResult),
		}, nil
	}

	switch prompt := latestDaemonUserText(request.Messages); prompt {
	case "delegate work":
		input, _ := json.Marshal(map[string]any{
			"name":       "worker",
			"prompt":     "child task",
			"background": false,
		})
		return engine.CompletionResponse{
			Message: engine.Message{
				Role: engine.RoleAssistant,
				Content: []engine.Block{
					{Type: engine.BlockText, Text: "delegating"},
					{Type: engine.BlockToolUse, ID: "toolu_delegate", Name: "agent_spawn", Input: input},
				},
			},
		}, nil
	case "background work":
		input, _ := json.Marshal(map[string]any{
			"name":       "worker",
			"prompt":     "block child",
			"background": true,
		})
		return engine.CompletionResponse{
			Message: engine.Message{
				Role: engine.RoleAssistant,
				Content: []engine.Block{
					{Type: engine.BlockText, Text: "delegating"},
					{Type: engine.BlockToolUse, ID: "toolu_delegate_bg", Name: "agent_spawn", Input: input},
				},
			},
		}, nil
	case "fanout work":
		input, _ := json.Marshal(map[string]any{
			"wait":         true,
			"max_parallel": 1,
			"tasks": []map[string]any{
				{"name": "one", "prompt": "task one"},
				{"name": "two", "prompt": "task two"},
			},
		})
		return engine.CompletionResponse{
			Message: engine.Message{
				Role: engine.RoleAssistant,
				Content: []engine.Block{
					{Type: engine.BlockText, Text: "fanout"},
					{Type: engine.BlockToolUse, ID: "toolu_fanout", Name: "agent_fanout", Input: input},
				},
			},
		}, nil
	case "block child":
		<-ctx.Done()
		return engine.CompletionResponse{}, ctx.Err()
	default:
		return engine.CompletionResponse{
			Message: engine.NewTextMessage(engine.RoleAssistant, "reply:"+prompt),
		}, nil
	}
}

func TestDaemonSessionLifecycle(t *testing.T) {
	server, testServer := newTestDaemon(t)
	defer func() {
		_ = server.Close()
		testServer.Close()
	}()

	created := postJSON(t, testServer.URL+"/v1/sessions", map[string]any{}, http.StatusCreated)
	session := created["session"].(map[string]any)
	sessionID := session["id"].(string)

	result := postJSON(t, testServer.URL+"/v1/sessions/"+sessionID+"/turns", map[string]any{
		"prompt": "hello daemon",
	}, http.StatusOK)
	runResult := result["result"].(map[string]any)
	if runResult["final_text"] != "reply:hello daemon" {
		t.Fatalf("unexpected final text: %+v", runResult)
	}

	messages := getJSON(t, testServer.URL+"/v1/sessions/"+sessionID+"/messages?limit=2", http.StatusOK)
	if got := len(messages["messages"].([]any)); got != 2 {
		t.Fatalf("expected 2 messages, got %d", got)
	}

	sessions := getJSON(t, testServer.URL+"/v1/sessions", http.StatusOK)
	items := sessions["sessions"].([]any)
	if len(items) != 1 {
		t.Fatalf("expected one session, got %d", len(items))
	}
	summary := items[0].(map[string]any)
	if summary["id"] != sessionID {
		t.Fatalf("unexpected session summary: %+v", summary)
	}
	if summary["loaded"] != true {
		t.Fatalf("expected loaded session summary, got %+v", summary)
	}
}

func TestDaemonCanReloadSavedSession(t *testing.T) {
	cfg := newTestConfig(t)
	serverA := New(cfg, io.Discard, io.Discard, func(config.Config) engine.Provider {
		return &daemonTestProvider{}
	})
	httpA := httptest.NewServer(serverA.Handler())

	created := postJSON(t, httpA.URL+"/v1/sessions", map[string]any{}, http.StatusCreated)
	sessionID := created["session"].(map[string]any)["id"].(string)
	postJSON(t, httpA.URL+"/v1/sessions/"+sessionID+"/turns", map[string]any{
		"prompt": "first turn",
	}, http.StatusOK)

	httpA.Close()
	if err := serverA.Close(); err != nil {
		t.Fatal(err)
	}

	serverB := New(cfg, io.Discard, io.Discard, func(config.Config) engine.Provider {
		return &daemonTestProvider{}
	})
	httpB := httptest.NewServer(serverB.Handler())
	defer func() {
		_ = serverB.Close()
		httpB.Close()
	}()

	sessions := getJSON(t, httpB.URL+"/v1/sessions", http.StatusOK)
	items := sessions["sessions"].([]any)
	if len(items) != 1 {
		t.Fatalf("expected one saved session, got %d", len(items))
	}
	summary := items[0].(map[string]any)
	if summary["id"] != sessionID {
		t.Fatalf("unexpected persisted session summary: %+v", summary)
	}
	if summary["loaded"] != false {
		t.Fatalf("expected persisted session to be unloaded before reopen, got %+v", summary)
	}

	postJSON(t, httpB.URL+"/v1/sessions/"+sessionID+"/turns", map[string]any{
		"prompt": "second turn",
	}, http.StatusOK)

	messages := getJSON(t, httpB.URL+"/v1/sessions/"+sessionID+"/messages", http.StatusOK)
	items = messages["messages"].([]any)
	if len(items) != 4 {
		t.Fatalf("expected 4 resumed messages, got %d", len(items))
	}
	var texts []string
	for _, item := range items {
		message := item.(map[string]any)
		content := message["content"].([]any)
		if len(content) == 0 {
			continue
		}
		block := content[0].(map[string]any)
		if text, ok := block["text"].(string); ok {
			texts = append(texts, text)
		}
	}
	joined := strings.Join(texts, " | ")
	if !strings.Contains(joined, "first turn") || !strings.Contains(joined, "second turn") {
		t.Fatalf("expected resumed transcript, got %s", joined)
	}
}

func TestDaemonCanRequireBearerToken(t *testing.T) {
	cfg := newTestConfig(t)
	cfg.DaemonToken = "secret-token"
	server := New(cfg, io.Discard, io.Discard, func(config.Config) engine.Provider {
		return &daemonTestProvider{}
	})
	testServer := httptest.NewServer(server.Handler())
	defer func() {
		_ = server.Close()
		testServer.Close()
	}()

	req, err := http.NewRequest(http.MethodGet, testServer.URL+"/v1/sessions", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 401 without token, got %d: %s", resp.StatusCode, string(body))
	}

	req, err = http.NewRequest(http.MethodGet, testServer.URL+"/v1/sessions", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer secret-token")
	payload := doJSON(t, req, http.StatusOK)
	if len(payload["sessions"].([]any)) != 0 {
		t.Fatalf("expected no sessions, got %+v", payload)
	}
}

func TestDaemonCanReloadRotatingTokenFile(t *testing.T) {
	cfg := newTestConfig(t)
	tokenFile := filepath.Join(t.TempDir(), "daemon-token.txt")
	if err := os.WriteFile(tokenFile, []byte("old-secret\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg.DaemonTokenFile = tokenFile
	server := New(cfg, io.Discard, io.Discard, func(config.Config) engine.Provider {
		return &daemonTestProvider{}
	})
	testServer := httptest.NewServer(server.Handler())
	defer func() {
		_ = server.Close()
		testServer.Close()
	}()

	doAuthorizedJSON(t, "old-secret", mustRequest(t, http.MethodGet, testServer.URL+"/v1/sessions", nil), http.StatusOK)

	if err := os.WriteFile(tokenFile, []byte(`["new-secret","old-secret"]`), 0o644); err != nil {
		t.Fatal(err)
	}
	doAuthorizedJSON(t, "old-secret", mustRequest(t, http.MethodGet, testServer.URL+"/v1/sessions", nil), http.StatusOK)
	doAuthorizedJSON(t, "new-secret", mustRequest(t, http.MethodGet, testServer.URL+"/v1/sessions", nil), http.StatusOK)

	if err := os.WriteFile(tokenFile, []byte("new-secret\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	doAuthorizedJSON(t, "new-secret", mustRequest(t, http.MethodGet, testServer.URL+"/v1/sessions", nil), http.StatusOK)
	doAuthorizedJSON(t, "old-secret", mustRequest(t, http.MethodGet, testServer.URL+"/v1/sessions", nil), http.StatusUnauthorized)
}

func TestDaemonCanValidateReloadAndHealthCheckMCP(t *testing.T) {
	cfg := newTestConfig(t)
	mcpServer := newDaemonMCPHTTPServer(t)
	defer mcpServer.Close()

	server := New(cfg, io.Discard, io.Discard, func(config.Config) engine.Provider {
		return &daemonTestProvider{}
	})
	testServer := httptest.NewServer(server.Handler())
	defer func() {
		_ = server.Close()
		testServer.Close()
	}()

	created := postJSON(t, testServer.URL+"/v1/sessions", map[string]any{}, http.StatusCreated)
	sessionID := created["session"].(map[string]any)["id"].(string)

	validate := postJSON(t, testServer.URL+"/v1/sessions/"+sessionID+"/mcp/validate", map[string]any{}, http.StatusOK)
	report := validate["validation"].(map[string]any)
	if report["present"] != false {
		t.Fatalf("expected missing MCP config to be reported, got %+v", report)
	}

	configJSON := fmt.Sprintf(`{
  "mcpServers": {
    "tester": {
      "transport": "http",
      "url": %q
    }
  }
}`, mcpServer.URL)
	if err := os.WriteFile(filepath.Join(cfg.WorkingDir, ".mcp.json"), []byte(configJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	reloaded := postJSON(t, testServer.URL+"/v1/sessions/"+sessionID+"/mcp/reload", map[string]any{}, http.StatusOK)
	summary := reloaded["mcp"].(map[string]any)
	if summary["server_count"].(float64) != 1 {
		t.Fatalf("expected one MCP server after reload, got %+v", summary)
	}

	health := getJSON(t, testServer.URL+"/v1/sessions/"+sessionID+"/mcp/health", http.StatusOK)
	statuses := health["statuses"].([]any)
	if len(statuses) != 1 {
		t.Fatalf("expected one MCP health status, got %+v", health)
	}
	status := statuses[0].(map[string]any)
	if status["healthy"] != true {
		t.Fatalf("expected MCP server to be healthy, got %+v", status)
	}
	if strings.TrimSpace(status["status"].(string)) != "connected" {
		t.Fatalf("expected connected MCP status, got %+v", status)
	}
	if _, ok := status["last_checked_at"].(string); !ok {
		t.Fatalf("expected health check timestamp, got %+v", status)
	}
}

func TestManagedSessionCloseCancelsActiveTurnAndPersistsState(t *testing.T) {
	cfg := newTestConfig(t)
	provider := &blockingProvider{started: make(chan struct{})}
	server := New(cfg, io.Discard, io.Discard, func(config.Config) engine.Provider {
		return provider
	})
	session, err := server.openSession(context.Background(), "closing-session", false)
	if err != nil {
		t.Fatal(err)
	}

	errCh := make(chan error, 1)
	go func() {
		_, runErr := session.runTurn(context.Background(), "block until close")
		errCh <- runErr
	}()

	select {
	case <-provider.started:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for blocking turn to start")
	}

	if err := session.close(); err != nil {
		t.Fatal(err)
	}

	select {
	case runErr := <-errCh:
		if !errors.Is(runErr, context.Canceled) {
			t.Fatalf("expected close to cancel the active turn, got %v", runErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for active turn to stop during close")
	}

	state, err := persist.Load(session.sessionFile)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Main) != 1 {
		t.Fatalf("expected partially completed turn to be persisted, got %d messages", len(state.Main))
	}
	if got := strings.TrimSpace(state.Main[0].Text()); got != "block until close" {
		t.Fatalf("unexpected persisted message: %q", got)
	}
}

func TestManagedSessionPublishEventDoesNotBlockOnSlowSubscriber(t *testing.T) {
	session := &managedSession{
		subs: make(map[int]*eventSubscriber),
	}
	session.subs[1] = &eventSubscriber{ch: make(chan engine.Event, 1)}
	session.subs[1].ch <- engine.Event{Kind: engine.EventAssistantText}

	done := make(chan struct{})
	go func() {
		session.publishEvent(engine.Event{Kind: engine.EventToolCall})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("publishEvent blocked on a full subscriber channel")
	}

	if got := len(session.subs[1].ch); got != 1 {
		t.Fatalf("expected bounded subscriber buffer to stay full at 1 item, got %d", got)
	}
}

func TestDaemonErrorsIncludeStructuredCode(t *testing.T) {
	cfg := newTestConfig(t)
	cfg.DaemonToken = "secret-token"
	server := New(cfg, io.Discard, io.Discard, func(config.Config) engine.Provider {
		return &daemonTestProvider{}
	})
	testServer := httptest.NewServer(server.Handler())
	defer func() {
		_ = server.Close()
		testServer.Close()
	}()

	req, err := http.NewRequest(http.MethodGet, testServer.URL+"/v1/sessions", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}

	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload["code"] != "unauthorized" {
		t.Fatalf("expected unauthorized code, got %+v", payload)
	}

	req, err = http.NewRequest(http.MethodGet, testServer.URL+"/v1/sessions/missing-session", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer secret-token")
	payload = doJSON(t, req, http.StatusNotFound)
	if payload["code"] != "session_not_found" {
		t.Fatalf("expected session_not_found code, got %+v", payload)
	}
}

func TestDaemonAddsTraceIDHeader(t *testing.T) {
	server, testServer := newTestDaemon(t)
	defer func() {
		_ = server.Close()
		testServer.Close()
	}()

	req, err := http.NewRequest(http.MethodGet, testServer.URL+"/healthz", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set(diag.TraceHeader, "trace_test")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if got := resp.Header.Get(diag.TraceHeader); got != "trace_test" {
		t.Fatalf("expected trace header to be preserved, got %q", got)
	}

	req, err = http.NewRequest(http.MethodGet, testServer.URL+"/healthz", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if !strings.HasPrefix(resp.Header.Get(diag.TraceHeader), "trace_") {
		t.Fatalf("expected daemon to generate a trace id, got %q", resp.Header.Get(diag.TraceHeader))
	}
}

func TestDaemonAuditLogsAuthFailuresAndPolicyBlocks(t *testing.T) {
	cfg := newTestConfig(t)
	cfg.DaemonToken = "secret-token"
	cfg.BashEnabled = false
	server := New(cfg, io.Discard, io.Discard, func(config.Config) engine.Provider {
		return &toolAuditProvider{}
	})
	testServer := httptest.NewServer(server.Handler())
	defer func() {
		_ = server.Close()
		testServer.Close()
	}()

	req, err := http.NewRequest(http.MethodGet, testServer.URL+"/v1/sessions", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}

	created := doAuthorizedJSON(t, "secret-token", mustRequest(t, http.MethodPost, testServer.URL+"/v1/sessions", map[string]any{
		"session_id": "audit-session",
	}), http.StatusCreated)
	sessionID := created["session"].(map[string]any)["id"].(string)

	doAuthorizedJSON(t, "secret-token", mustRequest(t, http.MethodPost, testServer.URL+"/v1/sessions/"+sessionID+"/turns", map[string]any{
		"prompt": "policy block",
	}), http.StatusOK)

	globalAudit := doAuthorizedJSON(t, "secret-token", mustRequest(t, http.MethodGet, testServer.URL+"/v1/audit?limit=50", nil), http.StatusOK)
	if !hasAuditEvent(globalAudit["events"].([]any), "auth", "unauthorized") {
		t.Fatalf("expected auth failure in global audit log, got %+v", globalAudit)
	}

	sessionAudit := doAuthorizedJSON(t, "secret-token", mustRequest(t, http.MethodGet, testServer.URL+"/v1/sessions/"+sessionID+"/audit?limit=50", nil), http.StatusOK)
	if !hasAuditEvent(sessionAudit["events"].([]any), "tool_result", "policy_block") {
		t.Fatalf("expected policy block in session audit log, got %+v", sessionAudit)
	}
}

func TestDaemonACLRestrictsModesAndSessionPrefixes(t *testing.T) {
	cfg := newTestConfig(t)
	cfg.DaemonAllowModes = []string{daemonModeSessionsRead, daemonModeSessionsWrite}
	cfg.DaemonAllowSessionPrefixes = []string{"team-"}
	server := New(cfg, io.Discard, io.Discard, func(config.Config) engine.Provider {
		return &daemonTestProvider{}
	})
	testServer := httptest.NewServer(server.Handler())
	defer func() {
		_ = server.Close()
		testServer.Close()
	}()

	getJSON(t, testServer.URL+"/v1/sessions", http.StatusOK)

	blocked := doJSON(t, mustRequest(t, http.MethodPost, testServer.URL+"/v1/sessions", map[string]any{
		"session_id": "other-1",
	}), http.StatusForbidden)
	if blocked["code"] != "forbidden" {
		t.Fatalf("expected forbidden code for blocked prefix, got %+v", blocked)
	}

	created := doJSON(t, mustRequest(t, http.MethodPost, testServer.URL+"/v1/sessions", map[string]any{
		"session_id": "team-1",
	}), http.StatusCreated)
	sessionID := created["session"].(map[string]any)["id"].(string)

	turnPayload := doJSON(t, mustRequest(t, http.MethodPost, testServer.URL+"/v1/sessions/"+sessionID+"/turns", map[string]any{
		"prompt": "blocked by mode",
	}), http.StatusForbidden)
	if turnPayload["code"] != "forbidden" {
		t.Fatalf("expected forbidden code for blocked mode, got %+v", turnPayload)
	}
}

func TestDaemonRateLimitRejectsBurst(t *testing.T) {
	cfg := newTestConfig(t)
	cfg.DaemonRateLimitPerMinute = 1
	cfg.DaemonRateLimitBurst = 1
	server := New(cfg, io.Discard, io.Discard, func(config.Config) engine.Provider {
		return &daemonTestProvider{}
	})
	testServer := httptest.NewServer(server.Handler())
	defer func() {
		_ = server.Close()
		testServer.Close()
	}()

	getJSON(t, testServer.URL+"/v1/sessions", http.StatusOK)

	req, err := http.NewRequest(http.MethodGet, testServer.URL+"/v1/sessions", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 429, got %d: %s", resp.StatusCode, string(body))
	}
	if resp.Header.Get("Retry-After") == "" {
		t.Fatal("expected Retry-After header on rate limited response")
	}
	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload["code"] != "rate_limited" {
		t.Fatalf("expected rate_limited code, got %+v", payload)
	}
}

func TestDaemonCanInspectPolicyHooksAndManagePlugins(t *testing.T) {
	cfg := newTestConfig(t)
	cfg.HookBeforeTool = "echo before"
	cfg.HookAfterTool = "echo after"
	cfg.HookAfterTurn = "echo turn"
	cfg.HookAgentEvent = "echo agent"
	cfg.HookEventFile = filepath.Join(cfg.WorkingDir, ".xxx-code", "hooks.jsonl")

	sourceDir := writeDaemonPluginSource(t, filepath.Join(cfg.WorkingDir, "plugin-sources"), "echoer", "#!/bin/sh\ncat\n")

	server := New(cfg, io.Discard, io.Discard, func(config.Config) engine.Provider {
		return &daemonTestProvider{}
	})
	testServer := httptest.NewServer(server.Handler())
	defer func() {
		_ = server.Close()
		testServer.Close()
	}()

	created := postJSON(t, testServer.URL+"/v1/sessions", map[string]any{
		"session_id": "plugin-session",
	}, http.StatusCreated)
	sessionID := created["session"].(map[string]any)["id"].(string)

	policy := getJSON(t, testServer.URL+"/v1/sessions/"+sessionID+"/policy", http.StatusOK)
	policyPayload := policy["policy"].(map[string]any)
	if policyPayload["BashEnabled"] != true {
		t.Fatalf("expected bash-enabled policy, got %+v", policyPayload)
	}

	hooksPayload := getJSON(t, testServer.URL+"/v1/sessions/"+sessionID+"/hooks", http.StatusOK)["hooks"].(map[string]any)
	if hooksPayload["before_tool"] != "echo before" || hooksPayload["timeout"] != time.Second.String() {
		t.Fatalf("unexpected hook config payload: %+v", hooksPayload)
	}
	if hooksPayload["event_file"] == "" {
		t.Fatalf("expected hook event file to be exposed, got %+v", hooksPayload)
	}

	plugins := getJSON(t, testServer.URL+"/v1/sessions/"+sessionID+"/plugins", http.StatusOK)["plugins"].(map[string]any)
	if plugins["plugin_count"].(float64) != 0 || plugins["tool_count"].(float64) != 0 {
		t.Fatalf("expected empty plugin summary, got %+v", plugins)
	}

	reloaded := postJSON(t, testServer.URL+"/v1/sessions/"+sessionID+"/plugins/reload", map[string]any{}, http.StatusOK)["plugins"].(map[string]any)
	if strings.TrimSpace(reloaded["plugin_dir"].(string)) == "" {
		t.Fatalf("expected plugin dir after reload, got %+v", reloaded)
	}

	validation := postJSON(t, testServer.URL+"/v1/sessions/"+sessionID+"/plugins/validate", map[string]any{
		"source": sourceDir,
	}, http.StatusOK)["validation"].(map[string]any)
	if validation["valid"] != true || validation["plugin_name"] != "echoer" || validation["tool_count"].(float64) != 1 {
		t.Fatalf("unexpected plugin validation report: %+v", validation)
	}

	installed := postJSON(t, testServer.URL+"/v1/sessions/"+sessionID+"/plugins/install", map[string]any{
		"source": sourceDir,
	}, http.StatusOK)["plugins"].(map[string]any)
	if installed["plugin_count"].(float64) != 1 || installed["tool_count"].(float64) != 1 {
		t.Fatalf("unexpected installed plugin summary: %+v", installed)
	}
	statuses := installed["statuses"].([]any)
	if len(statuses) != 1 || statuses[0].(map[string]any)["name"] != "echoer" {
		t.Fatalf("unexpected plugin statuses after install: %+v", installed)
	}

	removed := postJSON(t, testServer.URL+"/v1/sessions/"+sessionID+"/plugins/remove", map[string]any{
		"name": "echoer",
	}, http.StatusOK)["plugins"].(map[string]any)
	if removed["plugin_count"].(float64) != 0 || removed["tool_count"].(float64) != 0 {
		t.Fatalf("expected empty plugin summary after removal, got %+v", removed)
	}

	req, err := http.NewRequest(http.MethodGet, testServer.URL+"/v1/sessions/"+sessionID+"/plugins/install", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, body := doRawRequest(t, req, http.StatusMethodNotAllowed)
	if allow := resp.Header.Get("Allow"); allow != http.MethodPost {
		t.Fatalf("expected Allow header %q, got %q", http.MethodPost, allow)
	}
	var methodPayload map[string]any
	if err := json.Unmarshal(body, &methodPayload); err != nil {
		t.Fatal(err)
	}
	if methodPayload["code"] != "method_not_allowed" {
		t.Fatalf("expected method_not_allowed code, got %+v", methodPayload)
	}

	req, err = http.NewRequest(http.MethodPost, testServer.URL+"/v1/sessions/"+sessionID+"/plugins/validate", strings.NewReader(`{"source":`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	_, body = doRawRequest(t, req, http.StatusBadRequest)
	var invalidPayload map[string]any
	if err := json.Unmarshal(body, &invalidPayload); err != nil {
		t.Fatal(err)
	}
	if invalidPayload["code"] != "invalid_json" {
		t.Fatalf("expected invalid_json code, got %+v", invalidPayload)
	}
}

func TestDaemonCanListWaitSendAndCancelAgents(t *testing.T) {
	cfg := newTestConfig(t)
	server := New(cfg, io.Discard, io.Discard, func(config.Config) engine.Provider {
		return &daemonOrchestrationProvider{}
	})
	testServer := httptest.NewServer(server.Handler())
	defer func() {
		_ = server.Close()
		testServer.Close()
	}()

	created := postJSON(t, testServer.URL+"/v1/sessions", map[string]any{
		"session_id": "agent-session",
	}, http.StatusCreated)
	sessionID := created["session"].(map[string]any)["id"].(string)

	result := postJSON(t, testServer.URL+"/v1/sessions/"+sessionID+"/turns", map[string]any{
		"prompt": "delegate work",
	}, http.StatusOK)["result"].(map[string]any)
	if !strings.Contains(result["final_text"].(string), "reply:child task") {
		t.Fatalf("expected delegated child result, got %+v", result)
	}

	agentsPayload := getJSON(t, testServer.URL+"/v1/sessions/"+sessionID+"/agents", http.StatusOK)["agents"].([]any)
	if len(agentsPayload) != 1 {
		t.Fatalf("expected one delegated agent, got %+v", agentsPayload)
	}
	agentID := agentsPayload[0].(map[string]any)["id"].(string)

	waited := postJSON(t, testServer.URL+"/v1/sessions/"+sessionID+"/agents/"+agentID+"/wait", map[string]any{
		"timeout_seconds": 5,
	}, http.StatusOK)["agent"].(map[string]any)
	if waited["status"] != string(engine.AgentIdle) {
		t.Fatalf("expected idle agent after wait, got %+v", waited)
	}

	sent := postJSON(t, testServer.URL+"/v1/sessions/"+sessionID+"/agents/"+agentID+"/send", map[string]any{
		"prompt": "follow-up",
	}, http.StatusOK)["agent"].(map[string]any)
	if sent["status"] != string(engine.AgentIdle) || sent["result"] != "reply:follow-up" {
		t.Fatalf("unexpected send result: %+v", sent)
	}

	backgroundSession := postJSON(t, testServer.URL+"/v1/sessions", map[string]any{
		"session_id": "background-agent-session",
	}, http.StatusCreated)["session"].(map[string]any)
	backgroundSessionID := backgroundSession["id"].(string)

	postJSON(t, testServer.URL+"/v1/sessions/"+backgroundSessionID+"/turns", map[string]any{
		"prompt": "background work",
	}, http.StatusOK)

	agentsPayload = getJSON(t, testServer.URL+"/v1/sessions/"+backgroundSessionID+"/agents", http.StatusOK)["agents"].([]any)
	var backgroundID string
	for _, raw := range agentsPayload {
		agent := raw.(map[string]any)
		if agent["prompt"] == "block child" {
			backgroundID = agent["id"].(string)
			break
		}
	}
	if backgroundID == "" {
		t.Fatalf("expected background agent in %+v", agentsPayload)
	}

	cancelled := postJSON(t, testServer.URL+"/v1/sessions/"+backgroundSessionID+"/agents/"+backgroundID+"/cancel", map[string]any{
		"recursive": true,
	}, http.StatusOK)["agent"].(map[string]any)
	if cancelled["status"] != string(engine.AgentCancelled) {
		t.Fatalf("expected cancelled background agent, got %+v", cancelled)
	}

	notFound := postJSON(t, testServer.URL+"/v1/sessions/"+sessionID+"/agents/missing/send", map[string]any{
		"prompt": "hello",
	}, http.StatusNotFound)
	if notFound["code"] != "agent_not_found" {
		t.Fatalf("expected agent_not_found code, got %+v", notFound)
	}
}

func TestDaemonCanInspectAndResumeWorkflows(t *testing.T) {
	cfg := newTestConfig(t)
	server := New(cfg, io.Discard, io.Discard, func(config.Config) engine.Provider {
		return &daemonOrchestrationProvider{}
	})
	testServer := httptest.NewServer(server.Handler())
	defer func() {
		_ = server.Close()
		testServer.Close()
	}()

	created := postJSON(t, testServer.URL+"/v1/sessions", map[string]any{
		"session_id": "workflow-session",
	}, http.StatusCreated)
	sessionID := created["session"].(map[string]any)["id"].(string)

	result := postJSON(t, testServer.URL+"/v1/sessions/"+sessionID+"/turns", map[string]any{
		"prompt": "fanout work",
	}, http.StatusOK)["result"].(map[string]any)
	finalText := result["final_text"].(string)
	if !strings.Contains(finalText, "reply:task one") || !strings.Contains(finalText, "reply:task two") {
		t.Fatalf("expected both workflow task results, got %q", finalText)
	}

	workflows := getJSON(t, testServer.URL+"/v1/sessions/"+sessionID+"/workflows", http.StatusOK)["workflows"].([]any)
	if len(workflows) != 1 {
		t.Fatalf("expected one workflow, got %+v", workflows)
	}
	workflowID := workflows[0].(map[string]any)["id"].(string)

	detail := getJSON(t, testServer.URL+"/v1/sessions/"+sessionID+"/workflows/"+workflowID, http.StatusOK)["workflow"].(map[string]any)
	if detail["id"] != workflowID || detail["status"] != string(tools.WorkflowCompleted) {
		t.Fatalf("unexpected workflow detail payload: %+v", detail)
	}

	tasksPayload := getJSON(t, testServer.URL+"/v1/sessions/"+sessionID+"/workflows/"+workflowID+"/tasks", http.StatusOK)["tasks"].([]any)
	if len(tasksPayload) != 2 {
		t.Fatalf("expected two workflow tasks, got %+v", tasksPayload)
	}

	resumed := postJSON(t, testServer.URL+"/v1/sessions/"+sessionID+"/workflows/"+workflowID+"/resume", map[string]any{
		"task_names": []string{"one"},
	}, http.StatusOK)
	workflow := resumed["workflow"].(map[string]any)
	if workflow["status"] != string(tools.WorkflowCompleted) {
		t.Fatalf("expected completed workflow after resume, got %+v", workflow)
	}

	byName := map[string]map[string]any{}
	for _, raw := range resumed["tasks"].([]any) {
		task := raw.(map[string]any)
		byName[task["name"].(string)] = task
	}
	if byName["one"]["attempts"].(float64) != 2 {
		t.Fatalf("expected task one to rerun, got %+v", byName["one"])
	}
	if byName["two"]["attempts"].(float64) != 1 {
		t.Fatalf("expected task two attempts to remain unchanged, got %+v", byName["two"])
	}
}

func TestDaemonTurnStreamEmitsEventsAndResult(t *testing.T) {
	cfg := newTestConfig(t)
	server := New(cfg, io.Discard, io.Discard, func(config.Config) engine.Provider {
		return &daemonStreamingProvider{}
	})
	testServer := httptest.NewServer(server.Handler())
	defer func() {
		_ = server.Close()
		testServer.Close()
	}()

	created := postJSON(t, testServer.URL+"/v1/sessions", map[string]any{
		"session_id": "stream-session",
	}, http.StatusCreated)
	sessionID := created["session"].(map[string]any)["id"].(string)

	req := mustRequest(t, http.MethodPost, testServer.URL+"/v1/sessions/"+sessionID+"/turns/stream", map[string]any{
		"prompt": "stream me",
	})
	req.Header.Set("Accept", "text/event-stream")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("unexpected status %d: %s", resp.StatusCode, string(body))
	}
	if contentType := resp.Header.Get("Content-Type"); !strings.Contains(contentType, "text/event-stream") {
		t.Fatalf("expected event stream content type, got %q", contentType)
	}

	parser := sse.NewParser(resp.Body)
	var eventTypes []string
	var finalEvent turnStreamEvent
	for {
		name, raw, err := parser.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if len(bytes.TrimSpace(raw)) == 0 {
			continue
		}

		var event turnStreamEvent
		if err := json.Unmarshal(raw, &event); err != nil {
			t.Fatal(err)
		}
		if name == "event" {
			eventTypes = append(eventTypes, event.Type)
			continue
		}
		if name == "result" {
			finalEvent = event
		}
	}

	if !containsString(eventTypes, string(engine.EventAssistantTextDelta)) {
		t.Fatalf("expected assistant delta event in %+v", eventTypes)
	}
	if !containsString(eventTypes, string(engine.EventAssistantTextDone)) {
		t.Fatalf("expected assistant done event in %+v", eventTypes)
	}
	if finalEvent.Result == nil || finalEvent.Session == nil {
		t.Fatalf("expected final stream result and session payload, got %+v", finalEvent)
	}
	if finalEvent.Result.FinalText != "reply:stream me" || finalEvent.Session.ID != sessionID {
		t.Fatalf("unexpected final stream event: %+v", finalEvent)
	}
}

func TestDaemonCanListReadAndFetchMCPMetadata(t *testing.T) {
	cfg := newTestConfig(t)
	mcpServer := newDaemonMCPHTTPServer(t)
	defer mcpServer.Close()

	configJSON := fmt.Sprintf(`{
  "mcpServers": {
    "tester": {
      "transport": "http",
      "url": %q
    }
  }
}`, mcpServer.URL)
	if err := os.WriteFile(filepath.Join(cfg.WorkingDir, ".mcp.json"), []byte(configJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	server := New(cfg, io.Discard, io.Discard, func(config.Config) engine.Provider {
		return &daemonTestProvider{}
	})
	testServer := httptest.NewServer(server.Handler())
	defer func() {
		_ = server.Close()
		testServer.Close()
	}()

	created := postJSON(t, testServer.URL+"/v1/sessions", map[string]any{
		"session_id": "mcp-metadata",
	}, http.StatusCreated)
	sessionID := created["session"].(map[string]any)["id"].(string)

	summary := getJSON(t, testServer.URL+"/v1/sessions/"+sessionID+"/mcp", http.StatusOK)["mcp"].(map[string]any)
	if summary["server_count"].(float64) != 1 || summary["tool_count"].(float64) != 1 {
		t.Fatalf("expected loaded MCP summary, got %+v", summary)
	}

	resources := getJSON(t, testServer.URL+"/v1/sessions/"+sessionID+"/mcp/resources", http.StatusOK)["resources"].([]any)
	if len(resources) != 1 || resources[0].(map[string]any)["uri"] != "file:///a" {
		t.Fatalf("unexpected MCP resources payload: %+v", resources)
	}

	templates := getJSON(t, testServer.URL+"/v1/sessions/"+sessionID+"/mcp/resource-templates", http.StatusOK)["resource_templates"].([]any)
	if len(templates) != 1 || templates[0].(map[string]any)["uri_template"] != "file:///dir/{f}" {
		t.Fatalf("unexpected MCP resource templates payload: %+v", templates)
	}

	prompts := getJSON(t, testServer.URL+"/v1/sessions/"+sessionID+"/mcp/prompts", http.StatusOK)["prompts"].([]any)
	if len(prompts) != 1 || prompts[0].(map[string]any)["name"] != "greet" {
		t.Fatalf("unexpected MCP prompts payload: %+v", prompts)
	}

	resource := postJSON(t, testServer.URL+"/v1/sessions/"+sessionID+"/mcp/read-resource", map[string]any{
		"server": "tester",
		"uri":    "file:///a",
	}, http.StatusOK)["resource"].(map[string]any)
	contents := resource["contents"].([]any)
	if len(contents) != 1 || contents[0].(map[string]any)["text"] != "alpha" {
		t.Fatalf("unexpected MCP resource details payload: %+v", resource)
	}

	prompt := postJSON(t, testServer.URL+"/v1/sessions/"+sessionID+"/mcp/get-prompt", map[string]any{
		"server":    "tester",
		"name":      "greet",
		"arguments": map[string]string{"name": "Pat"},
	}, http.StatusOK)["prompt"].(map[string]any)
	messages := prompt["messages"].([]any)
	if len(messages) != 1 || !strings.Contains(messages[0].(map[string]any)["content"].(string), "Say hi to Pat") {
		t.Fatalf("unexpected MCP prompt payload: %+v", prompt)
	}
}

func TestManagedSessionSubscriptionLifecycle(t *testing.T) {
	session := &managedSession{
		subs: make(map[int]*eventSubscriber),
	}

	subscription := session.subscribeEvents()
	session.publishEvent(engine.Event{Kind: engine.EventAssistantText, Text: "hello"})

	select {
	case event := <-subscription.events:
		if event.Text != "hello" {
			t.Fatalf("unexpected published event: %+v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for published event")
	}

	subscription.close()
	session.unsubscribeEvent(999)
	if cancel := session.markClosed(); cancel != nil {
		t.Fatalf("expected no active turn cancel func, got %v", cancel)
	}
	if !session.isClosed() {
		t.Fatal("expected managed session to be marked closed")
	}

	closedSubscription := session.subscribeEvents()
	if _, ok := <-closedSubscription.events; ok {
		t.Fatal("expected closed session subscription to return a closed channel")
	}
}

func TestDaemonHelperFunctionsAndRun(t *testing.T) {
	recorder := &statusRecorder{ResponseWriter: &flushHijackRecorder{ResponseRecorder: httptest.NewRecorder()}}
	recorder.WriteHeader(http.StatusAccepted)
	recorder.Flush()
	if recorder.status != http.StatusAccepted {
		t.Fatalf("expected status recorder to track status, got %d", recorder.status)
	}
	if !recorder.ResponseWriter.(*flushHijackRecorder).flushed {
		t.Fatal("expected status recorder to forward flush calls")
	}
	if _, _, err := recorder.Hijack(); err == nil || err.Error() != "boom" {
		t.Fatalf("expected status recorder to forward hijack error, got %v", err)
	}

	var stream bytes.Buffer
	if err := writeSSE(&stream, "result", map[string]string{"hello": "world"}); err != nil {
		t.Fatal(err)
	}
	if err := writeSSEComment(&stream, " keep-alive "); err != nil {
		t.Fatal(err)
	}
	streamText := stream.String()
	if !strings.Contains(streamText, "event: result") || !strings.Contains(streamText, `"hello":"world"`) {
		t.Fatalf("unexpected SSE payload: %q", streamText)
	}
	if !strings.Contains(streamText, ": keep-alive") {
		t.Fatalf("expected SSE keep-alive comment, got %q", streamText)
	}

	var typed struct {
		Value int `json:"value"`
	}
	typeErr := json.Unmarshal([]byte(`{"value":"nope"}`), &typed)
	if !isInvalidJSONError(typeErr) {
		t.Fatalf("expected unmarshal type error to be classified as invalid json, got %v", typeErr)
	}
	if got := normalizeErrorStatus(http.StatusInternalServerError, context.DeadlineExceeded); got != http.StatusRequestTimeout {
		t.Fatalf("expected timeout status, got %d", got)
	}
	if got := normalizeErrorStatus(http.StatusInternalServerError, errors.New("agent not found")); got != http.StatusNotFound {
		t.Fatalf("expected agent not found to normalize to 404, got %d", got)
	}
	if got := normalizeErrorStatus(http.StatusInternalServerError, errors.New("session is closed")); got != http.StatusBadRequest {
		t.Fatalf("expected closed session to normalize to 400, got %d", got)
	}
	meta := errorMetaForStatus(http.StatusInternalServerError, context.DeadlineExceeded)
	if meta.Code != "timeout" || !meta.Retryable {
		t.Fatalf("expected timeout metadata, got %+v", meta)
	}
	if got := *ptr("value"); got != "value" {
		t.Fatalf("unexpected ptr helper result: %q", got)
	}

	cfg := newTestConfig(t)
	cfg.DaemonListenAddr = "127.0.0.1:0"
	server := New(cfg, io.Discard, io.Discard, func(config.Config) engine.Provider {
		return &daemonTestProvider{}
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Run(ctx)
	}()

	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("expected Run to shut down cleanly, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for daemon Run shutdown")
	}
}

func newTestDaemon(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	cfg := newTestConfig(t)
	server := New(cfg, io.Discard, io.Discard, func(config.Config) engine.Provider {
		return &daemonTestProvider{}
	})
	return server, httptest.NewServer(server.Handler())
}

func newDaemonMCPHTTPServer(t *testing.T) *httptest.Server {
	t.Helper()

	handler := sdkmcp.NewStreamableHTTPHandler(func(*http.Request) *sdkmcp.Server {
		server := sdkmcp.NewServer(&sdkmcp.Implementation{
			Name:    "daemon-test-mcp",
			Version: "1.0.0",
		}, nil)
		server.AddResource(&sdkmcp.Resource{
			Name:        "alpha",
			Description: "Alpha resource",
			URI:         "file:///a",
		}, func(ctx context.Context, req *sdkmcp.ReadResourceRequest) (*sdkmcp.ReadResourceResult, error) {
			_ = ctx
			if req.Params.URI != "file:///a" {
				return nil, sdkmcp.ResourceNotFoundError(req.Params.URI)
			}
			return &sdkmcp.ReadResourceResult{
				Contents: []*sdkmcp.ResourceContents{{
					URI:  "file:///a",
					Text: "alpha",
				}},
			}, nil
		})
		server.AddResourceTemplate(&sdkmcp.ResourceTemplate{
			Name:        "dir",
			Description: "Directory template",
			URITemplate: "file:///dir/{f}",
		}, func(ctx context.Context, req *sdkmcp.ReadResourceRequest) (*sdkmcp.ReadResourceResult, error) {
			_ = ctx
			uri := req.Params.URI
			if !strings.HasPrefix(uri, "file:///dir/") {
				return nil, sdkmcp.ResourceNotFoundError(uri)
			}
			return &sdkmcp.ReadResourceResult{
				Contents: []*sdkmcp.ResourceContents{{
					URI:  uri,
					Text: strings.TrimPrefix(uri, "file:///dir/"),
				}},
			}, nil
		})
		server.AddPrompt(&sdkmcp.Prompt{
			Name:        "greet",
			Description: "Greeting prompt",
			Arguments: []*sdkmcp.PromptArgument{{
				Name:        "name",
				Description: "Name to greet",
				Required:    true,
			}},
		}, func(ctx context.Context, req *sdkmcp.GetPromptRequest) (*sdkmcp.GetPromptResult, error) {
			_ = ctx
			name := req.Params.Arguments["name"]
			return &sdkmcp.GetPromptResult{
				Description: "Greeting prompt",
				Messages: []*sdkmcp.PromptMessage{{
					Role:    "user",
					Content: &sdkmcp.TextContent{Text: "Say hi to " + name},
				}},
			}, nil
		})
		sdkmcp.AddTool(server, &sdkmcp.Tool{
			Name:        "echo_text",
			Description: "Echo text back to the caller",
		}, func(ctx context.Context, req *sdkmcp.CallToolRequest, input struct {
			Value string `json:"value" jsonschema:"value to echo back"`
		}) (*sdkmcp.CallToolResult, map[string]string, error) {
			_ = ctx
			_ = req
			return nil, map[string]string{"echo": input.Value}, nil
		})
		return server
	}, nil)

	return httptest.NewServer(handler)
}

func newTestConfig(t *testing.T) config.Config {
	t.Helper()
	dir := t.TempDir()
	return config.Config{
		Model:             "test-model",
		SystemPrompt:      "test",
		MaxTurns:          4,
		MaxTokens:         4096,
		MaxParallelAgents: 2,
		ContextBudget:     4000,
		CompactKeep:       6,
		WorkingDir:        dir,
		DaemonDir:         filepath.Join(dir, ".xxx-code", "daemon"),
		ToolTimeout:       2 * time.Second,
		HookTimeout:       time.Second,
		ReadRoots:         []string{dir},
		WriteRoots:        []string{dir},
		BashEnabled:       true,
	}
}

func postJSON(t *testing.T, url string, body any, wantStatus int) map[string]any {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	return doJSON(t, req, wantStatus)
}

func getJSON(t *testing.T, url string, wantStatus int) map[string]any {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	return doJSON(t, req, wantStatus)
}

func doJSON(t *testing.T, req *http.Request, wantStatus int) map[string]any {
	t.Helper()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != wantStatus {
		t.Fatalf("unexpected status %d: %s", resp.StatusCode, string(body))
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	return payload
}

func doAuthorizedJSON(t *testing.T, token string, req *http.Request, wantStatus int) map[string]any {
	t.Helper()
	req.Header.Set("Authorization", "Bearer "+token)
	return doJSON(t, req, wantStatus)
}

func doRawRequest(t *testing.T, req *http.Request, wantStatus int) (*http.Response, []byte) {
	t.Helper()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != wantStatus {
		t.Fatalf("unexpected status %d: %s", resp.StatusCode, string(body))
	}
	return resp, body
}

func mustRequest(t *testing.T, method, url string, body any) *http.Request {
	t.Helper()
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req
}

func hasAuditEvent(events []any, action, code string) bool {
	for _, raw := range events {
		event, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		gotAction, _ := event["action"].(string)
		if strings.TrimSpace(gotAction) != action {
			continue
		}
		if code == "" {
			return true
		}
		if got, _ := event["code"].(string); got == code {
			return true
		}
	}
	return false
}

func latestDaemonUserText(messages []engine.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == engine.RoleUser {
			return messages[i].Text()
		}
	}
	return ""
}

func latestDaemonToolResult(messages []engine.Message) (string, bool) {
	for i := len(messages) - 1; i >= 0; i-- {
		for _, block := range messages[i].Content {
			if block.Type == engine.BlockToolResult {
				return strings.TrimSpace(block.Result), true
			}
		}
	}
	return "", false
}

func writeDaemonPluginSource(t *testing.T, rootDir, pluginName, script string) string {
	t.Helper()
	pluginDir := filepath.Join(rootDir, pluginName)
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "tool.sh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{
  "name": "` + pluginName + `",
  "tools": [{
    "name": "echo",
    "description": "Echo plugin",
    "input_schema": {"type": "object"},
    "command": "./tool.sh"
  }]
	}`
	if err := os.WriteFile(filepath.Join(pluginDir, "plugin.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	return pluginDir
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

type flushHijackRecorder struct {
	*httptest.ResponseRecorder
	flushed bool
}

func (r *flushHijackRecorder) Flush() {
	r.flushed = true
}

func (r *flushHijackRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return nil, nil, errors.New("boom")
}
