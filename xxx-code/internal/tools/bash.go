package tools

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"

	"github.com/caowenhua/x-agent/xxx-code/internal/engine"
)

type BashTool struct{}

type bashInput struct {
	Command string `json:"command"`
}

func (t *BashTool) Definition() engine.ToolDefinition {
	return engine.ToolDefinition{
		Name:        "bash",
		Description: "Run a shell command in the working directory and return stdout, stderr, and exit code.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]any{
					"type":        "string",
					"description": "Shell command to execute.",
				},
			},
			"required": []string{"command"},
		},
	}
}

func (t *BashTool) Call(ctx context.Context, execCtx *engine.ExecutionContext, input json.RawMessage) (engine.ToolResult, error) {
	var args bashInput
	if err := json.Unmarshal(input, &args); err != nil {
		return engine.ToolResult{}, err
	}

	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}

	cmd := exec.CommandContext(ctx, shell, "-lc", args.Command)
	cmd.Dir = execCtx.WorkingDir
	output, err := cmd.CombinedOutput()

	result := map[string]any{
		"command":   args.Command,
		"cwd":       execCtx.WorkingDir,
		"exit_code": 0,
		"output":    strings.TrimSpace(string(output)),
	}
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			result["exit_code"] = exitErr.ExitCode()
		} else {
			result["exit_code"] = -1
			result["error"] = err.Error()
		}
		return engine.ToolResult{
			Content: mustJSON(result),
			IsError: true,
		}, nil
	}

	return engine.ToolResult{
		Content: mustJSON(result),
	}, nil
}
