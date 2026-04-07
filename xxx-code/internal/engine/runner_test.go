package engine

import (
	"context"
	"encoding/json"
	"testing"
)

type stubProvider struct {
	calls int
}

func (p *stubProvider) CreateMessage(ctx context.Context, request CompletionRequest) (CompletionResponse, error) {
	_ = ctx
	p.calls++
	if p.calls == 1 {
		input, _ := json.Marshal(map[string]any{"value": "done"})
		return CompletionResponse{
			Message: Message{
				Role: RoleAssistant,
				Content: []Block{
					{Type: BlockText, Text: "calling tool"},
					{Type: BlockToolUse, ID: "tool-1", Name: "echo_tool", Input: input},
				},
			},
		}, nil
	}
	return CompletionResponse{
		Message: NewTextMessage(RoleAssistant, "final answer"),
	}, nil
}

type echoTool struct{}

func (t *echoTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "echo_tool",
		Description: "Echo input",
		InputSchema: map[string]any{"type": "object"},
	}
}

func (t *echoTool) Call(ctx context.Context, exec *ExecutionContext, input json.RawMessage) (ToolResult, error) {
	_ = ctx
	_ = exec
	return ToolResult{Content: string(input)}, nil
}

func TestRunnerExecutesToolLoop(t *testing.T) {
	provider := &stubProvider{}
	runner := NewRunner(provider, NewRegistry(&echoTool{}), RunnerConfig{
		Model:        "test-model",
		SystemPrompt: "test",
		MaxTurns:     4,
	})
	session := NewSession()

	result, err := runner.RunTurn(context.Background(), session, "hello")
	if err != nil {
		t.Fatal(err)
	}
	if result.FinalText != "final answer" {
		t.Fatalf("unexpected final text: %q", result.FinalText)
	}
	if provider.calls != 2 {
		t.Fatalf("expected 2 provider calls, got %d", provider.calls)
	}
}
