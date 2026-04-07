package engine

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sort"
	"time"
)

type AgentStatus string

const (
	AgentRunning   AgentStatus = "running"
	AgentCompleted AgentStatus = "completed"
	AgentFailed    AgentStatus = "failed"
)

type AgentSnapshot struct {
	ID          string      `json:"id"`
	Name        string      `json:"name"`
	Status      AgentStatus `json:"status"`
	Prompt      string      `json:"prompt"`
	Result      string      `json:"result,omitempty"`
	Error       string      `json:"error,omitempty"`
	Background  bool        `json:"background"`
	StartedAt   time.Time   `json:"started_at"`
	CompletedAt *time.Time  `json:"completed_at,omitempty"`
}

type SpawnRequest struct {
	Name           string
	Prompt         string
	Background     bool
	Model          string
	MaxTurns       int
	WorkingDir     string
	InheritHistory bool
}

type managedAgent struct {
	snapshot AgentSnapshot
	done     chan struct{}
}

func newAgentID() (string, error) {
	buf := make([]byte, 6)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "agent_" + hex.EncodeToString(buf), nil
}

func (r *Runner) SpawnAgent(parent *ExecutionContext, request SpawnRequest) (AgentSnapshot, error) {
	if parent != nil && parent.Depth >= r.config.MaxAgentDepth {
		return AgentSnapshot{}, errors.New("maximum agent depth reached")
	}

	id, err := newAgentID()
	if err != nil {
		return AgentSnapshot{}, err
	}

	name := request.Name
	if name == "" {
		name = id
	}

	started := time.Now()
	agent := &managedAgent{
		snapshot: AgentSnapshot{
			ID:         id,
			Name:       name,
			Status:     AgentRunning,
			Prompt:     request.Prompt,
			Background: request.Background,
			StartedAt:  started,
		},
		done: make(chan struct{}),
	}

	r.agentsMu.Lock()
	r.agents[id] = agent
	r.agentsMu.Unlock()

	if r.config.EventHandler != nil {
		r.config.EventHandler(Event{
			Kind:      EventAgentSpawned,
			AgentID:   id,
			AgentName: name,
			Text:      request.Prompt,
		})
	}

	childRunner := r.cloneForAgent(request)
	childSession := NewSession()
	if request.InheritHistory && parent != nil && parent.Session != nil {
		childSession.Replace(parent.Session.Snapshot())
	}

	childCtx := &ExecutionContext{
		Runner:     childRunner,
		Session:    childSession,
		WorkingDir: childRunner.config.WorkingDir,
		AgentID:    id,
		AgentName:  name,
	}
	if parent != nil {
		childCtx.Depth = parent.Depth + 1
	}

	go func() {
		defer close(agent.done)

		ctx := context.Background()
		result, runErr := childRunner.runTurn(ctx, childCtx, request.Prompt)

		finished := time.Now()

		r.agentsMu.Lock()
		defer r.agentsMu.Unlock()

		if runErr != nil {
			agent.snapshot.Status = AgentFailed
			agent.snapshot.Error = runErr.Error()
		} else {
			agent.snapshot.Status = AgentCompleted
			agent.snapshot.Result = result.FinalText
		}
		agent.snapshot.CompletedAt = &finished

		if r.config.EventHandler != nil {
			text := agent.snapshot.Result
			if agent.snapshot.Error != "" {
				text = agent.snapshot.Error
			}
			r.config.EventHandler(Event{
				Kind:      EventAgentCompleted,
				AgentID:   id,
				AgentName: name,
				Text:      text,
			})
		}
	}()

	if request.Background {
		return agent.snapshot, nil
	}

	return r.WaitAgent(context.Background(), id)
}

func (r *Runner) WaitAgent(ctx context.Context, id string) (AgentSnapshot, error) {
	r.agentsMu.RLock()
	agent, ok := r.agents[id]
	r.agentsMu.RUnlock()
	if !ok {
		return AgentSnapshot{}, errors.New("agent not found")
	}

	select {
	case <-ctx.Done():
		return AgentSnapshot{}, ctx.Err()
	case <-agent.done:
		r.agentsMu.RLock()
		defer r.agentsMu.RUnlock()
		return agent.snapshot, nil
	}
}

func (r *Runner) ListAgents() []AgentSnapshot {
	r.agentsMu.RLock()
	defer r.agentsMu.RUnlock()

	snapshots := make([]AgentSnapshot, 0, len(r.agents))
	for _, agent := range r.agents {
		snapshots = append(snapshots, agent.snapshot)
	}
	sort.Slice(snapshots, func(i, j int) bool {
		return snapshots[i].StartedAt.Before(snapshots[j].StartedAt)
	})
	return snapshots
}

func (r *Runner) cloneForAgent(request SpawnRequest) *Runner {
	cfg := r.config
	if request.Model != "" {
		cfg.Model = request.Model
	}
	if request.MaxTurns > 0 {
		cfg.MaxTurns = request.MaxTurns
	}
	if request.WorkingDir != "" {
		cfg.WorkingDir = request.WorkingDir
	}

	child := &Runner{
		provider: r.provider,
		registry: r.registry,
		config:   cfg,
		agents:   r.agents,
	}
	return child
}
