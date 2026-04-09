package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/caowenhua/x-agent/xxx-code/internal/config"
	pluginruntime "github.com/caowenhua/x-agent/xxx-code/internal/plugins"
)

func TestHandleCommandHelpIncludesPluginLifecycleCommands(t *testing.T) {
	app, out, _ := newTestApp(t)

	done, err := app.handleCommand(context.Background(), ":help")
	if err != nil {
		t.Fatal(err)
	}
	if done {
		t.Fatal("expected help command to keep repl running")
	}

	help := out.String()
	for _, needle := range []string{
		":plugins-validate <path>",
		":plugins-install <path> [force]",
		":plugins-remove <name>",
		":plugins-reload",
	} {
		if !strings.Contains(help, needle) {
			t.Fatalf("expected help output to contain %q, got %q", needle, help)
		}
	}
}

func TestHandleCommandPluginLifecycle(t *testing.T) {
	app, out, errOut := newTestApp(t)
	sourceDir := writeCLIPluginSource(t, filepath.Join(app.config.WorkingDir, "plugin-sources"), "echoer", "#!/bin/sh\ncat\n")

	validateOutput := mustRunCommand(t, app, out, errOut, ":plugins-validate "+sourceDir)
	var report pluginruntime.ValidationReport
	if err := json.Unmarshal([]byte(validateOutput), &report); err != nil {
		t.Fatalf("unmarshal validation report: %v", err)
	}
	if !report.Valid || report.PluginName != "echoer" || report.ToolCount != 1 {
		t.Fatalf("unexpected validation report: %+v", report)
	}

	installOutput := mustRunCommand(t, app, out, errOut, ":plugins-install "+sourceDir)
	installSummary := decodePluginSummary(t, installOutput)
	if installSummary.PluginCount != 1 || installSummary.ToolCount != 1 {
		t.Fatalf("unexpected plugin summary after install: %+v", installSummary)
	}
	if _, err := os.Stat(filepath.Join(app.config.WorkingDir, ".xxx-code", "plugins", "echoer", "plugin.json")); err != nil {
		t.Fatalf("expected installed plugin manifest: %v", err)
	}

	removeOutput := mustRunCommand(t, app, out, errOut, ":plugins-remove echoer")
	removeSummary := decodePluginSummary(t, removeOutput)
	if removeSummary.PluginCount != 0 || removeSummary.ToolCount != 0 {
		t.Fatalf("unexpected plugin summary after remove: %+v", removeSummary)
	}
	if _, err := os.Stat(filepath.Join(app.config.WorkingDir, ".xxx-code", "plugins", "echoer")); !os.IsNotExist(err) {
		t.Fatalf("expected plugin directory to be removed, got err=%v", err)
	}
}

type pluginSummaryPayload struct {
	PluginDir   string                 `json:"plugin_dir,omitempty"`
	PluginCount int                    `json:"plugin_count"`
	ToolCount   int                    `json:"tool_count"`
	Statuses    []pluginruntime.Status `json:"statuses"`
}

func decodePluginSummary(t *testing.T, raw string) pluginSummaryPayload {
	t.Helper()
	var summary pluginSummaryPayload
	if err := json.Unmarshal([]byte(raw), &summary); err != nil {
		t.Fatalf("unmarshal plugin summary: %v", err)
	}
	return summary
}

func mustRunCommand(t *testing.T, app *App, out, errOut *bytes.Buffer, command string) string {
	t.Helper()
	out.Reset()
	errOut.Reset()

	done, err := app.handleCommand(context.Background(), command)
	if err != nil {
		t.Fatalf("run %q: %v", command, err)
	}
	if done {
		t.Fatalf("expected %q to keep repl running", command)
	}
	if errText := strings.TrimSpace(errOut.String()); errText != "" {
		t.Fatalf("unexpected stderr for %q: %s", command, errText)
	}
	return strings.TrimSpace(out.String())
}

func newTestApp(t *testing.T) (*App, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	dir := t.TempDir()
	cfg := config.Config{
		Provider:          "anthropic",
		BaseURL:           "https://api.anthropic.com",
		Version:           "2023-06-01",
		Model:             "test-model",
		MaxTurns:          4,
		MaxTokens:         512,
		MaxParallelAgents: 1,
		ContextBudget:     2048,
		CompactKeep:       4,
		WorkingDir:        dir,
		SessionFile:       filepath.Join(dir, ".xxx-code", "session.json"),
		ReadRoots:         []string{dir},
		WriteRoots:        []string{dir},
		BashEnabled:       true,
		Stream:            false,
		HookTimeout:       time.Second,
		ToolTimeout:       time.Second,
		SystemPrompt:      "test prompt",
	}
	var out bytes.Buffer
	var errOut bytes.Buffer
	app := New(cfg, &out, &errOut)
	t.Cleanup(func() {
		_ = app.closePlugins()
		_ = app.closeMCP()
	})
	return app, &out, &errOut
}

func writeCLIPluginSource(t *testing.T, rootDir, pluginName, script string) string {
	t.Helper()
	pluginDir := filepath.Join(rootDir, pluginName)
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "tool.sh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{
  "name": "` + pluginName + `",
  "tools": [{
    "name": "echo",
    "description": "Echo plugin",
    "input_schema": {"type": "object"},
    "command": "./tool.sh"
  }]
}`
	if err := os.WriteFile(filepath.Join(pluginDir, "plugin.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	return pluginDir
}
