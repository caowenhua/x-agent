package hooks

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/caowenhua/x-agent/xxx-code/internal/engine"
)

func TestScriptManagerWritesHookPayload(t *testing.T) {
	dir := t.TempDir()
	output := filepath.Join(dir, "hook.json")

	manager := NewScriptManager(Config{
		AfterTurn: "cat > " + output,
	})

	err := manager.HandleHook(context.Background(), engine.HookEvent{
		Kind:       engine.HookAfterTurn,
		WorkingDir: dir,
		AgentID:    "agent_1",
		Status:     "completed",
		FinalText:  "done",
	})
	if err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, `"kind":"after_turn"`) {
		t.Fatalf("expected hook payload, got %s", text)
	}
	if !strings.Contains(text, `"agent_id":"agent_1"`) {
		t.Fatalf("expected agent id in payload, got %s", text)
	}
}

func TestScriptManagerReturnsHookCommandFailure(t *testing.T) {
	manager := NewScriptManager(Config{
		BeforeTool: "echo blocked >&2; exit 7",
	})

	err := manager.HandleHook(context.Background(), engine.HookEvent{
		Kind: engine.HookBeforeTool,
	})
	if err == nil || !strings.Contains(err.Error(), "blocked") {
		t.Fatalf("expected hook failure, got %v", err)
	}
}
