package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

type EventKind string

const (
	EventAssistantText      EventKind = "assistant_text"
	EventAssistantTextDelta EventKind = "assistant_text_delta"
	EventAssistantTextDone  EventKind = "assistant_text_done"
	EventToolCall           EventKind = "tool_call"
	EventToolResult         EventKind = "tool_result"
	EventAgentSpawned       EventKind = "agent_spawned"
	EventAgentCompleted     EventKind = "agent_completed"
	EventAgentCancelled     EventKind = "agent_cancelled"
	EventSessionCompacted   EventKind = "session_compacted"
	EventHookError          EventKind = "hook_error"
)

type Event struct {
	Kind      EventKind
	AgentID   string
	AgentName string
	ToolName  string
	Text      string
}

type RunnerConfig struct {
	Model               string
	SystemPrompt        string
	MaxTokens           int
	MaxTurns            int
	Temperature         float64
	StreamResponses     bool
	ContextBudget       int
	CompactKeepMessages int
	WorkingDir          string
	ToolTimeout         time.Duration
	HookTimeout         time.Duration
	MaxAgentDepth       int
	MaxParallelAgents   int
	PermissionPolicy    PermissionPolicy
	Hooks               HookHandler
	EventHandler        func(Event)
}

type Runner struct {
	provider Provider
	registry *Registry
	config   RunnerConfig

	agentState *agentState
}

type RunResult struct {
	FinalText string
	Usage     Usage
	Messages  []Message
}

type toolFailure struct {
	ToolName string
	Message  string
	Keys     []string
}

func NewRunner(provider Provider, registry *Registry, config RunnerConfig) *Runner {
	if config.MaxTurns <= 0 {
		config.MaxTurns = 12
	}
	if config.MaxTokens <= 0 {
		config.MaxTokens = 16_384
	}
	if config.CompactKeepMessages <= 0 {
		config.CompactKeepMessages = 12
	}
	if config.ToolTimeout <= 0 {
		config.ToolTimeout = 2 * time.Minute
	}
	if config.HookTimeout <= 0 {
		config.HookTimeout = 30 * time.Second
	}
	if config.MaxAgentDepth <= 0 {
		config.MaxAgentDepth = 3
	}
	if config.MaxParallelAgents <= 0 {
		config.MaxParallelAgents = 4
	}
	if !config.PermissionPolicy.ReadOnly &&
		!config.PermissionPolicy.BashEnabled &&
		len(config.PermissionPolicy.ReadRoots) == 0 &&
		len(config.PermissionPolicy.WriteRoots) == 0 {
		config.PermissionPolicy.BashEnabled = true
	}
	if len(config.PermissionPolicy.ReadRoots) == 0 && config.WorkingDir != "" {
		config.PermissionPolicy.ReadRoots = []string{config.WorkingDir}
	}
	if len(config.PermissionPolicy.WriteRoots) == 0 && config.WorkingDir != "" {
		config.PermissionPolicy.WriteRoots = []string{config.WorkingDir}
	}
	return &Runner{
		provider: provider,
		registry: registry,
		config:   config,
		agentState: &agentState{
			agents:      make(map[string]*managedAgent),
			maxParallel: config.MaxParallelAgents,
		},
	}
}

func (r *Runner) RunTurn(ctx context.Context, session *Session, prompt string) (RunResult, error) {
	exec := &ExecutionContext{
		Runner:     r,
		Session:    session,
		WorkingDir: r.config.WorkingDir,
	}
	return r.runTurn(ctx, exec, prompt)
}

func (r *Runner) runTurn(ctx context.Context, exec *ExecutionContext, prompt string) (RunResult, error) {
	finalize := func(result RunResult, runErr error) (RunResult, error) {
		if !errors.Is(runErr, context.Canceled) {
			r.afterTurnHook(ctx, exec, prompt, result, runErr)
		}
		return result, runErr
	}

	if strings.TrimSpace(prompt) == "" {
		return finalize(RunResult{}, errors.New("prompt is empty"))
	}

	exec.Session.Append(NewTextMessage(RoleUser, prompt))

	var total Usage
	var finalText string
	var pendingToolFailures []toolFailure
	failureReminderInjected := false

	for turn := 0; turn < r.config.MaxTurns; turn++ {
		if err := ctx.Err(); err != nil {
			return finalize(RunResult{
				FinalText: finalText,
				Usage:     total,
				Messages:  exec.Session.Snapshot(),
			}, err)
		}

		r.compactSessionIfNeeded(exec)

		response, streamedOutput, err := r.createMessage(ctx, exec, CompletionRequest{
			Model:       r.config.Model,
			System:      r.config.SystemPrompt,
			MaxTokens:   r.config.MaxTokens,
			Messages:    exec.Session.Snapshot(),
			Tools:       r.registry.Definitions(),
			Temperature: r.config.Temperature,
		}, len(pendingToolFailures) == 0)
		if err != nil {
			return finalize(RunResult{}, err)
		}

		total.InputTokens += response.Usage.InputTokens
		total.OutputTokens += response.Usage.OutputTokens

		exec.Session.Append(response.Message)

		assistantText := response.Message.Text()
		toolUses := collectToolUses(response.Message)
		if assistantText != "" {
			finalText = assistantText
			if !streamedOutput && len(toolUses) > 0 {
				r.emit(Event{
					Kind:      EventAssistantText,
					AgentID:   exec.AgentID,
					AgentName: exec.AgentName,
					Text:      assistantText,
				})
			}
		}

		if len(toolUses) == 0 {
			if len(pendingToolFailures) > 0 {
				if !finalAnswerAcknowledgesToolFailures(assistantText, pendingToolFailures) {
					if !failureReminderInjected {
						exec.Session.Append(NewTextMessage(RoleUser, buildToolFailureReminder(pendingToolFailures)))
						failureReminderInjected = true
						continue
					}

					finalText = synthesizeToolFailureSummary(pendingToolFailures)
					exec.Session.Append(NewTextMessage(RoleAssistant, finalText))
					r.emit(Event{
						Kind:      EventAssistantText,
						AgentID:   exec.AgentID,
						AgentName: exec.AgentName,
						Text:      finalText,
					})
					return finalize(RunResult{
						FinalText: finalText,
						Usage:     total,
						Messages:  exec.Session.Snapshot(),
					}, nil)
				}
			}

			if assistantText != "" && !streamedOutput {
				r.emit(Event{
					Kind:      EventAssistantText,
					AgentID:   exec.AgentID,
					AgentName: exec.AgentName,
					Text:      assistantText,
				})
			}
			return finalize(RunResult{
				FinalText: finalText,
				Usage:     total,
				Messages:  exec.Session.Snapshot(),
			}, nil)
		}

		for _, toolBlock := range toolUses {
			tool, ok := r.registry.Get(toolBlock.Name)
			if !ok {
				exec.Session.Append(Message{
					Role: RoleUser,
					Content: []Block{
						{
							Type:      BlockToolResult,
							ToolUseID: toolBlock.ID,
							Result:    "unknown tool: " + toolBlock.Name,
							IsError:   true,
						},
					},
				})
				continue
			}

			r.emit(Event{
				Kind:      EventToolCall,
				AgentID:   exec.AgentID,
				AgentName: exec.AgentName,
				ToolName:  toolBlock.Name,
				Text:      formatToolInput(toolBlock.Input),
			})

			if err := r.EnsureTool(toolBlock.Name); err != nil {
				result := ToolResult{
					Content: "tool blocked by policy: " + err.Error(),
					IsError: true,
				}
				r.emit(Event{
					Kind:      EventToolResult,
					AgentID:   exec.AgentID,
					AgentName: exec.AgentName,
					ToolName:  toolBlock.Name,
					Text:      result.Content,
				})
				exec.Session.Append(Message{
					Role: RoleUser,
					Content: []Block{
						{
							Type:      BlockToolResult,
							ToolUseID: toolBlock.ID,
							Result:    result.Content,
							IsError:   true,
						},
					},
				})
				continue
			}

			if err := r.beforeToolHook(ctx, exec, toolBlock.Name, toolBlock.Input); err != nil {
				result := ToolResult{
					Content: "tool blocked by before_tool hook: " + err.Error(),
					IsError: true,
				}
				r.emit(Event{
					Kind:      EventToolResult,
					AgentID:   exec.AgentID,
					AgentName: exec.AgentName,
					ToolName:  toolBlock.Name,
					Text:      result.Content,
				})
				exec.Session.Append(Message{
					Role: RoleUser,
					Content: []Block{
						{
							Type:      BlockToolResult,
							ToolUseID: toolBlock.ID,
							Result:    result.Content,
							IsError:   true,
						},
					},
				})
				continue
			}

			toolCtx, cancel := context.WithTimeout(ctx, r.config.ToolTimeout)
			result, callErr := tool.Call(toolCtx, exec, toolBlock.Input)
			cancel()
			if errors.Is(callErr, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
				return finalize(RunResult{
					FinalText: finalText,
					Usage:     total,
					Messages:  exec.Session.Snapshot(),
				}, context.Canceled)
			}

			if callErr != nil {
				result = ToolResult{
					Content: callErr.Error(),
					IsError: true,
				}
			}
			result.Content = truncate(result.Content, 120_000)

			r.emit(Event{
				Kind:      EventToolResult,
				AgentID:   exec.AgentID,
				AgentName: exec.AgentName,
				ToolName:  toolBlock.Name,
				Text:      result.Content,
			})
			r.afterToolHook(ctx, exec, toolBlock.Name, toolBlock.Input, result)

			exec.Session.Append(Message{
				Role: RoleUser,
				Content: []Block{
					{
						Type:      BlockToolResult,
						ToolUseID: toolBlock.ID,
						Result:    result.Content,
						IsError:   result.IsError,
					},
				},
			})
			pendingToolFailures = updatePendingToolFailures(pendingToolFailures, toolBlock.Name, toolBlock.Input, result)
			if len(pendingToolFailures) == 0 {
				failureReminderInjected = false
			}
		}
		if len(toolUses) > 0 {
			failureReminderInjected = false
		}
	}

	return finalize(RunResult{
		FinalText: finalText,
		Usage:     total,
		Messages:  exec.Session.Snapshot(),
	}, fmt.Errorf("max turns reached without a final answer"))
}

func (r *Runner) emit(event Event) {
	if r.config.EventHandler != nil {
		r.config.EventHandler(event)
	}
	switch event.Kind {
	case EventAgentSpawned, EventAgentCompleted, EventAgentCancelled:
		r.agentEventHook(event)
	}
}

func (r *Runner) createMessage(ctx context.Context, exec *ExecutionContext, request CompletionRequest, allowStream bool) (CompletionResponse, bool, error) {
	if !allowStream || !r.config.StreamResponses || exec.AgentID != "" {
		response, err := r.provider.CreateMessage(ctx, request)
		return response, false, err
	}

	streamingProvider, ok := r.provider.(StreamingProvider)
	if !ok {
		response, err := r.provider.CreateMessage(ctx, request)
		return response, false, err
	}

	streamedOutput := false
	response, err := streamingProvider.CreateMessageStream(ctx, request, func(event StreamEvent) {
		switch event.Kind {
		case StreamEventTextDelta:
			if event.Text == "" {
				return
			}
			streamedOutput = true
			r.emit(Event{
				Kind:      EventAssistantTextDelta,
				AgentID:   exec.AgentID,
				AgentName: exec.AgentName,
				Text:      event.Text,
			})
		}
	})
	if streamedOutput {
		r.emit(Event{
			Kind:      EventAssistantTextDone,
			AgentID:   exec.AgentID,
			AgentName: exec.AgentName,
		})
	}
	return response, streamedOutput, err
}

func updatePendingToolFailures(pending []toolFailure, toolName string, input json.RawMessage, result ToolResult) []toolFailure {
	keys := toolFailureKeys(toolName, input)
	if result.IsError {
		toolKey := toolFailureToolKey(toolName)
		filtered := pending[:0]
		for _, failure := range pending {
			if containsValue(failure.Keys, toolKey) {
				continue
			}
			filtered = append(filtered, failure)
		}
		return append(filtered, toolFailure{
			ToolName: strings.TrimSpace(toolName),
			Message:  strings.TrimSpace(result.Content),
			Keys:     keys,
		})
	}

	filtered := pending[:0]
	for _, failure := range pending {
		if toolFailureSharesKey(failure.Keys, keys) {
			continue
		}
		filtered = append(filtered, failure)
	}
	return filtered
}

func toolFailureKeys(toolName string, input json.RawMessage) []string {
	normalized := strings.ToLower(strings.TrimSpace(toolName))
	if normalized == "" {
		return nil
	}

	keys := []string{toolFailureToolKey(normalized)}
	switch normalized {
	case "edit_file", "write_file":
		keys = append(keys, "cap:file_write")
	case "bash":
		var payload struct {
			Command string `json:"command"`
		}
		if err := json.Unmarshal(input, &payload); err == nil && bashCommandMayWrite(payload.Command) {
			keys = append(keys, "cap:file_write")
		}
	}
	return keys
}

func toolFailureToolKey(toolName string) string {
	return "tool:" + strings.ToLower(strings.TrimSpace(toolName))
}

func toolFailureSharesKey(current, other []string) bool {
	for _, key := range current {
		if containsValue(other, key) {
			return true
		}
	}
	return false
}

func finalAnswerAcknowledgesToolFailures(text string, pending []toolFailure) bool {
	normalized := normalizeFailureText(text)
	if normalized == "" {
		return false
	}

	keywords := []string{
		"cannot", "can't", "could not", "couldn't", "unable", "was not able", "wasn't able",
		"failed", "failure", "error", "blocked", "prevented", "denied", "refused", "read-only",
		"permission", "policy", "not allowed", "not permitted", "outside allowed", "no changes",
		"unchanged", "missing", "no such", "not found",
	}
	for _, keyword := range keywords {
		if strings.Contains(normalized, keyword) {
			return true
		}
	}

	for _, failure := range pending {
		if toolName := strings.ToLower(strings.TrimSpace(failure.ToolName)); toolName != "" && strings.Contains(normalized, toolName) {
			return true
		}
		message := normalizeFailureText(failure.Message)
		if message != "" && strings.Contains(normalized, message) {
			return true
		}
	}
	return false
}

func buildToolFailureReminder(pending []toolFailure) string {
	return "One or more tool calls just failed. Do not claim the task succeeded unless you actually retried successfully. In your next response, either call more tools to recover or clearly explain what failed and why.\n\nFailed tool calls:\n" + formatToolFailureBullets(pending)
}

func synthesizeToolFailureSummary(pending []toolFailure) string {
	return "I couldn't complete the requested work because these tool calls failed:\n" + formatToolFailureBullets(pending)
}

func formatToolFailureBullets(pending []toolFailure) string {
	lines := make([]string, 0, len(pending))
	for _, failure := range pending {
		name := strings.TrimSpace(failure.ToolName)
		if name == "" {
			name = "tool"
		}
		message := strings.TrimSpace(failure.Message)
		if message == "" {
			message = "unknown error"
		}
		lines = append(lines, "- "+name+": "+message)
	}
	return strings.Join(lines, "\n")
}

func normalizeFailureText(text string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(text))), " ")
}

func collectToolUses(message Message) []Block {
	blocks := make([]Block, 0)
	for _, block := range message.Content {
		if block.Type == BlockToolUse {
			blocks = append(blocks, block)
		}
	}
	return blocks
}

func formatToolInput(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "{}"
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return string(raw)
	}
	pretty, err := json.Marshal(v)
	if err != nil {
		return string(raw)
	}
	return string(pretty)
}

func truncate(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "\n\n[truncated]"
}

func (r *Runner) beforeToolHook(ctx context.Context, exec *ExecutionContext, toolName string, input json.RawMessage) error {
	return r.invokeHook(ctx, HookEvent{
		Kind:            HookBeforeTool,
		Timestamp:       time.Now().UTC(),
		WorkingDir:      exec.WorkingDir,
		AgentID:         exec.AgentID,
		AgentName:       exec.AgentName,
		ToolName:        toolName,
		ToolInput:       formatToolInput(input),
		SessionMessages: len(exec.Session.Snapshot()),
	}, true)
}

func (r *Runner) afterToolHook(ctx context.Context, exec *ExecutionContext, toolName string, input json.RawMessage, result ToolResult) {
	_ = r.invokeHook(ctx, HookEvent{
		Kind:            HookAfterTool,
		Timestamp:       time.Now().UTC(),
		WorkingDir:      exec.WorkingDir,
		AgentID:         exec.AgentID,
		AgentName:       exec.AgentName,
		ToolName:        toolName,
		ToolInput:       formatToolInput(input),
		ToolResult:      result.Content,
		ToolError:       result.IsError,
		SessionMessages: len(exec.Session.Snapshot()),
	}, false)
}

func (r *Runner) afterTurnHook(ctx context.Context, exec *ExecutionContext, prompt string, result RunResult, runErr error) {
	status := "completed"
	errText := ""
	if runErr != nil {
		status = "failed"
		errText = runErr.Error()
	}
	_ = r.invokeHook(ctx, HookEvent{
		Kind:            HookAfterTurn,
		Timestamp:       time.Now().UTC(),
		WorkingDir:      exec.WorkingDir,
		AgentID:         exec.AgentID,
		AgentName:       exec.AgentName,
		Prompt:          prompt,
		FinalText:       result.FinalText,
		Status:          status,
		Error:           errText,
		SessionMessages: len(result.Messages),
		InputTokens:     result.Usage.InputTokens,
		OutputTokens:    result.Usage.OutputTokens,
	}, false)
}

func (r *Runner) agentEventHook(event Event) {
	status := "unknown"
	finalText := ""
	errText := ""
	switch event.Kind {
	case EventAgentSpawned:
		status = "spawned"
		finalText = event.Text
	case EventAgentCompleted:
		status = "completed"
		finalText = event.Text
	case EventAgentCancelled:
		status = "cancelled"
		errText = event.Text
	}
	_ = r.invokeHook(context.Background(), HookEvent{
		Kind:       HookAgentEvent,
		Timestamp:  time.Now().UTC(),
		WorkingDir: r.config.WorkingDir,
		AgentID:    event.AgentID,
		AgentName:  event.AgentName,
		Status:     status,
		FinalText:  finalText,
		Error:      errText,
	}, false)
}

func (r *Runner) invokeHook(ctx context.Context, event HookEvent, blocking bool) error {
	if r.config.Hooks == nil {
		return nil
	}

	hookCtx := ctx
	if hookCtx == nil {
		hookCtx = context.Background()
	}
	if r.config.HookTimeout > 0 {
		var cancel context.CancelFunc
		hookCtx, cancel = context.WithTimeout(hookCtx, r.config.HookTimeout)
		defer cancel()
	}

	if err := r.config.Hooks.HandleHook(hookCtx, event); err != nil {
		if event.Kind != HookAgentEvent {
			r.emit(Event{
				Kind:      EventHookError,
				AgentID:   event.AgentID,
				AgentName: event.AgentName,
				ToolName:  event.ToolName,
				Text:      string(event.Kind) + ": " + err.Error(),
			})
		}
		if blocking {
			return err
		}
	}
	return nil
}
