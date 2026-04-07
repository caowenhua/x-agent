package engine

import "sync"

type Session struct {
	mu       sync.RWMutex
	messages []Message
}

func NewSession(initial ...Message) *Session {
	return &Session{
		messages: CloneMessages(initial),
	}
}

func (s *Session) Append(message Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages = append(s.messages, CloneMessages([]Message{message})...)
}

func (s *Session) Snapshot() []Message {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return CloneMessages(s.messages)
}

func (s *Session) Replace(messages []Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages = CloneMessages(messages)
}
