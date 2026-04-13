package openai

import (
	"strings"
	"unicode"
)

const (
	thinkOpenTag  = "<think>"
	thinkCloseTag = "</think>"
)

// reasoningFilter strips reasoning tags such as <think>...</think> from
// OpenAI-compatible text output while preserving the user-visible answer.
type reasoningFilter struct {
	pending                 string
	insideThink             bool
	suppressLeadingSpace    bool
	emittedVisibleTextChunk bool
}

func sanitizeAssistantText(text string) string {
	if text == "" {
		return ""
	}
	var filter reasoningFilter
	return filter.Append(text) + filter.Finish()
}

func (f *reasoningFilter) Append(chunk string) string {
	if chunk == "" {
		return ""
	}
	f.pending += chunk
	return f.process(false)
}

func (f *reasoningFilter) Finish() string {
	if f.pending == "" {
		return ""
	}
	return f.process(true)
}

func (f *reasoningFilter) process(final bool) string {
	var visible strings.Builder

	for {
		lower := strings.ToLower(f.pending)
		if f.insideThink {
			index := strings.Index(lower, thinkCloseTag)
			if index < 0 {
				if final {
					f.pending = ""
					return visible.String()
				}
				f.pending = trailingCandidate(f.pending, thinkCloseTag)
				return visible.String()
			}
			f.pending = f.pending[index+len(thinkCloseTag):]
			f.insideThink = false
			if !f.emittedVisibleTextChunk {
				f.suppressLeadingSpace = true
			}
			continue
		}

		index := strings.Index(lower, thinkOpenTag)
		if index < 0 {
			if final {
				visible.WriteString(f.consumeVisible(f.pending))
				f.pending = ""
				return visible.String()
			}
			split := len(f.pending) - (len(thinkOpenTag) - 1)
			if split < 0 {
				split = 0
			}
			visible.WriteString(f.consumeVisible(f.pending[:split]))
			f.pending = f.pending[split:]
			return visible.String()
		}

		visible.WriteString(f.consumeVisible(f.pending[:index]))
		f.pending = f.pending[index+len(thinkOpenTag):]
		f.insideThink = true
	}
}

func (f *reasoningFilter) consumeVisible(text string) string {
	if text == "" {
		return ""
	}
	if f.suppressLeadingSpace {
		text = strings.TrimLeftFunc(text, unicode.IsSpace)
		if text == "" {
			return ""
		}
		f.suppressLeadingSpace = false
	}
	f.emittedVisibleTextChunk = true
	return text
}

func trailingCandidate(text, marker string) string {
	max := len(marker) - 1
	if len(text) <= max {
		return text
	}
	return text[len(text)-max:]
}
