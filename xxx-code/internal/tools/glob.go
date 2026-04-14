package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/caowenhua/x-agent/xxx-code/internal/engine"
)

type GlobTool struct{}

type globInput struct {
	Pattern string `json:"pattern"`
	Limit   int    `json:"limit,omitempty"`
}

func (t *GlobTool) Definition() engine.ToolDefinition {
	return engine.ToolDefinition{
		Name:        "glob",
		Description: "Find files matching a glob pattern. Supports ** for recursive matches.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern": map[string]any{
					"type":        "string",
					"description": "Glob pattern relative to the working directory.",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Maximum number of results.",
				},
			},
			"required": []string{"pattern"},
		},
	}
}

func (t *GlobTool) Call(ctx context.Context, execCtx *engine.ExecutionContext, input json.RawMessage) (engine.ToolResult, error) {
	if err := ensureToolAllowed(execCtx, t.Definition().Name); err != nil {
		return engine.ToolResult{}, err
	}
	var args globInput
	if err := json.Unmarshal(input, &args); err != nil {
		return engine.ToolResult{}, err
	}
	limit := args.Limit
	if limit <= 0 {
		limit = 200
	}

	regex, err := globToRegexp(args.Pattern)
	if err != nil {
		return engine.ToolResult{}, err
	}
	if err := ensureReadAllowed(execCtx, execCtx.WorkingDir); err != nil {
		return engine.ToolResult{}, err
	}

	var matches []string
	err = filepath.WalkDir(execCtx.WorkingDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		rel, err := filepath.Rel(execCtx.WorkingDir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}
		if regex.MatchString(rel) {
			matches = append(matches, rel)
			if len(matches) >= limit {
				return filepath.SkipAll
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

func globToRegexp(pattern string) (*regexp.Regexp, error) {
	var b strings.Builder
	b.WriteString("^")
	runes := []rune(filepath.ToSlash(pattern))
	for i := 0; i < len(runes); i++ {
		switch runes[i] {
		case '*':
			if i+1 < len(runes) && runes[i+1] == '*' {
				if i+2 < len(runes) && runes[i+2] == '/' {
					// Treat **/ as zero or more path segments so patterns like
					// pricing/**/*.go also match pricing/main.go.
					b.WriteString(`(?:[^/]+/)*`)
					i += 2
				} else {
					b.WriteString(".*")
					i++
				}
			} else {
				b.WriteString(`[^/]*`)
			}
		case '?':
			b.WriteString(`[^/]`)
		case '.', '+', '(', ')', '[', ']', '{', '}', '|', '^', '$', '\\':
			b.WriteString(`\`)
			b.WriteRune(runes[i])
		default:
			b.WriteRune(runes[i])
		}
	}
	b.WriteString("$")
	return regexp.Compile(b.String())
}
