package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"

	"github.com/caowenhua/x-agent/xxx-code/internal/engine"
)

type GrepTool struct{}

type grepInput struct {
	Pattern string `json:"pattern"`
	Path    string `json:"path,omitempty"`
	Literal bool   `json:"literal,omitempty"`
	Limit   int    `json:"limit,omitempty"`
}

func (t *GrepTool) Definition() engine.ToolDefinition {
	return engine.ToolDefinition{
		Name:        "grep",
		Description: "Search file contents recursively using a regular expression or literal string.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern": map[string]any{
					"type":        "string",
					"description": "Pattern to search for.",
				},
				"path": map[string]any{
					"type":        "string",
					"description": "Optional root path relative to the working directory.",
				},
				"literal": map[string]any{
					"type":        "boolean",
					"description": "Treat pattern as a literal string.",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Maximum number of matches.",
				},
			},
			"required": []string{"pattern"},
		},
	}
}

func (t *GrepTool) Call(ctx context.Context, execCtx *engine.ExecutionContext, input json.RawMessage) (engine.ToolResult, error) {
	var args grepInput
	if err := json.Unmarshal(input, &args); err != nil {
		return engine.ToolResult{}, err
	}

	root := execCtx.WorkingDir
	if args.Path != "" {
		path, err := resolvePath(execCtx.WorkingDir, args.Path)
		if err != nil {
			return engine.ToolResult{}, err
		}
		root = path
	}

	pattern := args.Pattern
	if args.Literal {
		pattern = regexp.QuoteMeta(pattern)
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return engine.ToolResult{}, err
	}

	limit := args.Limit
	if limit <= 0 {
		limit = 200
	}

	type match struct {
		Path string `json:"path"`
		Line int    `json:"line"`
		Text string `json:"text"`
	}

	var matches []match
	err = filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if info.IsDir() {
			return nil
		}

		file, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer file.Close()

		scanner := bufio.NewScanner(file)
		lineNumber := 0
		for scanner.Scan() {
			lineNumber++
			line := scanner.Text()
			if re.MatchString(line) {
				rel, _ := filepath.Rel(execCtx.WorkingDir, path)
				matches = append(matches, match{
					Path: rel,
					Line: lineNumber,
					Text: line,
				})
				if len(matches) >= limit {
					return filepath.SkipAll
				}
			}
		}
		return nil
	})
	if err != nil && err != filepath.SkipAll {
		return engine.ToolResult{}, err
	}

	return engine.ToolResult{
		Content: mustJSON(map[string]any{
			"pattern": args.Pattern,
			"matches": matches,
		}),
	}, nil
}
