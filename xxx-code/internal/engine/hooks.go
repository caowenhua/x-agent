package engine

import (
	"context"
	"time"
)

type HookKind string

const (
	HookBeforeTool HookKind = "before_tool"
	HookAfterTool  HookKind = "after_tool"
	HookAfterTurn  HookKind = "after_turn"
	HookAgentEvent HookKind = "agent_event"
)

type HookEvent struct {
	Kind            HookKind  `json:"kind"`
	Timestamp       time.Time `json:"timestamp"`
	WorkingDir      string    `json:"working_dir,omitempty"`
	AgentID         string    `json:"agent_id,omitempty"`
	AgentName       string    `json:"agent_name,omitempty"`
	ToolName        string    `json:"tool_name,omitempty"`
	ToolInput       string    `json:"tool_input,omitempty"`
	ToolResult      string    `json:"tool_result,omitempty"`
	ToolError       bool      `json:"tool_error,omitempty"`
	Prompt          string    `json:"prompt,omitempty"`
	FinalText       string    `json:"final_text,omitempty"`
	Status          string    `json:"status,omitempty"`
	Error           string    `json:"error,omitempty"`
	SessionMessages int       `json:"session_messages,omitempty"`
	InputTokens     int       `json:"input_tokens,omitempty"`
	OutputTokens    int       `json:"output_tokens,omitempty"`
}

type HookHandler interface {
	HandleHook(ctx context.Context, event HookEvent) error
}
