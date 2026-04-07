package tools

import (
	"context"
	"encoding/json"
	"os"

	"github.com/caowenhua/x-agent/xxx-code/internal/engine"
)

type WriteFileTool struct{}

type writeFileInput struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

func (t *WriteFileTool) Definition() engine.ToolDefinition {
	return engine.ToolDefinition{
		Name:        "write_file",
		Description: "Write content to a file, creating parent directories if needed.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "File path to write.",
				},
				"content": map[string]any{
					"type":        "string",
					"description": "Full file content.",
				},
			},
			"required": []string{"path", "content"},
		},
	}
}

func (t *WriteFileTool) Call(ctx context.Context, execCtx *engine.ExecutionContext, input json.RawMessage) (engine.ToolResult, error) {
	_ = ctx
	var args writeFileInput
	if err := json.Unmarshal(input, &args); err != nil {
		return engine.ToolResult{}, err
	}
	path, err := resolvePath(execCtx.WorkingDir, args.Path)
	if err != nil {
		return engine.ToolResult{}, err
	}
	if err := ensureWriteAllowed(execCtx, path); err != nil {
		return engine.ToolResult{}, err
	}
	if err := ensureParentDir(path); err != nil {
		return engine.ToolResult{}, err
	}
	if err := os.WriteFile(path, []byte(args.Content), 0o644); err != nil {
		return engine.ToolResult{}, err
	}
	return engine.ToolResult{
		Content: mustJSON(map[string]any{
			"path":    path,
			"written": true,
			"bytes":   len(args.Content),
		}),
	}, nil
}
