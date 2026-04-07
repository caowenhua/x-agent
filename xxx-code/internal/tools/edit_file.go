package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/caowenhua/x-agent/xxx-code/internal/engine"
)

type EditFileTool struct{}

type editFileInput struct {
	Path       string `json:"path"`
	OldString  string `json:"old_string"`
	NewString  string `json:"new_string"`
	ReplaceAll bool   `json:"replace_all,omitempty"`
}

func (t *EditFileTool) Definition() engine.ToolDefinition {
	return engine.ToolDefinition{
		Name:        "edit_file",
		Description: "Edit a file by replacing an existing string with a new string.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "File path to edit.",
				},
				"old_string": map[string]any{
					"type":        "string",
					"description": "Existing text to replace.",
				},
				"new_string": map[string]any{
					"type":        "string",
					"description": "Replacement text.",
				},
				"replace_all": map[string]any{
					"type":        "boolean",
					"description": "Replace every occurrence instead of exactly one.",
				},
			},
			"required": []string{"path", "old_string", "new_string"},
		},
	}
}

func (t *EditFileTool) Call(ctx context.Context, execCtx *engine.ExecutionContext, input json.RawMessage) (engine.ToolResult, error) {
	_ = ctx
	var args editFileInput
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
	content := string(data)
	count := strings.Count(content, args.OldString)
	if count == 0 {
		return engine.ToolResult{}, fmt.Errorf("old_string not found in file")
	}
	if count > 1 && !args.ReplaceAll {
		return engine.ToolResult{}, fmt.Errorf("old_string matches %d times; set replace_all=true to replace all", count)
	}

	var updated string
	if args.ReplaceAll {
		updated = strings.ReplaceAll(content, args.OldString, args.NewString)
	} else {
		updated = strings.Replace(content, args.OldString, args.NewString, 1)
	}
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		return engine.ToolResult{}, err
	}
	return engine.ToolResult{
		Content: mustJSON(map[string]any{
			"path":        path,
			"replaced":    true,
			"occurrences": count,
		}),
	}, nil
}
