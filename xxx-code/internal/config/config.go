package config

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const defaultSystemPrompt = `You are xxx-code, a Go-built coding agent inspired by Claude Code.

Your job is to help with software engineering tasks using the available tools.

Guidelines:
- Read files before changing them when the task depends on existing code.
- Prefer the smallest correct change over speculative refactors.
- Use bash for shell tasks, read_file/write_file/edit_file for direct file work, glob/grep for search.
- Use agent_spawn only when a sub-task is clearly separable and benefits from parallel or isolated execution.
- If you spawn a background agent, use agent_wait or agent_list to integrate its result before you finish.
- Be explicit about verification. If you did not run a check, say so.
- Keep final user-facing answers concise and practical.`

type Config struct {
	APIKey            string
	BaseURL           string
	Version           string
	Model             string
	MaxTurns          int
	MaxTokens         int
	MaxParallelAgents int
	ContextBudget     int
	CompactKeep       int
	WorkingDir        string
	SessionFile       string
	ReadRoots         []string
	WriteRoots        []string
	ReadOnly          bool
	BashEnabled       bool
	HookBeforeTool    string
	HookAfterTool     string
	HookAfterTurn     string
	HookAgentEvent    string
	HookTimeout       time.Duration
	Resume            bool
	Print             bool
	Verbose           bool
	SystemPrompt      string
	ToolTimeout       time.Duration
	Prompt            string
}

func Load() (Config, error) {
	cfg := Config{}

	flag.StringVar(&cfg.Model, "model", firstNonEmpty(os.Getenv("XXX_CODE_MODEL"), "claude-sonnet-4-5"), "Anthropic model to use")
	flag.StringVar(&cfg.BaseURL, "base-url", firstNonEmpty(os.Getenv("ANTHROPIC_BASE_URL"), "https://api.anthropic.com"), "Anthropic API base URL")
	flag.StringVar(&cfg.Version, "anthropic-version", firstNonEmpty(os.Getenv("ANTHROPIC_VERSION"), "2023-06-01"), "Anthropic API version header")
	flag.IntVar(&cfg.MaxTurns, "max-turns", 12, "Maximum agentic turns per user prompt")
	flag.IntVar(&cfg.MaxTokens, "max-tokens", 16384, "Max output tokens per model request")
	flag.IntVar(&cfg.MaxParallelAgents, "max-parallel-agents", 4, "Maximum number of sub-agents that can run concurrently")
	flag.IntVar(&cfg.ContextBudget, "context-budget", 120000, "Approximate context token budget before automatic compaction; set 0 to disable")
	flag.IntVar(&cfg.CompactKeep, "compact-keep", 12, "How many latest messages to keep verbatim during automatic compaction")
	flag.BoolVar(&cfg.ReadOnly, "read-only", false, "Disable write_file and edit_file tool writes")
	flag.BoolVar(&cfg.BashEnabled, "bash", true, "Enable or disable the bash tool")
	flag.BoolVar(&cfg.Print, "print", false, "Run once and exit")
	flag.BoolVar(&cfg.Verbose, "verbose", false, "Print tool and agent lifecycle events")
	flag.BoolVar(&cfg.Resume, "resume", false, "Resume the main session and known agents from the session file")
	flag.DurationVar(&cfg.ToolTimeout, "tool-timeout", 2*time.Minute, "Per-tool execution timeout")
	flag.DurationVar(&cfg.HookTimeout, "hook-timeout", 30*time.Second, "Timeout for each configured hook command")

	systemPromptFile := flag.String("system-prompt-file", "", "Read the system prompt from a file")
	cwdFlag := flag.String("cwd", "", "Working directory")
	sessionFileFlag := flag.String("session-file", "", "Path to the persisted session file")
	readRootsFlag := flag.String("allow-read", "", "Comma-separated read roots; the working directory is always included")
	writeRootsFlag := flag.String("allow-write", "", "Comma-separated write roots; the working directory is always included unless --read-only is set")
	hookBeforeToolFlag := flag.String("hook-before-tool", "", "Shell command to run before each tool call; non-zero exit blocks the tool")
	hookAfterToolFlag := flag.String("hook-after-tool", "", "Shell command to run after each tool call")
	hookAfterTurnFlag := flag.String("hook-after-turn", "", "Shell command to run after each turn")
	hookAgentEventFlag := flag.String("hook-agent-event", "", "Shell command to run for agent lifecycle events")

	flag.Parse()

	cfg.APIKey = strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY"))
	if cfg.APIKey == "" {
		return Config{}, fmt.Errorf("ANTHROPIC_API_KEY is required")
	}

	if *cwdFlag != "" {
		cfg.WorkingDir = *cwdFlag
	} else {
		wd, err := os.Getwd()
		if err != nil {
			return Config{}, err
		}
		cfg.WorkingDir = wd
	}
	cfg.WorkingDir = filepath.Clean(cfg.WorkingDir)

	if *sessionFileFlag != "" {
		if filepath.IsAbs(*sessionFileFlag) {
			cfg.SessionFile = filepath.Clean(*sessionFileFlag)
		} else {
			cfg.SessionFile = filepath.Join(cfg.WorkingDir, *sessionFileFlag)
		}
	} else {
		cfg.SessionFile = filepath.Join(cfg.WorkingDir, ".xxx-code", "session.json")
	}

	cfg.ReadRoots = append([]string{cfg.WorkingDir}, parseRoots(cfg.WorkingDir, *readRootsFlag)...)
	cfg.WriteRoots = append([]string{cfg.WorkingDir}, parseRoots(cfg.WorkingDir, *writeRootsFlag)...)
	cfg.HookBeforeTool = strings.TrimSpace(*hookBeforeToolFlag)
	cfg.HookAfterTool = strings.TrimSpace(*hookAfterToolFlag)
	cfg.HookAfterTurn = strings.TrimSpace(*hookAfterTurnFlag)
	cfg.HookAgentEvent = strings.TrimSpace(*hookAgentEventFlag)

	cfg.SystemPrompt = defaultSystemPrompt
	if *systemPromptFile != "" {
		data, err := os.ReadFile(*systemPromptFile)
		if err != nil {
			return Config{}, err
		}
		cfg.SystemPrompt = string(data)
	}

	cfg.Prompt = strings.TrimSpace(strings.Join(flag.Args(), " "))
	if cfg.Prompt != "" {
		cfg.Print = true
	}

	return cfg, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func parseRoots(base, raw string) []string {
	parts := strings.Split(raw, ",")
	roots := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if filepath.IsAbs(part) {
			roots = append(roots, filepath.Clean(part))
			continue
		}
		roots = append(roots, filepath.Join(base, part))
	}
	return roots
}
