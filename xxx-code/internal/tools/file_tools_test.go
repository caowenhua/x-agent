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
