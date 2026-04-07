package engine

import (
	"context"
	"encoding/json"
)

type ToolDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

type ToolResult struct {
	Content string
	IsError bool
}

type Tool interface {
	Definition() ToolDefinition
	Call(ctx context.Context, exec *ExecutionContext, input json.RawMessage) (ToolResult, error)
}

type ExecutionContext struct {
	Runner     *Runner
	Session    *Session
	WorkingDir string
	AgentID    string
	AgentName  string
	Depth      int
}
