package daemon

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/caowenhua/x-agent/xxx-code/internal/config"
	"github.com/caowenhua/x-agent/xxx-code/internal/engine"
)

func BenchmarkDaemonTurnHTTP(b *testing.B) {
	dir := b.TempDir()
	cfg := config.Config{
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

	server := New(cfg, io.Discard, io.Discard, func(config.Config) engine.Provider {
		return &daemonTestProvider{}
	})
	testServer := httptest.NewServer(server.Handler())
	defer func() {
		_ = server.Close()
		testServer.Close()
	}()

	sessionID := createBenchSession(b, testServer.URL)
	session := lookupBenchSession(b, server, sessionID)
	client := &http.Client{}
	body := []byte(`{"prompt":"benchmark turn"}`)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		session.session.Replace(nil)

		req, err := http.NewRequest(http.MethodPost, testServer.URL+"/v1/sessions/"+sessionID+"/turns", bytes.NewReader(body))
		if err != nil {
			b.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(req)
		if err != nil {
			b.Fatal(err)
		}
		if resp.StatusCode != http.StatusOK {
			payload, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			b.Fatalf("unexpected status %d: %s", resp.StatusCode, string(payload))
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
}

func createBenchSession(b *testing.B, baseURL string) string {
	b.Helper()
	req, err := http.NewRequest(http.MethodPost, baseURL+"/v1/sessions", bytes.NewReader([]byte(`{}`)))
	if err != nil {
		b.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		b.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		payload, _ := io.ReadAll(resp.Body)
		b.Fatalf("unexpected create session status %d: %s", resp.StatusCode, string(payload))
	}

	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		b.Fatal(err)
	}
	return payload["session"].(map[string]any)["id"].(string)
}

func lookupBenchSession(b *testing.B, server *Server, sessionID string) *managedSession {
	b.Helper()
	server.mu.Lock()
	defer server.mu.Unlock()
	session := server.sessions[sessionID]
	if session == nil {
		b.Fatalf("expected managed session %q to exist", sessionID)
	}
	return session
}
