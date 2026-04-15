package stability

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/caowenhua/x-agent/xxx-code/internal/config"
	"github.com/caowenhua/x-agent/xxx-code/internal/daemon"
	"github.com/caowenhua/x-agent/xxx-code/internal/engine"
	"github.com/caowenhua/x-agent/xxx-code/internal/remote"
	"github.com/caowenhua/x-agent/xxx-code/internal/tools"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/sync/errgroup"
)

type Config struct {
	Duration        time.Duration
	Iterations      int
	Concurrency     int
	RestartEvery    int
	ScenarioTimeout time.Duration
	ProgressEvery   time.Duration
	WorkingDir      string
	KeepWorkDir     bool
	Verbose         bool
	HelperBinary    string
}

type ScenarioStats struct {
	Count        int           `json:"count"`
	Failures     int           `json:"failures"`
	TotalLatency time.Duration `json:"total_latency"`
	MaxLatency   time.Duration `json:"max_latency"`
}

type Result struct {
	StartedAt    time.Time                `json:"started_at"`
	CompletedAt  time.Time                `json:"completed_at"`
	WorkDir      string                   `json:"work_dir"`
	Rounds       int                      `json:"rounds"`
	Restarts     int                      `json:"restarts"`
	TotalOps     int                      `json:"total_ops"`
	FailedOps    int                      `json:"failed_ops"`
	ScenarioInfo map[string]ScenarioStats `json:"scenario_info"`
}

func (s ScenarioStats) AverageLatency() time.Duration {
	if s.Count <= 0 {
		return 0
	}
	return s.TotalLatency / time.Duration(s.Count)
}

func (s ScenarioStats) MarshalJSON() ([]byte, error) {
	type scenarioStatsJSON struct {
		Count             int    `json:"count"`
		Failures          int    `json:"failures"`
		AvgLatency        string `json:"avg_latency"`
		AvgLatencyNanos   int64  `json:"avg_latency_nanos"`
		TotalLatency      string `json:"total_latency"`
		TotalLatencyNanos int64  `json:"total_latency_nanos"`
		MaxLatency        string `json:"max_latency"`
		MaxLatencyNanos   int64  `json:"max_latency_nanos"`
	}
	avg := s.AverageLatency()
	return json.Marshal(scenarioStatsJSON{
		Count:             s.Count,
		Failures:          s.Failures,
		AvgLatency:        avg.String(),
		AvgLatencyNanos:   avg.Nanoseconds(),
		TotalLatency:      s.TotalLatency.String(),
		TotalLatencyNanos: s.TotalLatency.Nanoseconds(),
		MaxLatency:        s.MaxLatency.String(),
		MaxLatencyNanos:   s.MaxLatency.Nanoseconds(),
	})
}

type runner struct {
	cfg     Config
	stdout  io.Writer
	stderr  io.Writer
	tracker *tracker
	env     *environment

	startedAt time.Time
}

type tracker struct {
	mu        sync.Mutex
	totalOps  int
	failedOps int
	restarts  int
	rounds    int
	scenarios map[string]ScenarioStats
}

type environment struct {
	cfg          Config
	daemonConfig config.Config
	helperBinary string
	pluginSource string
	mcpServer    *httptest.Server
	pluginMu     sync.Mutex

	mu         sync.RWMutex
	server     *daemon.Server
	httpServer *httptest.Server
	client     *remote.Client
}

type scenario struct {
	name string
	run  func(context.Context, *environment, int, int) error
}

type scenarioError struct {
	Round    int
	Worker   int
	Scenario string
	Err      error
}

type scenarioInput struct {
	Text       string
	ToolResult string
	HasTool    bool
}

func Run(ctx context.Context, cfg Config, stdout, stderr io.Writer) (Result, error) {
	cfg = normalizeConfig(cfg)
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}

	env, ownedWorkDir, err := newEnvironment(cfg)
	if err != nil {
		return Result{}, err
	}

	r := &runner{
		cfg:     cfg,
		stdout:  stdout,
		stderr:  stderr,
		tracker: newTracker(),
		env:     env,
	}

	r.startedAt = time.Now().UTC()
	fmt.Fprintf(r.stdout, "starting stability run workdir=%s duration=%s iterations=%d concurrency=%d restartEvery=%d\n",
		env.daemonConfig.WorkingDir,
		cfg.Duration,
		cfg.Iterations,
		cfg.Concurrency,
		cfg.RestartEvery,
	)

	runErr := r.run(ctx)
	result := r.resultSnapshot(env.daemonConfig.WorkingDir)

	if closeErr := env.Close(); closeErr != nil && runErr == nil {
		runErr = closeErr
	}

	keepWorkDir := cfg.KeepWorkDir || runErr != nil
	if ownedWorkDir && !keepWorkDir {
		if removeErr := os.RemoveAll(env.daemonConfig.WorkingDir); removeErr != nil && runErr == nil {
			runErr = removeErr
		}
	}

	if runErr != nil {
		fmt.Fprintf(r.stderr, "stability run failed: %v\n", runErr)
		fmt.Fprintf(r.stderr, "stability artifacts kept at %s\n", env.daemonConfig.WorkingDir)
		return result, runErr
	}

	if keepWorkDir {
		fmt.Fprintf(r.stdout, "stability artifacts kept at %s\n", env.daemonConfig.WorkingDir)
	}
	r.printSummary(result)
	return result, nil
}

func normalizeConfig(cfg Config) Config {
	if cfg.Duration <= 0 && cfg.Iterations <= 0 {
		cfg.Duration = 2 * time.Minute
	}
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 2
	}
	if cfg.RestartEvery <= 0 {
		cfg.RestartEvery = 10
	}
	if cfg.ScenarioTimeout <= 0 {
		cfg.ScenarioTimeout = 20 * time.Second
	}
	if cfg.ProgressEvery <= 0 {
		cfg.ProgressEvery = 20 * time.Second
	}
	return cfg
}

func newTracker() *tracker {
	return &tracker{
		scenarios: make(map[string]ScenarioStats),
	}
}

func (t *tracker) record(name string, latency time.Duration, err error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	stats := t.scenarios[name]
	stats.Count++
	stats.TotalLatency += latency
	if latency > stats.MaxLatency {
		stats.MaxLatency = latency
	}
	if err != nil {
		stats.Failures++
		t.failedOps++
	}
	t.scenarios[name] = stats
	t.totalOps++
}

func (t *tracker) noteRestart() {
	t.mu.Lock()
	t.restarts++
	t.mu.Unlock()
}

func (t *tracker) noteRound() {
	t.mu.Lock()
	t.rounds++
	t.mu.Unlock()
}

func (t *tracker) snapshot() (int, int, int, int, map[string]ScenarioStats) {
	t.mu.Lock()
	defer t.mu.Unlock()

	copyMap := make(map[string]ScenarioStats, len(t.scenarios))
	for name, stats := range t.scenarios {
		copyMap[name] = stats
	}
	return t.totalOps, t.failedOps, t.restarts, t.rounds, copyMap
}

func (e *environment) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	var errs []error
	if e.httpServer != nil {
		e.httpServer.Close()
		e.httpServer = nil
	}
	if e.server != nil {
		if err := e.server.Close(); err != nil {
			errs = append(errs, err)
		}
		e.server = nil
	}
	if e.mcpServer != nil {
		e.mcpServer.Close()
		e.mcpServer = nil
	}
	e.client = nil
	return errors.Join(errs...)
}

func (e *environment) restart() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.httpServer != nil {
		e.httpServer.Close()
		e.httpServer = nil
	}
	if e.server != nil {
		if err := e.server.Close(); err != nil {
			return err
		}
		e.server = nil
	}
	return e.startLocked()
}

func (e *environment) clientSnapshot() *remote.Client {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.client
}

func newEnvironment(cfg Config) (*environment, bool, error) {
	workingDir := strings.TrimSpace(cfg.WorkingDir)
	owned := false
	if workingDir == "" {
		dir, err := os.MkdirTemp("", "xxx-code-stability-*")
		if err != nil {
			return nil, false, err
		}
		workingDir = dir
		owned = true
	}
	workingDir = filepath.Clean(workingDir)
	if err := os.MkdirAll(workingDir, 0o755); err != nil {
		return nil, false, err
	}

	helperBinary := strings.TrimSpace(cfg.HelperBinary)
	if helperBinary == "" {
		exe, err := os.Executable()
		if err != nil {
			return nil, false, err
		}
		helperBinary = exe
	}

	env := &environment{
		cfg:          cfg,
		helperBinary: helperBinary,
		daemonConfig: config.Config{
			Model:             "stability-model",
			SystemPrompt:      "stability",
			MaxTurns:          6,
			MaxTokens:         1024,
			MaxParallelAgents: max(2, cfg.Concurrency*2),
			ContextBudget:     4000,
			CompactKeep:       6,
			WorkingDir:        workingDir,
			DaemonDir:         filepath.Join(workingDir, ".xxx-code", "daemon"),
			ToolTimeout:       3 * time.Second,
			HookTimeout:       time.Second,
			ReadRoots:         []string{workingDir},
			WriteRoots:        []string{workingDir},
			BashEnabled:       true,
			DaemonToken:       "stability-secret",
		},
	}

	var err error
	env.pluginSource, err = writePluginSource(workingDir, helperBinary)
	if err != nil {
		return nil, false, err
	}
	env.mcpServer = newMCPHTTPServer()
	if err := writeMCPConfig(workingDir, env.mcpServer.URL); err != nil {
		env.mcpServer.Close()
		return nil, false, err
	}
	if err := env.restart(); err != nil {
		_ = env.Close()
		return nil, false, err
	}

	return env, owned, nil
}

func (e *environment) startLocked() error {
	e.server = daemon.New(e.daemonConfig, io.Discard, io.Discard, func(config.Config) engine.Provider {
		return &scenarioProvider{}
	})
	e.httpServer = httptest.NewServer(e.server.Handler())
	e.client = remote.NewClient(e.httpServer.URL, e.daemonConfig.DaemonToken, e.httpServer.Client())
	return nil
}

func (r *runner) run(ctx context.Context) error {
	var deadline time.Time
	if r.cfg.Duration > 0 {
		deadline = time.Now().Add(r.cfg.Duration)
	}
	nextProgress := time.Now().Add(r.cfg.ProgressEvery)
	restartCount := 0

	for round := 1; ; round++ {
		if r.cfg.Iterations > 0 && round > r.cfg.Iterations {
			return nil
		}
		if !deadline.IsZero() && time.Now().After(deadline) {
			return nil
		}

		if err := r.runRound(ctx, round); err != nil {
			return err
		}
		r.tracker.noteRound()

		if r.cfg.Verbose {
			fmt.Fprintf(r.stdout, "completed round=%d elapsed=%s\n", round, time.Since(r.startedAt).Round(time.Millisecond))
		}
		if !deadline.IsZero() && time.Now().After(nextProgress) {
			r.printProgress(round)
			nextProgress = time.Now().Add(r.cfg.ProgressEvery)
		}

		if r.cfg.RestartEvery > 0 && round%r.cfg.RestartEvery == 0 {
			restartCount++
			if err := r.verifyRestart(ctx, restartCount); err != nil {
				return err
			}
			r.tracker.noteRestart()
			if r.cfg.Verbose {
				fmt.Fprintf(r.stdout, "restart verification passed index=%d\n", restartCount)
			}
		}
	}
}

func (r *runner) runRound(ctx context.Context, round int) error {
	g, roundCtx := errgroup.WithContext(ctx)
	for worker := 0; worker < r.cfg.Concurrency; worker++ {
		worker := worker
		g.Go(func() error {
			for _, sc := range scenarioSuite() {
				scCtx, cancel := context.WithTimeout(roundCtx, r.cfg.ScenarioTimeout)
				started := time.Now()
				err := sc.run(scCtx, r.env, worker, round)
				cancel()

				r.tracker.record(sc.name, time.Since(started), err)
				if err != nil {
					return scenarioError{
						Round:    round,
						Worker:   worker,
						Scenario: sc.name,
						Err:      err,
					}
				}
			}
			return nil
		})
	}
	return g.Wait()
}

func (r *runner) verifyRestart(ctx context.Context, restartIndex int) error {
	client := r.env.clientSnapshot()
	beforeSession, err := client.EnsureSession(ctx, "restart-check")
	if err != nil {
		return fmt.Errorf("ensure restart-check session: %w", err)
	}

	beforePrompt := fmt.Sprintf("restart-before:%d", restartIndex)
	beforeResult, _, err := client.RunTurn(ctx, beforeSession.ID, beforePrompt, 0)
	if err != nil {
		return fmt.Errorf("run restart check before restart: %w", err)
	}
	if beforeResult.FinalText != "reply:"+beforePrompt {
		return fmt.Errorf("unexpected restart preflight result: %s", beforeResult.FinalText)
	}

	saved, err := client.SaveSession(ctx, beforeSession.ID)
	if err != nil {
		return fmt.Errorf("save restart-check session: %w", err)
	}
	if strings.TrimSpace(saved.SessionFile) == "" {
		return errors.New("restart-check session save returned empty session file")
	}

	if err := r.env.restart(); err != nil {
		return fmt.Errorf("restart daemon: %w", err)
	}

	client = r.env.clientSnapshot()
	reloaded, err := client.GetSession(ctx, beforeSession.ID)
	if err != nil {
		return fmt.Errorf("reload restart-check session: %w", err)
	}
	if reloaded.MessageCount < 2 {
		return fmt.Errorf("restart-check session lost transcript: %+v", reloaded)
	}

	afterPrompt := fmt.Sprintf("restart-after:%d", restartIndex)
	afterResult, _, err := client.RunTurn(ctx, beforeSession.ID, afterPrompt, 0)
	if err != nil {
		return fmt.Errorf("run restart check after restart: %w", err)
	}
	if afterResult.FinalText != "reply:"+afterPrompt {
		return fmt.Errorf("unexpected restart postflight result: %s", afterResult.FinalText)
	}
	return nil
}

func (r *runner) printProgress(round int) {
	totalOps, failedOps, restarts, _, _ := r.tracker.snapshot()
	fmt.Fprintf(r.stdout, "progress elapsed=%s rounds=%d restarts=%d ops=%d failures=%d\n",
		time.Since(r.startedAt).Round(time.Second),
		round,
		restarts,
		totalOps,
		failedOps,
	)
}

func (r *runner) resultSnapshot(workDir string) Result {
	totalOps, failedOps, restarts, rounds, scenarios := r.tracker.snapshot()
	return Result{
		StartedAt:    r.startedAt,
		CompletedAt:  time.Now().UTC(),
		WorkDir:      workDir,
		Rounds:       rounds,
		Restarts:     restarts,
		TotalOps:     totalOps,
		FailedOps:    failedOps,
		ScenarioInfo: scenarios,
	}
}

func (r *runner) printSummary(result Result) {
	fmt.Fprintf(r.stdout, "completed stability run elapsed=%s rounds=%d restarts=%d ops=%d failures=%d\n",
		result.CompletedAt.Sub(result.StartedAt).Round(time.Millisecond),
		result.Rounds,
		result.Restarts,
		result.TotalOps,
		result.FailedOps,
	)

	names := make([]string, 0, len(result.ScenarioInfo))
	for name := range result.ScenarioInfo {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		stats := result.ScenarioInfo[name]
		fmt.Fprintf(r.stdout, "scenario=%s count=%d failures=%d avg=%s max=%s\n",
			name,
			stats.Count,
			stats.Failures,
			stats.AverageLatency().Round(time.Millisecond),
			stats.MaxLatency.Round(time.Millisecond),
		)
	}
}

func scenarioSuite() []scenario {
	return []scenario{
		{name: "basic_turn", run: runBasicTurn},
		{name: "stream_turn", run: runStreamTurn},
		{name: "plugin_lifecycle", run: runPluginLifecycle},
		{name: "mcp_lifecycle", run: runMCPLifecycle},
		{name: "agent_lifecycle", run: runAgentLifecycle},
		{name: "workflow_lifecycle", run: runWorkflowLifecycle},
		{name: "session_save", run: runSessionSave},
		{name: "stream_timeout", run: runStreamTimeout},
	}
}

func runBasicTurn(ctx context.Context, env *environment, worker, round int) error {
	client := env.clientSnapshot()
	sessionID := fmt.Sprintf("stability-basic-%d", worker)
	prompt := fmt.Sprintf("basic:%d:%d", worker, round)

	session, err := client.EnsureSession(ctx, sessionID)
	if err != nil {
		return err
	}
	result, _, err := client.RunTurn(ctx, session.ID, prompt, 0)
	if err != nil {
		return err
	}
	if result.FinalText != "reply:"+prompt {
		return fmt.Errorf("unexpected basic turn reply: %s", result.FinalText)
	}
	return nil
}

func runStreamTurn(ctx context.Context, env *environment, worker, round int) error {
	client := env.clientSnapshot()
	sessionID := fmt.Sprintf("stability-stream-%d", worker)
	prompt := fmt.Sprintf("stream:%d:%d", worker, round)

	session, err := client.EnsureSession(ctx, sessionID)
	if err != nil {
		return err
	}

	var streamed strings.Builder
	result, _, err := client.StreamTurn(ctx, session.ID, prompt, 0, func(event remote.TurnStreamEvent) {
		if event.Type == string(engine.EventAssistantTextDelta) {
			streamed.WriteString(event.Text)
		}
	})
	if err != nil {
		return err
	}
	if result.FinalText != "reply:"+prompt {
		return fmt.Errorf("unexpected stream result: %s", result.FinalText)
	}
	if streamed.String() != result.FinalText {
		return fmt.Errorf("unexpected streamed output: %q", streamed.String())
	}
	return nil
}

func runPluginLifecycle(ctx context.Context, env *environment, worker, round int) error {
	env.pluginMu.Lock()
	defer env.pluginMu.Unlock()

	client := env.clientSnapshot()
	sessionID := fmt.Sprintf("stability-plugin-%d", worker)

	session, err := client.EnsureSession(ctx, sessionID)
	if err != nil {
		return err
	}

	report, err := client.ValidatePlugin(ctx, session.ID, env.pluginSource)
	if err != nil {
		return err
	}
	if !report.Valid || report.PluginName != "echoer" || report.ToolCount != 1 {
		return fmt.Errorf("unexpected plugin validation report: %+v", report)
	}

	summary, err := client.InstallPlugin(ctx, session.ID, env.pluginSource, true)
	if err != nil {
		return err
	}
	if summary.PluginCount != 1 || summary.ToolCount != 1 {
		return fmt.Errorf("unexpected plugin install summary: %+v", summary)
	}

	usePrompt := fmt.Sprintf("use plugin:%d:%d", worker, round)
	result, _, err := client.RunTurn(ctx, session.ID, usePrompt, 0)
	if err != nil {
		return err
	}
	if !strings.Contains(result.FinalText, fmt.Sprintf("%d:%d", worker, round)) {
		return fmt.Errorf("unexpected plugin turn result: %s", result.FinalText)
	}

	removed, err := client.RemovePlugin(ctx, session.ID, "echoer")
	if err != nil {
		return err
	}
	if removed.PluginCount != 0 || removed.ToolCount != 0 {
		return fmt.Errorf("unexpected plugin removal summary: %+v", removed)
	}

	freshSessionID := fmt.Sprintf("stability-plugin-post-%d", worker)
	fresh, err := client.EnsureSession(ctx, freshSessionID)
	if err != nil {
		return err
	}
	afterRemoval, _, err := client.RunTurn(ctx, fresh.ID, usePrompt, 0)
	if err != nil {
		return err
	}
	if !strings.Contains(afterRemoval.FinalText, "unknown tool: plugin__echoer__echo") {
		return fmt.Errorf("unexpected removed plugin response: %s", afterRemoval.FinalText)
	}
	return nil
}

func runMCPLifecycle(ctx context.Context, env *environment, worker, round int) error {
	client := env.clientSnapshot()
	sessionID := fmt.Sprintf("stability-mcp-%d", worker)

	session, err := client.EnsureSession(ctx, sessionID)
	if err != nil {
		return err
	}

	summary, err := client.ReloadMCP(ctx, session.ID)
	if err != nil {
		return err
	}
	if summary.ServerCount != 1 || summary.ToolCount != 1 {
		return fmt.Errorf("unexpected mcp summary: %+v", summary)
	}

	resources, err := client.ListMCPResources(ctx, session.ID, "")
	if err != nil {
		return err
	}
	if len(resources) != 1 || resources[0].URI != "file:///a" {
		return fmt.Errorf("unexpected mcp resources: %+v", resources)
	}

	templates, err := client.ListMCPResourceTemplates(ctx, session.ID, "")
	if err != nil {
		return err
	}
	if len(templates) != 1 || templates[0].URITemplate != "file:///dir/{f}" {
		return fmt.Errorf("unexpected mcp templates: %+v", templates)
	}

	prompts, err := client.ListMCPPrompts(ctx, session.ID, "")
	if err != nil {
		return err
	}
	if len(prompts) != 1 || prompts[0].Name != "greet" {
		return fmt.Errorf("unexpected mcp prompts: %+v", prompts)
	}

	resource, err := client.ReadMCPResource(ctx, session.ID, "tester", "file:///a")
	if err != nil {
		return err
	}
	if len(resource.Contents) != 1 || resource.Contents[0].Text != "alpha" {
		return fmt.Errorf("unexpected mcp resource: %+v", resource)
	}

	prompt, err := client.GetMCPPrompt(ctx, session.ID, "tester", "greet", map[string]string{"name": "stability"})
	if err != nil {
		return err
	}
	if len(prompt.Messages) != 1 || !strings.Contains(prompt.Messages[0].Content, "Say hi to stability") {
		return fmt.Errorf("unexpected mcp prompt: %+v", prompt)
	}

	usePrompt := fmt.Sprintf("use mcp:%d:%d", worker, round)
	result, _, err := client.RunTurn(ctx, session.ID, usePrompt, 0)
	if err != nil {
		return err
	}
	if !strings.Contains(result.FinalText, fmt.Sprintf("%d:%d", worker, round)) {
		return fmt.Errorf("unexpected mcp turn result: %s", result.FinalText)
	}
	return nil
}

func runAgentLifecycle(ctx context.Context, env *environment, worker, round int) error {
	client := env.clientSnapshot()

	foregroundSessionID := fmt.Sprintf("stability-agent-fg-%d", worker)
	foregroundPrompt := fmt.Sprintf("delegate:%d:%d", worker, round)
	foregroundSession, err := client.EnsureSession(ctx, foregroundSessionID)
	if err != nil {
		return err
	}
	if _, _, err := client.RunTurn(ctx, foregroundSession.ID, foregroundPrompt, 0); err != nil {
		return err
	}

	agents, err := client.ListAgents(ctx, foregroundSession.ID)
	if err != nil {
		return err
	}

	var childID string
	expectedChildPrompt := fmt.Sprintf("child:%d:%d", worker, round)
	for _, agent := range agents {
		if agent.Prompt == expectedChildPrompt {
			childID = agent.ID
			break
		}
	}
	if childID == "" {
		return fmt.Errorf("missing child agent for prompt %s", expectedChildPrompt)
	}

	waited, err := client.WaitAgent(ctx, foregroundSession.ID, childID, int(runnerTimeoutSeconds(5*time.Second)))
	if err != nil {
		return err
	}
	if waited.Status != engine.AgentIdle {
		return fmt.Errorf("unexpected waited agent status: %+v", waited)
	}

	sent, err := client.SendAgent(ctx, foregroundSession.ID, childID, fmt.Sprintf("follow-up:%d:%d", worker, round), false)
	if err != nil {
		return err
	}
	if sent.Status != engine.AgentIdle || !strings.Contains(sent.Result, "follow-up") {
		return fmt.Errorf("unexpected sent agent result: %+v", sent)
	}

	backgroundSessionID := fmt.Sprintf("stability-agent-bg-%d", worker)
	backgroundPrompt := fmt.Sprintf("background:%d:%d", worker, round)
	backgroundSession, err := client.EnsureSession(ctx, backgroundSessionID)
	if err != nil {
		return err
	}
	if _, _, err := client.RunTurn(ctx, backgroundSession.ID, backgroundPrompt, 0); err != nil {
		return err
	}

	backgroundAgents, err := client.ListAgents(ctx, backgroundSession.ID)
	if err != nil {
		return err
	}

	var backgroundID string
	expectedBackgroundPrompt := fmt.Sprintf("block:%d:%d", worker, round)
	for _, agent := range backgroundAgents {
		if agent.Prompt == expectedBackgroundPrompt {
			backgroundID = agent.ID
			break
		}
	}
	if backgroundID == "" {
		return fmt.Errorf("missing background agent for prompt %s", expectedBackgroundPrompt)
	}

	cancelled, err := client.CancelAgent(ctx, backgroundSession.ID, backgroundID, true)
	if err != nil {
		return err
	}
	if cancelled.Status != engine.AgentCancelled {
		return fmt.Errorf("unexpected cancelled agent status: %+v", cancelled)
	}
	return nil
}

func runWorkflowLifecycle(ctx context.Context, env *environment, worker, round int) error {
	client := env.clientSnapshot()
	sessionID := fmt.Sprintf("stability-workflow-%d", worker)
	prompt := fmt.Sprintf("fanout:%d:%d", worker, round)

	session, err := client.EnsureSession(ctx, sessionID)
	if err != nil {
		return err
	}
	if _, _, err := client.RunTurn(ctx, session.ID, prompt, 0); err != nil {
		return err
	}

	workflows, err := client.ListWorkflows(ctx, session.ID)
	if err != nil {
		return err
	}
	if len(workflows) == 0 {
		return errors.New("expected at least one workflow")
	}
	workflow := workflows[0]

	tasks, err := client.ListWorkflowTasks(ctx, session.ID, workflow.ID, "", "")
	if err != nil {
		return err
	}
	if len(tasks) != 2 {
		return fmt.Errorf("unexpected workflow tasks: %+v", tasks)
	}

	resumed, err := client.ResumeWorkflow(ctx, session.ID, workflow.ID, remote.WorkflowResumeOptions{
		TaskNames: []string{"one"},
	})
	if err != nil {
		return err
	}
	if resumed.Workflow.Status != tools.WorkflowCompleted {
		return fmt.Errorf("unexpected resumed workflow: %+v", resumed.Workflow)
	}
	return nil
}

func runSessionSave(ctx context.Context, env *environment, worker, round int) error {
	client := env.clientSnapshot()
	sessionID := fmt.Sprintf("stability-save-%d", worker)
	prompt := fmt.Sprintf("save:%d:%d", worker, round)

	session, err := client.EnsureSession(ctx, sessionID)
	if err != nil {
		return err
	}
	if _, _, err := client.RunTurn(ctx, session.ID, prompt, 0); err != nil {
		return err
	}

	saved, err := client.SaveSession(ctx, session.ID)
	if err != nil {
		return err
	}
	if strings.TrimSpace(saved.SessionFile) == "" {
		return errors.New("empty saved session file")
	}
	if _, err := os.Stat(saved.SessionFile); err != nil {
		return err
	}
	return nil
}

func runStreamTimeout(ctx context.Context, env *environment, worker, round int) error {
	client := env.clientSnapshot()
	sessionID := fmt.Sprintf("stability-timeout-%d", worker)
	prompt := fmt.Sprintf("timeout:%d:%d", worker, round)

	session, err := client.EnsureSession(ctx, sessionID)
	if err != nil {
		return err
	}
	_, _, err = client.StreamTurn(ctx, session.ID, prompt, 1, nil)
	if err == nil {
		return errors.New("expected timeout error")
	}
	var remoteErr *remote.Error
	if !errors.As(err, &remoteErr) {
		return err
	}
	if remoteErr.Code != "timeout" || !remoteErr.Retryable {
		return fmt.Errorf("unexpected timeout error: %+v", remoteErr)
	}
	return nil
}

func writePluginSource(workingDir, helperBinary string) (string, error) {
	rootDir := filepath.Join(workingDir, "plugin-sources", "echoer")
	if err := os.MkdirAll(rootDir, 0o755); err != nil {
		return "", err
	}
	manifest := fmt.Sprintf(`{
  "name": "echoer",
  "tools": [{
    "name": "echo",
    "description": "Echo plugin",
    "input_schema": {"type": "object"},
    "command": %q,
    "args": ["--helper-plugin-echo"]
  }]
}`, helperBinary)
	if err := os.WriteFile(filepath.Join(rootDir, "plugin.json"), []byte(manifest), 0o644); err != nil {
		return "", err
	}
	return rootDir, nil
}

func writeMCPConfig(workingDir, serverURL string) error {
	configJSON := fmt.Sprintf(`{
  "mcpServers": {
    "tester": {
      "transport": "http",
      "url": %q
    }
  }
}`, serverURL)
	return os.WriteFile(filepath.Join(workingDir, ".mcp.json"), []byte(configJSON), 0o644)
}

func newMCPHTTPServer() *httptest.Server {
	handler := sdkmcp.NewStreamableHTTPHandler(func(*http.Request) *sdkmcp.Server {
		server := sdkmcp.NewServer(&sdkmcp.Implementation{
			Name:    "stability-mcp",
			Version: "1.0.0",
		}, nil)
		server.AddResource(&sdkmcp.Resource{
			Name:        "alpha",
			Description: "Alpha resource",
			URI:         "file:///a",
		}, func(ctx context.Context, req *sdkmcp.ReadResourceRequest) (*sdkmcp.ReadResourceResult, error) {
			_ = ctx
			if req.Params.URI != "file:///a" {
				return nil, sdkmcp.ResourceNotFoundError(req.Params.URI)
			}
			return &sdkmcp.ReadResourceResult{
				Contents: []*sdkmcp.ResourceContents{{
					URI:  "file:///a",
					Text: "alpha",
				}},
			}, nil
		})
		server.AddResourceTemplate(&sdkmcp.ResourceTemplate{
			Name:        "dir",
			Description: "Directory template",
			URITemplate: "file:///dir/{f}",
		}, func(ctx context.Context, req *sdkmcp.ReadResourceRequest) (*sdkmcp.ReadResourceResult, error) {
			_ = ctx
			uri := req.Params.URI
			if !strings.HasPrefix(uri, "file:///dir/") {
				return nil, sdkmcp.ResourceNotFoundError(uri)
			}
			return &sdkmcp.ReadResourceResult{
				Contents: []*sdkmcp.ResourceContents{{
					URI:  uri,
					Text: strings.TrimPrefix(uri, "file:///dir/"),
				}},
			}, nil
		})
		server.AddPrompt(&sdkmcp.Prompt{
			Name:        "greet",
			Description: "Greeting prompt",
			Arguments: []*sdkmcp.PromptArgument{{
				Name:        "name",
				Description: "Name to greet",
				Required:    true,
			}},
		}, func(ctx context.Context, req *sdkmcp.GetPromptRequest) (*sdkmcp.GetPromptResult, error) {
			_ = ctx
			return &sdkmcp.GetPromptResult{
				Description: "Greeting prompt",
				Messages: []*sdkmcp.PromptMessage{{
					Role:    "user",
					Content: &sdkmcp.TextContent{Text: "Say hi to " + req.Params.Arguments["name"]},
				}},
			}, nil
		})
		sdkmcp.AddTool(server, &sdkmcp.Tool{
			Name:        "echo_text",
			Description: "Echo text back to the caller",
		}, func(ctx context.Context, req *sdkmcp.CallToolRequest, input struct {
			Value string `json:"value" jsonschema:"value to echo back"`
		}) (*sdkmcp.CallToolResult, map[string]string, error) {
			_ = ctx
			_ = req
			return nil, map[string]string{"echo": input.Value}, nil
		})
		return server
	}, nil)
	return httptest.NewServer(handler)
}

type scenarioProvider struct{}

func (p *scenarioProvider) CreateMessage(ctx context.Context, request engine.CompletionRequest) (engine.CompletionResponse, error) {
	input := latestScenarioInput(request.Messages)
	if input.HasTool {
		return engine.CompletionResponse{
			Message: engine.NewTextMessage(engine.RoleAssistant, "result:"+input.ToolResult),
		}, nil
	}

	switch {
	case strings.HasPrefix(input.Text, "delegate:"):
		suffix := strings.TrimPrefix(input.Text, "delegate:")
		payload, _ := json.Marshal(map[string]any{
			"name":       "worker",
			"prompt":     "child:" + suffix,
			"background": false,
		})
		return engine.CompletionResponse{
			Message: engine.Message{
				Role: engine.RoleAssistant,
				Content: []engine.Block{
					{Type: engine.BlockText, Text: "delegating"},
					{Type: engine.BlockToolUse, ID: "toolu_delegate", Name: "agent_spawn", Input: payload},
				},
			},
		}, nil
	case strings.HasPrefix(input.Text, "background:"):
		suffix := strings.TrimPrefix(input.Text, "background:")
		payload, _ := json.Marshal(map[string]any{
			"name":       "worker",
			"prompt":     "block:" + suffix,
			"background": true,
		})
		return engine.CompletionResponse{
			Message: engine.Message{
				Role: engine.RoleAssistant,
				Content: []engine.Block{
					{Type: engine.BlockText, Text: "delegating"},
					{Type: engine.BlockToolUse, ID: "toolu_delegate_bg", Name: "agent_spawn", Input: payload},
				},
			},
		}, nil
	case strings.HasPrefix(input.Text, "block:"):
		<-ctx.Done()
		return engine.CompletionResponse{}, ctx.Err()
	case strings.HasPrefix(input.Text, "fanout:"):
		suffix := strings.TrimPrefix(input.Text, "fanout:")
		payload, _ := json.Marshal(map[string]any{
			"wait":         true,
			"max_parallel": 1,
			"tasks": []map[string]any{
				{"name": "one", "prompt": "task-one:" + suffix},
				{"name": "two", "prompt": "task-two:" + suffix},
			},
		})
		return engine.CompletionResponse{
			Message: engine.Message{
				Role: engine.RoleAssistant,
				Content: []engine.Block{
					{Type: engine.BlockText, Text: "fanout"},
					{Type: engine.BlockToolUse, ID: "toolu_fanout", Name: "agent_fanout", Input: payload},
				},
			},
		}, nil
	case strings.HasPrefix(input.Text, "use plugin:"):
		value := strings.TrimPrefix(input.Text, "use plugin:")
		payload, _ := json.Marshal(map[string]any{"value": value})
		return engine.CompletionResponse{
			Message: engine.Message{
				Role: engine.RoleAssistant,
				Content: []engine.Block{
					{Type: engine.BlockText, Text: "using plugin"},
					{Type: engine.BlockToolUse, ID: "toolu_plugin", Name: "plugin__echoer__echo", Input: payload},
				},
			},
		}, nil
	case strings.HasPrefix(input.Text, "use mcp:"):
		value := strings.TrimPrefix(input.Text, "use mcp:")
		payload, _ := json.Marshal(map[string]any{"value": value})
		return engine.CompletionResponse{
			Message: engine.Message{
				Role: engine.RoleAssistant,
				Content: []engine.Block{
					{Type: engine.BlockText, Text: "using mcp"},
					{Type: engine.BlockToolUse, ID: "toolu_mcp", Name: "mcp__tester__echo_text", Input: payload},
				},
			},
		}, nil
	case strings.HasPrefix(input.Text, "timeout:"):
		<-ctx.Done()
		return engine.CompletionResponse{}, ctx.Err()
	default:
		return engine.CompletionResponse{
			Message: engine.NewTextMessage(engine.RoleAssistant, "reply:"+input.Text),
		}, nil
	}
}

func (p *scenarioProvider) CreateMessageStream(ctx context.Context, request engine.CompletionRequest, handle func(engine.StreamEvent)) (engine.CompletionResponse, error) {
	input := latestScenarioInput(request.Messages)
	if input.HasTool ||
		strings.HasPrefix(input.Text, "delegate:") ||
		strings.HasPrefix(input.Text, "background:") ||
		strings.HasPrefix(input.Text, "fanout:") ||
		strings.HasPrefix(input.Text, "use plugin:") ||
		strings.HasPrefix(input.Text, "use mcp:") {
		return p.CreateMessage(ctx, request)
	}
	if strings.HasPrefix(input.Text, "timeout:") {
		<-ctx.Done()
		return engine.CompletionResponse{}, ctx.Err()
	}

	full := "reply:" + input.Text
	chunks := []string{"reply:", input.Text}
	for _, chunk := range chunks {
		if strings.TrimSpace(chunk) == "" {
			continue
		}
		handle(engine.StreamEvent{
			Kind: engine.StreamEventTextDelta,
			Text: chunk,
		})
	}
	return engine.CompletionResponse{
		Message: engine.NewTextMessage(engine.RoleAssistant, full),
	}, nil
}

func latestScenarioInput(messages []engine.Message) scenarioInput {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != engine.RoleUser {
			continue
		}
		input := scenarioInput{}
		for _, block := range messages[i].Content {
			switch block.Type {
			case engine.BlockText:
				text := strings.TrimSpace(block.Text)
				if text != "" {
					input.Text = text
				}
			case engine.BlockToolResult:
				result := strings.TrimSpace(block.Result)
				if result != "" {
					input.ToolResult = result
					input.HasTool = true
				}
			}
		}
		return input
	}
	return scenarioInput{}
}

func runnerTimeoutSeconds(timeout time.Duration) int {
	if timeout <= 0 {
		return 0
	}
	return int(timeout.Round(time.Second) / time.Second)
}

func (e scenarioError) Error() string {
	return fmt.Sprintf("round=%d worker=%d scenario=%s: %v", e.Round, e.Worker, e.Scenario, e.Err)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
