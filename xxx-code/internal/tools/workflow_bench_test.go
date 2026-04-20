package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/caowenhua/x-agent/xxx-code/internal/engine"
)

func BenchmarkAgentFanoutTool(b *testing.B) {
	input, err := json.Marshal(map[string]any{
		"wait":         true,
		"max_parallel": 2,
		"tasks": []map[string]any{
			{"name": "one", "prompt": "task one"},
			{"name": "two", "prompt": "task two"},
			{"name": "three", "prompt": "task three"},
			{"name": "four", "prompt": "task four"},
		},
	})
	if err != nil {
		b.Fatal(err)
	}

	dir := b.TempDir()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		manager := NewWorkflowManager()
		runner := engine.NewRunner(&toolPromptProvider{}, engine.NewRegistry(), engine.RunnerConfig{
			Model:             "test-model",
			SystemPrompt:      "test",
			MaxTurns:          4,
			WorkingDir:        dir,
			MaxParallelAgents: 2,
		})
		execCtx := &engine.ExecutionContext{
			Runner:     runner,
			Session:    engine.NewSession(),
			WorkingDir: dir,
		}
		if _, err := (&AgentFanoutTool{Manager: manager}).Call(context.Background(), execCtx, input); err != nil {
			b.Fatal(err)
		}
	}
}
