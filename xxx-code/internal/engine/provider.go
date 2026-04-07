package engine

import "context"

type CompletionRequest struct {
	Model       string
	System      string
	MaxTokens   int
	Messages    []Message
	Tools       []ToolDefinition
	Temperature float64
}

type CompletionResponse struct {
	ID         string
	StopReason string
	Message    Message
	Usage      Usage
}

type Provider interface {
	CreateMessage(ctx context.Context, request CompletionRequest) (CompletionResponse, error)
}
