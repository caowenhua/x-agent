package tools

import (
	"context"
	"encoding/json"
	"os"

	"github.com/caowenhua/x-agent/xxx-code/internal/engine"
)

type ReadFileTool struct{}

type readFileInput struct {
	Path     string `json:"path"`
	MaxBytes int    `json:"max_bytes,omitempty"`
}

func (t *ReadFileTool) Definition() engine.ToolDefinition {
	return engine.ToolDefinition{
		Name:        "read_file",
		Description: "Read a file from disk.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "Path to the file.",
				},
				"max_bytes": map[string]any{
					"type":        "integer",
					"description": "Optional byte limit.",
				},
			},
			"required": []string{"path"},
		},
	}
}

func (t *ReadFileTool) Call(ctx context.Context, execCtx *engine.ExecutionContext, input json.RawMessage) (engine.ToolResult, error) {
	_ = ctx
	var args readFileInput
	if err := json.Unmarshal(input, &args); err != nil {
		return engine.ToolResult{}, err
	}
	path, err := resolvePath(execCtx.WorkingDir, args.Path)
	if err != nil {
		return engine.ToolResult{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return engine.ToolResult{}, err
	}
	if args.MaxBytes > 0 && len(data) > args.MaxBytes {
		data = data[:args.MaxBytes]
	}
	return engine.ToolResult{
		Content: mustJSON(map[string]any{
			"path":    path,
			"content": string(data),
		}),
	}, nil
}
