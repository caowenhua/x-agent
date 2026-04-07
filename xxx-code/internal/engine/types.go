package engine

import (
	"encoding/json"
	"strings"
)

type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

type BlockType string

const (
	BlockText       BlockType = "text"
	BlockToolUse    BlockType = "tool_use"
	BlockToolResult BlockType = "tool_result"
)

type Block struct {
	Type      BlockType       `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Result    string          `json:"result,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
}

type Message struct {
	Role    Role    `json:"role"`
	Content []Block `json:"content"`
}

type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

func NewTextMessage(role Role, text string) Message {
	return Message{
		Role: role,
		Content: []Block{
			{
				Type: BlockText,
				Text: text,
			},
		},
	}
}

func (m Message) Text() string {
	var parts []string
	for _, block := range m.Content {
		if block.Type == BlockText && strings.TrimSpace(block.Text) != "" {
			parts = append(parts, block.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func CloneMessages(messages []Message) []Message {
	cloned := make([]Message, 0, len(messages))
	for _, msg := range messages {
		copyMsg := Message{
			Role:    msg.Role,
			Content: make([]Block, 0, len(msg.Content)),
		}
		for _, block := range msg.Content {
			copyBlock := block
			if len(block.Input) > 0 {
				copyBlock.Input = append(json.RawMessage(nil), block.Input...)
			}
			copyMsg.Content = append(copyMsg.Content, copyBlock)
		}
		cloned = append(cloned, copyMsg)
	}
	return cloned
}
