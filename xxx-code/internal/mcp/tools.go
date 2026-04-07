package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/caowenhua/x-agent/xxx-code/internal/engine"
)

func (m *Manager) registerSupportTools(registry *engine.Registry) {
	if registry == nil {
		return
	}
	_ = registry.AddTool(&listResourcesTool{manager: m})
	_ = registry.AddTool(&listResourceTemplatesTool{manager: m})
	_ = registry.AddTool(&readResourceTool{manager: m})
	_ = registry.AddTool(&listPromptsTool{manager: m})
	_ = registry.AddTool(&getPromptTool{manager: m})
}

type listResourcesTool struct {
	manager *Manager
}

func (t *listResourcesTool) Definition() engine.ToolDefinition {
	return engine.ToolDefinition{
		Name:        "list_mcp_resources",
		Description: "List available MCP resources. Optionally filter by a specific server name.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"server": map[string]any{
					"type":        "string",
					"description": "Optional MCP server name to filter by.",
				},
			},
		},
	}
}

func (t *listResourcesTool) Call(ctx context.Context, exec *engine.ExecutionContext, input json.RawMessage) (engine.ToolResult, error) {
	_ = exec
	var args struct {
		Server string `json:"server,omitempty"`
	}
	if err := json.Unmarshal(orEmptyObject(input), &args); err != nil {
		return engine.ToolResult{}, err
	}

	resources, err := t.manager.ListResources(ctx, args.Server)
	if err != nil {
		return engine.ToolResult{}, err
	}
	return engine.ToolResult{Content: mustJSON(map[string]any{"resources": resources})}, nil
}

type listResourceTemplatesTool struct {
	manager *Manager
}

func (t *listResourceTemplatesTool) Definition() engine.ToolDefinition {
	return engine.ToolDefinition{
		Name:        "list_mcp_resource_templates",
		Description: "List available MCP resource templates. Optionally filter by a specific server name.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"server": map[string]any{
					"type":        "string",
					"description": "Optional MCP server name to filter by.",
				},
			},
		},
	}
}

func (t *listResourceTemplatesTool) Call(ctx context.Context, exec *engine.ExecutionContext, input json.RawMessage) (engine.ToolResult, error) {
	_ = exec
	var args struct {
		Server string `json:"server,omitempty"`
	}
	if err := json.Unmarshal(orEmptyObject(input), &args); err != nil {
		return engine.ToolResult{}, err
	}

	templates, err := t.manager.ListResourceTemplates(ctx, args.Server)
	if err != nil {
		return engine.ToolResult{}, err
	}
	return engine.ToolResult{Content: mustJSON(map[string]any{"resource_templates": templates})}, nil
}

type readResourceTool struct {
	manager *Manager
}

func (t *readResourceTool) Definition() engine.ToolDefinition {
	return engine.ToolDefinition{
		Name:        "read_mcp_resource",
		Description: "Read a resource from a specific MCP server by URI.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"server": map[string]any{
					"type":        "string",
					"description": "MCP server name.",
				},
				"uri": map[string]any{
					"type":        "string",
					"description": "Resource URI to read.",
				},
			},
			"required": []string{"server", "uri"},
		},
	}
}

func (t *readResourceTool) Call(ctx context.Context, exec *engine.ExecutionContext, input json.RawMessage) (engine.ToolResult, error) {
	_ = exec
	var args struct {
		Server string `json:"server"`
		URI    string `json:"uri"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return engine.ToolResult{}, err
	}

	resource, err := t.manager.ReadResource(ctx, args.Server, args.URI)
	if err != nil {
		return engine.ToolResult{}, err
	}
	return engine.ToolResult{Content: mustJSON(resource)}, nil
}

type listPromptsTool struct {
	manager *Manager
}

func (t *listPromptsTool) Definition() engine.ToolDefinition {
	return engine.ToolDefinition{
		Name:        "list_mcp_prompts",
		Description: "List available MCP prompts. Optionally filter by a specific server name.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"server": map[string]any{
					"type":        "string",
					"description": "Optional MCP server name to filter by.",
				},
			},
		},
	}
}

func (t *listPromptsTool) Call(ctx context.Context, exec *engine.ExecutionContext, input json.RawMessage) (engine.ToolResult, error) {
	_ = exec
	var args struct {
		Server string `json:"server,omitempty"`
	}
	if err := json.Unmarshal(orEmptyObject(input), &args); err != nil {
		return engine.ToolResult{}, err
	}

	prompts, err := t.manager.ListPrompts(ctx, args.Server)
	if err != nil {
		return engine.ToolResult{}, err
	}
	return engine.ToolResult{Content: mustJSON(map[string]any{"prompts": prompts})}, nil
}

type getPromptTool struct {
	manager *Manager
}

func (t *getPromptTool) Definition() engine.ToolDefinition {
	return engine.ToolDefinition{
		Name:        "get_mcp_prompt",
		Description: "Resolve a prompt from a specific MCP server with optional string arguments.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"server": map[string]any{
					"type":        "string",
					"description": "MCP server name.",
				},
				"name": map[string]any{
					"type":        "string",
					"description": "Prompt name.",
				},
				"arguments": map[string]any{
					"type":                 "object",
					"description":          "Optional prompt arguments; all values should be strings.",
					"additionalProperties": map[string]any{"type": "string"},
				},
			},
			"required": []string{"server", "name"},
		},
	}
}

func (t *getPromptTool) Call(ctx context.Context, exec *engine.ExecutionContext, input json.RawMessage) (engine.ToolResult, error) {
	_ = exec
	var args struct {
		Server    string            `json:"server"`
		Name      string            `json:"name"`
		Arguments map[string]string `json:"arguments,omitempty"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return engine.ToolResult{}, err
	}

	prompt, err := t.manager.GetPrompt(ctx, args.Server, args.Name, args.Arguments)
	if err != nil {
		return engine.ToolResult{}, err
	}
	return engine.ToolResult{Content: mustJSON(prompt)}, nil
}

func mustJSON(value any) string {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Sprintf(`{"error": %q}`, err.Error())
	}
	return string(data)
}

func orEmptyObject(raw json.RawMessage) []byte {
	if len(raw) == 0 {
		return []byte("{}")
	}
	return raw
}
