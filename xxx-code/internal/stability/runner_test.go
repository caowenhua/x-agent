package stability

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestRunSingleIteration(t *testing.T) {
	helperBinary := buildEchoHelper(t)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	result, err := Run(context.Background(), Config{
		Iterations:      1,
		Concurrency:     1,
		RestartEvery:    1,
		ScenarioTimeout: 5 * time.Second,
		ProgressEvery:   time.Hour,
		HelperBinary:    helperBinary,
	}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("run stability smoke test: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	if result.Rounds != 1 {
		t.Fatalf("expected one completed round, got %+v", result)
	}
	if result.Restarts != 1 {
		t.Fatalf("expected one restart verification, got %+v", result)
	}
	if result.TotalOps != len(scenarioSuite()) {
		t.Fatalf("expected one pass through the scenario suite, got %+v", result)
	}
	if result.FailedOps != 0 {
		t.Fatalf("expected no failed operations, got %+v", result)
	}

	for _, name := range []string{
		"basic_turn",
		"stream_turn",
		"plugin_lifecycle",
		"mcp_lifecycle",
		"agent_lifecycle",
		"workflow_lifecycle",
		"session_save",
		"stream_timeout",
	} {
		stats, ok := result.ScenarioInfo[name]
		if !ok {
			t.Fatalf("missing scenario stats for %s in %+v", name, result.ScenarioInfo)
		}
		if stats.Count != 1 || stats.Failures != 0 {
			t.Fatalf("unexpected stats for %s: %+v", name, stats)
		}
	}

	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	if !strings.Contains(string(data), `"avg_latency"`) {
		t.Fatalf("expected JSON summary to include avg latency, got %s", string(data))
	}
}

func buildEchoHelper(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	sourcePath := filepath.Join(dir, "main.go")
	source := `package main

import (
	"io"
	"os"
)

func main() {
	_, _ = io.Copy(os.Stdout, os.Stdin)
}
`
	if err := os.WriteFile(sourcePath, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}

	binaryPath := filepath.Join(dir, "echo-helper")
	if runtime.GOOS == "windows" {
		binaryPath += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", binaryPath, sourcePath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build echo helper: %v (%s)", err, string(output))
	}
	return binaryPath
}
