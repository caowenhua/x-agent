package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/caowenhua/x-agent/xxx-code/internal/engine"
)

func TestEditFileTool(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "demo.txt")
	if err := os.WriteFile(path, []byte("hello world"), 0o644); err != nil {
		t.Fatal(err)
	}

	input, _ := json.Marshal(map[string]any{
		"path":       "demo.txt",
		"old_string": "world",
		"new_string": "gopher",
	})

	result, err := (&EditFileTool{}).Call(context.Background(), &engine.ExecutionContext{
		WorkingDir: dir,
	}, input)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error result: %s", result.Content)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); got != "hello gopher" {
		t.Fatalf("unexpected content: %q", got)
	}
}

func TestGlobTool(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "a", "b"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a", "b", "main.go"), []byte("package main"), 0o644); err != nil {
		t.Fatal(err)
	}

	input, _ := json.Marshal(map[string]any{
		"pattern": "**/*.go",
	})

	result, err := (&GlobTool{}).Call(context.Background(), &engine.ExecutionContext{
		WorkingDir: dir,
	}, input)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Content, "main.go") {
		t.Fatalf("expected match to include main.go, got %s", result.Content)
	}
}

func TestWriteFileToolRespectsReadOnlyPolicy(t *testing.T) {
	dir := t.TempDir()
	runner := engine.NewRunner(nil, engine.NewRegistry(), engine.RunnerConfig{
		WorkingDir: dir,
		PermissionPolicy: engine.PermissionPolicy{
			ReadRoots:   []string{dir},
			WriteRoots:  []string{dir},
			ReadOnly:    true,
			BashEnabled: true,
		},
	})

	input, _ := json.Marshal(map[string]any{
		"path":    "demo.txt",
		"content": "hello",
	})

	_, err := (&WriteFileTool{}).Call(context.Background(), &engine.ExecutionContext{
		Runner:     runner,
		WorkingDir: dir,
	}, input)
	if err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("expected read-only policy error, got %v", err)
	}
}

func TestReadFileToolRejectsOutsideReadRoots(t *testing.T) {
	dir := t.TempDir()
	otherDir := t.TempDir()
	path := filepath.Join(otherDir, "secret.txt")
	if err := os.WriteFile(path, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}

	runner := engine.NewRunner(nil, engine.NewRegistry(), engine.RunnerConfig{
		WorkingDir: dir,
		PermissionPolicy: engine.PermissionPolicy{
			ReadRoots:   []string{dir},
			WriteRoots:  []string{dir},
			BashEnabled: true,
		},
	})

	input, _ := json.Marshal(map[string]any{
		"path": path,
	})

	_, err := (&ReadFileTool{}).Call(context.Background(), &engine.ExecutionContext{
		Runner:     runner,
		WorkingDir: dir,
	}, input)
	if err == nil || !strings.Contains(err.Error(), "outside allowed read roots") {
		t.Fatalf("expected read root policy error, got %v", err)
	}
}

func TestWriteFileToolRejectsBlockedTool(t *testing.T) {
	dir := t.TempDir()
	runner := engine.NewRunner(nil, engine.NewRegistry(), engine.RunnerConfig{
		WorkingDir: dir,
		PermissionPolicy: engine.PermissionPolicy{
			ReadRoots:    []string{dir},
			WriteRoots:   []string{dir},
			BlockedTools: []string{"write_file"},
			BashEnabled:  true,
		},
	})

	input, _ := json.Marshal(map[string]any{
		"path":    "demo.txt",
		"content": "hello",
	})

	_, err := (&WriteFileTool{}).Call(context.Background(), &engine.ExecutionContext{
		Runner:     runner,
		WorkingDir: dir,
	}, input)
	if err == nil || !strings.Contains(err.Error(), "blocked by policy") {
		t.Fatalf("expected blocked tool policy error, got %v", err)
	}
}

func TestBashToolRejectsDisallowedCommandPrefix(t *testing.T) {
	dir := t.TempDir()
	runner := engine.NewRunner(nil, engine.NewRegistry(), engine.RunnerConfig{
		WorkingDir: dir,
		PermissionPolicy: engine.PermissionPolicy{
			ReadRoots:           []string{dir},
			WriteRoots:          []string{dir},
			BashEnabled:         true,
			BashAllowedPrefixes: []string{"go env", "go test"},
			BashBlockedPrefixes: []string{"rm ", "sudo "},
		},
	})

	input, _ := json.Marshal(map[string]any{
		"command": "rm -rf /tmp/demo",
	})
	_, err := (&BashTool{}).Call(context.Background(), &engine.ExecutionContext{
		Runner:     runner,
		WorkingDir: dir,
	}, input)
	if err == nil || !strings.Contains(err.Error(), "blocked by policy") {
		t.Fatalf("expected blocked bash prefix error, got %v", err)
	}

	input, _ = json.Marshal(map[string]any{
		"command": "go env GOROOT",
	})
	result, err := (&BashTool{}).Call(context.Background(), &engine.ExecutionContext{
		Runner:     runner,
		WorkingDir: dir,
	}, input)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("expected allowed bash command to run, got %s", result.Content)
	}
}
