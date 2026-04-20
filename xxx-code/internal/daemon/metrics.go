package daemon

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/caowenhua/x-agent/xxx-code/internal/engine"
)

type daemonMetrics struct {
	mu sync.Mutex

	startedAt time.Time

	httpRequests map[httpMetricKey]latencyAggregate
	turns        map[string]latencyAggregate
	tools        map[toolMetricKey]latencyAggregate
	agentEvents  map[string]uint64
}

type httpMetricKey struct {
	Method  string
	Route   string
	Status  int
	Outcome string
}

type toolMetricKey struct {
	Tool   string
	Result string
}

type latencyAggregate struct {
	Count      uint64
	SumSeconds float64
}

type daemonRuntimeSnapshot struct {
	Sessions      int
	AgentsByState map[string]int
	Workflows     map[string]int
	Goroutines    int
	MemStats      runtime.MemStats
}

func newDaemonMetrics() *daemonMetrics {
	return &daemonMetrics{
		startedAt:    time.Now().UTC(),
		httpRequests: make(map[httpMetricKey]latencyAggregate),
		turns:        make(map[string]latencyAggregate),
		tools:        make(map[toolMetricKey]latencyAggregate),
		agentEvents:  make(map[string]uint64),
	}
}

func (m *daemonMetrics) recordHTTPRequest(method, route string, status int, duration time.Duration) {
	if m == nil {
		return
	}
	key := httpMetricKey{
		Method:  normalizeMetricValue(strings.ToUpper(strings.TrimSpace(method)), "UNKNOWN"),
		Route:   normalizeMetricValue(route, "unknown"),
		Status:  status,
		Outcome: normalizeMetricValue(auditOutcomeForStatus(status), "ok"),
	}
	m.mu.Lock()
	m.httpRequests[key] = m.httpRequests[key].withObservation(duration)
	m.mu.Unlock()
}

func (m *daemonMetrics) recordTurn(result string, duration time.Duration) {
	if m == nil {
		return
	}
	result = normalizeMetricValue(result, "error")
	m.mu.Lock()
	m.turns[result] = m.turns[result].withObservation(duration)
	m.mu.Unlock()
}

func (m *daemonMetrics) recordEvent(event engine.Event) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	switch event.Kind {
	case engine.EventToolResult:
		if !event.Executed {
			return
		}
		key := toolMetricKey{
			Tool:   normalizeMetricValue(event.ToolName, "unknown"),
			Result: toolResultLabel(event.IsError),
		}
		m.tools[key] = m.tools[key].withObservation(event.ToolDuration)
	case engine.EventAgentSpawned, engine.EventAgentCompleted, engine.EventAgentCancelled:
		m.agentEvents[normalizeMetricValue(string(event.Kind), "unknown")]++
	}
}

func (m *daemonMetrics) render(s *Server) string {
	if m == nil {
		return ""
	}

	httpSnapshot, turnSnapshot, toolSnapshot, agentSnapshot, startedAt := m.snapshot()
	runtimeSnapshot := snapshotDaemonRuntime(s)

	var builder strings.Builder
	writeGauge(&builder, "xxx_code_daemon_uptime_seconds", "Process uptime for the daemon.", nil, time.Since(startedAt).Seconds())

	writeGauge(&builder, "xxx_code_daemon_sessions", "Loaded daemon sessions grouped by state.", map[string]string{
		"state": "loaded",
	}, float64(runtimeSnapshot.Sessions))

	writeGaugeSeries(&builder, "xxx_code_daemon_agents", "Managed agent snapshots grouped by status.", "status", runtimeSnapshot.AgentsByState)
	writeGaugeSeries(&builder, "xxx_code_daemon_workflows", "Managed workflows grouped by status.", "status", runtimeSnapshot.Workflows)

	writeCounterSeries(&builder, "xxx_code_daemon_agent_events_total", "Agent lifecycle events observed by the daemon.", "event", agentSnapshot)
	writeCounterSeriesFromLatency(&builder, "xxx_code_daemon_http_requests_total", "HTTP requests handled by the daemon.", httpSnapshot, func(key httpMetricKey) map[string]string {
		return map[string]string{
			"method":  key.Method,
			"route":   key.Route,
			"status":  strconv.Itoa(key.Status),
			"outcome": key.Outcome,
		}
	})
	writeCounterSeriesFromLatencyFiltered(&builder, "xxx_code_daemon_http_errors_total", "HTTP requests that completed with a non-success status.", httpSnapshot, func(key httpMetricKey) bool {
		return key.Status >= http.StatusBadRequest
	}, func(key httpMetricKey) map[string]string {
		return map[string]string{
			"method": key.Method,
			"route":  key.Route,
			"status": strconv.Itoa(key.Status),
		}
	})
	writeSummarySeries(&builder, "xxx_code_daemon_http_request_duration_seconds", "End-to-end HTTP request latency.", httpSnapshot, func(key httpMetricKey) map[string]string {
		return map[string]string{
			"method":  key.Method,
			"route":   key.Route,
			"status":  strconv.Itoa(key.Status),
			"outcome": key.Outcome,
		}
	})
	writeCounterSeriesFromLatency(&builder, "xxx_code_daemon_turns_total", "Completed turn executions grouped by result.", turnSnapshot, func(key string) map[string]string {
		return map[string]string{
			"result": key,
		}
	})
	writeSummarySeries(&builder, "xxx_code_daemon_turn_duration_seconds", "Turn execution latency grouped by result.", turnSnapshot, func(key string) map[string]string {
		return map[string]string{
			"result": key,
		}
	})
	writeCounterSeriesFromLatency(&builder, "xxx_code_daemon_tool_calls_total", "Executed tool calls grouped by tool name and result.", toolSnapshot, func(key toolMetricKey) map[string]string {
		return map[string]string{
			"tool":   key.Tool,
			"result": key.Result,
		}
	})
	writeSummarySeries(&builder, "xxx_code_daemon_tool_duration_seconds", "Executed tool latency grouped by tool name and result.", toolSnapshot, func(key toolMetricKey) map[string]string {
		return map[string]string{
			"tool":   key.Tool,
			"result": key.Result,
		}
	})

	mem := runtimeSnapshot.MemStats
	writeGauge(&builder, "go_goroutines", "Number of goroutines that currently exist.", nil, float64(runtimeSnapshot.Goroutines))
	writeGauge(&builder, "go_memstats_heap_alloc_bytes", "Number of heap bytes allocated and still in use.", nil, float64(mem.HeapAlloc))
	writeGauge(&builder, "go_memstats_heap_inuse_bytes", "Number of heap bytes in use.", nil, float64(mem.HeapInuse))
	writeGauge(&builder, "go_memstats_stack_inuse_bytes", "Number of stack bytes in use.", nil, float64(mem.StackInuse))
	writeGauge(&builder, "go_memstats_next_gc_bytes", "Heap size of the next garbage collection cycle.", nil, float64(mem.NextGC))
	writeGauge(&builder, "go_gc_cycles_total", "Number of completed GC cycles.", nil, float64(mem.NumGC))

	return builder.String()
}

func (m *daemonMetrics) snapshot() (map[httpMetricKey]latencyAggregate, map[string]latencyAggregate, map[toolMetricKey]latencyAggregate, map[string]uint64, time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()

	httpSnapshot := make(map[httpMetricKey]latencyAggregate, len(m.httpRequests))
	for key, value := range m.httpRequests {
		httpSnapshot[key] = value
	}
	turnSnapshot := make(map[string]latencyAggregate, len(m.turns))
	for key, value := range m.turns {
		turnSnapshot[key] = value
	}
	toolSnapshot := make(map[toolMetricKey]latencyAggregate, len(m.tools))
	for key, value := range m.tools {
		toolSnapshot[key] = value
	}
	agentSnapshot := make(map[string]uint64, len(m.agentEvents))
	for key, value := range m.agentEvents {
		agentSnapshot[key] = value
	}
	return httpSnapshot, turnSnapshot, toolSnapshot, agentSnapshot, m.startedAt
}

func snapshotDaemonRuntime(s *Server) daemonRuntimeSnapshot {
	snapshot := daemonRuntimeSnapshot{
		AgentsByState: make(map[string]int),
		Workflows:     make(map[string]int),
		Goroutines:    runtime.NumGoroutine(),
	}
	if s == nil {
		return snapshot
	}

	s.mu.Lock()
	sessions := make([]*managedSession, 0, len(s.sessions))
	for _, session := range s.sessions {
		sessions = append(sessions, session)
	}
	s.mu.Unlock()

	snapshot.Sessions = len(sessions)
	for _, session := range sessions {
		if session == nil {
			continue
		}
		for _, agent := range session.runner.ListAgents() {
			snapshot.AgentsByState[normalizeMetricValue(string(agent.Status), "unknown")]++
		}
		for _, workflow := range session.workflowManager.ListWorkflows() {
			snapshot.Workflows[normalizeMetricValue(string(workflow.Status), "unknown")]++
		}
	}

	runtime.ReadMemStats(&snapshot.MemStats)
	return snapshot
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	if !s.requireAccess(w, r, daemonModeIntrospection, "") {
		return
	}
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	_, _ = w.Write([]byte(s.metrics.render(s)))
}

func daemonRouteLabel(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return "unknown"
	}
	switch {
	case path == "/healthz":
		return "healthz"
	case path == "/metrics":
		return "metrics"
	case path == "/debug/pprof", path == "/debug/pprof/":
		return "debug_pprof_index"
	case strings.HasPrefix(path, "/debug/pprof/"):
		suffix := strings.Trim(strings.TrimPrefix(path, "/debug/pprof/"), "/")
		if suffix == "" {
			return "debug_pprof_index"
		}
		return "debug_pprof_" + normalizeMetricValue(strings.ReplaceAll(suffix, "/", "_"), "unknown")
	case path == "/v1/audit":
		return "v1_audit"
	case path == "/v1/sessions":
		return "v1_sessions"
	}
	if !strings.HasPrefix(path, "/v1/sessions/") {
		return "unknown"
	}

	parts := strings.Split(strings.Trim(strings.TrimPrefix(path, "/v1/sessions/"), "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		return "v1_sessions"
	}
	if len(parts) == 1 {
		return "v1_session"
	}

	switch parts[1] {
	case "messages":
		return "v1_session_messages"
	case "turns":
		if len(parts) > 2 && parts[2] == "stream" {
			return "v1_session_turns_stream"
		}
		return "v1_session_turns"
	case "save":
		return "v1_session_save"
	case "policy":
		return "v1_session_policy"
	case "hooks":
		return "v1_session_hooks"
	case "mcp":
		if len(parts) == 2 {
			return "v1_session_mcp"
		}
		return "v1_session_mcp_" + normalizeMetricValue(strings.Join(parts[2:], "_"), "unknown")
	case "plugins":
		if len(parts) == 2 {
			return "v1_session_plugins"
		}
		return "v1_session_plugins_" + normalizeMetricValue(strings.Join(parts[2:], "_"), "unknown")
	case "agents":
		if len(parts) == 2 {
			return "v1_session_agents"
		}
		if len(parts) >= 4 {
			return "v1_session_agent_" + normalizeMetricValue(strings.Join(parts[3:], "_"), "unknown")
		}
		return "v1_session_agent"
	case "workflows":
		if len(parts) == 2 {
			return "v1_session_workflows"
		}
		if len(parts) >= 4 {
			return "v1_session_workflow_" + normalizeMetricValue(strings.Join(parts[3:], "_"), "unknown")
		}
		return "v1_session_workflow"
	case "audit":
		return "v1_session_audit"
	default:
		return "v1_session_" + normalizeMetricValue(strings.Join(parts[1:], "_"), "unknown")
	}
}

func runOutcomeLabel(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, context.Canceled):
		return "cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	default:
		return "error"
	}
}

func toolResultLabel(isError bool) string {
	if isError {
		return "error"
	}
	return "ok"
}

func normalizeMetricValue(value, fallback string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return fallback
	}
	var builder strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			builder.WriteRune(r)
		case r == '-', r == '_', r == ':':
			builder.WriteRune(r)
		default:
			builder.WriteRune('_')
		}
	}
	if builder.Len() == 0 {
		return fallback
	}
	return builder.String()
}

func metricLabelString(labels map[string]string) string {
	if len(labels) == 0 {
		return ""
	}
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf(`%s="%s"`, key, escapeMetricLabel(labels[key])))
	}
	return "{" + strings.Join(parts, ",") + "}"
}

func escapeMetricLabel(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, "\n", `\n`)
	return strings.ReplaceAll(value, `"`, `\"`)
}

func writeGauge(builder *strings.Builder, name, help string, labels map[string]string, value float64) {
	fmt.Fprintf(builder, "# HELP %s %s\n", name, help)
	fmt.Fprintf(builder, "# TYPE %s gauge\n", name)
	fmt.Fprintf(builder, "%s%s %.6f\n", name, metricLabelString(labels), value)
}

func writeGaugeSeries(builder *strings.Builder, name, help, labelKey string, values map[string]int) {
	fmt.Fprintf(builder, "# HELP %s %s\n", name, help)
	fmt.Fprintf(builder, "# TYPE %s gauge\n", name)
	for _, key := range sortedStringKeys(values) {
		fmt.Fprintf(builder, "%s%s %d\n", name, metricLabelString(map[string]string{labelKey: key}), values[key])
	}
}

func writeCounterSeries(builder *strings.Builder, name, help, labelKey string, values map[string]uint64) {
	fmt.Fprintf(builder, "# HELP %s %s\n", name, help)
	fmt.Fprintf(builder, "# TYPE %s counter\n", name)
	for _, key := range sortedUintKeys(values) {
		fmt.Fprintf(builder, "%s%s %d\n", name, metricLabelString(map[string]string{labelKey: key}), values[key])
	}
}

func writeCounterSeriesFromLatency[K comparable](builder *strings.Builder, name, help string, values map[K]latencyAggregate, labels func(K) map[string]string) {
	fmt.Fprintf(builder, "# HELP %s %s\n", name, help)
	fmt.Fprintf(builder, "# TYPE %s counter\n", name)
	for _, key := range sortedLatencyKeys(values) {
		fmt.Fprintf(builder, "%s%s %d\n", name, metricLabelString(labels(key)), values[key].Count)
	}
}

func writeCounterSeriesFromLatencyFiltered[K comparable](builder *strings.Builder, name, help string, values map[K]latencyAggregate, include func(K) bool, labels func(K) map[string]string) {
	fmt.Fprintf(builder, "# HELP %s %s\n", name, help)
	fmt.Fprintf(builder, "# TYPE %s counter\n", name)
	for _, key := range sortedLatencyKeys(values) {
		if !include(key) {
			continue
		}
		fmt.Fprintf(builder, "%s%s %d\n", name, metricLabelString(labels(key)), values[key].Count)
	}
}

func writeSummarySeries[K comparable](builder *strings.Builder, name, help string, values map[K]latencyAggregate, labels func(K) map[string]string) {
	fmt.Fprintf(builder, "# HELP %s %s\n", name, help)
	fmt.Fprintf(builder, "# TYPE %s summary\n", name)
	for _, key := range sortedLatencyKeys(values) {
		labelSet := metricLabelString(labels(key))
		fmt.Fprintf(builder, "%s_sum%s %.6f\n", name, labelSet, values[key].SumSeconds)
		fmt.Fprintf(builder, "%s_count%s %d\n", name, labelSet, values[key].Count)
	}
}

func sortedStringKeys(values map[string]int) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedUintKeys(values map[string]uint64) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedLatencyKeys[K comparable](values map[K]latencyAggregate) []K {
	keys := make([]K, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		return fmt.Sprint(keys[i]) < fmt.Sprint(keys[j])
	})
	return keys
}

func (a latencyAggregate) withObservation(duration time.Duration) latencyAggregate {
	a.Count++
	a.SumSeconds += duration.Seconds()
	return a
}
